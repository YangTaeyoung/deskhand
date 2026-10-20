package xfer

import (
	"slices"
	"testing"
)

func TestHostPathsFollowTheSeparatorOfTheHost(t *testing.T) {
	for _, tc := range []struct{ p, sep, dir, base string }{
		{`C:\qa\build`, `\`, `C:\qa`, "build"},
		{`C:\qa\build\`, `\`, `C:\qa`, "build"},
		{`C:\build`, `\`, `C:\`, "build"},
		{`C:\`, `\`, `C:\`, `C:\`},
		{`C:/qa/x.txt`, `\`, `C:/qa`, "x.txt"},
		{`\\srv\share\x`, `\`, `\\srv\share`, "x"},
		{"/home/a/x", "/", "/home/a", "x"},
		{"/x", "/", "/", "x"},
		{"x", "/", ".", "x"},
	} {
		if got := remoteDir(tc.p, tc.sep); got != tc.dir {
			t.Errorf("remoteDir(%q) = %q, want %q", tc.p, got, tc.dir)
		}
		if got := remoteBase(tc.p, tc.sep); got != tc.base && tc.p != `C:\` {
			t.Errorf("remoteBase(%q) = %q, want %q", tc.p, got, tc.base)
		}
	}
	for _, tc := range []struct{ dir, rel, sep, want string }{
		{`C:\qa`, "sub/a.txt", `\`, `C:\qa\sub\a.txt`},
		{`C:\`, "a.txt", `\`, `C:\a.txt`},
		{`C:/qa/`, "a.txt", `\`, `C:/qa/a.txt`},
		{"/srv", "d/e", "/", "/srv/d/e"},
		{`C:\qa`, "", `\`, `C:\qa`},
	} {
		if got := remoteJoin(tc.dir, tc.rel, tc.sep); got != tc.want {
			t.Errorf("remoteJoin(%q, %q) = %q, want %q", tc.dir, tc.rel, got, tc.want)
		}
	}
	if got := remoteNumbered(`C:\qa\보고서.hwp`, `\`, 2); got != `C:\qa\보고서 (2).hwp` {
		t.Errorf("remoteNumbered = %q", got)
	}
}

func TestLeafFoldersAreTheDeepestOnes(t *testing.T) {
	got := leafFolders([]string{"", "a", "a/b", "c", "cd"})
	if want := []string{"a/b", "c", "cd"}; !slices.Equal(got, want) {
		t.Fatalf("leafFolders = %v, want %v", got, want)
	}
	if got := leafFolders([]string{""}); !slices.Equal(got, []string{""}) {
		t.Fatalf("leafFolders of the root alone = %v", got)
	}
}
