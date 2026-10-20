package qa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/artifacts"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

// Files of a result bundle.
const (
	reportJSON = "report.json"
	reportMD   = "report.md"
	reportHTML = "report.html"
)

// writeBundle writes report.json, report.md, report.html and manifest.json
// of rec into dir and sets the run's report paths.
func writeBundle(rec *record, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	run := &rec.Run
	run.ReportDir = dir
	run.Report = &ReportPaths{JSON: filepath.Join(dir, reportJSON), MD: filepath.Join(dir, reportMD), HTML: filepath.Join(dir, reportHTML),
		Manifest: filepath.Join(dir, artifacts.ManifestName)}
	generated := model.Time(time.Now())
	doc := map[string]any{"schema_version": model.SchemaVersion, "generated_at": generated, "run": run, "scenario": rec.Scenario,
		"vars": rec.Vars, "host": rec.Host, "daemon_version": rec.Daemon}
	if rec.Environment != nil {
		doc["environment"] = rec.Environment
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(run.Report.JSON, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(run.Report.MD, []byte(markdown(rec, dir)), 0o600); err != nil {
		return err
	}
	page, err := htmlReport(rec, dir)
	if err != nil {
		return err
	}
	if err := os.WriteFile(run.Report.HTML, page, 0o600); err != nil {
		return err
	}
	known := map[string]artifacts.ManifestFile{}
	for _, s := range run.Steps {
		for _, a := range s.Evidence {
			if rel, ok := relPath(dir, a.Path); ok {
				known[rel] = artifacts.ManifestFile{ArtifactID: a.ArtifactID, Kind: a.Kind, MIME: a.MIME}
			}
		}
	}
	for _, name := range []string{reportJSON, reportMD, reportHTML} {
		f, err := artifacts.Describe(dir, name, nil)
		if err != nil {
			return err
		}
		f.Kind = "report"
		known[name] = f
	}
	m := artifacts.Manifest{SchemaVersion: model.SchemaVersion, RunID: run.RunID, JobID: run.JobID, GeneratedAt: generated}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, ok := relPath(dir, p)
		if !ok || rel == artifacts.ManifestName {
			return nil
		}
		f, err := artifacts.Describe(dir, rel, known)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, f)
		return nil
	})
	if err != nil {
		return err
	}
	return artifacts.WriteManifest(dir, m)
}

// relPath returns p relative to dir with / between its parts.
func relPath(dir, p string) (string, bool) {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// phaseLabel is the Korean word of a phase.
func phaseLabel(p string) string {
	switch p {
	case PhaseSetup:
		return "준비"
	case PhaseTeardown:
		return "정리"
	}
	return "단계"
}

// localTime writes an API time in the local zone, "" when it is empty.
func localTime(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04:05 MST")
}

// duration writes milliseconds for people.
func duration(ms *int64) string {
	if ms == nil {
		return ""
	}
	if *ms < 1000 {
		return fmt.Sprintf("%dms", *ms)
	}
	return fmt.Sprintf("%.1f초", float64(*ms)/1000)
}

// runDuration is the time between the start and the end of a run.
func runDuration(run Run) string {
	a, err1 := time.Parse(time.RFC3339Nano, run.StartedAt)
	b, err2 := time.Parse(time.RFC3339Nano, run.FinishedAt)
	if err1 != nil || err2 != nil {
		return ""
	}
	ms := b.Sub(a).Milliseconds()
	return duration(&ms)
}

// valueText writes an assertion value in one line.
func valueText(v any) string {
	switch t := v.(type) {
	case nil:
		return "—"
	case string:
		if t == "" {
			return "(비어 있음)"
		}
		return t
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// mdCell escapes a text for a Markdown table cell.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\r\n", "<br>")
	return strings.ReplaceAll(s, "\n", "<br>")
}

func counts(run Run) string {
	return fmt.Sprintf("통과 %d · 실패 %d · 차단 %d · 실행 안 함 %d, 전체 %d단계", run.StepsPassed, run.StepsFailed, run.StepsBlocked, run.StepsNotRun, run.StepsTotal)
}

func hostText(h hostInfo) string {
	s := fmt.Sprintf("%s (%s, %s/%s", h.Name, h.HostID, h.OS, h.Arch)
	if h.AgentVersion != "" {
		s += ", 에이전트 " + h.AgentVersion
	}
	return s + ")"
}

// envLines describes the PC of the run from its diag.system fields.
func envLines(env map[string]any) []string {
	if env == nil {
		return nil
	}
	var out []string
	if os, ok := env["os"].(map[string]any); ok {
		line := "운영체제:"
		for _, k := range []string{"name", "version", "edition"} {
			if v, ok := os[k].(string); ok && v != "" {
				line += " " + v
			}
		}
		if b, ok := os["build"].(string); ok && b != "" {
			line += " (빌드 " + b + ")"
		}
		out = append(out, line)
	}
	if v, ok := env["computer_name"]; ok {
		out = append(out, fmt.Sprintf("컴퓨터 이름: %v", v))
	}
	if cpu, ok := env["cpu"].(map[string]any); ok {
		out = append(out, fmt.Sprintf("CPU: %v (%v코어 %v스레드)", cpu["name"], cpu["cores"], cpu["threads"]))
	}
	if mem, ok := env["memory"].(map[string]any); ok {
		if total, ok := mem["total_bytes"].(float64); ok {
			out = append(out, fmt.Sprintf("메모리: %.1f GiB", total/(1<<30)))
		}
	}
	if gpus, ok := env["gpus"].([]any); ok {
		for _, g := range gpus {
			if gm, ok := g.(map[string]any); ok {
				out = append(out, fmt.Sprintf("GPU: %v (드라이버 %v)", gm["name"], gm["driver_version"]))
			}
		}
	}
	return out
}

func sortedVars(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// markdown writes the Markdown report of rec, whose evidence lies in dir.
func markdown(rec *record, dir string) string {
	run := rec.Run
	var b strings.Builder
	fmt.Fprintf(&b, "# QA 결과: %s\n\n", run.Name)
	fmt.Fprintf(&b, "- 결과: **%s** (%s)\n", runLabel(run.Status), counts(run))
	fmt.Fprintf(&b, "- PC: %s\n", hostText(rec.Host))
	if rec.Scenario.Path != "" {
		fmt.Fprintf(&b, "- 시나리오: `%s` (sha256 `%s`)\n", rec.Scenario.Path, rec.Scenario.SHA256)
	} else {
		fmt.Fprintf(&b, "- 시나리오: 글로 받은 시나리오 (sha256 `%s`)\n", rec.Scenario.SHA256)
	}
	fmt.Fprintf(&b, "- 실행 ID: `%s` · 작업 ID: `%s`\n", run.RunID, run.JobID)
	fmt.Fprintf(&b, "- 시작: %s · 끝: %s · 걸린 시간: %s\n", localTime(run.StartedAt), localTime(run.FinishedAt), runDuration(run))
	if run.Error != nil {
		fmt.Fprintf(&b, "- 실행 오류: %s (`%s`)\n", run.Error.Message, run.Error.Code)
	}
	b.WriteString("\n## 단계\n\n| # | 구분 | 단계 | 종류 | 결과 | 걸린 시간 | 메시지 |\n|---|---|---|---|---|---|---|\n")
	for _, s := range run.Steps {
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s | %s |\n", s.Index, phaseLabel(s.Phase), mdCell(s.Name), s.Action, stepLabel(s.Status),
			duration(s.DurationMS), mdCell(s.Message))
	}
	detail := false
	for _, s := range run.Steps {
		if len(s.Assertions) == 0 && len(s.Evidence) == 0 && s.Status != StepFail && s.Status != StepBlocked {
			continue
		}
		if !detail {
			b.WriteString("\n## 세부와 증거\n")
			detail = true
		}
		fmt.Fprintf(&b, "\n### %d. %s (%s) — %s\n\n", s.Index, s.Name, s.Action, stepLabel(s.Status))
		if s.Message != "" {
			fmt.Fprintf(&b, "%s\n\n", s.Message)
		}
		if s.Error != nil && s.Error.Code != "" {
			fmt.Fprintf(&b, "오류 코드: `%s`\n\n", s.Error.Code)
		}
		if len(s.Assertions) > 0 {
			b.WriteString("| 기준 | 기대값 | 실제값 | 결과 |\n|---|---|---|---|\n")
			for _, a := range s.Assertions {
				ok := "통과"
				if !a.Passed {
					ok = "실패"
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", a.Kind, mdCell(cut(valueText(a.Expected), 300)), mdCell(cut(valueText(a.Actual), 300)), ok)
			}
			b.WriteString("\n")
		}
		for _, a := range s.Evidence {
			rel, ok := relPath(dir, a.Path)
			if !ok {
				continue
			}
			if a.Kind == "screenshot" {
				fmt.Fprintf(&b, "![%s](%s)\n\n", a.Name, rel)
			} else {
				fmt.Fprintf(&b, "- 증거: [%s](%s) (%s, %d바이트)\n", a.Name, rel, a.Kind, a.Size)
			}
		}
	}
	if len(run.Unsupported) > 0 {
		b.WriteString("\n## 이 PC·데몬에서 할 수 없던 단계\n\n")
		for _, u := range run.Unsupported {
			fmt.Fprintf(&b, "- %s: %s (`%s`)\n", u.Action, u.Reason, u.Code)
		}
	}
	b.WriteString("\n## 환경\n\n")
	for _, l := range envLines(rec.Environment) {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	fmt.Fprintf(&b, "- 데몬 버전: %s\n", rec.Daemon)
	if len(rec.Vars) > 0 {
		b.WriteString("\n## 변수\n\n| 이름 | 값 |\n|---|---|\n")
		for _, k := range sortedVars(rec.Vars) {
			fmt.Fprintf(&b, "| %s | %s |\n", k, mdCell(rec.Vars[k]))
		}
	}
	return b.String()
}

// htmlView is what the HTML report shows.
type htmlView struct {
	Run       Run
	RunLabel  string
	Counts    string
	Host      string
	Scenario  scenarioInfo
	Started   string
	Finished  string
	Duration  string
	Steps     []htmlStep
	Env       []string
	Daemon    string
	Vars      [][2]string
	Generated string
}

type htmlStep struct {
	StepResult
	PhaseLabel  string
	StatusLabel string
	Duration    string
	Asserts     []htmlAssert
	Shots       []htmlFile
	Files       []htmlFile
	Detail      bool
}

type htmlAssert struct {
	Kind, Expected, Actual string
	Passed                 bool
}

type htmlFile struct {
	Name, Rel, Kind string
	Size            int64
}

// htmlReport writes the HTML report of rec, whose evidence lies in dir.
func htmlReport(rec *record, dir string) ([]byte, error) {
	run := rec.Run
	v := htmlView{Run: run, RunLabel: runLabel(run.Status), Counts: counts(run), Host: hostText(rec.Host), Scenario: rec.Scenario,
		Started: localTime(run.StartedAt), Finished: localTime(run.FinishedAt), Duration: runDuration(run), Env: envLines(rec.Environment),
		Daemon: rec.Daemon, Generated: localTime(model.Time(time.Now()))}
	for _, k := range sortedVars(rec.Vars) {
		v.Vars = append(v.Vars, [2]string{k, rec.Vars[k]})
	}
	for _, s := range run.Steps {
		hs := htmlStep{StepResult: s, PhaseLabel: phaseLabel(s.Phase), StatusLabel: stepLabel(s.Status), Duration: duration(s.DurationMS)}
		for _, a := range s.Assertions {
			hs.Asserts = append(hs.Asserts, htmlAssert{Kind: a.Kind, Expected: cut(valueText(a.Expected), 500), Actual: cut(valueText(a.Actual), 500), Passed: a.Passed})
		}
		for _, a := range s.Evidence {
			rel, ok := relPath(dir, a.Path)
			if !ok {
				continue
			}
			f := htmlFile{Name: a.Name, Rel: rel, Kind: a.Kind, Size: a.Size}
			if a.Kind == "screenshot" {
				hs.Shots = append(hs.Shots, f)
			} else {
				hs.Files = append(hs.Files, f)
			}
		}
		hs.Detail = len(hs.Asserts) > 0 || len(hs.Shots) > 0 || len(hs.Files) > 0 || s.Status == StepFail || s.Status == StepBlocked
		v.Steps = append(v.Steps, hs)
	}
	var buf bytes.Buffer
	if err := reportPage.Execute(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var reportPage = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="ko">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>QA 결과: {{.Run.Name}}</title>
<style>
:root { color-scheme: light dark; --bg: #ffffff; --fg: #1d1d1f; --muted: #6e6e73; --line: #d2d2d7; --card: #f5f5f7;
  --pass: #1a7f37; --fail: #cf222e; --blocked: #9a6700; --skip: #6e6e73; }
@media (prefers-color-scheme: dark) {
  :root { --bg: #1c1c1e; --fg: #f5f5f7; --muted: #a1a1a6; --line: #3a3a3c; --card: #2c2c2e;
    --pass: #3fb950; --fail: #f85149; --blocked: #d29922; --skip: #8b8b90; }
}
* { box-sizing: border-box; }
body { margin: 0; padding: 24px 16px 48px; background: var(--bg); color: var(--fg);
  font: 14px/1.5 -apple-system, BlinkMacSystemFont, "Apple SD Gothic Neo", "Malgun Gothic", sans-serif; }
main { max-width: 1080px; margin: 0 auto; }
h1 { font-size: 22px; margin: 0 0 8px; }
h2 { font-size: 17px; margin: 32px 0 12px; }
h3 { font-size: 15px; margin: 0 0 8px; }
.summary { display: grid; grid-template-columns: max-content 1fr; gap: 4px 16px; margin: 16px 0; }
.summary dt { color: var(--muted); }
.summary dd { margin: 0; word-break: break-all; }
.badge { display: inline-block; padding: 1px 8px; border-radius: 10px; font-weight: 600; font-size: 12px; color: #fff; }
.s-pass, .s-passed { background: var(--pass); }
.s-fail, .s-failed, .s-error { background: var(--fail); }
.s-blocked, .s-cancelled { background: var(--blocked); }
.s-not_run, .s-pending, .s-running { background: var(--skip); }
table { width: 100%; border-collapse: collapse; margin: 8px 0; }
th, td { text-align: left; vertical-align: top; padding: 6px 8px; border-bottom: 1px solid var(--line); }
th { color: var(--muted); font-weight: 500; }
td.wrap { white-space: pre-wrap; word-break: break-word; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
.card { background: var(--card); border-radius: 10px; padding: 14px 16px; margin: 12px 0; }
.card img { max-width: 100%; border: 1px solid var(--line); border-radius: 6px; margin-top: 8px; }
.muted { color: var(--muted); }
.ok { color: var(--pass); } .bad { color: var(--fail); }
</style>
</head>
<body>
<main>
<h1>QA 결과: {{.Run.Name}}</h1>
<p><span class="badge s-{{.Run.Status}}">{{.RunLabel}}</span> <span class="muted">{{.Counts}}</span></p>
<dl class="summary">
<dt>PC</dt><dd>{{.Host}}</dd>
<dt>시나리오</dt><dd>{{if .Scenario.Path}}<code>{{.Scenario.Path}}</code>{{else}}글로 받은 시나리오{{end}} <span class="muted">sha256 <code>{{.Scenario.SHA256}}</code></span></dd>
<dt>실행 ID</dt><dd><code>{{.Run.RunID}}</code> · 작업 <code>{{.Run.JobID}}</code></dd>
<dt>시작</dt><dd>{{.Started}}</dd>
<dt>끝</dt><dd>{{.Finished}}</dd>
<dt>걸린 시간</dt><dd>{{.Duration}}</dd>
{{if .Run.Error}}<dt>실행 오류</dt><dd class="bad">{{.Run.Error.Message}} <code>{{.Run.Error.Code}}</code></dd>{{end}}
</dl>
<h2>단계</h2>
<table>
<thead><tr><th>#</th><th>구분</th><th>단계</th><th>종류</th><th>결과</th><th>걸린 시간</th><th>메시지</th></tr></thead>
<tbody>
{{range .Steps}}<tr><td>{{.Index}}</td><td>{{.PhaseLabel}}</td><td>{{.Name}}</td><td><code>{{.Action}}</code></td><td><span class="badge s-{{.Status}}">{{.StatusLabel}}</span></td><td>{{.Duration}}</td><td class="wrap">{{.Message}}</td></tr>
{{end}}</tbody>
</table>
<h2>세부와 증거</h2>
{{range .Steps}}{{if .Detail}}<section class="card">
<h3>{{.Index}}. {{.Name}} <span class="muted">({{.Action}})</span> <span class="badge s-{{.Status}}">{{.StatusLabel}}</span></h3>
{{if .Message}}<p class="wrap">{{.Message}}</p>{{end}}
{{if .Error}}{{if .Error.Code}}<p class="muted">오류 코드 <code>{{.Error.Code}}</code></p>{{end}}{{end}}
{{if .Asserts}}<table>
<thead><tr><th>기준</th><th>기대값</th><th>실제값</th><th>결과</th></tr></thead>
<tbody>{{range .Asserts}}<tr><td><code>{{.Kind}}</code></td><td class="wrap">{{.Expected}}</td><td class="wrap">{{.Actual}}</td><td>{{if .Passed}}<span class="ok">통과</span>{{else}}<span class="bad">실패</span>{{end}}</td></tr>
{{end}}</tbody>
</table>{{end}}
{{range .Files}}<p>증거: <a href="{{.Rel}}">{{.Name}}</a> <span class="muted">({{.Kind}}, {{.Size}}바이트)</span></p>
{{end}}{{range .Shots}}<figure><img src="{{.Rel}}" alt="{{.Name}}"><figcaption class="muted">{{.Name}}</figcaption></figure>
{{end}}</section>
{{end}}{{end}}
{{if .Run.Unsupported}}<h2>이 PC·데몬에서 할 수 없던 단계</h2>
<ul>{{range .Run.Unsupported}}<li><code>{{.Action}}</code>: {{.Reason}} <span class="muted">{{.Code}}</span></li>
{{end}}</ul>{{end}}
<h2>환경</h2>
<ul>{{range .Env}}<li>{{.}}</li>
{{end}}<li>데몬 버전: {{.Daemon}}</li></ul>
{{if .Vars}}<h2>변수</h2>
<table><thead><tr><th>이름</th><th>값</th></tr></thead><tbody>
{{range .Vars}}<tr><td><code>{{index . 0}}</code></td><td class="wrap">{{index . 1}}</td></tr>
{{end}}</tbody></table>{{end}}
<p class="muted">만든 시각 {{.Generated}} · Deskhand</p>
</main>
</body>
</html>
`))
