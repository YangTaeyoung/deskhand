package localapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// echoStream is a stream kind whose stream writes back each frame it reads
// until its client ends it.
func echoStream(ctx context.Context, _ *session, params json.RawMessage) (any, func(io.ReadWriteCloser), error) {
	var p struct {
		Tag string `json:"tag"`
	}
	if err := decode(params, &p); err != nil {
		return nil, nil, err
	}
	return map[string]any{"tag": p.Tag}, func(rw io.ReadWriteCloser) {
		defer rw.Close()
		for {
			kind, payload, err := wire.ReadFrame(rw, 1<<20)
			if err != nil {
				return
			}
			if err := wire.WriteFrame(rw, kind, payload); err != nil {
				return
			}
		}
	}, nil
}

// rawClient is a local API connection read line by line and then frame by
// frame.
type rawClient struct {
	t *testing.T
	c net.Conn
	r *bufio.Reader
}

func (r rig) dialRaw(t *testing.T) *rawClient {
	t.Helper()
	c, err := net.Dial("unix", r.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	return &rawClient{t: t, c: c, r: bufio.NewReader(c)}
}

func (c *rawClient) write(b []byte) {
	c.t.Helper()
	if _, err := c.c.Write(b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *rawClient) line() map[string]any {
	c.t.Helper()
	b, err := c.r.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("no line: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		c.t.Fatalf("line %q: %v", b, err)
	}
	return m
}

const helloLine = `{"jsonrpc":"2.0","id":0,"method":"hello","params":{"client":"app","name":"t","version":"1"}}` + "\n"

func TestStreamAttachHandsTheConnectionOverWithTheBytesAfterIt(t *testing.T) {
	r := newRig(t)
	r.srv.streams["echo"] = echoStream
	c := r.dialRaw(t)
	var frame bytes.Buffer
	if err := wire.WriteFrame(&frame, wire.KindPTYData, []byte("sent with the attach")); err != nil {
		t.Fatal(err)
	}
	c.write(append([]byte(helloLine+`{"jsonrpc":"2.0","id":1,"method":"stream.attach","params":{"kind":"echo","tag":"x"}}`+"\n"), frame.Bytes()...))
	if res := c.line(); res["result"] == nil {
		t.Fatalf("hello %v", res)
	}
	res := c.line()
	if res["id"] != float64(1) || res["result"].(map[string]any)["tag"] != "x" {
		t.Fatalf("stream.attach %v", res)
	}
	kind, payload, err := wire.ReadFrame(c.r, 1<<20)
	if err != nil || kind != wire.KindPTYData || string(payload) != "sent with the attach" {
		t.Fatalf("echoed frame %#x %q %v", kind, payload, err)
	}
	if err := wire.WriteFrame(c.c, wire.KindPTYResize, []byte(`{"cols":1,"rows":2}`)); err != nil {
		t.Fatal(err)
	}
	if kind, payload, err := wire.ReadFrame(c.r, 1<<20); err != nil || kind != wire.KindPTYResize || string(payload) != `{"cols":1,"rows":2}` {
		t.Fatalf("second echoed frame %#x %q %v", kind, payload, err)
	}
}

func TestARefusedStreamAttachLeavesTheSessionServingRequests(t *testing.T) {
	r := newRig(t)
	c := r.dialRaw(t)
	c.write([]byte(helloLine))
	c.line()
	for i, params := range []string{`{"kind":"nope"}`, `{"kind":"screen"}`, `{"kind":"pty"}`} {
		c.write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"stream.attach","params":%s}`+"\n", i+1, params)))
		res := c.line()
		e, _ := res["error"].(map[string]any)
		if e == nil || e["data"].(map[string]any)["code"] == "" {
			t.Fatalf("stream.attach %s: %v", params, res)
		}
	}
	c.write([]byte(`{"jsonrpc":"2.0","id":9,"method":"host.list"}` + "\n"))
	if res := c.line(); res["id"] != float64(9) || res["result"] == nil {
		t.Fatalf("host.list after the refusals %v", res)
	}
}

func TestStreamAttachIsRefusedOnAConnectionWithEvents(t *testing.T) {
	r := newRig(t)
	r.srv.streams["echo"] = echoStream
	c := r.dialRaw(t)
	c.write([]byte(helloLine + `{"jsonrpc":"2.0","id":1,"method":"events.subscribe","params":{}}` + "\n"))
	c.line()
	c.line()
	c.write([]byte(`{"jsonrpc":"2.0","id":2,"method":"stream.attach","params":{"kind":"echo"}}` + "\n"))
	res := c.line()
	if e, _ := res["error"].(map[string]any); e == nil || e["data"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("stream.attach after events.subscribe %v", res)
	}
}
