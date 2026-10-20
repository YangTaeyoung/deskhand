package xfer

import (
	"fmt"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// nameProblem returns the element of the slash-separated relative path rel
// of a folder transfer that its destination cannot hold, and why: an empty
// element, "." or "..", an element with "\", and for a Windows destination
// one that wire.CheckWindowsName refuses. It returns "", "" for a rel the
// destination holds.
func nameProblem(rel string, windows bool) (string, string) {
	for _, e := range strings.Split(rel, "/") {
		switch {
		case e == "":
			return e, "빈 이름이 들어 있어"
		case e == "." || e == "..":
			return e, "현재·상위 폴더를 가리키는 이름이라"
		case strings.Contains(e, `\`):
			return e, `Windows가 경로 구분자로 읽는 \가 들어 있어`
		}
		if !windows {
			continue
		}
		switch fault, r := wire.CheckWindowsName(e); fault {
		case wire.NameBadChar:
			return e, fmt.Sprintf("Windows가 이름에 허용하지 않는 문자 %q가 들어 있어", r)
		case wire.NameTrailing:
			return e, "점이나 공백으로 끝나 Windows가 그 끝을 지우므로"
		case wire.NameDevice:
			return e, "Windows가 장치 이름으로 쓰는 이름이라"
		}
	}
	return "", ""
}

// screen fails the entries of m that the destination cannot hold
// (nameProblem, Windows rules when windows is set) and, when fold is set,
// the later of two entries whose relative paths differ only in letter case.
// full returns the source path of a relative path for the error.
func (m *manifest) screen(full func(rel string) string, windows, fold bool) {
	seen := map[string]string{}
	for i := range m.files {
		f := &m.files[i]
		if f.State == StateFailed {
			continue
		}
		if bad, reason := nameProblem(f.Rel, windows); reason != "" {
			f.State, f.Error = StateFailed, encodeError(apierr.Invalid(fmt.Sprintf("%s의 이름 '%s'는 %s 옮기지 않았습니다. 이름을 바꾼 뒤 다시 옮기세요", full(f.Rel), bad, reason)))
			continue
		}
		if !fold {
			continue
		}
		key := strings.ToLower(f.Rel)
		if first, ok := seen[key]; ok {
			f.State, f.Error = StateFailed, encodeError(apierr.New(wire.CodeTransferConflict,
				fmt.Sprintf("%s는 %s와 대소문자만 다른 이름이라 대상에서 같은 이름이 되므로 옮기지 않았습니다. 둘 중 하나의 이름을 바꾼 뒤 다시 옮기세요", full(f.Rel), full(first)),
				false, apierr.NextFixParams))
			continue
		}
		seen[key] = f.Rel
	}
}
