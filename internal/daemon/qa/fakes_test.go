package qa

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// fakeElem is a UI element of the fake desktop.
type fakeElem struct {
	window   string
	role     string
	name     string
	aid      string
	text     string
	disabled bool
	// hiddenFor is how many more ui.find calls do not see the element.
	hiddenFor int
}

// fakeDisplay is a display of the fake desktop: its index, its origin in
// virtual desktop pixels and its image.
type fakeDisplay struct {
	index, x, y int
	img         *image.NRGBA
}

// desktopPixel is the colour of the virtual desktop pixel (x, y) on the
// fake displays.
func desktopPixel(x, y int) color.NRGBA {
	return color.NRGBA{R: uint8(x), G: uint8(y), B: uint8(x>>8<<4 | y>>8), A: 0xff}
}

// newFakeDisplay is the display index of w×h pixels at (x, y), each pixel
// coloured by desktopPixel.
func newFakeDisplay(index, x, y, w, h int) fakeDisplay {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			img.SetNRGBA(px, py, desktopPixel(x+px, y+py))
		}
	}
	return fakeDisplay{index: index, x: x, y: y, img: img}
}

// capture answers screen.capture from the fake displays: the display's
// image, narrowed by nearest pixel to max_width, as PNG.
func (pc *fakePC) capture(params proto.Message) (proto.Message, error) {
	for _, d := range pc.displays {
		if d.index != params.Int("display") {
			continue
		}
		b := d.img.Bounds()
		scale := 1.0
		if mw := params.Int("max_width"); mw > 0 && mw < b.Dx() {
			scale = float64(mw) / float64(b.Dx())
		}
		w, h := int(float64(b.Dx())*scale+0.5), int(float64(b.Dy())*scale+0.5)
		out := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out.SetNRGBA(x, y, d.img.NRGBAAt(int(float64(x)/scale), int(float64(y)/scale)))
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, out); err != nil {
			return nil, err
		}
		return proto.Message{"data": base64.StdEncoding.EncodeToString(buf.Bytes()), "width": float64(w), "height": float64(h),
			"scale": scale, "origin": map[string]any{"x": float64(d.x), "y": float64(d.y)}, "capture_id": "cap-2", "method": "dd"}, nil
	}
	return nil, apierr.New(wire.CodeNotFound, "no such display", false, "")
}

// fakePC answers the agent ops a run sends, from a small model of a
// desktop, a file system and a shell.
type fakePC struct {
	mu      sync.Mutex
	caps    map[string]Capability
	windows []map[string]any
	// displays answer display.list and screen.capture with images; without
	// any, display.list is unknown and screen.capture answers a fixed
	// image.
	displays []fakeDisplay
	// windowsAfter is how many more win.list calls see no window.
	windowsAfter int
	elems        []*fakeElem
	files        map[string][]byte
	utf16        map[string]bool
	// exec answers exec.run; nil answers code 0 with stdout "ok\n".
	exec func(p proto.Message) (proto.Message, error)
	// fail makes an op answer the error.
	fail map[string]error
	// onAct runs after a ui.act.
	onAct func(pc *fakePC, p proto.Message)
	calls []fakeCall
	// started is closed when the first call of op blockOp arrives; the
	// call then waits for its context.
	blockOp string
	started chan struct{}
}

type fakeCall struct {
	op     string
	params proto.Message
	rid    string
}

func newFakePC() *fakePC {
	return &fakePC{
		caps: map[string]Capability{"uia": {State: "ready"}, "exec": {State: "ready"}, "files": {State: "ready"},
			"diag": {State: "ready"}, "screen": {State: "unsupported", Reason: "not implemented in this agent version"},
			"input": {State: "unsupported", Reason: "not implemented in this agent version"}},
		files: map[string][]byte{}, utf16: map[string]bool{}, fail: map[string]error{}, started: make(chan struct{}),
	}
}

func (pc *fakePC) Caps() map[string]Capability {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	out := map[string]Capability{}
	for k, v := range pc.caps {
		out[k] = v
	}
	return out
}

func (pc *fakePC) setCap(name, state string) {
	pc.mu.Lock()
	pc.caps[name] = Capability{State: state}
	pc.mu.Unlock()
}

func (pc *fakePC) ops() []string {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	var out []string
	for _, c := range pc.calls {
		out = append(out, c.op)
	}
	return out
}

func (pc *fakePC) callsOf(op string) []fakeCall {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	var out []fakeCall
	for _, c := range pc.calls {
		if c.op == op {
			out = append(out, c)
		}
	}
	return out
}

func (pc *fakePC) addWindow(id, title, process string, pid int) {
	pc.mu.Lock()
	pc.windows = append(pc.windows, map[string]any{"id": id, "title": title, "process": process, "pid": float64(pid),
		"x": float64(100), "y": float64(50), "w": float64(720), "h": float64(520)})
	pc.mu.Unlock()
}

func (pc *fakePC) addElem(e *fakeElem) {
	pc.mu.Lock()
	pc.elems = append(pc.elems, e)
	pc.mu.Unlock()
}

func (pc *fakePC) setText(name, text string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	for _, e := range pc.elems {
		if e.name == name {
			e.text = text
		}
	}
}

func (pc *fakePC) windowTitle(ref string) (string, bool) {
	if ref == "" || ref == "foreground" {
		if len(pc.windows) == 0 {
			return "", false
		}
		return pc.windows[len(pc.windows)-1]["title"].(string), true
	}
	for _, w := range pc.windows {
		if w["id"] == ref || strings.Contains(strings.ToLower(w["title"].(string)), strings.ToLower(ref)) {
			return w["title"].(string), true
		}
	}
	return "", false
}

// match returns the elements the selector of p names, without hidden ones.
func (pc *fakePC) match(p proto.Message, counting bool) ([]*fakeElem, error) {
	title, ok := pc.windowTitle(p.String("window"))
	if !ok {
		return nil, apierr.New(wire.CodeInvalidArgument, fmt.Sprintf("no window has %q in its title; call win.list to see the open windows", p.String("window")), false, "")
	}
	var out []*fakeElem
	for _, e := range pc.elems {
		if e.window != title {
			continue
		}
		if r := p.String("role"); r != "" && r != e.role {
			continue
		}
		if n := p.String("name"); n != "" && n != e.name {
			continue
		}
		if n := p.String("nameContains"); n != "" && !strings.Contains(strings.ToLower(e.name), strings.ToLower(n)) {
			continue
		}
		if a := p.String("automationId"); a != "" && a != e.aid {
			continue
		}
		if e.hiddenFor > 0 {
			if counting {
				e.hiddenFor--
			}
			continue
		}
		out = append(out, e)
	}
	if p.Has("index") {
		i := p.Int("index")
		if i >= len(out) {
			return nil, nil
		}
		out = out[i : i+1]
	}
	return out, nil
}

func elemNode(e *fakeElem) map[string]any {
	flags := []any{}
	if e.disabled {
		flags = append(flags, "disabled")
	}
	n := map[string]any{"ref": "e-" + e.name, "role": e.role, "name": e.name, "flags": flags, "x": float64(10), "y": float64(20), "w": float64(80), "h": float64(30)}
	if e.aid != "" {
		n["aid"] = e.aid
	}
	if e.role == "edit" && e.text != "" {
		n["value"] = e.text
	}
	return n
}

var errBlockedCall = fmt.Errorf("blocked call ended")

func (pc *fakePC) Call(ctx context.Context, op string, params proto.Message, opts conn.CallOpts) (proto.Message, error) {
	if opts.Ready != nil {
		if err := opts.Ready(); err != nil {
			return nil, err
		}
	}
	rid := opts.RID
	if rid == "" && wire.Journaled(op) {
		rid = conn.NewRID()
	}
	if opts.Info != nil {
		opts.Info.RID = rid
		opts.Info.Sent = true
	}
	if b, err := json.Marshal(params); err == nil {
		params = proto.Message{}
		_ = json.Unmarshal(b, &params)
	}
	pc.mu.Lock()
	pc.calls = append(pc.calls, fakeCall{op: op, params: params, rid: rid})
	block := pc.blockOp == op
	if block {
		pc.blockOp = ""
	}
	failErr := pc.fail[op]
	pc.mu.Unlock()
	if block {
		close(pc.started)
		<-ctx.Done()
		return nil, apierr.New(wire.CodeTimeout, "요청을 기다리는 동안 시간이 다 됐습니다", false, apierr.NextCheckState)
	}
	if failErr != nil {
		return nil, failErr
	}
	if opts.Info != nil {
		opts.Info.Answered = true
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	switch op {
	case "win.list":
		if pc.windowsAfter > 0 {
			pc.windowsAfter--
			return proto.Message{"windows": []any{}}, nil
		}
		list := make([]any, 0, len(pc.windows))
		for _, w := range pc.windows {
			list = append(list, w)
		}
		return proto.Message{"windows": list}, nil
	case "win.action":
		title, ok := pc.windowTitle(params.String("window"))
		if !ok {
			return nil, apierr.New(wire.CodeInvalidArgument, "no window", false, "")
		}
		kept := pc.windows[:0]
		for _, w := range pc.windows {
			if w["title"] != title {
				kept = append(kept, w)
			}
		}
		pc.windows = kept
		return proto.Message{"window": nil}, nil
	case "app.launch":
		w := map[string]any{"id": "0x10", "title": "Deskhand Fixture", "process": "deskhand-fixture.exe", "pid": float64(4242),
			"x": float64(100), "y": float64(50), "w": float64(720), "h": float64(520)}
		pc.windows = append(pc.windows, w)
		return proto.Message{"pid": float64(4242), "window": w}, nil
	case "ui.find":
		found, err := pc.match(params, true)
		if err != nil {
			return nil, err
		}
		nodes := []any{}
		for _, e := range found {
			nodes = append(nodes, elemNode(e))
		}
		return proto.Message{"nodes": nodes}, nil
	case "ui.text":
		found, err := pc.match(params, false)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, apierr.New(wire.CodeInvalidArgument, "no element matches; call ui.tree to see what exists", false, "")
		}
		return proto.Message{"text": found[0].text, "total": float64(len([]rune(found[0].text))), "source": "value"}, nil
	case "ui.rect":
		found, err := pc.match(params, false)
		if err != nil || len(found) == 0 {
			return nil, apierr.New(wire.CodeInvalidArgument, "no element matches", false, "")
		}
		return proto.Message{"node": elemNode(found[0]), "cx": float64(150), "cy": float64(85), "clickable": true}, nil
	case "ui.tree":
		title, ok := pc.windowTitle(params.String("window"))
		if !ok {
			return nil, apierr.New(wire.CodeInvalidArgument, "no window has that title", false, "")
		}
		nodes := []any{map[string]any{"ref": "e-root", "role": "window", "name": title, "depth": float64(0), "flags": []any{}}}
		for _, e := range pc.elems {
			if e.role == "text" && params.String("filter") != "all" {
				continue
			}
			if e.window == title && e.hiddenFor == 0 {
				n := elemNode(e)
				n["depth"] = float64(1)
				nodes = append(nodes, n)
			}
		}
		return proto.Message{"nodes": nodes, "truncated": false}, nil
	case "ui.act":
		found, err := pc.match(params, false)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, apierr.New(wire.CodeInvalidArgument, "no element matches; call ui.tree to see what exists", false, "")
		}
		if params.String("action") == "set_value" {
			found[0].text = params.String("value")
		}
		if pc.onAct != nil {
			pc.onAct(pc, params)
		}
		return proto.Message{"node": elemNode(found[0])}, nil
	case "fs.stat":
		data, ok := pc.files[params.String("path")]
		if !ok {
			return proto.Message{"exists": false, "path": params.String("path")}, nil
		}
		out := proto.Message{"exists": true, "dir": false, "size": float64(len(data)), "path": params.String("path")}
		if params.Bool("sha256") {
			sum := sha256.Sum256(data)
			out["sha256"] = hex.EncodeToString(sum[:])
		}
		return out, nil
	case "fs.read":
		data, ok := pc.files[params.String("path")]
		if !ok {
			return nil, apierr.New(wire.CodeNotFound, "cannot open: the file does not exist", false, "")
		}
		off := params.Int("offset")
		n := params.Int("length")
		if n <= 0 || n > 262144 {
			n = 262144
		}
		end := off + n
		if end > len(data) {
			end = len(data)
		}
		out := proto.Message{"data": base64.StdEncoding.EncodeToString(data[off:end]), "size": float64(len(data)), "eof": end >= len(data)}
		if pc.utf16[params.String("path")] {
			out["encoding"] = "utf-16le"
		}
		return out, nil
	case "fs.mkdir":
		return proto.Message{"path": params.String("path")}, nil
	case "exec.run":
		if pc.exec != nil {
			pc.mu.Unlock()
			res, err := pc.exec(params)
			pc.mu.Lock()
			return res, err
		}
		return proto.Message{"code": float64(0), "stdout": "ok\n", "stderr": "", "timed_out": false, "truncated": false}, nil
	case "screen.capture":
		if params["window"] != nil {
			return nil, apierr.New(wire.CodeCapabilityUnsupported, "screen.capture of one window is not supported by this agent; capture its display and crop the image to the window's x, y, w and h from win.list", false, "")
		}
		if len(pc.displays) > 0 {
			return pc.capture(params)
		}
		return proto.Message{"data": base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfake")), "width": float64(1920), "height": float64(1080),
			"capture_id": "cap-1", "method": "dd"}, nil
	case "display.list":
		if len(pc.displays) == 0 {
			break
		}
		list := make([]any, 0, len(pc.displays))
		for _, d := range pc.displays {
			b := d.img.Bounds()
			list = append(list, map[string]any{"index": float64(d.index), "x": float64(d.x), "y": float64(d.y),
				"width": float64(b.Dx()), "height": float64(b.Dy())})
		}
		return proto.Message{"displays": list}, nil
	case "diag.system":
		return proto.Message{"os": map[string]any{"name": "Windows 11 Pro", "build": "26200.9278"}, "computer_name": "DEV-PC"}, nil
	case "diag.events":
		return proto.Message{"events": []any{map[string]any{"record_id": float64(1), "level": "error", "provider": "App", "message": "boom"}}, "truncated": false}, nil
	case "diag.crashes":
		return proto.Message{"dumps": []any{}}, nil
	}
	return nil, apierr.New(wire.CodeCapabilityUnsupported, "unknown op '"+op+"'; update deskhand-agent", false, apierr.NextHostRepair)
}

type fakeHosts struct{ pc *fakePC }

func (h fakeHosts) Resolve(ref string) (Host, error) {
	if ref != "pc" {
		return Host{}, apierr.NotFound(fmt.Sprintf("'%s' PC가 등록돼 있지 않습니다. host.list로 이름을 확인하세요", ref))
	}
	return Host{HostID: "pc", Name: "pc", OS: "windows", Arch: "amd64", Address: "192.0.2.10", AgentVersion: "test"}, nil
}

func (h fakeHosts) Agent(string) Agent { return h.pc }

// fakeControl answers Check with gen, or with err, and Recheck with
// recheckErr; gens is the next gen of each check when set.
type fakeControl struct {
	err        error
	gen        uint64
	gens       *[]uint64
	recheckErr error
}

func (c fakeControl) Recheck(string, model.Caller, uint64) error { return c.recheckErr }

func (c fakeControl) Check(context.Context, string, model.Caller) (uint64, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.gens != nil && len(*c.gens) > 0 {
		g := (*c.gens)[0]
		*c.gens = (*c.gens)[1:]
		return g, nil
	}
	return c.gen, nil
}

type fakeInput struct {
	mu     sync.Mutex
	clicks [][2]int
	typed  []string
	keys   []string
}

func (in *fakeInput) Click(_ context.Context, _ string, x, y int, _ string, _ int) (map[string]any, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.clicks = append(in.clicks, [2]int{x, y})
	return map[string]any{"executed": true}, nil
}

func (in *fakeInput) Type(_ context.Context, _, text, _ string) (map[string]any, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.typed = append(in.typed, text)
	return map[string]any{"executed": true}, nil
}

func (in *fakeInput) Key(_ context.Context, _, keys string) (map[string]any, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.keys = append(in.keys, keys)
	return map[string]any{"executed": true}, nil
}

type pushCall struct{ local, remote, onConflict string }

type fakePush struct {
	mu    sync.Mutex
	calls []pushCall
	// wait makes Push wait for its context.
	wait    bool
	started chan struct{}
	ended   chan error
}

func (p *fakePush) Push(ctx context.Context, _ string, local, remote, onConflict string, _ model.Caller) (model.TransferDetail, error) {
	p.mu.Lock()
	p.calls = append(p.calls, pushCall{local, remote, onConflict})
	wait := p.wait
	p.mu.Unlock()
	if wait {
		close(p.started)
		<-ctx.Done()
		p.ended <- ctx.Err()
		return model.TransferDetail{Transfer: model.Transfer{TransferID: "t-1", State: "cancelled"}}, ctx.Err()
	}
	data, _ := os.ReadFile(local)
	sum := sha256.Sum256(data)
	return model.TransferDetail{Transfer: model.Transfer{TransferID: "t-1", State: "done", FilesTotal: 1, FilesDone: 1, BytesTotal: int64(len(data))},
		Files: []model.TransferFile{{Rel: "app.exe", Size: int64(len(data)), State: "done", SHA256: hex.EncodeToString(sum[:]), Path: remote}}}, nil
}

type testOpts struct {
	control Control
	input   Input
	push    Pusher
}

type testEnv struct {
	m   *Manager
	st  *store.Store
	bus *events.Bus
	jr  *jobs.Registry
	pc  *fakePC
	dir string
}

func newEnv(t *testing.T, pc *fakePC, o testOpts) *testEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("DESKHAND_HOME", home)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus(st, nil)
	jr := jobs.New(st, bus, nil)
	e := &testEnv{st: st, bus: bus, jr: jr, pc: pc, dir: t.TempDir()}
	if o.push == nil {
		o.push = &fakePush{}
	}
	e.m = e.manager(o)
	t.Cleanup(func() {
		e.m.Close()
		jr.Close()
		st.Close()
	})
	return e
}

func (e *testEnv) manager(o testOpts) *Manager {
	return New(Options{Store: e.st, Bus: e.bus, Jobs: e.jr, Hosts: fakeHosts{e.pc}, Push: o.push, Control: o.control, Input: o.input,
		Version: "test", Poll: 10 * time.Millisecond})
}

var tester = model.Caller{Actor: model.ActorAI, ClientID: "c-1", ClientName: "test"}

func (e *testEnv) start(t *testing.T, src string, vars map[string]string) Run {
	t.Helper()
	run, err := e.m.Start(context.Background(), StartParams{HostRef: "pc", Text: []byte(src), BaseDir: e.dir, Vars: vars}, tester)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return run
}

func (e *testEnv) wait(t *testing.T, runID string) Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		run, err := e.m.Get(runID)
		if err != nil {
			t.Fatal(err)
		}
		if run.FinishedAt != "" {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish: %+v", runID, run)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *testEnv) run(t *testing.T, src string) Run {
	t.Helper()
	return e.wait(t, e.start(t, src, nil).RunID)
}

func statuses(run Run) string {
	var out []string
	for _, s := range run.Steps {
		out = append(out, s.Status)
	}
	return strings.Join(out, " ")
}
