package localapi

import (
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/screen"
)

func TestInputAttachRefusesAIBeforeResolvingHost(t *testing.T) {
	r := newRig(t)
	h := screen.New(screen.Options{})
	t.Cleanup(h.Close)
	r.srv.d.Screens = h
	r.srv.registerScreen()
	c := r.dialRaw(t)
	c.write([]byte(`{"jsonrpc":"2.0","id":0,"method":"hello","params":{"client":"mcp"}}` + "\n"))
	c.line()
	c.write([]byte(`{"jsonrpc":"2.0","id":1,"method":"stream.attach","params":{"kind":"input","host_id":"unknown"}}` + "\n"))
	res := c.line()
	e, _ := res["error"].(map[string]any)
	if e == nil || e["data"].(map[string]any)["code"] != "PERMISSION_DENIED" {
		t.Fatalf("AI input attach: %v", res)
	}
}

func TestInputAttachRequiresExactIdentityParams(t *testing.T) {
	for _, params := range []string{
		`{"kind":"input","host_id":"pc"}`,
		`{"kind":"input","host_id":"pc","session_id":7,"sid":"S-1-5-21-1-1001"}`,
		`{"kind":"input","host_id":"pc","connection_generation":1,"sid":"S-1-5-21-1-1001"}`,
		`{"kind":"input","host_id":"pc","connection_generation":1,"session_id":7}`,
		`{"kind":"input","host_id":"pc","connection_generation":1,"session_id":7,"sid":""}`,
		`{"kind":"input","host_id":"pc","connection_generation":0,"session_id":7,"sid":"S-1-5-21-1-1001"}`,
		`{"kind":"input","host_id":"pc","connection_generation":1,"session_id":-1,"sid":"S-1-5-21-1-1001"}`,
		`{"kind":"input","host_id":"pc","connection_generation":1,"session_id":4294967296,"sid":"S-1-5-21-1-1001"}`,
		`{"kind":"input","host_id":"pc","connection_generation":18446744073709551616,"session_id":7,"sid":"S-1-5-21-1-1001"}`,
		`{"kind":"input","host_id":"pc","connection_generation":1,"session_id":7.5,"sid":"S-1-5-21-1-1001"}`,
		`{"kind":"input","host_id":"pc","connection_generation":"1","session_id":7,"sid":"S-1-5-21-1-1001"}`,
		`{"kind":"input","host_id":"pc","connection_generation":1,"session_id":null,"sid":"S-1-5-21-1-1001"}`,
		`{"kind":"input","host_id":"pc","connection_generation":1,"session_id":7,"sid":"S-1-5-21-1-1001","display":0}`,
		`{"kind":"input","host_id":"pc","connection_generation":1,"session_id":7,"sid":"S-1-5-21-1-1001","SID":"ignored"}`,
	} {
		t.Run(params, func(t *testing.T) {
			r := newRig(t)
			h := screen.New(screen.Options{})
			t.Cleanup(h.Close)
			r.srv.d.Screens = h
			r.srv.registerScreen()
			c := r.dialRaw(t)
			c.write([]byte(`{"jsonrpc":"2.0","id":0,"method":"hello","params":{"client":"app"}}` + "\n"))
			c.line()
			c.write([]byte(`{"jsonrpc":"2.0","id":1,"method":"stream.attach","params":` + params + `}` + "\n"))
			res := c.line()
			e, _ := res["error"].(map[string]any)
			if e == nil || e["data"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
				t.Fatalf("invalid params: %v", res)
			}
		})
	}
}
