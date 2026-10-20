package xfer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestNameProblemFindsWhatTheDestinationCannotHold(t *testing.T) {
	for _, tc := range []struct {
		rel      string
		windows  bool
		bad      string
		contains string
	}{
		{"ok.txt", true, "", ""},
		{"sub/보고서 (1).hwp", true, "", ""},
		{`..\..\evil.bat`, false, `..\..\evil.bat`, `\`},
		{`a\b.txt`, true, `a\b.txt`, `\`},
		{"a/../b", false, "..", "상위"},
		{"a/./b", false, ".", "상위"},
		{"a//b", false, "", "빈"},
		{"sub/CON.txt", true, "CON.txt", "장치"},
		{"sub/CON.txt", false, "", ""},
		{"x:y", true, "x:y", "':'"},
		{"trail.", true, "trail.", "점이나 공백"},
		{"dir /f", true, "dir ", "점이나 공백"},
	} {
		bad, reason := nameProblem(tc.rel, tc.windows)
		if bad != tc.bad || tc.contains != "" && !strings.Contains(reason, tc.contains) || tc.contains == "" && reason != "" {
			t.Errorf("nameProblem(%q, windows %v) = %q, %q; want %q with %q", tc.rel, tc.windows, bad, reason, tc.bad, tc.contains)
		}
	}
}

func TestPushPlanToWindowsFailsNamesWindowsCannotHold(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"ok.txt", `a\b.txt`, "CON.txt", "x:y", "trail.", "trail ", "ctl\x01", "sub/ok2.txt", "LPT1.log"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr := &transfer{m: &Manager{}, row: store.Transfer{TransferID: "t-names", Direction: Push, Local: root, Folder: true, Sep: `\`}}
	files, err := tr.planPush(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]store.TransferFile{}
	for _, f := range files {
		states[f.Rel] = f
	}
	for _, rel := range []string{"ok.txt", "sub", "sub/ok2.txt"} {
		if f := states[rel]; f.State != StateQueued {
			t.Errorf("%s: %s %s", rel, f.State, f.Error)
		}
	}
	for _, rel := range []string{`a\b.txt`, "CON.txt", "x:y", "trail.", "trail ", "ctl\x01", "LPT1.log"} {
		f := states[rel]
		e := decodeError(f.Error)
		if f.State != StateFailed || e == nil || e.Code != wire.CodeInvalidArgument || !strings.Contains(e.Message, rel) {
			t.Errorf("%q: %s %s", rel, f.State, f.Error)
		}
	}
}

func TestCaseFoldFailsTheLaterOfTwoNames(t *testing.T) {
	m := &manifest{id: "t-case"}
	for _, f := range []store.TransferFile{
		{Rel: "Docs", Dir: true}, {Rel: "Docs/a.txt"}, {Rel: "docs", Dir: true}, {Rel: "docs/A.TXT"}, {Rel: "docs/b.txt"},
		{Rel: "README"}, {Rel: "readme"}, {Rel: "other"},
	} {
		m.add(f)
	}
	m.screen(func(rel string) string { return "/dst/" + rel }, false, true)
	for _, f := range m.files {
		e := decodeError(f.Error)
		switch f.Rel {
		case "docs", "docs/A.TXT", "readme":
			if f.State != StateFailed || e == nil || e.Code != wire.CodeTransferConflict {
				t.Errorf("%s: %s %v", f.Rel, f.State, e)
			}
		default:
			if f.State != StateQueued {
				t.Errorf("%s: %s %v", f.Rel, f.State, e)
			}
		}
	}
	m = &manifest{id: "t-case"}
	m.add(store.TransferFile{Rel: "README"})
	m.add(store.TransferFile{Rel: "readme"})
	m.screen(func(rel string) string { return rel }, false, false)
	if m.files[1].State != StateQueued {
		t.Fatalf("names are folded on a case-sensitive destination: %+v", m.files[1])
	}
}
