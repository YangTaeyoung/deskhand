//go:build !windows

package pty_test

import "fmt"

// testShell runs the commands below.
const (
	testShell  = "sh"
	sizeLine   = "stty size\r"
	koreanLine = "K=테스트; echo 한글$K\r"
	// longChildText is what longChild prints before its child starts.
	longChildText = "slept-2"
)

// longChild is input that starts a child process that runs for secs
// seconds, whose command line holds longChildCmd(secs).
func longChild(secs int) string { return fmt.Sprintf("echo slept-$((1+1)); sleep %d\r", secs) }

func longChildCmd(secs int) string { return fmt.Sprintf("sleep %d", secs) }

// markLine is input that makes testShell print markText(a, b).
func markLine(a, b int) string { return fmt.Sprintf("echo mark$((%d+%d))mark\r", a, b) }

func markText(a, b int) string { return fmt.Sprintf("mark%dmark", a+b) }

// exitLine is input that makes testShell exit with code.
func exitLine(code int) string { return fmt.Sprintf("exit %d\r", code) }

// sizeText is what sizeLine prints for a terminal of cols and rows.
func sizeText(cols, rows int) string { return fmt.Sprintf("%d %d", rows, cols) }
