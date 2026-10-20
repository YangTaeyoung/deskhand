package conn

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// maxAgentLine bounds a line the agent sends.
	maxAgentLine = 8 << 20
	// maxDaemonLine bounds a line the daemon sends.
	maxDaemonLine = 1 << 20
	// helloTimeout bounds the wait for the agent's hello.
	helloTimeout = 10 * time.Second
	// writeTimeout bounds one write to the rpc stream.
	writeTimeout = 30 * time.Second
)

var (
	// errNotSent is the error of a request whose connection ended before
	// any of it was written.
	errNotSent = errors.New("the connection ended before the request was sent")
	// errConnLost is the error of a request whose connection ended after it
	// was written, before its answer arrived.
	errConnLost = errors.New("the connection ended before the answer arrived")
	// errDropped ends a connection on the DropAfterSend hook.
	errDropped = errors.New("the connection was dropped by DESKHAND_TEST_DROP_AFTER_SEND")
)

// lineReader reads the NDJSON messages of the agent, lines of at most
// maxAgentLine bytes.
type lineReader struct{ sc *bufio.Scanner }

func newLineReader(r io.Reader) *lineReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxAgentLine)
	return &lineReader{sc: sc}
}

// next returns the next JSON object with a string t, skipping other lines.
func (l *lineReader) next() (proto.Message, error) {
	for l.sc.Scan() {
		line := l.sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m proto.Message
		if json.Unmarshal(line, &m) != nil || m.Type() == "" {
			continue
		}
		return m, nil
	}
	if err := l.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

// handshake writes the rpc header on st, reads the agent's hello and
// answers it with a welcome that names controller.
func handshake(st Stream, controller string) (proto.Message, *lineReader, error) {
	_ = st.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := wire.WriteHeader(st, wire.StreamHeader{S: "rpc"}); err != nil {
		return nil, nil, fmt.Errorf("write the rpc header: %w", err)
	}
	r := newLineReader(st)
	_ = st.SetReadDeadline(time.Now().Add(helloTimeout))
	hello, err := r.next()
	_ = st.SetReadDeadline(time.Time{})
	if err != nil {
		return nil, nil, fmt.Errorf("read the hello: %w", err)
	}
	if hello.Type() != "hello" {
		return nil, nil, fmt.Errorf("the agent sent %q instead of hello", hello.Type())
	}
	if err := writeMessage(st, proto.Message{"t": "welcome", "v": proto.Version, "controller": controller}); err != nil {
		return nil, nil, fmt.Errorf("write the welcome: %w", err)
	}
	return hello, r, nil
}

func writeMessage(st Stream, m proto.Message) error {
	b, err := proto.Encode(m)
	if err != nil {
		return err
	}
	_ = st.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err = st.Write(b)
	return err
}

// pendingCall waits for the answer to one request; drop ends the
// connection when that answer arrives instead of delivering it.
type pendingCall struct {
	ch   chan proto.Message
	drop bool
}

// rpcClient sends requests on an rpc stream after the handshake and
// matches the answers by id; events go to onEvent.
type rpcClient struct {
	// sess is the connection of the stream; nil when the client was made
	// without one.
	sess    Session
	st      Stream
	r       *lineReader
	onEvent func(proto.Message)
	closeFn func()

	wmu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]*pendingCall
	done    chan struct{}
	err     error
	once    sync.Once
}

func newRPCClient(st Stream, r *lineReader, onEvent func(proto.Message), closeFn func()) *rpcClient {
	return &rpcClient{st: st, r: r, onEvent: onEvent, closeFn: closeFn, pending: map[int64]*pendingCall{}, done: make(chan struct{})}
}

// run reads the stream until it ends, then shuts the client down.
func (c *rpcClient) run() {
	for {
		m, err := c.r.next()
		if err != nil {
			c.shutdown(err)
			return
		}
		switch m.Type() {
		case "res":
			id, ok := m["id"].(float64)
			if !ok {
				continue
			}
			c.mu.Lock()
			pc := c.pending[int64(id)]
			delete(c.pending, int64(id))
			c.mu.Unlock()
			if pc == nil {
				continue
			}
			if pc.drop {
				c.shutdown(errDropped)
				return
			}
			pc.ch <- m
		case "ev":
			if c.onEvent != nil {
				c.onEvent(m)
			}
		}
	}
}

// shutdown ends the client once: it records err, closes the stream and the
// connection, and releases the waiting calls.
func (c *rpcClient) shutdown(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		close(c.done)
		c.mu.Unlock()
		_ = c.st.Close()
		if c.closeFn != nil {
			c.closeFn()
		}
	})
}

// Done is closed when the client has shut down.
func (c *rpcClient) Done() <-chan struct{} { return c.done }

// closed reports whether the client has shut down.
func (c *rpcClient) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Err returns why the client shut down.
func (c *rpcClient) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// requestError is a request refused before any of it was written: it
// cannot be encoded or its line is longer than maxDaemonLine.
type requestError struct{ err *apierr.Error }

func (e *requestError) Error() string { return e.err.Message }
func (e *requestError) Unwrap() error { return e.err }

// IsRequestError reports whether err is a request refused before it was
// sent, an INVALID_ARGUMENT problem of the request itself.
func IsRequestError(err error) bool {
	var re *requestError
	return errors.As(err, &re)
}

// encodeRequest returns the line of req with id, or a *requestError.
func encodeRequest(req proto.Message, id int64) ([]byte, error) {
	msg := make(proto.Message, len(req)+1)
	for k, v := range req {
		msg[k] = v
	}
	msg["id"] = id
	line, err := proto.Encode(msg)
	if err != nil {
		return nil, &requestError{apierr.Invalid(fmt.Sprintf("요청을 JSON으로 만들 수 없습니다(%v). 파라미터 값을 확인하세요", err))}
	}
	if len(line) > maxDaemonLine {
		return nil, &requestError{apierr.Invalid(fmt.Sprintf("요청이 %d바이트로 1 MiB를 넘습니다. 데이터를 나눠서 보내세요", len(line)))}
	}
	return line, nil
}

// call sends req with a new id and returns the answer. A request that
// cannot be encoded or is too long gives a *requestError, a connection that
// ended before req was written errNotSent, one that ended after it
// errConnLost.
func (c *rpcClient) call(ctx context.Context, req proto.Message, drop bool) (proto.Message, error) {
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, errNotSent
	default:
	}
	c.nextID++
	id := c.nextID
	line, err := encodeRequest(req, id)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	pc := &pendingCall{ch: make(chan proto.Message, 1), drop: drop}
	c.pending[id] = pc
	c.mu.Unlock()

	c.wmu.Lock()
	_ = c.st.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err = c.st.Write(line)
	c.wmu.Unlock()
	if err != nil {
		c.forget(id)
		c.shutdown(err)
		return nil, errConnLost
	}
	select {
	case res := <-pc.ch:
		return res, nil
	case <-c.done:
		select {
		case res := <-pc.ch:
			return res, nil
		default:
		}
		return nil, errConnLost
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

func (c *rpcClient) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}
