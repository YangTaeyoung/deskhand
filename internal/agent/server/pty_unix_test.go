//go:build !windows

package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// The shells of the pty tests: testShell and sizeShell run the commands
// below, defaultTestShell is the first shell the pty capability lists.
const (
	testShell        = "sh"
	defaultTestShell = "sh"
	sizeShell        = "sh"
	sizePrompt       = "$ "
	sizeLine         = "stty size\r"
)

// sumLine is input that makes testShell print sumText(a, b).
func sumLine(a, b int) string { return fmt.Sprintf("echo mark$((%d+%d))mark\r", a, b) }

func sumText(a, b int) string { return fmt.Sprintf("mark%dmark", a+b) }

// exitLine is input that makes testShell exit with code.
func exitLine(code int) string { return fmt.Sprintf("exit %d\r", code) }

// sizeText is what sizeLine prints for a terminal of cols and rows.
func sizeText(cols, rows int) string { return fmt.Sprintf("%d %d", rows, cols) }

// knownShell reports whether the pty capability may list sh.
func knownShell(sh string) bool { return sh == "sh" }

func TestPTYScrollbackKeepsTheLastMiB(t *testing.T) {
	_, s := newRPCClient(t, Options{})
	p := newPTY(t, s, wire.PTYNew{Shell: testShell})
	p.input("yes 0123456789012345678901234567890123456789 | head -n 40000; " + sumLine(1, 1))
	p.waitOutput(sumText(1, 1) + "\r\n")
	p.detach()

	again := servePTY(t, s, wire.StreamHeader{Job: p.attached.Job})
	replay := again.waitOutput(sumText(1, 1) + "\r\n")
	quiet := time.After(500 * time.Millisecond)
	for done := false; !done; {
		select {
		case f := <-again.frames:
			again.take(f)
		case <-quiet:
			done = true
		}
	}
	replay = again.output()
	if len(replay) > ptyScrollbackBytes || len(replay) < ptyScrollbackBytes-ptyReplayAlign-64 {
		t.Fatalf("the replay holds %d bytes; want the last %d bytes of the output", len(replay), ptyScrollbackBytes)
	}
	if !strings.HasPrefix(replay, "0123456789012345678901234567890123456789\r\n") {
		t.Fatalf("the replay starts with %q, not with a whole line", replay[:min(len(replay), 80)])
	}
	if strings.Contains(replay, "yes 0123") {
		t.Fatal("the replay holds the start of the output")
	}
}
