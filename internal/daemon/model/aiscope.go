package model

import (
	"fmt"
	"path"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
)

// Values of AIScope.Files.
const (
	FilesAll       = "all"
	FilesWorkRoots = "work_roots"
	FilesNone      = "none"
)

// AIScope is what of a host an AI client (mcp) may reach: its screen and
// the input on it, its files, and its clipboard. WorkRoots are the folders
// of the host that Files "work_roots" allows.
type AIScope struct {
	Screen    bool     `json:"screen"`
	Files     string   `json:"files"`
	Clipboard bool     `json:"clipboard"`
	WorkRoots []string `json:"work_roots"`
}

// UnrestrictedAIScope is the scope of a host nobody restricted.
func UnrestrictedAIScope() AIScope {
	return AIScope{Screen: true, Files: FilesAll, Clipboard: true, WorkRoots: []string{}}
}

// Restricted reports whether the scope holds something back from an AI.
func (s AIScope) Restricted() bool {
	return !s.Screen || !s.Clipboard || s.Files != FilesAll
}

// AIScopeUpdate is the ai_scope of host.update: the fields it names
// replace those of the host's scope.
type AIScopeUpdate struct {
	Screen    *bool     `json:"screen"`
	Files     *string   `json:"files"`
	Clipboard *bool     `json:"clipboard"`
	WorkRoots *[]string `json:"work_roots"`
}

// Apply returns s with the fields of u, or an INVALID_ARGUMENT error.
func (u AIScopeUpdate) Apply(s AIScope) (AIScope, error) {
	if u.Screen != nil {
		s.Screen = *u.Screen
	}
	if u.Clipboard != nil {
		s.Clipboard = *u.Clipboard
	}
	if u.Files != nil {
		switch *u.Files {
		case FilesAll, FilesWorkRoots, FilesNone:
			s.Files = *u.Files
		default:
			return s, apierr.Invalid(fmt.Sprintf(`ai_scope.files %q를 모릅니다. "all", "work_roots", "none" 중 하나로 주세요`, *u.Files))
		}
	}
	if u.WorkRoots != nil {
		roots := make([]string, 0, len(*u.WorkRoots))
		seen := map[string]bool{}
		for _, r := range *u.WorkRoots {
			r = strings.TrimSpace(r)
			if !AbsHostPath(r) {
				return s, apierr.Invalid(fmt.Sprintf("ai_scope.work_roots의 %q는 절대 경로가 아닙니다. C:\\work처럼 PC의 절대 경로를 주세요", r))
			}
			if !seen[r] {
				seen[r] = true
				roots = append(roots, r)
			}
		}
		s.WorkRoots = roots
	}
	return s, nil
}

// AbsHostPath reports whether p is an absolute path of a Windows or
// POSIX host: a drive path, a UNC path or a path from "/".
func AbsHostPath(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

// AllowsFile reports whether an AI may reach the file p of a host whose
// operating system is os. A path is compared by its text: "..", "." and
// repeated separators are resolved, and on Windows the case and the
// separator kind do not matter; links inside a root are not followed.
func (s AIScope) AllowsFile(os, p string) bool {
	switch s.Files {
	case FilesAll:
		return true
	case FilesWorkRoots:
		windows := os == "windows"
		target, ok := canonHostPath(p, windows)
		if !ok {
			return false
		}
		for _, root := range s.WorkRoots {
			r, ok := canonHostPath(root, windows)
			if ok && within(r, target) {
				return true
			}
		}
	}
	return false
}

func canonHostPath(p string, windows bool) (string, bool) {
	p = strings.TrimSpace(p)
	if windows {
		p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	}
	if !AbsHostPath(p) {
		return "", false
	}
	unc := windows && strings.HasPrefix(p, "//")
	c := path.Clean(p)
	if unc {
		c = "/" + c
	}
	return c, true
}

func within(root, p string) bool {
	if root == "/" {
		return true
	}
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/")
}
