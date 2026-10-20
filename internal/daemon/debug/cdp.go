package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// cdpEvent is one CDP event; SessionID names the flattened target session
// it came from, "" for the connection's own target.
type cdpEvent struct {
	SessionID string
	Method    string
	Params    json.RawMessage
}

// CDPError is an error answer of a CDP command.
type CDPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *CDPError) Error() string {
	if e.Data != "" {
		return e.Message + ": " + e.Data
	}
	return e.Message
}

// errConnClosed is the error of a command whose connection ended.
var errConnClosed = errors.New("the debugger connection closed")

type cdpReply struct {
	result json.RawMessage
	err    error
}

// cdpConn is a CDP connection over a WebSocket: commands are matched to
// their answers by id, and events are handed to the handler one at a time
// in the order they arrived.
type cdpConn struct {
	ws      *wsConn
	handler func(cdpEvent)
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan cdpReply
	queue   []cdpEvent
	wake    chan struct{}
	err     error
	done    chan struct{}
}

// dialCDP opens a CDP connection to the WebSocket path on addr; handler
// receives its events.
func dialCDP(ctx context.Context, addr, path string, handler func(cdpEvent)) (*cdpConn, error) {
	ws, err := dialWS(ctx, addr, path)
	if err != nil {
		return nil, err
	}
	c := &cdpConn{ws: ws, handler: handler, pending: map[int64]chan cdpReply{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go c.read()
	go c.dispatch()
	return c, nil
}

type cdpMessage struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"sessionId"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     *CDPError       `json:"error"`
}

func (c *cdpConn) read() {
	var err error
	for {
		var msg []byte
		msg, err = c.ws.ReadMessage()
		if err != nil {
			break
		}
		var m cdpMessage
		if json.Unmarshal(msg, &m) != nil {
			continue
		}
		if m.Method != "" && m.ID == 0 {
			c.mu.Lock()
			c.queue = append(c.queue, cdpEvent{SessionID: m.SessionID, Method: m.Method, Params: m.Params})
			c.mu.Unlock()
			select {
			case c.wake <- struct{}{}:
			default:
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[m.ID]
		delete(c.pending, m.ID)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if m.Error != nil {
			ch <- cdpReply{err: m.Error}
		} else {
			ch <- cdpReply{result: m.Result}
		}
	}
	if errors.Is(err, io.EOF) {
		err = errConnClosed
	}
	c.mu.Lock()
	c.err = err
	pend := c.pending
	c.pending = map[int64]chan cdpReply{}
	c.mu.Unlock()
	for _, ch := range pend {
		ch <- cdpReply{err: errConnClosed}
	}
	_ = c.ws.Close()
	close(c.done)
}

// dispatch hands the queued events to the handler; after the connection
// ends it hands over what is left and returns.
func (c *cdpConn) dispatch() {
	for {
		c.mu.Lock()
		q := c.queue
		c.queue = nil
		c.mu.Unlock()
		for _, ev := range q {
			c.handler(ev)
		}
		if len(q) > 0 {
			continue
		}
		select {
		case <-c.wake:
		case <-c.done:
			c.mu.Lock()
			left := len(c.queue)
			c.mu.Unlock()
			if left == 0 {
				return
			}
		}
	}
}

// Call sends method with params to the target session sessionID ("" for the
// connection's target) and returns its result.
func (c *cdpConn) Call(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if sessionID != "" {
		msg["sessionId"] = sessionID
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	ch := make(chan cdpReply, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, errConnClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.ws.WriteText(b); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, errConnClosed
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Done is closed when the connection has ended.
func (c *cdpConn) Done() <-chan struct{} { return c.done }

// Close ends the connection.
func (c *cdpConn) Close() { _ = c.ws.Close() }

// httpTimeout bounds one request to a DevTools HTTP endpoint.
const httpTimeout = 3 * time.Second

// jsonVersion is the answer of /json/version.
type jsonVersion struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// jsonTarget is one entry of /json/list.
type jsonTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// getJSON reads the DevTools HTTP endpoint path of addr into v.
func getJSON(ctx context.Context, addr, path string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return err
	}
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{}).DialContext, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	res, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", path, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(v)
}

// wsPath returns the path and query of a webSocketDebuggerUrl, which names
// the PC's port; the daemon reaches it through its own tunnel port.
func wsPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return "", fmt.Errorf("the debugger address %q cannot be read", raw)
	}
	p := u.EscapedPath()
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p, nil
}

// isNode reports whether a /json/version Browser names Node.js.
func isNode(browser string) bool { return strings.HasPrefix(strings.ToLower(browser), "node.js/") }
