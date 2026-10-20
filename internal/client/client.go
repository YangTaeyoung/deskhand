// Package client is the Go client of the daemon's local API
// (docs/local-api.md). Dial starts the daemon when its socket does not
// answer.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/flock"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Error is the error of a failed call: the daemon's code, message,
// retryable and next_action.
type Error = apierr.Error

const (
	// defaultStartTimeout bounds the start of the daemon.
	defaultStartTimeout = 5 * time.Second
	// probeTimeout bounds one check that the socket answers.
	probeTimeout = time.Second
	// eventBuffer is how many events wait for the reader before newer
	// ones are dropped.
	eventBuffer = 1024
	// maxResponseLine bounds a line from the daemon.
	maxResponseLine = 64 << 20
)

// Options tune DialOptions.
type Options struct {
	// Socket is the daemon socket; "" means paths.Socket().
	Socket string
	// Start starts the daemon; nil means Spawn. It runs while the start
	// lock is held, at most once per start.
	Start func(ctx context.Context) error
	// Version is the client version sent in hello.
	Version string
	// StartTimeout bounds the wait for a started daemon (5 s when 0).
	StartTimeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.Socket == "" {
		o.Socket = paths.Socket()
	}
	if o.Start == nil {
		o.Start = Spawn
	}
	if o.StartTimeout <= 0 {
		o.StartTimeout = defaultStartTimeout
	}
	return o
}

// Event is an event notification.
type Event struct {
	Seq    int64
	TS     string
	HostID string
	Kind   string
	// Params holds every member of the notification params.
	Params map[string]any
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	} `json:"error"`
}

// Client is one connection to the daemon.
type Client struct {
	// ClientID is the id the daemon gave this client in hello.
	ClientID string
	// DaemonVersion is the version the daemon reported in hello.
	DaemonVersion string

	c   net.Conn
	wmu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan response
	closed  bool
	done    chan struct{}
	events  chan Event
}

// Dial connects to the daemon of paths.Socket() as clientKind ("app",
// "cli" or "mcp") named name, starting the daemon with Spawn when the
// socket does not answer.
func Dial(ctx context.Context, clientKind, name string) (*Client, error) {
	return DialOptions(ctx, clientKind, name, Options{})
}

// DialOptions is Dial with options.
func DialOptions(ctx context.Context, clientKind, name string, opt Options) (*Client, error) {
	opt = opt.withDefaults()
	if c, err := connect(ctx, opt.Socket, clientKind, name, opt.Version); err == nil {
		return c, nil
	}
	if err := ensure(ctx, opt); err != nil {
		return nil, err
	}
	return connect(ctx, opt.Socket, clientKind, name, opt.Version)
}

// Start makes sure the daemon of paths.Socket() runs, spawning it when its
// socket does not answer, and returns once it answers hello.
func Start(ctx context.Context) error {
	return ensure(ctx, Options{}.withDefaults())
}

// Running reports whether a daemon answers hello on socket ("" means
// paths.Socket()).
func Running(ctx context.Context, socket string) bool {
	if socket == "" {
		socket = paths.Socket()
	}
	return answers(ctx, socket)
}

// ensure starts the daemon under the start lock next to the socket unless
// the socket answers, and waits until it does, at most StartTimeout.
func ensure(ctx context.Context, opt Options) error {
	ctx, cancel := context.WithTimeout(ctx, opt.StartTimeout)
	defer cancel()
	dir := filepath.Dir(opt.Socket)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("데몬 폴더 %s를 만들지 못했습니다. 폴더 권한을 확인하세요: %w", dir, err)
	}
	lock, err := flock.Acquire(ctx, filepath.Join(dir, "start.lock"))
	if err != nil {
		return fmt.Errorf("다른 프로그램이 데몬을 시작하는 중이라 %s 안에 끝나지 않았습니다. %s 로그를 확인하세요: %w", opt.StartTimeout, paths.DaemonLog(), err)
	}
	defer lock.Release()
	if answers(ctx, opt.Socket) {
		return nil
	}
	started := make(chan error, 1)
	go func() { started <- opt.Start(ctx) }()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-started:
			if err != nil {
				return fmt.Errorf("Deskhand 데몬을 시작하지 못했습니다(%v). %s 로그를 확인하세요", err, paths.DaemonLog())
			}
			started = nil
		case <-tick.C:
			if answers(ctx, opt.Socket) {
				return nil
			}
		case <-ctx.Done():
			if answers(context.Background(), opt.Socket) {
				return nil
			}
			return fmt.Errorf("Deskhand 데몬이 %s 안에 응답하지 않았습니다. %s 로그를 확인하세요", opt.StartTimeout, paths.DaemonLog())
		}
	}
}

// answers reports whether the socket answers hello within probeTimeout.
func answers(ctx context.Context, socket string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c, err := connect(ctx, socket, "cli", "deskhand-probe", "")
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// Spawn starts `<this executable> daemon run` in its own session with its
// output appended to the daemon log. It returns when ctx ends, or earlier
// with an error when the daemon exits first.
func Spawn(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("실행 파일 위치를 알 수 없습니다: %w", err)
	}
	if err := os.MkdirAll(paths.Logs(), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(paths.DaemonLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon", "run")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = detached()
	err = cmd.Start()
	logf.Close()
	if err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err == nil {
			err = errors.New("exit status 0")
		}
		return fmt.Errorf("데몬이 시작하자마자 끝났습니다(%v)", err)
	case <-ctx.Done():
		return nil
	}
}

// connect opens the socket and sends hello.
func connect(ctx context.Context, socket, kind, name, version string) (*Client, error) {
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, probeTimeout)
	nc, err := d.DialContext(dctx, "unix", socket)
	cancel()
	if err != nil {
		return nil, err
	}
	c := &Client{c: nc, pending: map[int64]chan response{}, done: make(chan struct{}), events: make(chan Event, eventBuffer)}
	go c.read()
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var hello struct {
		ClientID      string `json:"client_id"`
		DaemonVersion string `json:"daemon_version"`
	}
	if err := c.Call(hctx, "hello", map[string]any{"client": kind, "name": name, "version": version}, &hello); err != nil {
		c.Close()
		return nil, err
	}
	c.ClientID, c.DaemonVersion = hello.ClientID, hello.DaemonVersion
	return c, nil
}

func (c *Client) read() {
	defer c.shutdown()
	sc := bufio.NewScanner(c.c)
	sc.Buffer(make([]byte, 64<<10), maxResponseLine)
	for sc.Scan() {
		var m struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		line := sc.Bytes()
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m.Method == "event" {
			c.deliver(m.Params)
			continue
		}
		if m.ID == nil {
			continue
		}
		var res response
		if json.Unmarshal(line, &res) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- res
		}
	}
}

func (c *Client) deliver(raw json.RawMessage) {
	var p map[string]any
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	ev := Event{Params: p}
	if v, ok := p["seq"].(float64); ok {
		ev.Seq = int64(v)
	}
	ev.TS, _ = p["ts"].(string)
	ev.HostID, _ = p["host_id"].(string)
	ev.Kind, _ = p["kind"].(string)
	select {
	case c.events <- ev:
	default:
	}
}

func (c *Client) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.done)
	c.c.Close()
}

// Close closes the connection.
func (c *Client) Close() error {
	c.shutdown()
	return nil
}

var errClosed = apierr.New(wire.CodeOffline, "데몬과의 연결이 끊겼습니다. 명령을 다시 실행하세요", true, apierr.NextRetry)

// Call sends method with params and decodes the result into out (when not
// nil). A daemon error is an *Error.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	if params == nil {
		params = map[string]any{}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errClosed
	}
	c.nextID++
	id := c.nextID
	ch := make(chan response, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		forget()
		return apierr.Invalid(fmt.Sprintf("%s 파라미터를 JSON으로 만들 수 없습니다(%v). 값을 확인하세요", method, err))
	}
	c.wmu.Lock()
	_ = c.c.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err := c.c.Write(buf.Bytes())
	c.wmu.Unlock()
	if err != nil {
		forget()
		c.shutdown()
		return errClosed
	}
	select {
	case res := <-ch:
		return decodeResponse(method, res, out)
	case <-c.done:
		select {
		case res := <-ch:
			return decodeResponse(method, res, out)
		default:
		}
		return errClosed
	case <-ctx.Done():
		forget()
		return apierr.New(wire.CodeTimeout, fmt.Sprintf("%s 응답을 기다리는 동안 시간이 다 됐습니다. 결과는 jobs.list로 확인하세요", method), false, apierr.NextCheckState)
	}
}

func decodeResponse(method string, res response, out any) error {
	if res.Error != nil {
		e := &Error{Code: wire.CodeInternal, Message: res.Error.Message}
		if code, ok := res.Error.Data["code"].(string); ok && code != "" {
			e.Code = code
		}
		e.Retryable, _ = res.Error.Data["retryable"].(bool)
		e.NextAction, _ = res.Error.Data["next_action"].(string)
		return e
	}
	if out == nil || len(res.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(res.Result, out); err != nil {
		return apierr.Internal(fmt.Sprintf("%s 결과를 읽지 못했습니다(%v). deskhand와 데몬 버전을 확인하세요", method, err))
	}
	return nil
}

// Subscribe asks for the events of kinds (all when empty) and returns the
// channel they arrive on. A connection has one subscription; a later call
// replaces the filter and returns the same channel.
func (c *Client) Subscribe(ctx context.Context, kinds []string) (<-chan Event, error) {
	return c.SubscribeHost(ctx, "", kinds)
}

// SubscribeHost is Subscribe limited to the events of one host.
func (c *Client) SubscribeHost(ctx context.Context, hostID string, kinds []string) (<-chan Event, error) {
	params := map[string]any{}
	if hostID != "" {
		params["host_id"] = hostID
	}
	if len(kinds) > 0 {
		params["kinds"] = kinds
	}
	if err := c.Call(ctx, "events.subscribe", params, nil); err != nil {
		return nil, err
	}
	return c.events, nil
}
