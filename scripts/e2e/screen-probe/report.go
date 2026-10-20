package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// reportOptions are the flags of report.
type reportOptions struct {
	dir              string
	delta, minPixels int
	windowMS         int
	keyMAD, keyLit   float64
}

func cmdReport(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("report", stderr)
	var o reportOptions
	fs.StringVar(&o.dir, "dir", "", "run이 쓴 .json·.h264 폴더")
	fs.IntVar(&o.delta, "delta", 24, "바뀐 픽셀로 셀 밝기 차이(0–255)")
	fs.IntVar(&o.minPixels, "min-pixels", 40, "화면 변화로 볼 바뀐 픽셀 수")
	fs.IntVar(&o.windowMS, "window-ms", 1000, "입력 뒤 화면 변화를 찾는 시간(ms)")
	fs.Float64Var(&o.keyMAD, "key-mad", 8, "키프레임과 screen.capture의 fixture 영역 평균 절대 차이 상한(0–255)")
	fs.Float64Var(&o.keyLit, "key-lit", 0.01, "검은 화면이 아닌 키프레임의 밝기 24 넘는 값 비율 하한")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if o.dir == "" {
		return fail(stderr, "-dir이 비어 있습니다. run의 -out 폴더를 주세요", nil)
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fail(stderr, tool+"를 찾지 못했습니다. ffmpeg를 설치하고(brew install ffmpeg) 다시 실행하세요", nil)
		}
	}
	paths, _ := filepath.Glob(filepath.Join(o.dir, "*.json"))
	var reports []recordReport
	for _, p := range paths {
		if filepath.Base(p) == "report.json" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return fail(stderr, p+"를 읽지 못했습니다", err)
		}
		var rec record
		if err := json.Unmarshal(b, &rec); err != nil {
			return fail(stderr, p+"가 run의 결과가 아닙니다", err)
		}
		rep, err := analyze(&rec, o)
		if err != nil {
			return fail(stderr, p+"를 분석하지 못했습니다", err)
		}
		reports = append(reports, rep)
		printReport(stdout, rep)
	}
	if len(reports) == 0 {
		return fail(stderr, o.dir+"에 run의 결과(.json)가 없습니다", nil)
	}
	b, _ := json.MarshalIndent(reports, "", " ")
	if err := os.WriteFile(filepath.Join(o.dir, "report.json"), append(b, '\n'), 0o644); err != nil {
		return fail(stderr, "report.json을 쓰지 못했습니다", err)
	}
	return 0
}

// recordReport is the analysis of one record; durations are milliseconds.
type recordReport struct {
	Name        string        `json:"name"`
	Scenario    string        `json:"scenario"`
	Session     string        `json:"session"`
	ClockSource string        `json:"clock_source"`
	TimerResMS  []float64     `json:"timer_resolution_ms"`
	SyncBefore  *clockOffset  `json:"sync_before,omitempty"`
	SyncAfter   *clockOffset  `json:"sync_after,omitempty"`
	DriftUS     float64       `json:"drift_us"`
	Video       *videoReport  `json:"video,omitempty"`
	Decode      *decodeReport `json:"decode,omitempty"`
	Input       *inputReport  `json:"input,omitempty"`
	Keys        *keyReport    `json:"keys,omitempty"`
	Skipped     string        `json:"skipped,omitempty"`
	Notes       []string      `json:"notes,omitempty"`
}

type videoReport struct {
	Display          int      `json:"display"`
	Encoder          string   `json:"encoder"`
	Hardware         bool     `json:"hardware"`
	Capture          string   `json:"capture"`
	Width            int      `json:"width"`
	Height           int      `json:"height"`
	SrcWidth         int      `json:"src_width"`
	SrcHeight        int      `json:"src_height"`
	TargetBitrate    int      `json:"target_bitrate"`
	Refusal          string   `json:"refusal,omitempty"`
	Error            string   `json:"error,omitempty"`
	Paused           []string `json:"paused,omitempty"`
	SpanS            float64  `json:"span_s"`
	AUs              int      `json:"aus"`
	Keyframes        int      `json:"keyframes"`
	FPS              float64  `json:"fps"`
	BitrateBPS       float64  `json:"bitrate_bps"`
	Repeats          int      `json:"repeats"`
	RepeatsChecked   bool     `json:"repeats_checked"`
	FirstAUMS        float64  `json:"first_au_ms"`
	FirstAUBytes     int      `json:"first_au_bytes"`
	AUBytes          summary  `json:"au_bytes"`
	KeyBytes         summary  `json:"key_bytes"`
	CaptureToRecv    summary  `json:"capture_to_recv_ms"`
	CaptureToEncoded summary  `json:"capture_to_encoded_ms"`
	EncodedToRecv    summary  `json:"encoded_to_recv_ms"`
	StatsFPS         summary  `json:"stats_fps"`
	StatsBitrate     summary  `json:"stats_bitrate"`
	StatsEncodeP50   summary  `json:"stats_encode_ms_p50"`
	StatsEncodeP95   summary  `json:"stats_encode_ms_p95"`
	StatsCaptureP50  summary  `json:"stats_capture_ms_p50"`
	Dropped          int      `json:"dropped"`
	EncoderBusy      int      `json:"encoder_busy"`
}

type decodeReport struct {
	Errors     int    `json:"errors"`
	FirstError string `json:"first_error,omitempty"`
	Frames     int    `json:"frames"`
	Codec      string `json:"codec"`
	Profile    string `json:"profile"`
	Level      int    `json:"level"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	ColorRange string `json:"color_range"`
	ColorSpace string `json:"color_space"`
}

type inputReport struct {
	Crop      [4]int                 `json:"crop"`
	Delta     int                    `json:"delta"`
	MinPixels int                    `json:"min_pixels"`
	Kinds     map[string]*inputKinds `json:"kinds"`
	Order     []string               `json:"order"`
}

type inputKinds struct {
	Events     int     `json:"events"`
	Failed     int     `json:"failed"`
	Matched    int     `json:"matched"`
	AckRTT     summary `json:"ack_rtt_ms"`
	SendToDone summary `json:"send_to_done_ms"`
	ToChange   summary `json:"send_to_change_recv_ms"`
	DoneToCap  summary `json:"done_to_change_capture_ms"`
	Pixels     summary `json:"changed_pixels"`
	MaxBelow   int     `json:"max_below"`
}

// analyze computes the report of rec; the .h264 next to it is decoded.
func analyze(rec *record, o reportOptions) (recordReport, error) {
	rep := recordReport{Name: rec.Name, Scenario: rec.Scenario, ClockSource: rec.ClockSource, TimerResMS: rec.TimerResMS,
		Skipped: rec.Skipped, Notes: rec.Notes}
	if rec.Session["session_id"] != nil {
		rep.Session = fmt.Sprintf("session %v %v %v locked=%v desktop=%v", rec.Session["session_id"], rec.Session["protocol"],
			rec.Session["connect_state"], rec.Session["locked"], rec.Session["input_desktop"])
	}
	before, okBefore := estimateOffset(rec.SyncBefore)
	after, okAfter := estimateOffset(rec.SyncAfter)
	if okBefore {
		rep.SyncBefore = &before
	}
	if okAfter {
		rep.SyncAfter = &after
	}
	if okBefore && okAfter {
		rep.DriftUS = after.OffsetUS - before.OffsetUS
	}
	if rec.Video == nil || !okBefore {
		return rep, nil
	}
	offset := before.OffsetUS
	var repeats map[int]bool
	if rec.Scenario != "motion" && len(rec.Video.AUs) > 0 {
		r, err := findRepeats(o.dir, rec.Video, o.delta)
		if err != nil {
			return rep, err
		}
		repeats = r
	}
	rep.Video = videoSummary(rec.Video, offset, repeats)
	if len(rec.Video.AUs) > 0 {
		d, err := decodeCheck(filepath.Join(o.dir, rec.Video.File))
		if err != nil {
			return rep, err
		}
		rep.Decode = d
	}
	if len(rec.Keys) > 0 {
		k, err := keySummary(rec, o)
		if err != nil {
			return rep, err
		}
		rep.Keys = k
	}
	if len(rec.Inputs) > 0 && len(rec.Video.AUs) > 0 && rec.Fixture != nil && rec.Display != nil {
		in, err := inputSummary(rec, offset, o)
		if err != nil {
			return rep, err
		}
		rep.Input = in
	}
	return rep, nil
}

func ms(us float64) float64 { return us / 1000 }

// videoSummary describes v with the agent clock mapped to the probe clock
// by offset (probe = agent − offset). The latency distributions leave out
// the first access unit and the access units in repeats, which encode the
// image of the one before again; repeats nil means they were not looked
// for.
func videoSummary(v *videoRecord, offset float64, repeats map[int]bool) *videoReport {
	r := &videoReport{Refusal: v.Refusal, Error: v.Error, Display: v.Header.Display, Repeats: len(repeats), RepeatsChecked: repeats != nil}
	if len(v.Infos) > 0 {
		i := v.Infos[0].Info
		r.Encoder, r.Hardware, r.Capture, r.Width, r.Height, r.SrcWidth, r.SrcHeight, r.TargetBitrate =
			i.Encoder, i.Hardware, i.Capture, i.Width, i.Height, i.SrcWidth, i.SrcHeight, i.Bitrate
	}
	for _, p := range v.Paused {
		r.Paused = append(r.Paused, p.Paused.Reason)
	}
	span := float64(v.ClosedUS - v.OpenedUS)
	r.SpanS = span / 1e6
	r.AUs = len(v.AUs)
	var total float64
	var sizes, keys, c2r, c2e, e2r []float64
	for i, au := range v.AUs {
		total += float64(au.Size)
		if au.Key {
			r.Keyframes++
			keys = append(keys, float64(au.Size))
		} else {
			sizes = append(sizes, float64(au.Size))
		}
		if i == 0 {
			r.FirstAUMS = ms(float64(au.RecvUS - v.OpenedUS))
			r.FirstAUBytes = au.Size
			continue
		}
		if repeats[i] {
			continue
		}
		c2r = append(c2r, ms(float64(au.RecvUS)-(float64(au.CaptureUS)-offset)))
		c2e = append(c2e, ms(float64(au.EncodeDoneUS)-float64(au.CaptureUS)))
		e2r = append(e2r, ms(float64(au.RecvUS)-(float64(au.EncodeDoneUS)-offset)))
	}
	if span > 0 {
		r.FPS = float64(r.AUs) / (span / 1e6)
		r.BitrateBPS = total * 8 / (span / 1e6)
	}
	r.AUBytes, r.KeyBytes = summarize(sizes), summarize(keys)
	r.CaptureToRecv, r.CaptureToEncoded, r.EncodedToRecv = summarize(c2r), summarize(c2e), summarize(e2r)
	var fps, bitrate, e50, e95, c50 []float64
	for _, s := range v.Stats {
		fps = append(fps, s.Stats.FPS)
		bitrate = append(bitrate, float64(s.Stats.Bitrate))
		if s.Stats.FPS > 0 {
			e50 = append(e50, s.Stats.EncodeMSP50)
			e95 = append(e95, s.Stats.EncodeMSP95)
			c50 = append(c50, s.Stats.CaptureMSP50)
		}
		r.Dropped += s.Stats.Dropped
		r.EncoderBusy += s.Stats.EncoderBusy
	}
	r.StatsFPS, r.StatsBitrate = summarize(fps), summarize(bitrate)
	r.StatsEncodeP50, r.StatsEncodeP95, r.StatsCaptureP50 = summarize(e50), summarize(e95), summarize(c50)
	return r
}

// decodeCheck decodes path with ffmpeg, counting the error lines, and
// reads the stream's description and frame count with ffprobe.
func decodeCheck(path string) (*decodeReport, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-i", path, "-f", "null", "-")
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	d := &decodeReport{}
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		d.Errors++
		if d.FirstError == "" {
			d.FirstError = strings.TrimSpace(line)
		}
	}
	if runErr != nil && d.Errors == 0 {
		d.Errors, d.FirstError = 1, runErr.Error()
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0", "-show_entries",
		"stream=codec_name,profile,level,width,height,color_range,color_space,nb_read_frames", "-of", "json", path).Output()
	if err != nil {
		return d, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	var probe struct {
		Streams []struct {
			Codec      string `json:"codec_name"`
			Profile    string `json:"profile"`
			Level      int    `json:"level"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			ColorRange string `json:"color_range"`
			ColorSpace string `json:"color_space"`
			Frames     string `json:"nb_read_frames"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probe); err != nil || len(probe.Streams) == 0 {
		return d, fmt.Errorf("ffprobe %s printed no stream: %s", path, out)
	}
	s := probe.Streams[0]
	d.Codec, d.Profile, d.Level, d.Width, d.Height, d.ColorRange, d.ColorSpace = s.Codec, s.Profile, s.Level, s.Width, s.Height, s.ColorRange, s.ColorSpace
	fmt.Sscan(s.Frames, &d.Frames)
	return d, nil
}

// measuredKinds are the input kinds inputSummary reports, in order.
var measuredKinds = []string{"click_down", "click_up", "key_down", "key_up"}

// inputSummary matches every measured input of rec with the first decoded
// frame after it whose crop of the fixture window changed.
func inputSummary(rec *record, offset float64, o reportOptions) (*inputReport, error) {
	v, fx, disp := rec.Video, rec.Fixture, rec.Display
	scale := 1.0
	if len(v.Infos) > 0 && v.Infos[0].Info.SrcWidth > 0 {
		scale = float64(v.Infos[0].Info.Width) / float64(v.Infos[0].Info.SrcWidth)
	}
	fw, fh := int(v.AUs[0].Width), int(v.AUs[0].Height)
	x0 := clamp(int(math.Floor(float64(fx.X-disp.X)*scale)), 0, fw-1)
	y0 := clamp(int(math.Floor(float64(fx.Y-disp.Y)*scale)), 0, fh-1)
	x1 := clamp(int(math.Ceil(float64(fx.X+fx.W-disp.X)*scale)), x0+1, fw)
	y1 := clamp(int(math.Ceil(float64(fx.Y+fx.H-disp.Y)*scale)), y0+1, fh)
	x0, y0 = x0&^1, y0&^1
	cw, ch := (x1-x0)&^1, (y1-y0)&^1
	if cw < 2 || ch < 2 {
		return nil, fmt.Errorf("the fixture window %+v leaves no crop of the %dx%d video", fx, fw, fh)
	}
	rep := &inputReport{Crop: [4]int{x0, y0, cw, ch}, Delta: o.delta, MinPixels: o.minPixels, Kinds: map[string]*inputKinds{}, Order: measuredKinds}

	events, refs, last := measuredEvents(rec.Inputs, v.AUs, measuredKinds, o.windowMS)
	finder := newChangeFinder(refs, last, o.delta, o.minPixels)
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-i", v.File, "-vf",
		fmt.Sprintf("crop=%d:%d:%d:%d,format=gray", cw, ch, x0, y0), "-fps_mode", "passthrough", "-f", "rawvideo", "pipe:1")
	cmd.Dir = o.dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(stdout, 4<<20)
	frame := make([]byte, cw*ch)
	n := 0
	for {
		if _, err := io.ReadFull(br, frame); err != nil {
			break
		}
		finder.frame(n, frame)
		n++
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffmpeg crop of %s: %v: %s", v.File, err, stderr.String())
	}
	if n != len(v.AUs) {
		return nil, fmt.Errorf("%s decoded to %d frames for %d access units; the frames cannot be matched to the AUs", v.File, n, len(v.AUs))
	}

	changes := make([]change, len(events))
	for i := range events {
		idx, pixels, below := finder.result(i)
		changes[i] = change{index: idx, pixels: pixels, below: below}
	}
	rep.Kinds = inputTimings(events, changes, v.AUs, measuredKinds, offset)
	return rep, nil
}

// findRepeats decodes the video of v, scaled to 960 pixels wide in
// grayscale, and returns the indexes of the access units whose image equals
// the one before by repeatFinder with delta.
func findRepeats(dir string, v *videoRecord, delta int) (map[int]bool, error) {
	w, h := int(v.AUs[0].Width), int(v.AUs[0].Height)
	sw := min(960, w)
	sh := (h*sw/w + 1) &^ 1
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-i", v.File, "-vf",
		fmt.Sprintf("scale=%d:%d:flags=area,format=gray", sw, sh), "-fps_mode", "passthrough", "-f", "rawvideo", "pipe:1")
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(stdout, 4<<20)
	frame := make([]byte, sw*sh)
	finder := newRepeatFinder(delta)
	n := 0
	for {
		if _, err := io.ReadFull(br, frame); err != nil {
			break
		}
		finder.frame(n, frame)
		n++
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode of %s: %v: %s", v.File, err, stderr.String())
	}
	if n != len(v.AUs) {
		return nil, fmt.Errorf("%s decoded to %d frames for %d access units; repeats cannot be matched to the AUs", v.File, n, len(v.AUs))
	}
	return finder.repeats, nil
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }

// f formats a value with digits decimals, or "—" for NaN.
func f(v float64, digits int) string {
	if math.IsNaN(v) {
		return "—"
	}
	return fmt.Sprintf("%.*f", digits, v)
}

// dist formats a summary as "p50 / p95 (최대 max, n개)".
func dist(s summary, digits int) string {
	if s.N == 0 {
		return "—"
	}
	return fmt.Sprintf("%s / %s (최대 %s, %d개)", f(s.P50, digits), f(s.P95, digits), f(s.Max, digits), s.N)
}

// printReport writes rep as Korean Markdown.
func printReport(w io.Writer, rep recordReport) {
	fmt.Fprintf(w, "## %s (%s)\n\n", rep.Name, rep.Scenario)
	if rep.Session != "" {
		fmt.Fprintf(w, "- 세션: %s\n", rep.Session)
	}
	fmt.Fprintf(w, "- 시계: 프로브 %s, 타이머 해상도 %v ms\n", rep.ClockSource, rep.TimerResMS)
	if s := rep.SyncBefore; s != nil {
		fmt.Fprintf(w, "- time.sync 앞: %d회, 최소 RTT %s ms(중앙 %s ms), 오프셋 %s ms, 빠른 %d회 오프셋 폭 %s ms(중앙 %s ms)\n",
			s.Samples, f(ms(float64(s.RTTUS)), 3), f(ms(s.RTTP50US), 3), f(ms(s.OffsetUS), 3), min(lowRTTSamples, s.Samples), f(ms(s.SpreadUS), 3), f(ms(s.MedianLowUS), 3))
	}
	if s := rep.SyncAfter; s != nil {
		fmt.Fprintf(w, "- time.sync 뒤: 최소 RTT %s ms, 오프셋 변화 %s ms\n", f(ms(float64(s.RTTUS)), 3), f(ms(rep.DriftUS), 3))
	}
	if v := rep.Video; v != nil {
		if v.Refusal != "" {
			fmt.Fprintf(w, "- video 거절: %s\n", v.Refusal)
		}
		if v.Error != "" {
			fmt.Fprintf(w, "- video 오류: %s\n", v.Error)
		}
		if len(v.Paused) > 0 {
			fmt.Fprintf(w, "- paused: %v\n", v.Paused)
		}
		fmt.Fprintf(w, "- 인코더: %s(하드웨어 %v), 캡처 %s, 디스플레이 %d, %d×%d(원본 %d×%d), 목표 %s Mbps\n",
			v.Encoder, v.Hardware, v.Capture, v.Display, v.Width, v.Height, v.SrcWidth, v.SrcHeight, f(float64(v.TargetBitrate)/1e6, 1))
		fmt.Fprintf(w, "- %s초 동안 AU %d개(키프레임 %d), 평균 %s fps, %s Mbps\n", f(v.SpanS, 1), v.AUs, v.Keyframes, f(v.FPS, 1), f(v.BitrateBPS/1e6, 2))
		fmt.Fprintf(w, "- 첫 AU: 스트림 연 뒤 %s ms, %d바이트\n", f(v.FirstAUMS, 1), v.FirstAUBytes)
		if v.RepeatsChecked {
			fmt.Fprintf(w, "- 앞 AU와 같은 이미지(밝기 차 %d 이상 픽셀 0개)를 다시 인코딩한 AU %d개는 지연 분포에서 뺐다\n", 24, v.Repeats)
		}
		fmt.Fprintf(w, "- AU 크기(키프레임 제외, 바이트) p50 / p95: %s; 키프레임: %s\n", dist(v.AUBytes, 0), dist(v.KeyBytes, 0))
		fmt.Fprintf(w, "- 캡처→수신(시계 보정, 첫 AU 제외, ms) p50 / p95: %s\n", dist(v.CaptureToRecv, 2))
		fmt.Fprintf(w, "- 캡처→인코딩 완료(에이전트 시계, ms): %s\n", dist(v.CaptureToEncoded, 2))
		fmt.Fprintf(w, "- 인코딩 완료→수신(시계 보정, ms): %s\n", dist(v.EncodedToRecv, 2))
		fmt.Fprintf(w, "- stats(초당): fps %s, bitrate(Mbps) p50 %s, encode_ms_p50 %s, encode_ms_p95 %s, capture_ms_p50 %s, dropped 합 %d, encoder_busy 합 %d\n",
			dist(v.StatsFPS, 1), f(v.StatsBitrate.P50/1e6, 2), dist(v.StatsEncodeP50, 2), dist(v.StatsEncodeP95, 2), dist(v.StatsCaptureP50, 2), v.Dropped, v.EncoderBusy)
	}
	if d := rep.Decode; d != nil {
		fmt.Fprintf(w, "- Mac ffmpeg 디코딩: 오류 %d줄, 프레임 %d개, %s %s L%d %d×%d, %s/%s\n", d.Errors, d.Frames, d.Codec, d.Profile, d.Level, d.Width, d.Height, d.ColorRange, d.ColorSpace)
		if d.FirstError != "" {
			fmt.Fprintf(w, "  - 첫 오류: %s\n", d.FirstError)
		}
	}
	if in := rep.Input; in != nil {
		fmt.Fprintf(w, "- 입력: fixture 창 영역 %v(x,y,w,h)에서 밝기 차 %d 이상 픽셀 %d개 이상을 변화로 본다\n", in.Crop, in.Delta, in.MinPixels)
		for _, k := range in.Order {
			kd := in.Kinds[k]
			fmt.Fprintf(w, "  - %s: %d개(실패 %d, 변화 찾음 %d), ACK 왕복 %s, 송신→주입 완료(보정) %s, 송신→변화 AU 수신 %s, 주입 완료→변화 캡처(에이전트 시계) %s, 변화 픽셀 %s, 변화 전 프레임 최대 %d픽셀\n",
				k, kd.Events, kd.Failed, kd.Matched, dist(kd.AckRTT, 2), dist(kd.SendToDone, 2), dist(kd.ToChange, 1), dist(kd.DoneToCap, 1), dist(kd.Pixels, 0), kd.MaxBelow)
		}
	}
	if rep.Keys != nil {
		printKeys(w, rep.Keys)
	}
	if rep.Skipped != "" {
		fmt.Fprintf(w, "- 건너뜀: %s\n", rep.Skipped)
	}
	for _, n := range rep.Notes {
		fmt.Fprintf(w, "- 메모: %s\n", n)
	}
	fmt.Fprintln(w)
}
