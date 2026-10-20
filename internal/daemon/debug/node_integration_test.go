package debug

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

const fixtureJS = `function calcTotal(items) {
  let total = 0;
  for (const it of items) { total += it.price; }
  return total;
}
const order = { items: [{ price: 10 }, { price: 20 }], city: '서울' };
console.log('start', order.city);
globalThis.result = calcTotal(order.items);
console.log('result', globalThis.result);
`

var wsLine = regexp.MustCompile(`ws://([^/]+)(/\S+)`)

// TestRealNodeInspectorBreakpointAndScopes drives the CDP client against a
// real Node inspector: it sets a breakpoint, pauses, reads a scope,
// evaluates on the frame and resumes.
func TestRealNodeInspectorBreakpointAndScopes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "fixture.js")
	if err := os.WriteFile(file, []byte(fixtureJS), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--inspect-brk=127.0.0.1:0", file)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	addr, path := "", ""
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		if m := wsLine.FindStringSubmatch(sc.Text()); m != nil {
			addr, path = m[1], m[2]
			break
		}
	}
	if addr == "" {
		t.Fatal("node did not print its inspector URL")
	}

	paused := make(chan pausedEvent, 8)
	cdp, err := dialCDP(ctx, addr, path, func(ev cdpEvent) {
		if ev.Method == "Debugger.paused" {
			var p pausedEvent
			_ = json.Unmarshal(ev.Params, &p)
			paused <- p
		}
	})
	if err != nil {
		t.Fatalf("dial node inspector: %v", err)
	}
	defer cdp.Close()

	call := func(method string, params any) json.RawMessage {
		t.Helper()
		raw, err := cdp.Call(ctx, "", method, params)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return raw
	}
	call("Runtime.enable", nil)
	call("Debugger.enable", nil)
	call("Debugger.setBreakpointByUrl", map[string]any{"lineNumber": 3, "urlRegex": fileNameRegex("fixture.js")})
	call("Runtime.runIfWaitingForDebugger", nil)

	// --inspect-brk stops on the first line; resume to reach the breakpoint.
	first := waitPause(t, paused)
	if len(first.CallFrames) == 0 {
		t.Fatal("first pause had no frames")
	}
	call("Debugger.resume", nil)
	hit := waitPause(t, paused)
	frame := hit.CallFrames[0]
	if frame.FunctionName != "calcTotal" || frame.Location.LineNumber != 3 {
		t.Fatalf("breakpoint frame = %s line %d", frame.FunctionName, frame.Location.LineNumber)
	}
	var localObj string
	for _, sc := range frame.ScopeChain {
		if sc.Type == "local" {
			localObj = sc.Object.ObjectID
		}
	}
	if localObj == "" {
		t.Fatal("no local scope object")
	}
	raw := call("Runtime.getProperties", map[string]any{"objectId": localObj, "ownProperties": true, "generatePreview": true})
	props := (&session{}).properties(&targetState{consoleObjects: map[string]bool{}, pauseObjects: map[string]int64{}}, localObj, raw)
	if !hasProp(props, "total") || !hasProp(props, "items") {
		t.Fatalf("local scope has no total/items: %+v", names(props))
	}
	if p := propByName(props, "total"); p == nil || p.ValuePreview != "30" {
		t.Fatalf("total = %+v", p)
	}

	evalRaw := call("Debugger.evaluateOnCallFrame", map[string]any{"callFrameId": frame.CallFrameID,
		"expression": "items.length", "objectGroup": "console", "generatePreview": true})
	var evalRes struct {
		Result remoteObject `json:"result"`
	}
	_ = json.Unmarshal(evalRaw, &evalRes)
	if evalRes.Result.value().ValuePreview != "2" {
		t.Fatalf("items.length = %q", evalRes.Result.value().ValuePreview)
	}
	call("Debugger.resume", nil)
}

func waitPause(t *testing.T, ch <-chan pausedEvent) pausedEvent {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(10 * time.Second):
		t.Fatal("no Debugger.paused arrived")
		return pausedEvent{}
	}
}

func hasProp(props []Property, name string) bool { return propByName(props, name) != nil }

func propByName(props []Property, name string) *Property {
	for i := range props {
		if props[i].Name == name {
			return &props[i]
		}
	}
	return nil
}

func names(props []Property) []string {
	var out []string
	for _, p := range props {
		out = append(out, p.Name)
	}
	return out
}
