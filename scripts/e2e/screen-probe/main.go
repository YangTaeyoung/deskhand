// screen-probe measures a deskhand-agent's screen and input streams over
// QUIC without the daemon (docs/acceptance/screen.md).
//
//	screen-probe keygen -dir DIR
//	screen-probe run -addr HOST:PORT -id DIR -agent-fp FP -out DIR -name NAME -scenario static|motion|input|click|keyframe [flags]
//	screen-probe report -dir DIR [-delta N] [-min-pixels N] [-key-mad N] [-key-lit N]
//
// keygen writes a throwaway identity (probe.crt, probe.key) to DIR and prints
// {"fingerprint":…}, the value for deskhand-agent init --controller-fp.
//
// run connects with the identity in -id to the agent whose certificate
// fingerprint is -agent-fp, measures the agent clock with time.sync, starts
// -fixture through app.launch (with --animate for motion), opens a video
// stream of the display that shows the fixture and records it for -seconds
// (static, motion) or while it clicks and presses keys in the fixture
// (input) or moves the pointer onto its button and clicks it after a rest
// (click), or takes -repeats key frames of a new stream, of a keyframe
// control, of a config control with the same settings and of one that
// changes max_width, each with a screen.capture still right after it
// (keyframe), measures the clock again, ends the fixture and
// writes NAME.json and NAME.h264 to -out (keyframe adds one .h264 and one
// .png per key frame). With -gpu it reads the GPU's performance state and
// clocks through nvidia-smi every 50 ms while it measures. Input goes only to the fixture: before every event
// the fixture's window has to be in the foreground, before every button
// press the element under the target point and before every key press the
// focused element has to be the fixture's Same button; otherwise the input
// stream is closed and the run records why.
//
// report reads every *.json of -dir, decodes the .h264 next to each with
// ffmpeg and prints the measurements in Korean; it compares each key frame
// with its still in RGB.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/YangTaeyoung/deskhand/internal/transport"
)

const usage = `사용법:
  screen-probe keygen -dir 폴더
  screen-probe run -addr 호스트:포트 -id 폴더 -agent-fp 지문 -out 폴더 -name 이름 -scenario static|motion|input|click|keyframe [옵션]
  screen-probe report -dir 폴더 [-delta N] [-min-pixels N] [-key-mad N] [-key-lit N]
`

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdout, os.Stderr))
}

// runMain runs the subcommand of args and returns the exit code.
func runMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "keygen":
		return cmdKeygen(args[1:], stdout, stderr)
	case "run":
		return cmdRun(args[1:], stdout, stderr)
	case "report":
		return cmdReport(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "오류: 알 수 없는 명령 %q입니다. 아래 명령 중 하나를 쓰세요.\n%s", args[0], usage)
	return 2
}

// newFlags returns a flag set whose messages go to stderr.
func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// fail prints the Korean sentence what and err to stderr and returns 1.
func fail(stderr io.Writer, what string, err error) int {
	if err != nil {
		fmt.Fprintf(stderr, "오류: %s: %v\n", what, err)
	} else {
		fmt.Fprintf(stderr, "오류: %s\n", what)
	}
	return 1
}

// identityPaths returns the certificate and key paths of the identity in dir.
func identityPaths(dir string) (cert, key string) {
	return filepath.Join(dir, "probe.crt"), filepath.Join(dir, "probe.key")
}

func cmdKeygen(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("keygen", stderr)
	dir := fs.String("dir", "", "인증서와 키를 쓸 폴더")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" {
		return fail(stderr, "-dir이 비어 있습니다. 인증서와 키를 쓸 폴더를 주세요", nil)
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return fail(stderr, "폴더를 만들지 못했습니다", err)
	}
	id, err := transport.NewIdentity("screen-probe")
	if err != nil {
		return fail(stderr, "키를 만들지 못했습니다", err)
	}
	cert, key := identityPaths(*dir)
	if err := id.Save(cert, key); err != nil {
		return fail(stderr, "키를 저장하지 못했습니다", err)
	}
	b, _ := json.Marshal(map[string]string{"fingerprint": id.Fingerprint})
	fmt.Fprintln(stdout, string(b))
	return 0
}
