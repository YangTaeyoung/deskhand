package daemon_test

import "fmt"

// The shells of the pty tests: testShell runs the commands below,
// defaultShell is the first shell the pty capability lists and
// missingShell one it does not list.
const (
	testShell    = "sh"
	defaultShell = "sh"
	missingShell = "powershell"
	sizeLine     = "stty size\r"
)

// markLine is input that makes testShell print markText(a, b).
func markLine(a, b int) string { return fmt.Sprintf("echo mark$((%d+%d))mark\r", a, b) }

func markText(a, b int) string { return fmt.Sprintf("mark%dmark", a+b) }

// exitLine is input that makes testShell exit with code.
func exitLine(code int) string { return fmt.Sprintf("exit %d\r", code) }

// delayedExit is input that makes testShell print going and exit with code
// half a second later.
func delayedExit(code int) string { return fmt.Sprintf("echo going; sleep 0.5; exit %d\r", code) }

// sizeText is what sizeLine prints for a terminal of cols and rows.
func sizeText(cols, rows int) string { return fmt.Sprintf("%d %d", rows, cols) }
