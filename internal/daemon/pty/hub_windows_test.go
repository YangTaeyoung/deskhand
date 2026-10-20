package pty_test

import "fmt"

// testShell runs the commands below.
const (
	testShell  = "cmd"
	sizeLine   = "powershell -NoProfile -Command \"'size' + [Console]::WindowWidth + 'x' + [Console]::WindowHeight\"\r"
	koreanLine = "set K=테스트\recho 한글%K%\r"
	// longChildText is what longChild prints before its child starts.
	longChildText = "slept-2"
)

// longChild is input that starts a child process that runs for about secs
// seconds, whose command line holds longChildCmd(secs).
func longChild(secs int) string {
	return fmt.Sprintf("set /a S=1+1 >nul\recho slept-%%S%% & ping -n %d 127.0.0.1 >nul\r", secs)
}

func longChildCmd(secs int) string { return fmt.Sprintf("-n %d 127.0.0.1", secs) }

// markLine is input that makes cmd print markText(a, b).
func markLine(a, b int) string { return fmt.Sprintf("set /a S=%d+%d >nul\recho mark%%S%%mark\r", a, b) }

func markText(a, b int) string { return fmt.Sprintf("mark%dmark", a+b) }

// exitLine is input that makes cmd exit with code.
func exitLine(code int) string { return fmt.Sprintf("exit %d\r", code) }

// sizeText is what sizeLine prints for a console of cols and rows.
func sizeText(cols, rows int) string { return fmt.Sprintf("size%dx%d", cols, rows) }
