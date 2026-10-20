package server

import (
	"fmt"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// The shells of the pty tests: testShell and sizeShell run the commands
// below, defaultTestShell is the first shell the pty capability lists.
const (
	testShell        = "cmd"
	defaultTestShell = "powershell"
	sizeShell        = "powershell"
	sizePrompt       = "PS "
	sizeLine         = "'size' + [Console]::WindowWidth + 'x' + [Console]::WindowHeight\r"
)

// sumLine is input that makes cmd print sumText(a, b).
func sumLine(a, b int) string { return fmt.Sprintf("set /a S=%d+%d >nul\recho mark%%S%%mark\r", a, b) }

func sumText(a, b int) string { return fmt.Sprintf("mark%dmark", a+b) }

// exitLine is input that makes cmd exit with code.
func exitLine(code int) string { return fmt.Sprintf("exit %d\r", code) }

// sizeText is what sizeLine prints for a console of cols and rows.
func sizeText(cols, rows int) string { return fmt.Sprintf("size%dx%d", cols, rows) }

// knownShell reports whether the pty capability may list sh.
func knownShell(sh string) bool { return sh == "powershell" || sh == "cmd" || sh == "pwsh" }

func TestConPTYEchoesInput(t *testing.T) {
	c, s := newRPCClient(t, Options{})
	shells := s.checkCaps()["pty"].Shells
	for _, c := range []struct {
		shell, prompt, codePage, korean string
	}{
		{"cmd", ">", "chcp\r", "set K=테스트\recho 한글%K%\r"},
		{"powershell", "PS ", "chcp.com\r", "'한글' + '테스트'\r"},
		{"pwsh", "PS ", "chcp.com\r", "'한글' + '테스트'\r"},
	} {
		listed := false
		for _, sh := range shells {
			listed = listed || sh == c.shell
		}
		if !listed {
			if c.shell == "pwsh" {
				t.Logf("pwsh is not installed; skipping it")
				continue
			}
			t.Fatalf("the pty capability does not list %s: %v", c.shell, shells)
		}
		t.Run(c.shell, func(t *testing.T) {
			p := newPTY(t, s, wire.PTYNew{Shell: c.shell, Cols: 120, Rows: 30})
			p.waitOutput(c.prompt)
			p.input(c.codePage)
			p.waitOutput("65001")
			p.input(c.korean)
			p.waitOutput("한글테스트")
			p.input("exit\r")
			if code := p.waitExit(); code != 0 {
				t.Fatalf("%s exited with %d", c.shell, code)
			}
		})
	}
	_ = c
}
