package qa

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// fileReadCap bounds what assert_file reads of a file.
	fileReadCap = 8 << 20
	// fsChunk is the size of one fs.read.
	fsChunk = 256 << 10
	// findLimit bounds the elements assert_ui counts.
	findLimit = 200
	// screenTreeMax bounds the elements assert_screen_text reads.
	screenTreeMax = 2000
)

// caps names the host capability each step kind needs.
var caps = map[string]string{ActPush: "files", ActLaunch: "exec", ActWaitWindow: "uia", ActClick: "input", ActType: "input", ActKey: "input",
	ActUIAct: "uia", ActAssertUI: "uia", ActAssertScreenText: "uia", ActAssertFile: "files", ActExec: "exec", ActScreenshot: "screen",
	ActClose: "uia"}

// noInput is the reason of an input step on a daemon without input.
const noInput = "이 데몬은 아직 마우스·키보드 입력을 보내지 못합니다. ui_act로 바꾸거나 입력을 지원하는 deskhand로 업데이트하세요"

// do runs the step st at index i; timeout is the step's timeout in
// milliseconds.
func (r *runner) do(ctx context.Context, st Step, i int, timeout int64) stepOut {
	if r.agent == nil && st.Action != ActSleep {
		return blockedOut(wire.CodeOffline, fmt.Sprintf("%s PC의 연결을 찾지 못했습니다. host.list로 PC를 확인하세요", r.host.Name))
	}
	if st.Action == ActClick || st.Action == ActType || st.Action == ActKey {
		if r.m.opt.Input == nil {
			return blockedOut(wire.CodeCapabilityUnsupported, noInput)
		}
	}
	if st.Action == ActPush && r.m.opt.Push == nil {
		return blockedOut(wire.CodeCapabilityUnsupported, "이 데몬은 파일을 보낼 수 없습니다. deskhand를 업데이트하세요")
	}
	need := caps[st.Action]
	if c, ok := st.Spec.(*CollectSpec); ok {
		need = "files"
		if c.File == "" {
			need = "diag"
		}
	}
	if need != "" {
		if c, ok := r.agent.Caps()[need]; ok && c.State != "ready" {
			reason := c.Reason
			if reason == "" {
				reason = c.State
			}
			return blockedOut(wire.CodeCapabilityUnsupported, fmt.Sprintf("이 PC는 %s 단계를 할 수 없습니다(%s: %s). 그 기능이 있는 PC나 에이전트에서 실행하세요", st.Action, need, reason))
		}
	}
	r.writerGen = 0
	if gated[st.Action] {
		gen, err := r.m.opt.Control.Check(ctx, r.host.HostID, r.caller)
		if err != nil {
			e := apierr.As(err)
			switch e.Code {
			case wire.CodeControlHeldByHuman:
				return blockedOut(e.Code, "사람이 조작 중입니다. 제어권을 가져오지 않았습니다. 사람이 제어권을 AI에게 넘긴 뒤 다시 실행하세요")
			case wire.CodeControlPreempted:
				return blockedOut(e.Code, e.Message)
			}
			return fromError(err)
		}
		r.writerGen = gen
	}
	switch s := st.Spec.(type) {
	case *PushSpec:
		return r.push(ctx, s)
	case *LaunchSpec:
		return r.launch(ctx, s)
	case *WaitWindowSpec:
		return r.waitWindow(ctx, s)
	case *ClickSpec:
		return r.click(ctx, s)
	case *TypeSpec:
		res, err := r.m.opt.Input.Type(ctx, r.host.HostID, r.caller, s.Text, s.Method)
		if err != nil {
			return fromError(err)
		}
		out := pass("")
		out.result = res
		return out
	case *KeySpec:
		res, err := r.m.opt.Input.Key(ctx, r.host.HostID, r.caller, s.Keys)
		if err != nil {
			return fromError(err)
		}
		out := pass("")
		out.result = res
		return out
	case *UIActSpec:
		return r.uiAct(ctx, s)
	case *AssertUISpec:
		return r.assertUI(ctx, s)
	case *AssertScreenTextSpec:
		return r.assertScreen(ctx, s)
	case *AssertFileSpec:
		return r.assertFile(ctx, s, i)
	case *ExecSpec:
		return r.exec(ctx, s, i, timeout)
	case *ScreenshotSpec:
		return r.shot(ctx, s, i)
	case *CollectSpec:
		return r.collect(ctx, s, i)
	case *SleepSpec:
		select {
		case <-ctx.Done():
			return failOut(wire.CodeTimeout, "기다리는 중에 멈췄습니다")
		case <-time.After(time.Duration(s.MS) * time.Millisecond):
			return pass("")
		}
	case *CloseSpec:
		return r.close(ctx, s)
	}
	return failOut(wire.CodeInternal, fmt.Sprintf("%s 단계를 실행하지 못했습니다. deskhand를 업데이트하세요", st.Action))
}

// call sends op and returns its answer and the request's rid; a writer op
// carries the step's writer generation and goes out only while
// Control.Recheck passes.
func (r *runner) call(ctx context.Context, op string, params proto.Message) (proto.Message, string, error) {
	opts := conn.CallOpts{}
	if wire.WriterOp(op) {
		if params == nil {
			params = proto.Message{}
		}
		gen := r.writerGen
		params["gen"] = gen
		opts.Ready = func() error { return r.m.opt.Control.Recheck(r.host.HostID, r.caller, gen) }
	}
	info := conn.CallInfo{}
	opts.Info = &info
	res, err := r.agent.Call(ctx, op, params, opts)
	return res, info.RID, err
}

// withRID returns res with the request's rid as request_id.
func withRID(res map[string]any, rid string) map[string]any {
	out := map[string]any{}
	for k, v := range res {
		out[k] = v
	}
	if rid != "" {
		out["request_id"] = rid
	}
	return out
}

// windowRef is the window a step names: its own, else the current window.
func (r *runner) windowRef(own string) string {
	if own != "" {
		return own
	}
	if r.window != nil {
		if id, _ := r.window["id"].(string); id != "" {
			return id
		}
	}
	return ""
}

// selectorParams returns the ui op params of a selector.
func (r *runner) selectorParams(s Selector) proto.Message {
	p := proto.Message{}
	if w := r.windowRef(s.Window); w != "" {
		p["window"] = w
	}
	for k, v := range map[string]string{"role": s.Role, "name": s.Name, "nameContains": s.NameContains, "automationId": s.AutomationID} {
		if v != "" {
			p[k] = v
		}
	}
	if s.Index != nil {
		p["index"] = *s.Index
	}
	return p
}

// sleep waits the poll interval; it reports false when ctx ended first.
func (r *runner) sleep(ctx context.Context) bool {
	t := time.NewTimer(r.m.poll)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// blocking reports whether err stops a polling step at once.
func blocking(err error) bool { return blockingCodes[apierr.CodeOf(err)] }

func (r *runner) push(ctx context.Context, s *PushSpec) stepOut {
	if s.Mkdir {
		if parent := remoteParent(s.To); parent != "" {
			if _, _, err := r.call(ctx, "fs.mkdir", proto.Message{"path": parent}); err != nil {
				return fromError(err)
			}
		}
	}
	td, err := r.m.opt.Push.Push(ctx, r.host.HostID, s.From, s.To, s.OnConflict, r.caller)
	if err != nil {
		return fromError(err)
	}
	res := map[string]any{"transfer_id": td.TransferID, "state": td.State, "files_total": td.FilesTotal, "bytes_total": td.BytesTotal}
	var files []any
	for i, f := range td.Files {
		if i == 50 {
			break
		}
		e := map[string]any{"rel": f.Rel, "size": f.Size}
		if f.SHA256 != "" {
			e["sha256"] = f.SHA256
		}
		if f.Path != "" {
			e["path"] = f.Path
		}
		files = append(files, e)
	}
	res["files"] = files
	if td.State != "done" {
		code, msg := wire.CodeInternal, fmt.Sprintf("전송 %s가 %s 상태로 끝났습니다. deskhand transfer get %s로 파일별 오류를 확인하세요", td.TransferID, td.State, td.TransferID)
		if td.Error != nil {
			code, msg = td.Error.Code, td.Error.Message
		}
		out := fromError(apierr.New(code, msg, false, ""))
		out.result = res
		return out
	}
	out := pass(fmt.Sprintf("파일 %d개(%s)를 보냈습니다", td.FilesTotal, byteSize(td.BytesTotal)))
	if td.Outcome == "skipped" {
		out.message = "대상에 이미 있어 건너뛰었습니다"
	}
	out.result = res
	return out
}

// remoteParent returns the folder of a PC path, "" for a path at a root.
func remoteParent(p string) string {
	i := strings.LastIndexAny(p, `\/`)
	if i <= 0 {
		return ""
	}
	parent := p[:i]
	if strings.HasSuffix(parent, ":") || strings.Trim(parent, `\/`) == "" {
		return ""
	}
	return parent
}

func (r *runner) launch(ctx context.Context, s *LaunchSpec) stepOut {
	params := proto.Message{"path": s.Path}
	if len(s.Args) > 0 {
		params["args"] = s.Args
	}
	if s.Cwd != "" {
		params["cwd"] = s.Cwd
	}
	if s.WaitWindowMS != nil {
		params["wait_window_ms"] = *s.WaitWindowMS
	}
	res, rid, err := r.call(ctx, "app.launch", params)
	if err != nil {
		return fromError(err)
	}
	out := pass(fmt.Sprintf("pid %d로 실행했습니다", res.Int("pid")))
	if w, ok := res["window"].(map[string]any); ok {
		r.window = w
		out.message += fmt.Sprintf(", 창 %q", r.sc.Scrub(fmt.Sprint(w["title"])))
	}
	out.result = withRID(res, rid)
	return out
}

// matchWindow reports whether the window w fits s.
func matchWindow(w map[string]any, s *WaitWindowSpec) bool {
	title, _ := w["title"].(string)
	process, _ := w["process"].(string)
	pid, _ := w["pid"].(float64)
	switch {
	case s.Title != "" && title != s.Title,
		s.TitleContains != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(s.TitleContains)),
		s.Process != "" && !strings.EqualFold(process, s.Process),
		s.PID != 0 && int(pid) != s.PID:
		return false
	}
	return true
}

// describeWindow describes the window s looks for, with secret values
// masked.
func (r *runner) describeWindow(s *WaitWindowSpec) string {
	var parts []string
	if s.Title != "" {
		parts = append(parts, fmt.Sprintf("제목 %q", r.sc.Scrub(s.Title)))
	}
	if s.TitleContains != "" {
		parts = append(parts, fmt.Sprintf("제목에 %q", r.sc.Scrub(s.TitleContains)))
	}
	if s.Process != "" {
		parts = append(parts, "프로세스 "+r.sc.Scrub(s.Process))
	}
	if s.PID != 0 {
		parts = append(parts, fmt.Sprintf("pid %d", s.PID))
	}
	return strings.Join(parts, ", ")
}

func (r *runner) waitWindow(ctx context.Context, s *WaitWindowSpec) stepOut {
	want := r.describeWindow(s)
	var lastErr error
	listed := false
	for {
		res, _, err := r.call(ctx, "win.list", proto.Message{})
		if err != nil && blocking(err) {
			return fromError(err)
		}
		if err == nil {
			listed = true
			var found map[string]any
			list, _ := res["windows"].([]any)
			for _, item := range list {
				if w, ok := item.(map[string]any); ok && matchWindow(w, s) {
					found = w
					break
				}
			}
			if s.Gone && found == nil {
				out := pass("창이 닫혔습니다")
				out.assertions = []Assertion{{Kind: "window", Expected: "없음: " + want, Actual: "없음", Passed: true}}
				return out
			}
			if !s.Gone && found != nil {
				r.window = found
				out := pass(fmt.Sprintf("창 %q를 찾았습니다", r.sc.Scrub(fmt.Sprint(found["title"]))))
				out.assertions = []Assertion{{Kind: "window", Expected: want, Actual: found["title"], Passed: true}}
				out.result = map[string]any{"window": found}
				return out
			}
			lastErr = nil
		} else {
			lastErr = err
		}
		if !r.sleep(ctx) {
			break
		}
	}
	if !listed && lastErr != nil {
		return fromError(lastErr)
	}
	out := failOut("", fmt.Sprintf("%s인 창이 제한 시간 안에 나타나지 않았습니다. 앱이 떴는지 확인하거나 timeout_ms를 늘리세요", want))
	expected, actual := any(want), any("없음")
	if s.Gone {
		out.message = fmt.Sprintf("%s인 창이 제한 시간 안에 닫히지 않았습니다. 앱 상태를 확인하거나 timeout_ms를 늘리세요", want)
		expected, actual = "없음: "+want, "열려 있음"
	}
	out.err = nil
	out.polled = true
	out.assertions = []Assertion{{Kind: "window", Expected: expected, Actual: actual, Passed: false}}
	return out
}

func (r *runner) click(ctx context.Context, s *ClickSpec) stepOut {
	var x, y int
	if s.Element != nil {
		if c, ok := r.agent.Caps()["uia"]; ok && c.State != "ready" {
			return blockedOut(wire.CodeCapabilityUnsupported, fmt.Sprintf("이 PC에서 요소를 찾을 수 없습니다(uia: %s). x·y로 클릭하세요", c.Reason))
		}
		res, _, err := r.call(ctx, "ui.rect", r.selectorParams(*s.Element))
		if err != nil {
			return fromError(err)
		}
		if clickable, ok := res["clickable"].(bool); ok && !clickable {
			return failOut(wire.CodeStaleTarget, fmt.Sprintf("요소를 클릭할 수 없습니다(%s). 창을 앞으로 가져오거나 요소가 보이게 하세요", res.String("blocked")))
		}
		x, y = res.Int("cx"), res.Int("cy")
	} else {
		w, err := r.findWindow(ctx, s.Window)
		if err != nil {
			return fromError(err)
		}
		if w == nil {
			return failOut(wire.CodeInvalidArgument, "x·y 클릭의 기준 창이 없습니다. 앞에 launch나 wait_window를 두거나 window를 주세요")
		}
		wx, _ := w["x"].(float64)
		wy, _ := w["y"].(float64)
		x, y = int(wx)+*s.X, int(wy)+*s.Y
	}
	res, err := r.m.opt.Input.Click(ctx, r.host.HostID, r.caller, x, y, s.Button, s.Count)
	if err != nil {
		return fromError(err)
	}
	out := pass(fmt.Sprintf("(%d, %d)를 클릭했습니다", x, y))
	out.result = map[string]any{"x": x, "y": y}
	for k, v := range res {
		out.result[k] = v
	}
	return out
}

// findWindow returns the window ref names (a win.list id, foreground or a
// title part), or the current window when ref is "".
func (r *runner) findWindow(ctx context.Context, ref string) (map[string]any, error) {
	if ref == "" {
		return r.window, nil
	}
	res, _, err := r.call(ctx, "win.list", proto.Message{})
	if err != nil {
		return nil, err
	}
	list, _ := res["windows"].([]any)
	for _, item := range list {
		w, ok := item.(map[string]any)
		if !ok {
			continue
		}
		title, _ := w["title"].(string)
		if w["id"] == ref || (ref == "foreground" && w["foreground"] == true) || strings.Contains(strings.ToLower(title), strings.ToLower(ref)) {
			return w, nil
		}
	}
	return nil, apierr.New(wire.CodeNotFound, fmt.Sprintf("창 %q가 없습니다. win.list로 열린 창을 확인하세요", r.sc.Scrub(ref)), false, "")
}

func (r *runner) uiAct(ctx context.Context, s *UIActSpec) stepOut {
	params := r.selectorParams(s.Selector)
	params["action"] = s.Action
	if s.Action == "set_value" {
		params["value"] = s.Value
	}
	res, rid, err := r.call(ctx, "ui.act", params)
	if err != nil {
		return fromError(err)
	}
	out := pass("")
	switch {
	case res.Bool("gone"):
		out.message = "동작 뒤 요소나 창이 사라졌습니다"
	case res.Bool("pending"):
		out.message = "앱이 아직 동작을 마치지 않았습니다"
	case res.Bool("unresponsive"):
		out.message = "동작 뒤 창이 응답하지 않습니다"
	}
	out.result = withRID(res, rid)
	return out
}

// textValue masks the secret values of a text recorded as an assertion's
// value, then cuts it.
func (r *runner) textValue(s string) string { return cut(r.sc.Scrub(s), actualCut) }

func (r *runner) assertUI(ctx context.Context, s *AssertUISpec) stepOut {
	window := r.windowRef(s.Selector.Window)
	var last []Assertion
	var lastErr error
	for {
		got, err := r.checkUI(ctx, s)
		if err != nil && blocking(err) {
			return fromError(err)
		}
		if err == nil {
			last, lastErr = got, nil
			if allPassed(got) {
				out := pass("")
				out.assertions = got
				return out
			}
		} else {
			lastErr = err
		}
		if !r.sleep(ctx) {
			break
		}
	}
	out := r.assertionFail(last, lastErr)
	out.uiWindow = &window
	return out
}

// assertionFail is the failure of a polling step whose last check gave
// assertions, or only the error err.
func (r *runner) assertionFail(last []Assertion, err error) stepOut {
	if last == nil && err != nil {
		out := fromError(err)
		out.polled = true
		return out
	}
	out := stepOut{status: StepFail, assertions: last, polled: true}
	for _, a := range last {
		if !a.Passed {
			out.message = fmt.Sprintf("기대와 다릅니다: %s 기대 %s, 실제 %s", a.Kind, r.show(a.Expected), r.show(a.Actual))
			break
		}
	}
	if err != nil {
		out.message += " (" + apierr.As(err).Message + ")"
	}
	return out
}

// show writes an assertion value for a message: secret values masked,
// then cut and quoted.
func (r *runner) show(v any) string {
	switch t := v.(type) {
	case nil:
		return "없음"
	case string:
		return fmt.Sprintf("%q", cut(r.sc.Scrub(t), 120))
	}
	return r.sc.Scrub(fmt.Sprint(v))
}

func allPassed(list []Assertion) bool {
	for _, a := range list {
		if !a.Passed {
			return false
		}
	}
	return true
}

// checkUI checks the expectations of s once.
func (r *runner) checkUI(ctx context.Context, s *AssertUISpec) ([]Assertion, error) {
	params := r.selectorParams(s.Selector)
	if s.Selector.Index == nil {
		params["limit"] = findLimit
	}
	res, _, err := r.call(ctx, "ui.find", params)
	if err != nil {
		return nil, err
	}
	nodes, _ := res["nodes"].([]any)
	var out []Assertion
	if s.Exists != nil || s.Count == nil {
		want := s.Exists == nil || *s.Exists
		out = append(out, Assertion{Kind: "exists", Expected: want, Actual: len(nodes) > 0, Passed: (len(nodes) > 0) == want})
	}
	if s.Count != nil {
		out = append(out, Assertion{Kind: "count", Expected: *s.Count, Actual: len(nodes), Passed: len(nodes) == *s.Count})
	}
	var first map[string]any
	if len(nodes) > 0 {
		first, _ = nodes[0].(map[string]any)
	}
	if s.Enabled != nil {
		a := Assertion{Kind: "enabled", Expected: *s.Enabled}
		if first != nil {
			enabled := true
			flags, _ := first["flags"].([]any)
			for _, f := range flags {
				enabled = enabled && f != "disabled"
			}
			a.Actual, a.Passed = enabled, enabled == *s.Enabled
		}
		out = append(out, a)
	}
	if s.Text != nil || s.TextContains != nil || s.TextMatches != nil {
		var text *string
		if first != nil {
			tres, _, err := r.call(ctx, "ui.text", r.selectorParams(s.Selector))
			if err != nil {
				return nil, err
			}
			t := tres.String("text")
			text = &t
		}
		check := func(kind string, expected any, ok func(string) bool) {
			a := Assertion{Kind: kind, Expected: expected}
			if text != nil {
				a.Actual, a.Passed = r.textValue(*text), ok(*text)
			}
			out = append(out, a)
		}
		if s.Text != nil {
			check("text", *s.Text, func(t string) bool { return t == *s.Text })
		}
		if s.TextContains != nil {
			check("text_contains", *s.TextContains, func(t string) bool { return strings.Contains(t, *s.TextContains) })
		}
		if s.TextMatches != nil {
			check("text_matches", s.TextMatches.String(), s.TextMatches.MatchString)
		}
	}
	return out, nil
}

// screenText joins the names and values of a UI tree into lines.
func screenText(tree proto.Message) string {
	var lines []string
	nodes, _ := tree["nodes"].([]any)
	for _, item := range nodes {
		n, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, k := range []string{"name", "value"} {
			if v, _ := n[k].(string); v != "" && (len(lines) == 0 || lines[len(lines)-1] != v) {
				lines = append(lines, v)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// textAssertions checks text against contains, not_contains and matches.
func (r *runner) textAssertions(text string, contains, notContains []string, matches *regexp.Regexp) []Assertion {
	var out []Assertion
	for _, c := range contains {
		out = append(out, Assertion{Kind: "contains", Expected: c, Actual: r.textValue(text), Passed: strings.Contains(text, c)})
	}
	for _, c := range notContains {
		out = append(out, Assertion{Kind: "not_contains", Expected: c, Actual: r.textValue(text), Passed: !strings.Contains(text, c)})
	}
	if matches != nil {
		out = append(out, Assertion{Kind: "matches", Expected: matches.String(), Actual: r.textValue(text), Passed: matches.MatchString(text)})
	}
	return out
}

func (r *runner) assertScreen(ctx context.Context, s *AssertScreenTextSpec) stepOut {
	params := proto.Message{"filter": "all", "max": screenTreeMax}
	if w := r.windowRef(s.Window); w != "" {
		params["window"] = w
	}
	var last []Assertion
	var lastTree proto.Message
	var lastErr error
	for {
		tree, _, err := r.call(ctx, "ui.tree", params)
		if err != nil && blocking(err) {
			return fromError(err)
		}
		if err == nil {
			got := r.textAssertions(screenText(tree), s.Contains, s.NotContains, s.Matches)
			last, lastTree, lastErr = got, tree, nil
			if allPassed(got) {
				out := pass("")
				out.assertions = got
				return out
			}
		} else {
			lastErr = err
		}
		if !r.sleep(ctx) {
			break
		}
	}
	out := r.assertionFail(last, lastErr)
	out.tree = lastTree
	return out
}

func (r *runner) assertFile(ctx context.Context, s *AssertFileSpec, i int) stepOut {
	var last []Assertion
	var lastErr error
	var content []byte
	for {
		got, data, err := r.checkFile(ctx, s)
		if err != nil && blocking(err) {
			return fromError(err)
		}
		if err == nil {
			last, lastErr, content = got, nil, data
			if allPassed(got) {
				out := pass("")
				out.assertions = got
				return out
			}
		} else {
			lastErr = err
		}
		if !r.sleep(ctx) {
			break
		}
	}
	out := r.assertionFail(last, lastErr)
	for _, a := range last {
		if !a.Passed && (a.Kind == "contains" || a.Kind == "not_contains" || a.Kind == "matches") {
			out.content = content
		}
	}
	return out
}

// checkFile checks the expectations of s once and returns the content it
// read.
func (r *runner) checkFile(ctx context.Context, s *AssertFileSpec) ([]Assertion, []byte, error) {
	params := proto.Message{"path": s.Path}
	if s.SHA256 != "" {
		params["sha256"] = true
	}
	st, _, err := r.call(ctx, "fs.stat", params)
	if err != nil {
		return nil, nil, err
	}
	exists := st.Bool("exists") && !st.Bool("dir")
	want := s.Exists == nil || *s.Exists
	out := []Assertion{{Kind: "exists", Expected: want, Actual: exists, Passed: exists == want}}
	if !exists {
		return out, nil, nil
	}
	if s.Size != nil {
		size := int64(st.Int("size"))
		out = append(out, Assertion{Kind: "size", Expected: *s.Size, Actual: size, Passed: size == *s.Size})
	}
	if s.SHA256 != "" {
		sum := strings.ToLower(st.String("sha256"))
		out = append(out, Assertion{Kind: "sha256", Expected: s.SHA256, Actual: sum, Passed: sum == s.SHA256})
	}
	if len(s.Contains) == 0 && len(s.NotContains) == 0 && s.Matches == nil {
		return out, nil, nil
	}
	data, _, err := r.readFile(ctx, s.Path, 0, fileReadCap)
	if err != nil {
		return nil, nil, err
	}
	return append(out, r.textAssertions(string(data), s.Contains, s.NotContains, s.Matches)...), data, nil
}

// readFile reads up to max bytes of a PC file from offset; a UTF-16LE file
// comes back as UTF-8. It returns whether the file goes on past what it
// read.
func (r *runner) readFile(ctx context.Context, path string, offset, max int64) ([]byte, bool, error) {
	var data []byte
	wide := false
	for int64(len(data)) < max {
		n := max - int64(len(data))
		if n > fsChunk {
			n = fsChunk
		}
		res, _, err := r.call(ctx, "fs.read", proto.Message{"path": path, "offset": offset + int64(len(data)), "length": n})
		if err != nil {
			return nil, false, err
		}
		chunk, err := decodeBase64(res.String("data"))
		if err != nil {
			return nil, false, apierr.Internal("에이전트가 보낸 파일 내용을 읽지 못했습니다. 에이전트를 업데이트하세요")
		}
		wide = wide || res.String("encoding") == "utf-16le"
		data = append(data, chunk...)
		if res.Bool("eof") || len(chunk) == 0 {
			if wide {
				data = fromUTF16(data)
			}
			return data, false, nil
		}
	}
	if wide {
		data = fromUTF16(data)
	}
	return data, true, nil
}

// fromUTF16 decodes UTF-16LE bytes, without a byte order mark, to UTF-8.
func fromUTF16(b []byte) []byte {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return []byte(string(utf16.Decode(units)))
}

func decodeBase64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

func (r *runner) exec(ctx context.Context, s *ExecSpec, i int, timeout int64) stepOut {
	params := proto.Message{"timeout_ms": timeout}
	if len(s.Argv) > 0 {
		params["argv"] = s.Argv
	} else {
		params["cmd"] = s.Cmd
	}
	if s.Shell != "" {
		params["shell"] = s.Shell
	}
	if s.Cwd != "" {
		params["cwd"] = s.Cwd
	}
	res, rid, err := r.call(ctx, "exec.run", params)
	if err != nil {
		return fromError(err)
	}
	code := res.Int("code")
	stdout, stderr := res.String("stdout"), res.String("stderr")
	var out stepOut
	for _, st := range []struct{ name, data string }{{"stdout", stdout}, {"stderr", stderr}} {
		if st.data == "" {
			continue
		}
		if a, err := r.save(i, ActExec, st.name, ".txt", "log", []byte(r.sc.Scrub(st.data))); err == nil {
			out.evidence = append(out.evidence, a)
		}
	}
	out.result = withRID(map[string]any{"code": code, "timed_out": res.Bool("timed_out"), "truncated": res.Bool("truncated")}, rid)
	switch {
	case res.Bool("timed_out"):
		msg := fmt.Sprintf("명령이 %s 안에 끝나지 않아 종료했습니다. timeout_ms를 늘리세요", millis(timeout))
		out.status, out.err, out.message = StepFail, &ErrorInfo{Code: wire.CodeTimeout, Message: msg}, msg
		return out
	case res.String("killed") == "connection_closed":
		msg := "PC와의 연결이 끊겨 에이전트가 명령을 중간에 끝냈습니다. PC 상태를 확인한 뒤 다시 실행하세요"
		out.status, out.err, out.message = StepBlocked, &ErrorInfo{Code: wire.CodeOffline, Message: msg}, msg
		return out
	}
	out.assertions = []Assertion{{Kind: "exit_code", Expected: s.ExpectCode, Actual: code, Passed: code == s.ExpectCode}}
	for _, c := range s.StdoutContains {
		out.assertions = append(out.assertions, Assertion{Kind: "stdout_contains", Expected: c, Actual: r.textValue(stdout), Passed: strings.Contains(stdout, c)})
	}
	for _, c := range s.StderrContains {
		out.assertions = append(out.assertions, Assertion{Kind: "stderr_contains", Expected: c, Actual: r.textValue(stderr), Passed: strings.Contains(stderr, c)})
	}
	if allPassed(out.assertions) {
		out.status, out.message = StepPass, fmt.Sprintf("종료 코드 %d", code)
		return out
	}
	failed := r.assertionFail(out.assertions, nil)
	out.status, out.message = StepFail, failed.message
	return out
}

func (r *runner) shot(ctx context.Context, s *ScreenshotSpec, i int) stepOut {
	if s.Window != "" {
		if c, ok := r.agent.Caps()["uia"]; ok && c.State != "ready" {
			return blockedOut(wire.CodeCapabilityUnsupported, fmt.Sprintf("이 PC에서 창을 찾을 수 없습니다(uia: %s). window 없이 화면 전체를 찍으세요", c.Reason))
		}
	}
	data, res, err := r.screenshot(ctx, s.Display, s.Window, s.MaxWidth)
	if err != nil {
		return fromError(err)
	}
	a, err := r.save(i, ActScreenshot, s.Name, ".png", "screenshot", data)
	if err != nil {
		return failOut(wire.CodeInternal, fmt.Sprintf("스크린샷을 저장하지 못했습니다(%v). 디스크 공간을 확인하세요", err))
	}
	out := pass(fmt.Sprintf("%d×%d 화면을 저장했습니다", res.Int("width"), res.Int("height")))
	out.evidence = []model.Artifact{a}
	out.result = map[string]any{"width": res.Int("width"), "height": res.Int("height")}
	if m := res.String("method"); m != "" {
		out.result["method"] = m
	}
	return out
}

func (r *runner) collect(ctx context.Context, s *CollectSpec, i int) stepOut {
	switch {
	case s.File != "":
		st, _, err := r.call(ctx, "fs.stat", proto.Message{"path": s.File})
		if err != nil {
			return fromError(err)
		}
		if !st.Bool("exists") || st.Bool("dir") {
			return failOut(wire.CodeNotFound, fmt.Sprintf("모을 파일 %s가 없습니다. 경로를 확인하세요", s.File))
		}
		size := int64(st.Int("size"))
		start := size - s.MaxBytes
		if start < 0 {
			start = 0
		}
		data, _, err := r.readFile(ctx, s.File, start, s.MaxBytes)
		if err != nil {
			return fromError(err)
		}
		name := s.Name
		if name == "" {
			name = remoteBase(s.File)
		}
		kind := "file"
		if ext := strings.ToLower(filepath.Ext(name)); ext == ".log" || ext == ".txt" {
			kind = "log"
			data = []byte(r.sc.Scrub(string(data)))
		}
		a, err := r.save(i, ActCollect, strings.TrimSuffix(name, filepath.Ext(name)), filepath.Ext(name), kind, data)
		if err != nil {
			return failOut(wire.CodeInternal, fmt.Sprintf("모은 파일을 저장하지 못했습니다(%v). 디스크 공간을 확인하세요", err))
		}
		out := pass(fmt.Sprintf("%s의 %s를 모았습니다", s.File, byteSize(int64(len(data)))))
		out.evidence = []model.Artifact{a}
		out.result = map[string]any{"path": s.File, "size": size, "collected_bytes": int64(len(data)), "truncated": start > 0}
		return out
	case s.Events != nil:
		params := proto.Message{"log": s.Events.Log, "since": s.Events.Since}
		if s.Events.Since == "" {
			params["since"] = model.Time(r.started)
		}
		if s.Events.Until != "" {
			params["until"] = s.Events.Until
		}
		if len(s.Events.Levels) > 0 {
			params["levels"] = s.Events.Levels
		}
		if s.Events.Provider != "" {
			params["provider"] = s.Events.Provider
		}
		if s.Events.Max > 0 {
			params["max"] = s.Events.Max
		}
		res, _, err := r.call(ctx, "diag.events", params)
		if err != nil {
			return fromError(err)
		}
		name := s.Name
		if name == "" {
			name = "events-" + s.Events.Log
		}
		evs, _ := res["events"].([]any)
		return r.collected(i, "diag.events", name, map[string]any{"events": res["events"], "truncated": res.Bool("truncated")},
			fmt.Sprintf("%s 로그 %d건을 모았습니다", s.Events.Log, len(evs)), len(evs))
	case s.Crashes != nil:
		params := proto.Message{"since": s.Crashes.Since}
		if s.Crashes.Since == "" {
			params["since"] = model.Time(r.started)
		}
		res, _, err := r.call(ctx, "diag.crashes", params)
		if err != nil {
			return fromError(err)
		}
		name := s.Name
		if name == "" {
			name = "crashes"
		}
		dumps, _ := res["dumps"].([]any)
		return r.collected(i, "diag.crashes", name, map[string]any{"dumps": res["dumps"]}, fmt.Sprintf("크래시 기록 %d건을 모았습니다", len(dumps)), len(dumps))
	}
	return failOut(wire.CodeInvalidArgument, "collect에 모을 대상이 없습니다. file, events, crashes 중 하나를 쓰세요")
}

// collected keeps the result of a diag method as JSON evidence.
func (r *runner) collected(i int, method, name string, result map[string]any, msg string, n int) stepOut {
	data, err := json.MarshalIndent(map[string]any{"schema_version": model.SchemaVersion, "host_id": r.host.HostID, "method": method,
		"saved_at": model.Time(time.Now()), "result": r.sc.ScrubAny(result)}, "", "  ")
	var a model.Artifact
	if err == nil {
		a, err = r.save(i, ActCollect, name, ".json", "report", data)
	}
	if err != nil {
		return failOut(wire.CodeInternal, fmt.Sprintf("모은 결과를 저장하지 못했습니다(%v). 디스크 공간을 확인하세요", err))
	}
	out := pass(msg)
	out.evidence = []model.Artifact{a}
	out.result = map[string]any{"count": n}
	return out
}

// remoteBase returns the last element of a PC path.
func remoteBase(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		p = p[i+1:]
	}
	return p
}

func (r *runner) close(ctx context.Context, s *CloseSpec) stepOut {
	window := r.windowRef(s.Window)
	if window == "" {
		return failOut(wire.CodeInvalidArgument, "닫을 창이 없습니다. window를 주거나 앞에 launch나 wait_window를 두세요")
	}
	res, rid, err := r.call(ctx, "win.action", proto.Message{"window": window, "action": "close"})
	if err != nil {
		return fromError(err)
	}
	if r.window != nil && r.window["id"] == window {
		r.window = nil
	}
	out := pass("창을 닫았습니다")
	out.result = withRID(res, rid)
	return out
}
