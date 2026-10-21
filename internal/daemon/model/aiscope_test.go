package model

import "testing"

func TestAIScopeAllowsFile(t *testing.T) {
	roots := AIScope{Screen: true, Files: FilesWorkRoots, Clipboard: true, WorkRoots: []string{`C:\Work`, `\\srv\share\proj`}}
	cases := []struct {
		os, path string
		want     bool
	}{
		{"windows", `C:\Work`, true},
		{"windows", `c:/work/a/b.txt`, true},
		{"windows", `C:\WORK\sub\..\x`, true},
		{"windows", `C:\Work\..\Windows`, false},
		{"windows", `C:\Workshop`, false},
		{"windows", `D:\Work`, false},
		{"windows", `\\SRV\Share\Proj\f`, true},
		{"windows", `\\srv\share\other`, false},
		{"windows", `Work\a`, false},
		{"windows", ``, false},
		{"windows", `\\?\C:\Work\a`, false},
		{"linux", `C:\Work\a`, false},
	}
	for _, c := range cases {
		if got := roots.AllowsFile(c.os, c.path); got != c.want {
			t.Errorf("%s %q: %v, want %v", c.os, c.path, got, c.want)
		}
	}
	posix := AIScope{Files: FilesWorkRoots, WorkRoots: []string{"/home/u/work"}}
	for p, want := range map[string]bool{"/home/u/work/x": true, "/home/u/Work/x": false, "/home/u/work/../x": false, "/home/u/work-2": false} {
		if got := posix.AllowsFile("linux", p); got != want {
			t.Errorf("posix %q: %v, want %v", p, got, want)
		}
	}
	if !(AIScope{Files: FilesWorkRoots, WorkRoots: []string{"/"}}).AllowsFile("linux", "/etc/passwd") {
		t.Error("root / allows every path")
	}
	if (AIScope{Files: FilesWorkRoots}).AllowsFile("windows", `C:\a`) || (AIScope{Files: FilesNone}).AllowsFile("windows", `C:\a`) {
		t.Error("no work roots and none allow nothing")
	}
	if !UnrestrictedAIScope().AllowsFile("windows", "") || UnrestrictedAIScope().Restricted() {
		t.Error("the default scope allows everything")
	}
}

func TestAIScopeUpdateApply(t *testing.T) {
	no, files, roots := false, FilesWorkRoots, []string{` C:\a `, `C:\a`, `/b`}
	got, err := AIScopeUpdate{Screen: &no, Files: &files, WorkRoots: &roots}.Apply(UnrestrictedAIScope())
	if err != nil || got.Screen || !got.Clipboard || got.Files != FilesWorkRoots || len(got.WorkRoots) != 2 || got.WorkRoots[0] != `C:\a` {
		t.Fatalf("apply %+v %v", got, err)
	}
	bad := "some"
	if _, err := (AIScopeUpdate{Files: &bad}).Apply(got); err == nil {
		t.Error("an unknown files value is refused")
	}
	rel := []string{"work"}
	if _, err := (AIScopeUpdate{WorkRoots: &rel}).Apply(got); err == nil {
		t.Error("a relative work root is refused")
	}
}
