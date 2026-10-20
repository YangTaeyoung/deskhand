package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// record is what one run saw; report reads it back. Times ending in _us
// without "agent" are on the probe clock (nowUS), capture_us,
// encode_done_us, done_us and agent_us on the agent's mono_us.
type record struct {
	Name        string        `json:"name"`
	Scenario    string        `json:"scenario"`
	Addr        string        `json:"addr"`
	Started     string        `json:"started"`
	ClockSource string        `json:"clock_source"`
	TimerResMS  []float64     `json:"timer_resolution_ms"`
	Hello       proto.Message `json:"hello"`
	Session     proto.Message `json:"session,omitempty"`
	SyncBefore  []syncSample  `json:"sync_before"`
	SyncAfter   []syncSample  `json:"sync_after"`
	Fixture     *fixtureInfo  `json:"fixture,omitempty"`
	Display     *displayInfo  `json:"display,omitempty"`
	Video       *videoRecord  `json:"video,omitempty"`
	Inputs      []inputRecord `json:"inputs,omitempty"`
	Keys        []keySample   `json:"keys,omitempty"`
	GPU         []gpuSample   `json:"gpu,omitempty"`
	Skipped     string        `json:"skipped,omitempty"`
	Notes       []string      `json:"notes,omitempty"`
}

// fixtureInfo is the fixture a run started: its window's frame in virtual
// desktop pixels and, for input, the point it clicks.
type fixtureInfo struct {
	Path    string   `json:"path"`
	Args    []string `json:"args"`
	PID     int      `json:"pid"`
	Window  string   `json:"window"`
	X       int      `json:"x"`
	Y       int      `json:"y"`
	W       int      `json:"w"`
	H       int      `json:"h"`
	TargetX int      `json:"target_x,omitempty"`
	TargetY int      `json:"target_y,omitempty"`
}

// displayInfo is the display a run streamed, in virtual desktop pixels.
type displayInfo struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// videoRecord is one video stream: its header, the frames it carried and
// the .h264 file that holds the access units in order.
type videoRecord struct {
	Header   wire.StreamHeader `json:"header"`
	File     string            `json:"file"`
	OpenedUS int64             `json:"opened_us"`
	ClosedUS int64             `json:"closed_us"`
	Infos    []timedInfo       `json:"infos"`
	Stats    []timedStats      `json:"stats"`
	Paused   []timedPaused     `json:"paused"`
	AUs      []auRecord        `json:"aus"`
	Refusal  string            `json:"refusal,omitempty"`
	Error    string            `json:"error,omitempty"`
}

type timedInfo struct {
	RecvUS int64          `json:"recv_us"`
	Info   wire.VideoInfo `json:"info"`
}

type timedStats struct {
	RecvUS int64           `json:"recv_us"`
	Stats  wire.VideoStats `json:"stats"`
}

type timedPaused struct {
	RecvUS int64            `json:"recv_us"`
	Paused wire.VideoPaused `json:"paused"`
}

// auRecord is one access unit without its bytes.
type auRecord struct {
	Seq          uint64 `json:"seq"`
	Key          bool   `json:"key"`
	Config       bool   `json:"config"`
	Width        uint32 `json:"width"`
	Height       uint32 `json:"height"`
	CaptureUS    uint64 `json:"capture_us"`
	EncodeDoneUS uint64 `json:"encode_done_us"`
	RecvUS       int64  `json:"recv_us"`
	Size         int    `json:"size"`
}

// keySample is one key frame of the keyframe scenario and the still image
// taken right after it arrived. Case is start (the first access unit of a
// new stream), keyframe (after a keyframe control), recreate (after a config
// control with the same settings, which makes the encoder again) or resize
// (after a config control with another max_width); SentUS is when the
// stream was opened or the control written.
type keySample struct {
	Case        string `json:"case"`
	N           int    `json:"n"`
	SentUS      int64  `json:"sent_us"`
	Seq         uint64 `json:"seq"`
	CaptureUS   uint64 `json:"capture_us"`
	RecvUS      int64  `json:"recv_us"`
	Size        int    `json:"size"`
	AUFile      string `json:"au_file,omitempty"`
	Still       string `json:"still_file,omitempty"`
	StillMonoUS uint64 `json:"still_mono_us,omitempty"`
	StillMethod string `json:"still_method,omitempty"`
	StillW      int    `json:"still_w,omitempty"`
	StillH      int    `json:"still_h,omitempty"`
	Error       string `json:"error,omitempty"`
}

// inputRecord is one input event and its ack.
type inputRecord struct {
	Seq    uint64 `json:"seq"`
	Kind   string `json:"kind"`
	SentUS int64  `json:"sent_us"`
	AckUS  int64  `json:"ack_us"`
	DoneUS uint64 `json:"done_us"`
	OK     bool   `json:"ok"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

// options are the flags of run.
type options struct {
	addr, idDir, agentFP, out, name, scenario, fixture string
	gpu                                                string
	display, seconds, maxWidth, maxFPS, bitrate        int
	clicks, keys, syncs, repeats, stillWidth           int
	approachMS                                         int
}

func cmdRun(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("run", stderr)
	var o options
	fs.StringVar(&o.addr, "addr", "", "에이전트의 QUIC 주소(호스트:포트)")
	fs.StringVar(&o.idDir, "id", "", "keygen으로 만든 인증서·키 폴더")
	fs.StringVar(&o.agentFP, "agent-fp", "", "에이전트 인증서의 SHA-256 지문")
	fs.StringVar(&o.out, "out", "", "결과(.json, .h264)를 쓸 폴더")
	fs.StringVar(&o.name, "name", "", "결과 파일 이름")
	fs.StringVar(&o.scenario, "scenario", "static", "static, motion, input, click 또는 keyframe")
	fs.StringVar(&o.gpu, "gpu", "", "측정하는 동안 50 ms마다 GPU 클록을 읽을 nvidia-smi 경로(비우면 읽지 않는다)")
	fs.StringVar(&o.fixture, "fixture", "", "에이전트 PC의 deskhand-fixture.exe 경로")
	fs.IntVar(&o.display, "display", 0, "fixture 없이 측정할 디스플레이 인덱스")
	fs.IntVar(&o.seconds, "seconds", 60, "static·motion 수신 시간(초)")
	fs.IntVar(&o.maxWidth, "max-width", 0, "video 헤더 max_width(0은 원본)")
	fs.IntVar(&o.maxFPS, "max-fps", 0, "video 헤더 max_fps(0은 60)")
	fs.IntVar(&o.bitrate, "bitrate", 0, "video 헤더 bitrate(0은 기본값)")
	fs.IntVar(&o.clicks, "clicks", 20, "input에서 누를 클릭 수")
	fs.IntVar(&o.keys, "keys", 20, "input에서 누를 Space 키 수")
	fs.IntVar(&o.approachMS, "approach-ms", 320, "click에서 클릭 전에 포인터를 움직이는 시간(ms, 16 ms마다 한 번)")
	fs.IntVar(&o.syncs, "syncs", 50, "앞뒤로 보낼 time.sync 수")
	fs.IntVar(&o.repeats, "repeats", 10, "keyframe에서 경우마다 받을 키프레임 수")
	fs.IntVar(&o.stillWidth, "still-width", 1920, "keyframe에서 비교할 screen.capture의 max_width")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case o.addr == "" || o.idDir == "" || o.out == "" || o.name == "":
		return fail(stderr, "-addr, -id, -out, -name을 모두 주세요", nil)
	case !validFingerprint(o.agentFP):
		return fail(stderr, "-agent-fp는 deskhand-agent init이 출력한 64자리 16진수 지문이어야 합니다", nil)
	case !slices.Contains([]string{"static", "motion", "input", "click", "keyframe"}, o.scenario):
		return fail(stderr, "-scenario는 static, motion, input, click, keyframe 중 하나여야 합니다", nil)
	case (o.scenario == "input" || o.scenario == "click" || o.scenario == "keyframe") && o.fixture == "":
		return fail(stderr, o.scenario+"은 -fixture가 있어야 합니다. 에이전트 PC의 deskhand-fixture.exe 경로를 주세요", nil)
	}
	rec, err := run(o)
	if rec != nil {
		if werr := writeRecord(filepath.Join(o.out, o.name+".json"), rec); werr != nil {
			return fail(stderr, "결과를 쓰지 못했습니다", werr)
		}
	}
	if err != nil {
		return fail(stderr, "측정을 마치지 못했습니다", err)
	}
	fmt.Fprintf(stdout, "%s: AU %d개, 입력 %d개를 기록했습니다\n", o.name, auCount(rec), len(rec.Inputs))
	if rec.Skipped != "" {
		fmt.Fprintf(stdout, "%s: 건너뜀: %s\n", o.name, rec.Skipped)
	}
	return 0
}

func validFingerprint(fp string) bool {
	if len(fp) != 64 {
		return false
	}
	for _, c := range fp {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func auCount(rec *record) int {
	if rec == nil || rec.Video == nil {
		return 0
	}
	return len(rec.Video.AUs)
}

func writeRecord(path string, rec *record) error {
	b, err := json.MarshalIndent(rec, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// run measures o.scenario and returns what it recorded, with an error when
// the run could not finish; a partial record comes with the error.
func run(o options) (*record, error) {
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return nil, err
	}
	certPath, keyPath := identityPaths(o.idDir)
	id, err := transport.LoadIdentity(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(o.seconds+o.clicks*3+o.keys*3+o.repeats*20+180)*time.Second)
	defer cancel()
	rec := &record{Name: o.name, Scenario: o.scenario, Addr: o.addr, Started: time.Now().Format(time.RFC3339),
		ClockSource: clockSource(), TimerResMS: []float64{timerResolutionMS()}}
	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	conn, err := transport.Dial(dialCtx, o.addr, id, o.agentFP)
	dialCancel()
	if err != nil {
		return rec, err
	}
	defer conn.CloseWithError(0, "screen-probe done")
	rpc, hello, err := openRPC(ctx, conn)
	if err != nil {
		return rec, err
	}
	defer rpc.close()
	rec.Hello = hello
	if s, err := rpc.call("session.info", nil, 10*time.Second); err == nil {
		delete(s, "t")
		delete(s, "id")
		delete(s, "ok")
		rec.Session = s
	}
	if rec.SyncBefore, err = syncRounds(rpc, o.syncs); err != nil {
		return rec, err
	}
	if o.gpu != "" {
		g, err := startGPU(o.gpu, 50)
		if err != nil {
			rec.Notes = append(rec.Notes, "nvidia-smi: "+err.Error())
		} else {
			defer func() { rec.GPU = g.stop() }()
		}
	}

	disp, err := pickDisplay(hello, o.display, nil)
	if o.fixture != "" {
		var args []string
		if o.scenario == "motion" {
			args = []string{"--animate"}
		}
		fx, ferr := launchFixture(rpc, o.fixture, args)
		if fx != nil {
			rec.Fixture = fx
			defer func() {
				if _, err := rpc.call("proc.kill", proto.Message{"pid": fx.PID}, 15*time.Second); err != nil {
					rec.Notes = append(rec.Notes, "fixture proc.kill: "+err.Error())
				}
			}()
		}
		if ferr != nil {
			return rec, ferr
		}
		disp, err = pickDisplay(hello, -1, fx)
	}
	if err != nil {
		return rec, err
	}
	rec.Display = disp

	header := wire.StreamHeader{S: "video", Display: disp.Index, MaxWidth: o.maxWidth, MaxFPS: o.maxFPS, Bitrate: o.bitrate, Codec: "h264"}
	if o.scenario == "keyframe" {
		time.Sleep(time.Second)
		video, runErr := runKeys(ctx, conn, rpc, rec, o, header)
		if video != nil {
			rec.Video = video.rec
			video.stop()
		}
		return rec, finish(rpc, rec, o, runErr)
	}
	video, err := startVideo(ctx, conn, header, filepath.Join(o.out, o.name+".h264"), false)
	if err != nil {
		return rec, err
	}
	rec.Video = video.rec
	var runErr error
	switch o.scenario {
	case "static", "motion":
		select {
		case <-time.After(time.Duration(o.seconds) * time.Second):
		case <-video.done:
		case <-ctx.Done():
			runErr = ctx.Err()
		}
	case "input":
		time.Sleep(2 * time.Second)
		runErr = runInput(ctx, conn, rpc, rec, o)
		time.Sleep(1500 * time.Millisecond)
	case "click":
		time.Sleep(2 * time.Second)
		runErr = runClick(ctx, conn, rpc, rec, o)
		time.Sleep(1500 * time.Millisecond)
	}
	video.stop()
	return rec, finish(rpc, rec, o, runErr)
}

// finish measures the agent clock again after a run and returns runErr.
func finish(rpc *rpcClient, rec *record, o options, runErr error) error {
	if after, err := syncRounds(rpc, o.syncs); err == nil {
		rec.SyncAfter = after
	} else {
		rec.Notes = append(rec.Notes, "time.sync after: "+err.Error())
	}
	rec.TimerResMS = append(rec.TimerResMS, timerResolutionMS())
	return runErr
}

// syncRounds sends n time.sync requests one after another, 5 ms apart.
func syncRounds(rpc *rpcClient, n int) ([]syncSample, error) {
	out := make([]syncSample, 0, n)
	for range n {
		res, sent, recv, err := rpc.callTimed("time.sync", nil, 5*time.Second)
		if err != nil {
			return out, fmt.Errorf("time.sync: %w", err)
		}
		mono, ok := res["mono_us"].(float64)
		if !ok {
			return out, fmt.Errorf("time.sync answered %v without mono_us; run an agent with time.sync", res)
		}
		out = append(out, syncSample{SentUS: sent, RecvUS: recv, AgentUS: uint64(mono)})
		time.Sleep(5 * time.Millisecond)
	}
	return out, nil
}

// pickDisplay returns the display of hello that holds the centre of fx's
// window, or the display with index want when fx is nil.
func pickDisplay(hello proto.Message, want int, fx *fixtureInfo) (*displayInfo, error) {
	raw, _ := json.Marshal(hello["displays"])
	var list []displayInfo
	if err := json.Unmarshal(raw, &list); err != nil || len(list) == 0 {
		return nil, fmt.Errorf("the agent's hello lists no displays (%s); start the agent in the signed-in user's session", raw)
	}
	for i := range list {
		d := &list[i]
		if fx == nil && d.Index == want {
			return d, nil
		}
		if fx != nil {
			cx, cy := fx.X+fx.W/2, fx.Y+fx.H/2
			if cx >= d.X && cy >= d.Y && cx < d.X+d.Width && cy < d.Y+d.Height {
				return d, nil
			}
		}
	}
	if fx != nil {
		return nil, fmt.Errorf("no display holds the centre of the fixture window at (%d,%d) %dx%d", fx.X, fx.Y, fx.W, fx.H)
	}
	return nil, fmt.Errorf("the agent has no display %d", want)
}

// launchFixture starts the fixture through app.launch, brings its window to
// the foreground and reads its frame.
func launchFixture(rpc *rpcClient, path string, args []string) (*fixtureInfo, error) {
	res, err := rpc.call("app.launch", proto.Message{"path": path, "args": args, "wait_window_ms": 15000}, 40*time.Second)
	if err != nil {
		return nil, fmt.Errorf("app.launch %s: %w", path, err)
	}
	fx := &fixtureInfo{Path: path, Args: args, PID: res.Int("pid")}
	w, _ := res["window"].(map[string]any)
	if w == nil {
		return fx, fmt.Errorf("the fixture (pid %d) showed no window within 15 s", fx.PID)
	}
	fx.Window, _ = w["id"].(string)
	if _, err := rpc.call("win.action", proto.Message{"action": "focus", "window": fx.Window}, 20*time.Second); err != nil {
		return fx, fmt.Errorf("win.action focus on the fixture: %w", err)
	}
	time.Sleep(1500 * time.Millisecond)
	win, err := findWindow(rpc, fx.Window)
	if err != nil {
		return fx, err
	}
	fx.X, fx.Y, fx.W, fx.H = win.Int("x"), win.Int("y"), win.Int("w"), win.Int("h")
	return fx, nil
}

// findWindow returns the win.list entry with id.
func findWindow(rpc *rpcClient, id string) (proto.Message, error) {
	res, err := rpc.call("win.list", nil, 20*time.Second)
	if err != nil {
		return nil, fmt.Errorf("win.list: %w", err)
	}
	list, _ := res["windows"].([]any)
	for _, item := range list {
		if w, ok := item.(map[string]any); ok && w["id"] == id {
			return proto.Message(w), nil
		}
	}
	return nil, fmt.Errorf("window %s is not in win.list", id)
}

// foregroundWindow returns the id and title of the window win.list marks
// as the foreground.
func foregroundWindow(rpc *rpcClient) (id, title string, err error) {
	res, err := rpc.call("win.list", nil, 20*time.Second)
	if err != nil {
		return "", "", fmt.Errorf("win.list: %w", err)
	}
	list, _ := res["windows"].([]any)
	for _, item := range list {
		if w, ok := item.(map[string]any); ok && w["foreground"] == true {
			id, _ = w["id"].(string)
			title, _ = w["title"].(string)
			return id, title, nil
		}
	}
	return "", "", nil
}

// rpcClient is the probe's side of the rpc stream.
type rpcClient struct {
	st      *quic.Stream
	mu      sync.Mutex
	nextID  int
	waiting map[int]chan reply
	done    chan struct{}
	err     error
}

// reply is an answer and the probe clock when it was read.
type reply struct {
	m      proto.Message
	recvUS int64
}

// openRPC opens the rpc stream, reads the hello and answers with a welcome.
func openRPC(ctx context.Context, conn *quic.Conn) (*rpcClient, proto.Message, error) {
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := wire.WriteHeader(st, map[string]string{"s": "rpc"}); err != nil {
		return nil, nil, err
	}
	sc := bufio.NewScanner(st)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	_ = st.SetReadDeadline(time.Now().Add(15 * time.Second))
	if !sc.Scan() {
		return nil, nil, fmt.Errorf("no hello from the agent: %v", sc.Err())
	}
	_ = st.SetReadDeadline(time.Time{})
	var hello proto.Message
	if err := json.Unmarshal(sc.Bytes(), &hello); err != nil || hello.Type() != "hello" {
		return nil, nil, fmt.Errorf("the agent's first line is not a hello: %s", sc.Bytes())
	}
	c := &rpcClient{st: st, waiting: map[int]chan reply{}, done: make(chan struct{})}
	line, _ := proto.Encode(proto.Message{"t": "welcome", "v": 1, "controller": "screen-probe"})
	if _, err := st.Write(line); err != nil {
		return nil, nil, err
	}
	go c.readLoop(sc)
	return c, hello, nil
}

func (c *rpcClient) readLoop(sc *bufio.Scanner) {
	defer close(c.done)
	for sc.Scan() {
		now := nowUS()
		var m proto.Message
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.Type() != "res" {
			continue
		}
		c.mu.Lock()
		ch := c.waiting[m.Int("id")]
		delete(c.waiting, m.Int("id"))
		c.mu.Unlock()
		if ch != nil {
			ch <- reply{m: m, recvUS: now}
		}
	}
	c.err = sc.Err()
	if c.err == nil {
		c.err = io.EOF
	}
}

// call sends op with params and returns a successful answer.
func (c *rpcClient) call(op string, params proto.Message, timeout time.Duration) (proto.Message, error) {
	m, _, _, err := c.callTimed(op, params, timeout)
	return m, err
}

// callTimed is call that also returns the probe clock when the request was
// written and when the answer was read.
func (c *rpcClient) callTimed(op string, params proto.Message, timeout time.Duration) (proto.Message, int64, int64, error) {
	req := proto.Message{"t": "req", "op": op}
	for k, v := range params {
		req[k] = v
	}
	ch := make(chan reply, 1)
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	req["id"] = id
	line, err := proto.Encode(req)
	if err != nil {
		c.mu.Unlock()
		return nil, 0, 0, err
	}
	c.waiting[id] = ch
	sent := nowUS()
	_, err = c.st.Write(line)
	c.mu.Unlock()
	if err != nil {
		return nil, 0, 0, err
	}
	select {
	case r := <-ch:
		if r.m["ok"] != true {
			return r.m, sent, r.recvUS, fmt.Errorf("%s: %s %s", op, r.m.String("code"), r.m.String("error"))
		}
		return r.m, sent, r.recvUS, nil
	case <-c.done:
		return nil, 0, 0, fmt.Errorf("%s: the rpc stream ended (%v)", op, c.err)
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
		return nil, 0, 0, fmt.Errorf("%s: no answer within %s", op, timeout)
	}
}

func (c *rpcClient) close() {
	c.st.CancelRead(0)
	_ = c.st.Close()
}

// videoRun records a video stream until stop.
type videoRun struct {
	rec  *videoRecord
	st   *quic.Stream
	f    *os.File
	w    *bufio.Writer
	done chan struct{}
	// keys receives the key access units with their bytes when the run
	// watches them, and is nil otherwise.
	keys chan keyAU
}

// keyAU is a key access unit and its Annex-B bytes.
type keyAU struct {
	rec  auRecord
	data []byte
}

// startVideo opens a video stream with header and records its frames, the
// access units' bytes in order to path unless it is empty; with keys the
// key access units also go to the run's keys channel.
func startVideo(ctx context.Context, conn *quic.Conn, header wire.StreamHeader, path string, keys bool) (*videoRun, error) {
	v := &videoRun{rec: &videoRecord{Header: header}, done: make(chan struct{})}
	if path != "" {
		f, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		v.f, v.w, v.rec.File = f, bufio.NewWriterSize(f, 1<<20), filepath.Base(path)
	}
	if keys {
		v.keys = make(chan keyAU, 64)
	}
	closeFile := func() {
		if v.f != nil {
			v.f.Close()
		}
	}
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		closeFile()
		return nil, err
	}
	v.st = st
	v.rec.OpenedUS = nowUS()
	if err := wire.WriteHeader(st, header); err != nil {
		closeFile()
		return nil, err
	}
	go v.loop(bufio.NewReaderSize(st, 1<<20))
	return v, nil
}

// control writes c on the stream.
func (v *videoRun) control(c wire.VideoControl) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return wire.WriteFrame(v.st, wire.KindClientControl, b)
}

// nextKey returns the first key access unit with a seq above after that
// arrives within timeout.
func (v *videoRun) nextKey(after uint64, timeout time.Duration) (keyAU, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case k := <-v.keys:
			if k.rec.Seq > after {
				return k, nil
			}
		case <-v.done:
			return keyAU{}, fmt.Errorf("the video stream ended before a key frame after AU %d (%s%s)", after, v.rec.Refusal, v.rec.Error)
		case <-timer.C:
			return keyAU{}, fmt.Errorf("no key frame after AU %d within %v", after, timeout)
		}
	}
}

func (v *videoRun) loop(br *bufio.Reader) {
	defer close(v.done)
	if b, err := br.Peek(1); err == nil && b[0] == '{' {
		line, _ := br.ReadString('\n')
		v.rec.Refusal = strings.TrimSpace(line)
		return
	}
	for {
		kind, payload, err := wire.ReadFrame(br, wire.MaxFrame)
		now := nowUS()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				v.rec.Error = err.Error()
			}
			return
		}
		switch kind {
		case wire.KindAU:
			au, err := wire.UnmarshalAU(payload)
			if err != nil {
				v.rec.Error = err.Error()
				return
			}
			r := auRecord{Seq: au.Seq, Key: au.Key, Config: au.HasConfig, Width: au.Width, Height: au.Height,
				CaptureUS: au.CaptureMonoUS, EncodeDoneUS: au.EncodeDoneMonoUS, RecvUS: now, Size: len(au.Data)}
			v.rec.AUs = append(v.rec.AUs, r)
			if v.w != nil {
				if _, err := v.w.Write(au.Data); err != nil {
					v.rec.Error = err.Error()
					return
				}
			}
			if v.keys != nil && au.Key {
				select {
				case v.keys <- keyAU{rec: r, data: bytes.Clone(au.Data)}:
				default:
				}
			}
		case wire.KindVideoInfo:
			var info wire.VideoInfo
			_ = json.Unmarshal(payload, &info)
			v.rec.Infos = append(v.rec.Infos, timedInfo{RecvUS: now, Info: info})
		case wire.KindVideoStats:
			var s wire.VideoStats
			_ = json.Unmarshal(payload, &s)
			v.rec.Stats = append(v.rec.Stats, timedStats{RecvUS: now, Stats: s})
		case wire.KindVideoPaused:
			var p wire.VideoPaused
			_ = json.Unmarshal(payload, &p)
			v.rec.Paused = append(v.rec.Paused, timedPaused{RecvUS: now, Paused: p})
		}
	}
}

// stop closes the probe's direction of the stream, waits up to 5 s for the
// agent to end it and closes the file.
func (v *videoRun) stop() {
	_ = v.st.Close()
	select {
	case <-v.done:
	case <-time.After(5 * time.Second):
		v.st.CancelRead(0)
		<-v.done
	}
	v.rec.ClosedUS = nowUS()
	if v.f != nil {
		_ = v.w.Flush()
		_ = v.f.Close()
	}
}

// resizeWidth is the max_width the resize case of the keyframe scenario
// switches to and back from.
const resizeWidth = 1920

// runKeys takes o.repeats key frames in each case of the keyframe scenario
// and a still image with screen.capture right after each: the first access
// unit of a new stream, then on one stream the key frame of a keyframe
// control, the one after a config control with the stream's settings, which
// makes the encoder again, and the one after a config control that switches
// max_width between resizeWidth and the stream's. It returns that stream,
// whose access units go to NAME.h264; each key frame goes to
// NAME_<case>_<n>.h264 and its still to NAME_<case>_<n>.png.
func runKeys(ctx context.Context, conn *quic.Conn, rpc *rpcClient, rec *record, o options, header wire.StreamHeader) (*videoRun, error) {
	keep := func(s *keySample, k keyAU) error {
		s.Seq, s.CaptureUS, s.RecvUS, s.Size = k.rec.Seq, k.rec.CaptureUS, k.rec.RecvUS, k.rec.Size
		s.AUFile = fmt.Sprintf("%s_%s_%02d.h264", o.name, s.Case, s.N)
		if err := os.WriteFile(filepath.Join(o.out, s.AUFile), k.data, 0o644); err != nil {
			return err
		}
		res, err := rpc.call("screen.capture", proto.Message{"display": header.Display, "format": "png", "max_width": o.stillWidth}, 30*time.Second)
		if err != nil {
			s.Error = err.Error()
			return nil
		}
		data, err := base64.StdEncoding.DecodeString(res.String("data"))
		if err != nil {
			s.Error = "screen.capture data: " + err.Error()
			return nil
		}
		s.Still = fmt.Sprintf("%s_%s_%02d.png", o.name, s.Case, s.N)
		if mono, ok := res["mono_us"].(float64); ok {
			s.StillMonoUS = uint64(mono)
		}
		s.StillMethod, s.StillW, s.StillH = res.String("method"), res.Int("width"), res.Int("height")
		return os.WriteFile(filepath.Join(o.out, s.Still), data, 0o644)
	}
	for i := range o.repeats {
		s := keySample{Case: "start", N: i, SentUS: nowUS()}
		v, err := startVideo(ctx, conn, header, "", true)
		if err != nil {
			return nil, err
		}
		k, err := v.nextKey(0, 15*time.Second)
		if err == nil {
			err = keep(&s, k)
		} else {
			s.Error = err.Error()
			err = nil
		}
		v.stop()
		rec.Keys = append(rec.Keys, s)
		if err != nil {
			return nil, err
		}
		time.Sleep(700 * time.Millisecond)
	}
	v, err := startVideo(ctx, conn, header, filepath.Join(o.out, o.name+".h264"), true)
	if err != nil {
		return nil, err
	}
	first, err := v.nextKey(0, 15*time.Second)
	if err != nil {
		return v, err
	}
	last := first.rec.Seq
	time.Sleep(1500 * time.Millisecond)
	resized := header.MaxWidth
	for _, c := range []struct {
		name string
		ctl  func() wire.VideoControl
	}{
		{"keyframe", func() wire.VideoControl { return wire.VideoControl{Op: wire.ControlKeyframe} }},
		{"recreate", func() wire.VideoControl {
			return wire.VideoControl{Op: wire.ControlConfig, MaxFPS: header.MaxFPS, MaxWidth: header.MaxWidth}
		}},
		{"resize", func() wire.VideoControl {
			if resized == header.MaxWidth {
				resized = resizeWidth
			} else {
				resized = header.MaxWidth
			}
			return wire.VideoControl{Op: wire.ControlConfig, MaxFPS: header.MaxFPS, MaxWidth: resized}
		}},
	} {
		for i := range o.repeats {
			if ctx.Err() != nil {
				return v, ctx.Err()
			}
			s := keySample{Case: c.name, N: i, SentUS: nowUS()}
			if err := v.control(c.ctl()); err != nil {
				return v, err
			}
			k, err := v.nextKey(last, 10*time.Second)
			if err == nil {
				last = k.rec.Seq
				err = keep(&s, k)
			} else {
				s.Error = err.Error()
				err = nil
			}
			rec.Keys = append(rec.Keys, s)
			if err != nil {
				return v, err
			}
			time.Sleep(time.Second)
		}
	}
	return v, nil
}

// inputRun is an input stream that sends one event at a time and reads its
// ack.
type inputRun struct {
	st  *quic.Stream
	br  *bufio.Reader
	seq uint64
	gen uint64
}

// openInput opens an input stream with gen 0 and reads its accept line.
func openInput(ctx context.Context, conn *quic.Conn) (*inputRun, error) {
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	if err := wire.WriteHeader(st, map[string]any{"s": "input", "gen": 0}); err != nil {
		return nil, err
	}
	br := bufio.NewReader(st)
	_ = st.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := br.ReadString('\n')
	_ = st.SetReadDeadline(time.Time{})
	if err != nil {
		return nil, fmt.Errorf("no accept line on the input stream: %w", err)
	}
	var accept struct {
		OK    bool   `json:"ok"`
		Gen   uint64 `json:"gen"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &accept); err != nil || !accept.OK {
		return nil, fmt.Errorf("the input stream was refused: %s", strings.TrimSpace(line))
	}
	return &inputRun{st: st, br: br, gen: accept.Gen}, nil
}

// send writes ev with the next seq and returns it with its ack.
func (in *inputRun) send(ev wire.InputEvent, kind string) (inputRecord, error) {
	in.seq++
	ev.Seq, ev.Gen = in.seq, in.gen
	payload, err := json.Marshal(ev)
	if err != nil {
		return inputRecord{}, err
	}
	r := inputRecord{Seq: ev.Seq, Kind: kind, SentUS: nowUS()}
	if err := wire.WriteFrame(in.st, wire.KindClientInput, payload); err != nil {
		return r, err
	}
	_ = in.st.SetReadDeadline(time.Now().Add(10 * time.Second))
	k, p, err := wire.ReadFrame(in.br, wire.MaxFrame)
	r.AckUS = nowUS()
	_ = in.st.SetReadDeadline(time.Time{})
	if err != nil {
		return r, fmt.Errorf("no ack for input %d: %w", ev.Seq, err)
	}
	var ack wire.InputAck
	if k != wire.KindInputAck || json.Unmarshal(p, &ack) != nil || ack.Seq != ev.Seq {
		return r, fmt.Errorf("frame %#x %s is not the ack of input %d", k, p, ev.Seq)
	}
	r.OK, r.DoneUS, r.Code, r.Error = ack.OK, ack.DoneMonoUS, ack.Code, ack.Error
	if !ack.OK {
		return r, fmt.Errorf("input %d (%s) failed: %s %s", ev.Seq, kind, ack.Code, ack.Error)
	}
	return r, nil
}

// close ends the probe's direction and waits up to 5 s for the agent to
// end the stream, which releases what the stream holds pressed.
func (in *inputRun) close() {
	_ = in.st.Close()
	_ = in.st.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.Copy(io.Discard, in.br)
	in.st.CancelRead(0)
}

// Input timing of runInput and runClick.
const (
	pressHold   = 400 * time.Millisecond
	pressRest   = 600 * time.Millisecond
	warmupSleep = 1500 * time.Millisecond
	vkSpace     = 0x20
	// clickRest is the least time runClick lets pass between the last
	// input and the first move of the next click.
	clickRest = 2500 * time.Millisecond
	// The pointer moves of a click, approachStep apart, take the pointer
	// from approachFrom of the button's half width left of its centre to
	// the centre.
	approachStep = 16 * time.Millisecond
	approachFrom = 0.7
)

// errGuard marks a run stopped by a guard; the record says why.
var errGuard = errors.New("guard")

// inputSession is an input stream aimed at the centre (tx, ty) of the
// fixture's first Same button, w×h, with the guards every input passes.
type inputSession struct {
	in      *inputRun
	rec     *record
	rpc     *rpcClient
	tx, ty  int
	w, h    int
	display int
	dispX   int
	dispY   int
}

// openInputSession finds the fixture's first Same button and opens an
// input stream; a nil session without an error means the run records why
// it was skipped.
func openInputSession(ctx context.Context, conn *quic.Conn, rpc *rpcClient, rec *record) (*inputSession, error) {
	fx, disp := rec.Fixture, rec.Display
	res, err := rpc.call("ui.find", proto.Message{"window": fx.Window, "name": "Same", "index": 0}, 20*time.Second)
	if err != nil {
		rec.Skipped = "the fixture's Same button was not found through ui.find: " + err.Error()
		return nil, nil
	}
	nodes, _ := res["nodes"].([]any)
	if len(nodes) == 0 {
		rec.Skipped = "ui.find found no Same button in the fixture"
		return nil, nil
	}
	node := proto.Message(nodes[0].(map[string]any))
	tx, ty := node.Int("x")+node.Int("w")/2, node.Int("y")+node.Int("h")/2
	if node.Int("w") <= 0 || tx < fx.X || ty < fx.Y || tx >= fx.X+fx.W || ty >= fx.Y+fx.H ||
		tx < disp.X || ty < disp.Y || tx >= disp.X+disp.Width || ty >= disp.Y+disp.Height {
		rec.Skipped = fmt.Sprintf("the Same button's centre (%d,%d) is outside the fixture window or its display", tx, ty)
		return nil, nil
	}
	fx.TargetX, fx.TargetY = tx, ty
	in, err := openInput(ctx, conn)
	if err != nil {
		return nil, err
	}
	return &inputSession{in: in, rec: rec, rpc: rpc, tx: tx, ty: ty, w: node.Int("w"), h: node.Int("h"),
		display: disp.Index, dispX: disp.X, dispY: disp.Y}, nil
}

// foreground fails unless the fixture's window is in the foreground.
func (s *inputSession) foreground() error {
	id, title, err := foregroundWindow(s.rpc)
	if err != nil {
		return err
	}
	if id != s.rec.Fixture.Window {
		return fmt.Errorf("%w: the foreground window is %s %q, not the fixture %s", errGuard, id, title, s.rec.Fixture.Window)
	}
	return nil
}

// overTarget fails unless the element at the target point is the
// fixture's Same button.
func (s *inputSession) overTarget() error {
	at, err := s.rpc.call("ui.at", proto.Message{"x": s.tx, "y": s.ty}, 20*time.Second)
	if err != nil {
		return fmt.Errorf("%w: ui.at (%d,%d): %v", errGuard, s.tx, s.ty, err)
	}
	w, _ := at["window"].(map[string]any)
	n, _ := at["node"].(map[string]any)
	if w == nil || w["id"] != s.rec.Fixture.Window || n == nil || n["name"] != "Same" {
		return fmt.Errorf("%w: the element at (%d,%d) is %v in window %v, not the fixture's Same button", errGuard, s.tx, s.ty, n, w)
	}
	return nil
}

// focusedOnTarget fails unless the focused element is the fixture's Same
// button that holds the target point.
func (s *inputSession) focusedOnTarget() error {
	at, err := s.rpc.call("ui.focused", nil, 20*time.Second)
	if err != nil {
		return fmt.Errorf("%w: ui.focused: %v", errGuard, err)
	}
	w, _ := at["window"].(map[string]any)
	n, _ := at["node"].(map[string]any)
	if w == nil || w["id"] != s.rec.Fixture.Window || n == nil || n["name"] != "Same" {
		return fmt.Errorf("%w: the focused element is %v in window %v, not the fixture's Same button", errGuard, n, w)
	}
	node := proto.Message(n)
	if x, y := node.Int("x"), node.Int("y"); s.tx < x || s.ty < y || s.tx >= x+node.Int("w") || s.ty >= y+node.Int("h") {
		return fmt.Errorf("%w: the focused Same button at (%d,%d) %dx%d is not the one at (%d,%d)", errGuard, x, y, node.Int("w"), node.Int("h"), s.tx, s.ty)
	}
	return nil
}

// send injects ev and records it as kind.
func (s *inputSession) send(ev wire.InputEvent, kind string) error {
	r, err := s.in.send(ev, kind)
	s.rec.Inputs = append(s.rec.Inputs, r)
	return err
}

// moveTo moves the pointer to (x, y) of the virtual desktop, recorded as
// kind.
func (s *inputSession) moveTo(x, y int, kind string) error {
	return s.send(wire.InputEvent{K: "move", Display: s.display, X: x - s.dispX, Y: y - s.dispY}, kind)
}

// press checks the foreground and the point (guardPoint) or the focus,
// sends down, waits pressHold, checks the foreground again and sends up;
// a press that is not measured is recorded with the suffix _warmup.
func (s *inputSession) press(kind string, down, up wire.InputEvent, guardPoint, measured bool) error {
	if err := s.foreground(); err != nil {
		return err
	}
	if guardPoint {
		if err := s.overTarget(); err != nil {
			return err
		}
	} else if err := s.focusedOnTarget(); err != nil {
		return err
	}
	suffix := ""
	if !measured {
		suffix = "_warmup"
	}
	if err := s.send(down, kind+"_down"+suffix); err != nil {
		return err
	}
	time.Sleep(pressHold)
	if err := s.foreground(); err != nil {
		return err
	}
	return s.send(up, kind+"_up"+suffix)
}

// stop turns a guard's error into the record's reason to skip the rest.
func (s *inputSession) stop(err error) error {
	if errors.Is(err, errGuard) {
		s.rec.Skipped = fmt.Sprintf("stopped after %d inputs: %v", len(s.rec.Inputs), err)
		return nil
	}
	return err
}

func clickEvents() (wire.InputEvent, wire.InputEvent) {
	return wire.InputEvent{K: "button", B: "left", Down: true}, wire.InputEvent{K: "button", B: "left"}
}

// warmUp moves the pointer to the target and clicks once without
// measuring, which gives the button the focus.
func (s *inputSession) warmUp() error {
	if err := s.foreground(); err != nil {
		return err
	}
	if err := s.moveTo(s.tx, s.ty, "move"); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	d, u := clickEvents()
	if err := s.press("click", d, u, true, false); err != nil {
		return err
	}
	time.Sleep(pressRest + warmupSleep)
	return nil
}

// runInput clicks the fixture's Same button o.clicks times and presses
// Space on it o.keys times, after a warm-up click that is not measured.
func runInput(ctx context.Context, conn *quic.Conn, rpc *rpcClient, rec *record, o options) error {
	s, err := openInputSession(ctx, conn, rpc, rec)
	if s == nil || err != nil {
		return err
	}
	defer s.in.close()
	if err := s.warmUp(); err != nil {
		return s.stop(err)
	}
	space := func() (wire.InputEvent, wire.InputEvent) {
		return wire.InputEvent{K: "key", VK: vkSpace, Down: true}, wire.InputEvent{K: "key", VK: vkSpace}
	}
	for range o.clicks {
		d, u := clickEvents()
		if err := s.press("click", d, u, true, true); err != nil {
			return s.stop(err)
		}
		time.Sleep(pressRest)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	for range o.keys {
		d, u := space()
		if err := s.press("key", d, u, false, true); err != nil {
			return s.stop(err)
		}
		time.Sleep(pressRest)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// runClick clicks the fixture's Same button o.clicks times the way a
// person does, after a warm-up click that is not measured: it lets
// clickRest pass after the input before, moves the pointer for
// o.approachMS in steps approachStep apart from the button's left part to
// its centre, staying on the button, and then clicks.
func runClick(ctx context.Context, conn *quic.Conn, rpc *rpcClient, rec *record, o options) error {
	s, err := openInputSession(ctx, conn, rpc, rec)
	if s == nil || err != nil {
		return err
	}
	defer s.in.close()
	if err := s.warmUp(); err != nil {
		return s.stop(err)
	}
	fromX := s.tx - int(approachFrom*float64(s.w)/2)
	moves := max(2, int(time.Duration(o.approachMS)*time.Millisecond/approachStep)+1)
	for range o.clicks {
		last := rec.Inputs[len(rec.Inputs)-1]
		if wait := time.Duration(last.SentUS+int64(clickRest/time.Microsecond)-nowUS()) * time.Microsecond; wait > 0 {
			time.Sleep(wait)
		}
		if err := s.foreground(); err != nil {
			return s.stop(err)
		}
		for i := range moves {
			x := fromX + (s.tx-fromX)*i/(moves-1)
			if err := s.moveTo(x, s.ty, "pointer_move"); err != nil {
				return err
			}
			if i < moves-1 {
				time.Sleep(approachStep)
			}
		}
		d, u := clickEvents()
		if err := s.press("click", d, u, true, true); err != nil {
			return s.stop(err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}
