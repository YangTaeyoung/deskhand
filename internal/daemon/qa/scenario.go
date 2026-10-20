package qa

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Step kinds.
const (
	ActPush             = "push"
	ActLaunch           = "launch"
	ActWaitWindow       = "wait_window"
	ActClick            = "click"
	ActType             = "type"
	ActKey              = "key"
	ActUIAct            = "ui_act"
	ActAssertUI         = "assert_ui"
	ActAssertScreenText = "assert_screen_text"
	ActAssertFile       = "assert_file"
	ActExec             = "exec"
	ActScreenshot       = "screenshot"
	ActCollect          = "collect"
	ActSleep            = "sleep"
	ActClose            = "close"
)

// stepKinds are the step kinds in the order the errors list them.
var stepKinds = []string{ActPush, ActLaunch, ActWaitWindow, ActClick, ActType, ActKey, ActUIAct, ActAssertUI, ActAssertScreenText,
	ActAssertFile, ActExec, ActScreenshot, ActCollect, ActSleep, ActClose}

// commonKeys are the keys every step may have besides its kind.
var commonKeys = []string{"name", "timeout_ms", "continue_on_fail", "optional"}

// uiActions are the actions of ui_act.
var uiActions = []string{"invoke", "toggle", "expand", "collapse", "select", "set_value", "focus", "scroll_into_view"}

const (
	// maxCollectBytes bounds collect's max_bytes.
	maxCollectBytes = 64 << 20
	// maxSleepMS bounds a sleep step.
	maxSleepMS = 600000
	// secretPrefix starts the names of the vars whose values are masked.
	secretPrefix = "secret_"
	// masked replaces a secret value in what a run records.
	masked = "***"
)

// ScenarioError is a problem of a scenario at a line of its file.
type ScenarioError struct {
	File string
	Line int
	Msg  string
}

func (e *ScenarioError) Error() string { return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg) }

// ParseOptions tune Parse: File names the source in errors, Dir is the
// folder relative push sources start from ("" refuses them), Vars override
// the scenario's vars and RunID is the value of ${run_id}.
type ParseOptions struct {
	File  string
	Dir   string
	Vars  map[string]string
	RunID string
}

// Step is a step of a scenario. Params holds its values as a run records
// them, with secret values masked; Spec is the kind's *…Spec.
type Step struct {
	Phase          string
	Action         string
	Name           string
	Line           int
	TimeoutMS      int64
	ContinueOnFail bool
	Optional       bool
	Params         map[string]any
	Spec           any
}

// Scenario is a parsed and checked scenario. Steps holds the setup, steps
// and teardown in that order.
type Scenario struct {
	Name        string
	Description string
	Host        string
	TimeoutMS   int64
	Steps       []Step
	vars        map[string]string
	display     map[string]string
	secrets     []string
}

// DisplayVars returns the vars of the scenario with the secret values
// masked.
func (s *Scenario) DisplayVars() map[string]string {
	out := make(map[string]string, len(s.display))
	for k, v := range s.display {
		out[k] = v
	}
	return out
}

// Scrub returns v with every secret value replaced by ***.
func (s *Scenario) Scrub(v string) string {
	for _, sec := range s.secrets {
		v = strings.ReplaceAll(v, sec, masked)
	}
	return v
}

// ScrubAny returns v with Scrub applied to every string inside it; maps,
// slices and structs of other types are first turned into their JSON
// values.
func (s *Scenario) ScrubAny(v any) any {
	if len(s.secrets) == 0 {
		return v
	}
	switch t := v.(type) {
	case nil, bool, float64, int, int64:
		return v
	case string:
		return s.Scrub(t)
	case proto.Message:
		return s.ScrubAny(map[string]any(t))
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = s.ScrubAny(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = s.ScrubAny(x)
		}
		return out
	case []string:
		out := make([]string, len(t))
		for i, x := range t {
			out[i] = s.Scrub(x)
		}
		return out
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct, reflect.Pointer:
		b, err := json.Marshal(v)
		if err != nil {
			return masked
		}
		var plain any
		if err := json.Unmarshal(b, &plain); err != nil {
			return masked
		}
		return s.ScrubAny(plain)
	}
	return v
}

// decoder turns the nodes of a scenario into its steps.
type decoder struct {
	file    string
	dir     string
	vars    map[string]string
	display map[string]string
}

func (d *decoder) errf(line int, format string, a ...any) error {
	return &ScenarioError{File: d.file, Line: line, Msg: fmt.Sprintf(format, a...)}
}

// maxSteps bounds the steps of a scenario, setup and teardown included.
const maxSteps = 1000

// Parse parses and checks a scenario. The error is a *ScenarioError whose
// text has the secret values masked.
func Parse(src []byte, opt ParseOptions) (*Scenario, error) {
	d := &decoder{}
	sc, err := d.parse(src, opt)
	if err != nil {
		var se *ScenarioError
		if errors.As(err, &se) {
			se.Msg = d.mask(se.Msg)
		}
		return nil, err
	}
	return sc, nil
}

// mask replaces the secret values the decoder knows in s with ***.
func (d *decoder) mask(s string) string {
	var secrets []string
	for k, v := range d.vars {
		if strings.HasPrefix(k, secretPrefix) && v != "" {
			secrets = append(secrets, v)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, masked)
	}
	return s
}

func (d *decoder) parse(src []byte, opt ParseOptions) (*Scenario, error) {
	file := opt.File
	if file == "" {
		file = "scenario"
	}
	root, err := parseYAML(file, src)
	if err != nil {
		return nil, err
	}
	d.file, d.dir = file, opt.Dir
	d.vars, d.display = map[string]string{"run_id": opt.RunID}, map[string]string{"run_id": opt.RunID}
	if root.kind == nodeNull {
		return nil, d.errf(1, "시나리오가 비어 있습니다. name과 steps를 쓰세요")
	}
	top, err := d.object(root, "시나리오")
	if err != nil {
		return nil, err
	}
	if err := top.only("name", "description", "host", "vars", "timeout_ms", "setup", "steps", "teardown"); err != nil {
		return nil, err
	}
	if err := d.readVars(top.get("vars"), opt.Vars); err != nil {
		return nil, err
	}
	sc := &Scenario{}
	if sc.Name, _, err = top.str("name"); err != nil {
		return nil, err
	}
	if sc.Name == "" {
		return nil, d.errf(root.line, "name이 없습니다. 시나리오 이름을 name에 쓰세요")
	}
	if sc.Description, _, err = top.str("description"); err != nil {
		return nil, err
	}
	if sc.Host, _, err = top.str("host"); err != nil {
		return nil, err
	}
	if sc.TimeoutMS, _, err = top.int("timeout_ms", 0, -1); err != nil {
		return nil, err
	}
	steps := top.get("steps")
	if steps == nil {
		return nil, d.errf(root.line, "steps가 없습니다. 실행할 단계를 steps 목록에 쓰세요")
	}
	for _, ph := range []struct {
		key, phase string
		n          *node
	}{{"setup", PhaseSetup, top.get("setup")}, {"steps", PhaseSteps, steps}, {"teardown", PhaseTeardown, top.get("teardown")}} {
		if ph.n == nil || ph.n.kind == nodeNull && ph.key != "steps" {
			continue
		}
		if ph.n.kind != nodeSeq {
			return nil, d.errf(ph.n.line, "%s는 단계 목록이어야 합니다. 단계마다 - 로 시작해 쓰세요", ph.key)
		}
		if ph.key == "steps" && len(ph.n.items) == 0 {
			return nil, d.errf(ph.n.line, "steps가 비어 있습니다. 단계를 하나 이상 쓰세요")
		}
		if len(sc.Steps)+len(ph.n.items) > maxSteps {
			return nil, d.errf(ph.n.line, "setup·steps·teardown의 단계가 모두 %d개를 넘습니다. 시나리오를 여러 개로 나누세요", maxSteps)
		}
		for _, item := range ph.n.items {
			st, err := d.step(item, ph.phase)
			if err != nil {
				return nil, err
			}
			sc.Steps = append(sc.Steps, st)
		}
	}
	sc.vars, sc.display = d.vars, d.display
	for k, v := range d.vars {
		if strings.HasPrefix(k, secretPrefix) && v != "" {
			sc.secrets = append(sc.secrets, v)
		}
	}
	sort.Slice(sc.secrets, func(i, j int) bool { return len(sc.secrets[i]) > len(sc.secrets[j]) })
	return sc, nil
}

// readVars reads the scenario's vars in order, each with the vars before
// it; a var of the run replaces the scenario's.
func (d *decoder) readVars(n *node, run map[string]string) error {
	for k, v := range run {
		if !validVarName(k) {
			return d.errf(1, "변수 이름 %q는 쓸 수 없습니다. 영문자·숫자·_로 쓰고 숫자로 시작하지 마세요", k)
		}
		d.vars[k] = v
		d.display[k] = v
		if strings.HasPrefix(k, secretPrefix) {
			d.display[k] = masked
		}
	}
	if n == nil || n.kind == nodeNull {
		return nil
	}
	if n.kind != nodeMap {
		return d.errf(n.line, "vars는 이름: 값 매핑이어야 합니다")
	}
	for i, k := range n.keys {
		name := k.value
		if !validVarName(name) {
			return d.errf(k.line, "변수 이름 %q는 쓸 수 없습니다. 영문자·숫자·_로 쓰고 숫자로 시작하지 마세요", name)
		}
		if _, ok := run[name]; ok {
			continue
		}
		v := n.vals[i]
		if v.kind != nodeScalar && v.kind != nodeNull {
			return d.errf(v.line, "변수 %s의 값은 글자여야 합니다", name)
		}
		real, shown, err := d.subst(v.value, v.line)
		if err != nil {
			return err
		}
		d.vars[name] = real
		d.display[name] = shown
		if strings.HasPrefix(name, secretPrefix) {
			d.display[name] = masked
		}
	}
	return nil
}

func validVarName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		switch {
		case c == '_', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// subst replaces the ${name} of s with the values of the vars, and returns
// it and its form with secret values masked.
func (d *decoder) subst(s string, line int) (string, string, error) {
	var real, shown strings.Builder
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "$${"):
			real.WriteString("${")
			shown.WriteString("${")
			i += 3
		case strings.HasPrefix(s[i:], "${"):
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", "", d.errf(line, "${가 닫히지 않았습니다. ${이름} 형식으로 쓰고, 글자 ${는 $${로 쓰세요")
			}
			name := s[i+2 : i+2+end]
			if !validVarName(name) {
				return "", "", d.errf(line, "${%s}의 변수 이름이 올바르지 않습니다. 영문자·숫자·_로 쓰세요", name)
			}
			v, ok := d.vars[name]
			if !ok {
				return "", "", d.errf(line, "${%s}가 정의되지 않았습니다. 시나리오 vars나 실행할 때의 vars(--var %s=값)에 넣으세요", name, name)
			}
			real.WriteString(v)
			shown.WriteString(d.display[name])
			i += 3 + end
		default:
			real.WriteByte(s[i])
			shown.WriteByte(s[i])
			i++
		}
	}
	return real.String(), shown.String(), nil
}

// object is a mapping being read: the keys read so far go into params with
// their recorded values.
type object struct {
	d      *decoder
	n      *node
	what   string
	params map[string]any
}

func (d *decoder) object(n *node, what string) (*object, error) {
	if n.kind != nodeMap {
		return nil, d.errf(n.line, "%s는 키: 값 매핑이어야 합니다", what)
	}
	return &object{d: d, n: n, what: what, params: map[string]any{}}, nil
}

// only refuses a key that is not in keys.
func (o *object) only(keys ...string) error {
	for _, k := range o.n.keys {
		ok := false
		for _, a := range keys {
			ok = ok || a == k.value
		}
		if !ok {
			return o.d.errf(k.line, "%s에 모르는 키 %q가 있습니다. 쓸 수 있는 키: %s", o.what, k.value, strings.Join(keys, ", "))
		}
	}
	return nil
}

// get returns the value of key, nil when it is absent.
func (o *object) get(key string) *node {
	for i, k := range o.n.keys {
		if k.value == key {
			return o.n.vals[i]
		}
	}
	return nil
}

// shown returns the recorded form of the text of key, with secret values
// masked.
func (o *object) shown(key string) string {
	v, _ := o.params[key].(string)
	return v
}

// text returns the substituted text of a scalar node.
func (o *object) text(key string, n *node) (string, string, error) {
	switch n.kind {
	case nodeNull:
		return "", "", nil
	case nodeScalar:
		return o.d.subst(n.value, n.line)
	}
	return "", "", o.d.errf(n.line, "%s의 %s에는 글자 값을 쓰세요", o.what, key)
}

// str returns the text of key; present is false when it is absent or null.
func (o *object) str(key string) (string, bool, error) {
	n := o.get(key)
	if n == nil || n.kind == nodeNull {
		return "", false, nil
	}
	real, shown, err := o.text(key, n)
	if err != nil {
		return "", false, err
	}
	o.params[key] = shown
	return real, true, nil
}

// need returns the text of key and refuses it absent or empty.
func (o *object) need(key string) (string, error) {
	v, ok, err := o.str(key)
	if err != nil {
		return "", err
	}
	if !ok || v == "" {
		return "", o.d.errf(o.n.line, "%s에는 %s가 필요합니다", o.what, key)
	}
	return v, nil
}

// strs returns the texts of key, written as one text or a list of texts.
func (o *object) strs(key string) ([]string, error) {
	n := o.get(key)
	if n == nil || n.kind == nodeNull {
		return nil, nil
	}
	items := []*node{n}
	if n.kind == nodeSeq {
		items = n.items
	}
	var real []string
	var shown []any
	for _, it := range items {
		if it.kind != nodeScalar {
			return nil, o.d.errf(it.line, "%s의 %s에는 글자나 글자 목록을 쓰세요", o.what, key)
		}
		r, s, err := o.d.subst(it.value, it.line)
		if err != nil {
			return nil, err
		}
		real = append(real, r)
		shown = append(shown, s)
	}
	o.params[key] = shown
	return real, nil
}

// texts is strs for expected texts: an empty text is refused.
func (o *object) texts(key string) ([]string, error) {
	out, err := o.strs(key)
	if err != nil {
		return nil, err
	}
	for _, v := range out {
		if v == "" {
			return nil, o.d.errf(o.get(key).line, "%s의 %s에 빈 글이 있습니다. 찾을 글을 쓰거나 그 항목을 지우세요", o.what, key)
		}
	}
	return out, nil
}

// int returns the integer of key, at least min and, when max ≥ 0, at most
// max.
func (o *object) int(key string, min, max int64) (int64, bool, error) {
	n := o.get(key)
	if n == nil || n.kind == nodeNull {
		return 0, false, nil
	}
	real, shown, err := o.text(key, n)
	if err != nil {
		return 0, false, err
	}
	v, err := strconv.ParseInt(strings.TrimSpace(real), 10, 64)
	if err != nil {
		return 0, false, o.d.errf(n.line, "%s의 %s %q는 정수가 아닙니다. 숫자로 쓰세요", o.what, key, shown)
	}
	if v < min || max >= 0 && v > max {
		if max >= 0 {
			return 0, false, o.d.errf(n.line, "%s의 %s는 %d부터 %d까지여야 합니다. 값을 고치세요", o.what, key, min, max)
		}
		return 0, false, o.d.errf(n.line, "%s의 %s는 %d 이상이어야 합니다. 값을 고치세요", o.what, key, min)
	}
	o.params[key] = v
	return v, true, nil
}

// bool returns the boolean of key.
func (o *object) bool(key string) (bool, bool, error) {
	n := o.get(key)
	if n == nil || n.kind == nodeNull {
		return false, false, nil
	}
	real, _, err := o.text(key, n)
	if err != nil {
		return false, false, err
	}
	switch real {
	case "true", "True", "TRUE":
		o.params[key] = true
		return true, true, nil
	case "false", "False", "FALSE":
		o.params[key] = false
		return false, true, nil
	}
	return false, false, o.d.errf(n.line, "%s의 %s는 true나 false여야 합니다", o.what, key)
}

// regex returns the compiled expression of key.
func (o *object) regex(key string) (*regexp.Regexp, error) {
	v, ok, err := o.str(key)
	if err != nil || !ok {
		return nil, err
	}
	re, err := regexp.Compile(v)
	if err != nil {
		return nil, o.d.errf(o.get(key).line, "%s의 %s %q는 올바른 정규식이 아닙니다. Go 정규식(RE2) 문법으로 고치세요", o.what, key, cut(o.shown(key), 40))
	}
	return re, nil
}

// optBool returns a pointer to the boolean of key, nil when absent.
func (o *object) optBool(key string) (*bool, error) {
	v, ok, err := o.bool(key)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// optStr returns a pointer to the text of key, nil when absent.
func (o *object) optStr(key string) (*string, error) {
	v, ok, err := o.str(key)
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// short reads the scalar form of a step: the value of a kind written as
// text goes to key.
func (d *decoder) short(n *node, kind, key string) (string, map[string]any, error) {
	o := &object{d: d, n: &node{kind: nodeMap, line: n.line, keys: []*node{{kind: nodeScalar, value: key}}, vals: []*node{n}}, what: kind, params: map[string]any{}}
	v, _, err := o.str(key)
	return v, o.params, err
}

// step reads one step of phase.
func (d *decoder) step(n *node, phase string) (Step, error) {
	if n.kind != nodeMap {
		return Step{}, d.errf(n.line, "단계는 키: 값 매핑이어야 합니다. - 종류: 값 형식으로 쓰세요(종류: %s)", strings.Join(stepKinds, ", "))
	}
	st := Step{Phase: phase, Line: n.line}
	var kindNode *node
	for i, k := range n.keys {
		switch {
		case contains(commonKeys, k.value):
		case contains(stepKinds, k.value):
			if st.Action != "" {
				return Step{}, d.errf(n.line, "단계에는 종류가 하나만 있어야 합니다(%s, %s). 단계를 둘로 나누세요", st.Action, k.value)
			}
			st.Action, kindNode = k.value, n.vals[i]
		default:
			return Step{}, d.errf(k.line, "단계에 모르는 키 %q가 있습니다. 단계 종류는 %s 중 하나이고 공통 키는 %s입니다",
				k.value, strings.Join(stepKinds, ", "), strings.Join(commonKeys, ", "))
		}
	}
	if st.Action == "" {
		return Step{}, d.errf(n.line, "단계에 종류가 없습니다. %s 중 하나를 키로 쓰세요", strings.Join(stepKinds, ", "))
	}
	common := &object{d: d, n: n, what: "단계", params: map[string]any{}}
	var err error
	if st.Name, _, err = common.str("name"); err != nil {
		return Step{}, err
	}
	if st.TimeoutMS, _, err = common.int("timeout_ms", 0, -1); err != nil {
		return Step{}, err
	}
	if st.ContinueOnFail, _, err = common.bool("continue_on_fail"); err != nil {
		return Step{}, err
	}
	if st.Optional, _, err = common.bool("optional"); err != nil {
		return Step{}, err
	}
	spec, params, err := d.kind(st.Action, kindNode)
	if err != nil {
		return Step{}, err
	}
	st.Spec, st.Params = spec, params
	if st.Name == "" {
		st.Name = defaultName(st.Action, params)
	}
	return st, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// kind reads the value of a step of kind k.
func (d *decoder) kind(k string, n *node) (any, map[string]any, error) {
	switch k {
	case ActLaunch, ActWaitWindow, ActType, ActKey, ActExec, ActScreenshot, ActSleep, ActClose, ActAssertScreenText:
		if n.kind == nodeScalar || n.kind == nodeNull && (k == ActScreenshot || k == ActClose) {
			return d.shortKind(k, n)
		}
	}
	if n.kind == nodeNull {
		return nil, nil, d.errf(n.line, "%s에 값이 없습니다. %s: {…} 형식으로 값을 쓰세요", k, k)
	}
	o, err := d.object(n, k)
	if err != nil {
		return nil, nil, err
	}
	var spec any
	switch k {
	case ActPush:
		spec, err = d.push(o)
	case ActLaunch:
		spec, err = d.launch(o)
	case ActWaitWindow:
		spec, err = d.waitWindow(o)
	case ActClick:
		spec, err = d.click(o)
	case ActType:
		spec, err = d.typeSpec(o)
	case ActKey:
		if err = o.only("keys"); err == nil {
			var keys string
			keys, err = o.need("keys")
			spec = &KeySpec{Keys: keys}
		}
	case ActUIAct:
		spec, err = d.uiAct(o)
	case ActAssertUI:
		spec, err = d.assertUI(o)
	case ActAssertScreenText:
		spec, err = d.assertScreen(o)
	case ActAssertFile:
		spec, err = d.assertFile(o)
	case ActExec:
		spec, err = d.exec(o)
	case ActScreenshot:
		spec, err = d.screenshot(o)
	case ActCollect:
		spec, err = d.collect(o)
	case ActSleep:
		if err = o.only("ms"); err == nil {
			var ms int64
			var ok bool
			ms, ok, err = o.int("ms", 0, maxSleepMS)
			if err == nil && !ok {
				err = d.errf(n.line, "sleep에는 ms가 필요합니다")
			}
			spec = &SleepSpec{MS: ms}
		}
	case ActClose:
		if err = o.only("window"); err == nil {
			var w string
			w, _, err = o.str("window")
			spec = &CloseSpec{Window: w}
		}
	}
	if err != nil {
		return nil, nil, err
	}
	return spec, o.params, nil
}

// shortKind reads a step written as kind: text.
func (d *decoder) shortKind(k string, n *node) (any, map[string]any, error) {
	key := map[string]string{ActLaunch: "path", ActWaitWindow: "title_contains", ActType: "text", ActKey: "keys", ActExec: "cmd",
		ActScreenshot: "name", ActSleep: "ms", ActClose: "window", ActAssertScreenText: "contains"}[k]
	v, params, err := d.short(n, k, key)
	if err != nil {
		return nil, nil, err
	}
	switch k {
	case ActLaunch, ActWaitWindow, ActKey, ActExec, ActAssertScreenText:
		if strings.TrimSpace(v) == "" {
			return nil, nil, d.errf(n.line, "%s의 값이 비어 있습니다. %s: 뒤에 값을 쓰세요", k, k)
		}
	}
	switch k {
	case ActLaunch:
		return &LaunchSpec{Path: v}, params, nil
	case ActWaitWindow:
		return &WaitWindowSpec{TitleContains: v}, params, nil
	case ActType:
		params["method"] = "auto"
		return &TypeSpec{Text: v, Method: "auto"}, params, nil
	case ActKey:
		return &KeySpec{Keys: v}, params, nil
	case ActExec:
		params["expect_code"] = int64(0)
		return &ExecSpec{Cmd: v}, params, nil
	case ActScreenshot:
		params["display"], params["max_width"] = int64(0), int64(defaultMaxWidth)
		return &ScreenshotSpec{Name: v, MaxWidth: defaultMaxWidth}, params, nil
	case ActSleep:
		ms, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return nil, nil, d.errf(n.line, "sleep의 값 %q는 정수가 아닙니다. 밀리초를 숫자로 쓰세요", params[key])
		}
		if ms < 0 || ms > maxSleepMS {
			return nil, nil, d.errf(n.line, "sleep은 0부터 %d밀리초까지입니다. 값을 고치세요", maxSleepMS)
		}
		params["ms"] = ms
		return &SleepSpec{MS: ms}, params, nil
	case ActClose:
		return &CloseSpec{Window: v}, params, nil
	case ActAssertScreenText:
		params["contains"] = []any{params["contains"]}
		return &AssertScreenTextSpec{Contains: []string{v}}, params, nil
	}
	return nil, nil, d.errf(n.line, "%s의 값을 읽지 못했습니다", k)
}

// defaultMaxWidth is the width a screenshot is scaled down to.
const defaultMaxWidth = 1920

func (d *decoder) push(o *object) (*PushSpec, error) {
	if err := o.only("from", "to", "on_conflict", "mkdir"); err != nil {
		return nil, err
	}
	from, err := o.need("from")
	if err != nil {
		return nil, err
	}
	to, err := o.need("to")
	if err != nil {
		return nil, err
	}
	line := o.get("from").line
	shownFrom := o.shown("from")
	if !filepath.IsAbs(from) {
		if d.dir == "" {
			return nil, d.errf(line, "push의 from %q가 상대 경로입니다. 절대 경로로 쓰거나 시나리오를 파일로 주거나 base_dir을 주세요", shownFrom)
		}
		from, shownFrom = filepath.Join(d.dir, from), filepath.Join(d.dir, shownFrom)
	}
	from = filepath.Clean(from)
	if _, err := os.Stat(from); err != nil {
		return nil, d.errf(line, "push의 from %s가 없습니다. 이 Mac의 경로를 확인하세요", filepath.Clean(shownFrom))
	}
	o.params["from"] = from
	p := &PushSpec{From: from, To: to, OnConflict: "replace", Mkdir: true}
	if v, ok, err := o.str("on_conflict"); err != nil {
		return nil, err
	} else if ok {
		if !wire.IsConflictPolicy(v) {
			return nil, d.errf(o.get("on_conflict").line, "push의 on_conflict %q는 쓸 수 없습니다. %s 중 하나를 쓰세요", o.shown("on_conflict"), strings.Join(wire.ConflictPolicies(), ", "))
		}
		p.OnConflict = v
	}
	o.params["on_conflict"] = p.OnConflict
	if v, ok, err := o.bool("mkdir"); err != nil {
		return nil, err
	} else if ok {
		p.Mkdir = v
	}
	o.params["mkdir"] = p.Mkdir
	return p, nil
}

func (d *decoder) launch(o *object) (*LaunchSpec, error) {
	if err := o.only("path", "args", "cwd", "wait_window_ms"); err != nil {
		return nil, err
	}
	path, err := o.need("path")
	if err != nil {
		return nil, err
	}
	l := &LaunchSpec{Path: path}
	if l.Args, err = o.strs("args"); err != nil {
		return nil, err
	}
	if l.Cwd, _, err = o.str("cwd"); err != nil {
		return nil, err
	}
	if v, ok, err := o.int("wait_window_ms", 0, 300000); err != nil {
		return nil, err
	} else if ok {
		l.WaitWindowMS = &v
	}
	return l, nil
}

func (d *decoder) waitWindow(o *object) (*WaitWindowSpec, error) {
	if err := o.only("title", "title_contains", "process", "pid", "gone"); err != nil {
		return nil, err
	}
	w := &WaitWindowSpec{}
	var err error
	if w.Title, _, err = o.str("title"); err != nil {
		return nil, err
	}
	if w.TitleContains, _, err = o.str("title_contains"); err != nil {
		return nil, err
	}
	if w.Process, _, err = o.str("process"); err != nil {
		return nil, err
	}
	pid, _, err := o.int("pid", 1, -1)
	if err != nil {
		return nil, err
	}
	w.PID = int(pid)
	if w.Gone, _, err = o.bool("gone"); err != nil {
		return nil, err
	}
	if w.Title == "" && w.TitleContains == "" && w.Process == "" && w.PID == 0 {
		return nil, d.errf(o.n.line, "wait_window에는 title, title_contains, process, pid 중 하나 이상이 필요합니다")
	}
	return w, nil
}

// selector reads the selector keys of o.
func (d *decoder) selector(o *object) (Selector, error) {
	var s Selector
	var err error
	for _, f := range []struct {
		key string
		dst *string
	}{{"window", &s.Window}, {"role", &s.Role}, {"name", &s.Name}, {"name_contains", &s.NameContains}, {"automation_id", &s.AutomationID}} {
		if *f.dst, _, err = o.str(f.key); err != nil {
			return s, err
		}
	}
	if v, ok, err := o.int("index", 0, -1); err != nil {
		return s, err
	} else if ok {
		i := int(v)
		s.Index = &i
	}
	if s.Role == "" && s.Name == "" && s.NameContains == "" && s.AutomationID == "" {
		return s, d.errf(o.n.line, "%s의 선택자에 role, name, name_contains, automation_id 중 하나 이상이 필요합니다", o.what)
	}
	return s, nil
}

var selectorKeys = []string{"window", "role", "name", "name_contains", "automation_id", "index"}

func (d *decoder) click(o *object) (*ClickSpec, error) {
	if err := o.only("element", "x", "y", "window", "button", "count"); err != nil {
		return nil, err
	}
	c := &ClickSpec{Button: "left", Count: 1}
	if n := o.get("element"); n != nil && n.kind != nodeNull {
		eo, err := d.object(n, "click의 element")
		if err != nil {
			return nil, err
		}
		if err := eo.only(selectorKeys...); err != nil {
			return nil, err
		}
		sel, err := d.selector(eo)
		if err != nil {
			return nil, err
		}
		c.Element = &sel
		o.params["element"] = eo.params
	}
	x, hasX, err := o.int("x", 0, -1)
	if err != nil {
		return nil, err
	}
	y, hasY, err := o.int("y", 0, -1)
	if err != nil {
		return nil, err
	}
	if hasX != hasY || (c.Element != nil) == hasX {
		return nil, d.errf(o.n.line, "click에는 element나 x·y 둘 중 하나가 필요합니다")
	}
	if hasX {
		xi, yi := int(x), int(y)
		c.X, c.Y = &xi, &yi
	}
	if c.Window, _, err = o.str("window"); err != nil {
		return nil, err
	}
	if v, ok, err := o.str("button"); err != nil {
		return nil, err
	} else if ok {
		if !contains([]string{"left", "right", "middle"}, v) {
			return nil, d.errf(o.get("button").line, "click의 button %q는 쓸 수 없습니다. left, right, middle 중 하나를 쓰세요", o.shown("button"))
		}
		c.Button = v
	}
	if v, ok, err := o.int("count", 1, 2); err != nil {
		return nil, err
	} else if ok {
		c.Count = int(v)
	}
	o.params["button"], o.params["count"] = c.Button, int64(c.Count)
	return c, nil
}

func (d *decoder) typeSpec(o *object) (*TypeSpec, error) {
	if err := o.only("text", "method"); err != nil {
		return nil, err
	}
	text, ok, err := o.str("text")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, d.errf(o.n.line, "type에는 text가 필요합니다")
	}
	t := &TypeSpec{Text: text, Method: "auto"}
	if v, ok, err := o.str("method"); err != nil {
		return nil, err
	} else if ok {
		if !contains([]string{"auto", "unicode", "paste"}, v) {
			return nil, d.errf(o.get("method").line, "type의 method %q는 쓸 수 없습니다. auto, unicode, paste 중 하나를 쓰세요", o.shown("method"))
		}
		t.Method = v
	}
	o.params["method"] = t.Method
	return t, nil
}

func (d *decoder) uiAct(o *object) (*UIActSpec, error) {
	if err := o.only(append([]string{"action", "value"}, selectorKeys...)...); err != nil {
		return nil, err
	}
	action, err := o.need("action")
	if err != nil {
		return nil, err
	}
	if !contains(uiActions, action) {
		return nil, d.errf(o.get("action").line, "ui_act의 action %q는 쓸 수 없습니다. %s 중 하나를 쓰세요", o.shown("action"), strings.Join(uiActions, ", "))
	}
	sel, err := d.selector(o)
	if err != nil {
		return nil, err
	}
	value, hasValue, err := o.str("value")
	if err != nil {
		return nil, err
	}
	if action == "set_value" && !hasValue {
		if n := o.get("value"); n != nil {
			hasValue = true
		} else {
			return nil, d.errf(o.n.line, "ui_act set_value에는 value가 필요합니다")
		}
	}
	if action != "set_value" && hasValue {
		return nil, d.errf(o.get("value").line, "value는 action set_value에만 씁니다. value를 지우세요")
	}
	return &UIActSpec{Selector: sel, Action: action, Value: value}, nil
}

func (d *decoder) assertUI(o *object) (*AssertUISpec, error) {
	if err := o.only(append([]string{"exists", "text", "text_contains", "text_matches", "enabled", "count"}, selectorKeys...)...); err != nil {
		return nil, err
	}
	sel, err := d.selector(o)
	if err != nil {
		return nil, err
	}
	a := &AssertUISpec{Selector: sel}
	if a.Exists, err = o.optBool("exists"); err != nil {
		return nil, err
	}
	if a.Text, err = o.optStr("text"); err != nil {
		return nil, err
	}
	if a.TextContains, err = o.optStr("text_contains"); err != nil {
		return nil, err
	}
	if a.TextMatches, err = o.regex("text_matches"); err != nil {
		return nil, err
	}
	if a.Enabled, err = o.optBool("enabled"); err != nil {
		return nil, err
	}
	if v, ok, err := o.int("count", 0, -1); err != nil {
		return nil, err
	} else if ok {
		c := int(v)
		a.Count = &c
	}
	if a.Exists != nil && !*a.Exists && (a.Text != nil || a.TextContains != nil || a.TextMatches != nil || a.Enabled != nil) {
		return nil, d.errf(o.n.line, "exists: false와 text·enabled 기준을 함께 쓸 수 없습니다. 하나만 남기세요")
	}
	return a, nil
}

func (d *decoder) assertScreen(o *object) (*AssertScreenTextSpec, error) {
	if err := o.only("contains", "not_contains", "matches", "window"); err != nil {
		return nil, err
	}
	a := &AssertScreenTextSpec{}
	var err error
	if a.Contains, err = o.texts("contains"); err != nil {
		return nil, err
	}
	if a.NotContains, err = o.texts("not_contains"); err != nil {
		return nil, err
	}
	if a.Matches, err = o.regex("matches"); err != nil {
		return nil, err
	}
	if a.Window, _, err = o.str("window"); err != nil {
		return nil, err
	}
	if len(a.Contains) == 0 && len(a.NotContains) == 0 && a.Matches == nil {
		return nil, d.errf(o.n.line, "assert_screen_text에는 contains, not_contains, matches 중 하나 이상이 필요합니다")
	}
	return a, nil
}

func (d *decoder) assertFile(o *object) (*AssertFileSpec, error) {
	if err := o.only("path", "exists", "contains", "not_contains", "matches", "sha256", "size"); err != nil {
		return nil, err
	}
	path, err := o.need("path")
	if err != nil {
		return nil, err
	}
	a := &AssertFileSpec{Path: path}
	if a.Exists, err = o.optBool("exists"); err != nil {
		return nil, err
	}
	if a.Contains, err = o.texts("contains"); err != nil {
		return nil, err
	}
	if a.NotContains, err = o.texts("not_contains"); err != nil {
		return nil, err
	}
	if a.Matches, err = o.regex("matches"); err != nil {
		return nil, err
	}
	if v, ok, err := o.str("sha256"); err != nil {
		return nil, err
	} else if ok {
		v = strings.ToLower(v)
		if len(v) != 64 || strings.Trim(v, "0123456789abcdef") != "" {
			return nil, d.errf(o.get("sha256").line, "assert_file의 sha256 %q는 64자리 16진수가 아닙니다. 해시를 그대로 쓰세요", cut(o.shown("sha256"), 20))
		}
		a.SHA256 = v
		o.params["sha256"] = v
	}
	if v, ok, err := o.int("size", 0, -1); err != nil {
		return nil, err
	} else if ok {
		a.Size = &v
	}
	if a.Exists != nil && !*a.Exists && (len(a.Contains) > 0 || len(a.NotContains) > 0 || a.Matches != nil || a.SHA256 != "" || a.Size != nil) {
		return nil, d.errf(o.n.line, "exists: false와 내용·크기·해시 기준을 함께 쓸 수 없습니다. 하나만 남기세요")
	}
	return a, nil
}

func (d *decoder) exec(o *object) (*ExecSpec, error) {
	if err := o.only("argv", "cmd", "shell", "cwd", "expect_code", "stdout_contains", "stderr_contains"); err != nil {
		return nil, err
	}
	e := &ExecSpec{}
	var err error
	if e.Argv, err = o.strs("argv"); err != nil {
		return nil, err
	}
	if e.Cmd, _, err = o.str("cmd"); err != nil {
		return nil, err
	}
	if (len(e.Argv) > 0) == (e.Cmd != "") {
		return nil, d.errf(o.n.line, "exec에는 argv와 cmd 중 하나만 씁니다")
	}
	if e.Shell, _, err = o.str("shell"); err != nil {
		return nil, err
	}
	if e.Cwd, _, err = o.str("cwd"); err != nil {
		return nil, err
	}
	code, _, err := o.int("expect_code", -1<<31, 1<<32)
	if err != nil {
		return nil, err
	}
	e.ExpectCode = int(code)
	o.params["expect_code"] = code
	if e.StdoutContains, err = o.texts("stdout_contains"); err != nil {
		return nil, err
	}
	if e.StderrContains, err = o.texts("stderr_contains"); err != nil {
		return nil, err
	}
	return e, nil
}

func (d *decoder) screenshot(o *object) (*ScreenshotSpec, error) {
	if err := o.only("name", "display", "window", "max_width"); err != nil {
		return nil, err
	}
	s := &ScreenshotSpec{MaxWidth: defaultMaxWidth}
	var err error
	if s.Name, _, err = o.str("name"); err != nil {
		return nil, err
	}
	display, _, err := o.int("display", 0, 64)
	if err != nil {
		return nil, err
	}
	s.Display = int(display)
	if s.Window, _, err = o.str("window"); err != nil {
		return nil, err
	}
	if v, ok, err := o.int("max_width", 0, 16384); err != nil {
		return nil, err
	} else if ok {
		s.MaxWidth = int(v)
	}
	o.params["display"], o.params["max_width"] = int64(s.Display), int64(s.MaxWidth)
	return s, nil
}

func (d *decoder) collect(o *object) (*CollectSpec, error) {
	if err := o.only("file", "max_bytes", "events", "crashes", "name"); err != nil {
		return nil, err
	}
	c := &CollectSpec{MaxBytes: DefaultCollectBytes}
	var err error
	if c.File, _, err = o.str("file"); err != nil {
		return nil, err
	}
	if c.Name, _, err = o.str("name"); err != nil {
		return nil, err
	}
	sources := 0
	if c.File != "" {
		sources++
	}
	if n := o.get("events"); n != nil {
		sources++
		if c.Events, err = d.events(n, o); err != nil {
			return nil, err
		}
	}
	if n := o.get("crashes"); n != nil {
		sources++
		c.Crashes = &CrashesQuery{}
		if n.kind != nodeNull {
			co, err := d.object(n, "collect의 crashes")
			if err != nil {
				return nil, err
			}
			if err := co.only("since"); err != nil {
				return nil, err
			}
			if c.Crashes.Since, _, err = co.str("since"); err != nil {
				return nil, err
			}
			if c.Crashes.Since != "" {
				if _, err := wire.ParseDiagTime(c.Crashes.Since); err != nil {
					return nil, d.errf(co.get("since").line, "crashes의 since %q는 RFC 3339 시각이 아닙니다. 2026-10-20T00:00:00Z처럼 쓰세요", co.shown("since"))
				}
			}
			o.params["crashes"] = co.params
		} else {
			o.params["crashes"] = map[string]any{}
		}
	}
	if sources != 1 {
		return nil, d.errf(o.n.line, "collect에는 file, events, crashes 중 하나만 씁니다")
	}
	if v, ok, err := o.int("max_bytes", 1, maxCollectBytes); err != nil {
		return nil, err
	} else if ok {
		if c.File == "" {
			return nil, d.errf(o.get("max_bytes").line, "max_bytes는 collect file에만 씁니다. max_bytes를 지우세요")
		}
		c.MaxBytes = v
	}
	if c.File != "" {
		o.params["max_bytes"] = c.MaxBytes
	}
	return c, nil
}

// events reads the diag.events query of collect.
func (d *decoder) events(n *node, parent *object) (*EventsQuery, error) {
	q := &EventsQuery{}
	if n.kind == nodeNull {
		parent.params["events"] = map[string]any{}
		return q, nil
	}
	eo, err := d.object(n, "collect의 events")
	if err != nil {
		return nil, err
	}
	if err := eo.only("log", "since", "until", "levels", "provider", "max"); err != nil {
		return nil, err
	}
	for _, f := range []struct {
		key string
		dst *string
	}{{"log", &q.Log}, {"since", &q.Since}, {"until", &q.Until}, {"provider", &q.Provider}} {
		if *f.dst, _, err = eo.str(f.key); err != nil {
			return nil, err
		}
	}
	if q.Levels, err = eo.strs("levels"); err != nil {
		return nil, err
	}
	max, hasMax, err := eo.int("max", 1, wire.DiagEventsMaxLimit)
	if err != nil {
		return nil, err
	}
	p := wire.DiagEventsParams{Log: q.Log, Since: q.Since, Until: q.Until, Levels: q.Levels, Provider: q.Provider}
	if hasMax {
		m := int(max)
		p.Max = &m
	}
	checked, fault := p.Check(time.Now())
	if fault != nil {
		return nil, d.errf(n.line, "collect의 events에서 %s %v를 쓸 수 없습니다. log는 %s, levels는 %s, 시각은 RFC 3339로 쓰세요",
			fault.Field, eo.params[fault.Field], strings.Join(wire.DiagEventLogs, "·"), strings.Join(wire.DiagEventLevels, "·"))
	}
	q.Log, q.Levels, q.Max = checked.Log, checked.Levels, int(max)
	parent.params["events"] = eo.params
	return q, nil
}

// defaultName names a step without a name from its kind and main value.
func defaultName(k string, p map[string]any) string {
	pick := func(keys ...string) string {
		for _, key := range keys {
			switch v := p[key].(type) {
			case string:
				if v != "" {
					return cut(strings.ReplaceAll(strings.TrimSpace(v), "\n", " "), 60)
				}
			case []any:
				var parts []string
				for _, x := range v {
					parts = append(parts, fmt.Sprint(x))
				}
				if len(parts) > 0 {
					return cut(strings.Join(parts, " "), 60)
				}
			case int64:
				return strconv.FormatInt(v, 10)
			case map[string]any:
				for _, sub := range []string{"name", "name_contains", "automation_id", "role"} {
					if s, ok := v[sub].(string); ok && s != "" {
						return s
					}
				}
			}
		}
		return ""
	}
	var what string
	switch k {
	case ActPush:
		what = pick("to")
	case ActLaunch:
		what = pick("path")
	case ActWaitWindow:
		what = pick("title", "title_contains", "process", "pid")
	case ActClick:
		what = pick("element")
		if what == "" {
			what = fmt.Sprintf("%v,%v", p["x"], p["y"])
		}
	case ActKey:
		what = pick("keys")
	case ActUIAct:
		what = strings.TrimSpace(pick("action") + " " + pick("name", "name_contains", "automation_id", "role"))
	case ActAssertUI:
		what = pick("name", "name_contains", "automation_id", "role")
	case ActAssertScreenText:
		what = pick("contains", "not_contains", "matches")
	case ActAssertFile:
		what = pick("path")
	case ActExec:
		what = pick("cmd", "argv")
	case ActScreenshot:
		what = pick("name")
	case ActCollect:
		what = pick("file", "name")
		if what == "" && p["events"] != nil {
			what = "events"
		}
		if what == "" && p["crashes"] != nil {
			what = "crashes"
		}
	case ActSleep:
		what = pick("ms") + "ms"
	case ActClose:
		what = pick("window")
	}
	if what == "" {
		return k
	}
	return k + " " + what
}
