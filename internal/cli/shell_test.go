package cli

import (
	"errors"
	"strings"
	"testing"
)

// noTerm is a Terminal for a stdin that is not a terminal.
type noTerm struct{}

func (noTerm) Size() (int, int, bool)             { return 0, 0, false }
func (noTerm) Raw() (func(), error)               { return nil, errors.New("not a terminal") }
func (noTerm) Resizes() (<-chan struct{}, func()) { return nil, func() {} }

func TestShellRefusesWrongArguments(t *testing.T) {
	startFake(t, nil)
	for _, args := range [][]string{
		{"shell"},
		{"shell", "a", "b"},
		{"shell", "--attach", "j-1", "pc"},
		{"shell", "--attach", "j-1", "--shell", "cmd"},
		{"shell", "pc", "--", "dir"},
		{"--json", "shell", "pc"},
	} {
		if r := runCLI(t, false, "", args...); r.code != exitUsage || !strings.HasPrefix(r.err, "사용법 오류: ") {
			t.Fatalf("%v: code %d, stderr %q", args, r.code, r.err)
		}
	}
}

func TestShellReportsAPTYThatDidNotOpen(t *testing.T) {
	refused := envelope("failed", map[string]any{"pty_id": nil, "action": map[string]any{"executed": false, "outcome": "not_started"},
		"error": map[string]any{"code": "CAPABILITY_UNSUPPORTED", "message": "이 PC에는 pwsh 셸이 없습니다. [powershell cmd] 중 하나로 여세요"}})
	f := startFake(t, map[string]handler{"pty.open": ok(refused)})
	r := runCLI(t, false, "", "shell", "windows-dev", "--shell", "pwsh")
	if r.code != exitFailure || !strings.Contains(r.err, "pwsh 셸이 없습니다") {
		t.Fatalf("code %d, stderr %q", r.code, r.err)
	}
	p := f.called("pty.open")[0]
	if p["host_id"] != "windows-dev" || p["shell"] != "pwsh" || p["cols"] != float64(80) || p["rows"] != float64(24) {
		t.Fatalf("pty.open params %v", p)
	}
}

func TestJobsListFiltersByKindAndStatus(t *testing.T) {
	f := startFake(t, map[string]handler{"jobs.list": ok(map[string]any{"jobs": []any{}})})
	if r := runCLI(t, false, "", "jobs", "list", "--host", "pc", "--kind", "pty", "--status", "running"); r.code != exitOK {
		t.Fatalf("code %d, stderr %q", r.code, r.err)
	}
	if p := f.called("jobs.list")[0]; p["host_id"] != "pc" || p["kind"] != "pty" || p["status"] != "running" {
		t.Fatalf("jobs.list params %v", p)
	}
}
