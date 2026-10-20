// deskhand-agent runs on the Windows PC and serves docs/protocol.md over QUIC
// to the paired Mac.
//
//	deskhand-agent init --name N --controller-fp FP [--listen :47600] [--config PATH] [--force]
//	deskhand-agent install [--config PATH]
//	deskhand-agent uninstall [--purge]
//	deskhand-agent run [--config PATH]
//	deskhand-agent version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/config"
	"github.com/YangTaeyoung/deskhand/internal/agent/logfile"
	"github.com/YangTaeyoung/deskhand/internal/agent/quicserve"
	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/transport"
)

// exitKeyExists is the exit code of init when a key already exists and --force
// is not given.
const exitKeyExists = 4

// version is set with -ldflags "-X main.version=…".
var version = "dev"

const usage = `사용법:
  deskhand-agent init --name 이름 --controller-fp 지문 [--listen :47600] [--config 경로] [--force]
  deskhand-agent install [--config 경로]
  deskhand-agent uninstall [--purge]
  deskhand-agent run [--config 경로]
  deskhand-agent version
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
	case "init":
		return cmdInit(args[1:], stdout, stderr)
	case "install":
		return cmdInstall(args[1:], stdout, stderr)
	case "uninstall":
		return cmdUninstall(args[1:], stdout, stderr)
	case "run":
		return cmdRun(args[1:], stderr)
	case "version":
		return cmdVersion(stdout)
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

// fail prints the Korean sentence what and the error to stderr and returns 1.
func fail(stderr io.Writer, what string, err error) int {
	if err != nil {
		fmt.Fprintf(stderr, "오류: %s: %v\n", what, err)
	} else {
		fmt.Fprintf(stderr, "오류: %s\n", what)
	}
	return 1
}

func cmdVersion(stdout io.Writer) int {
	b, _ := json.Marshal(map[string]string{"version": version, "os": runtime.GOOS, "arch": runtime.GOARCH})
	fmt.Fprintln(stdout, string(b))
	return 0
}

// cmdInit creates the agent's key and certificate next to agent.json, writes
// agent.json and prints {"fingerprint","name"} as the only line on stdout.
// An existing key is replaced only with --force.
func cmdInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("init", stderr)
	name := fs.String("name", "", "이 PC의 이름")
	fp := fs.String("controller-fp", "", "페어링할 Mac 인증서의 SHA-256 지문(64자리 16진수)")
	listen := fs.String("listen", config.DefaultListen, "QUIC을 받을 UDP 주소")
	cfgPath := fs.String("config", config.DefaultPath(), "agent.json 경로")
	force := fs.Bool("force", false, "이미 있는 키를 새 키로 바꾼다")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*name) == "" {
		return fail(stderr, "--name이 비어 있습니다. 이 PC의 이름을 --name으로 주세요", nil)
	}
	controller := strings.ToLower(strings.TrimSpace(*fp))
	if !config.ValidFingerprint(controller) {
		return fail(stderr, "--controller-fp는 64자리 16진수 지문이어야 합니다. Mac에서 deskhand가 보여 주는 지문을 그대로 주세요", nil)
	}
	certPath, keyPath := config.KeyPaths(*cfgPath)
	if _, err := os.Lstat(keyPath); err == nil && !*force {
		fail(stderr, fmt.Sprintf("%s에 이미 키가 있습니다. 키를 바꾸려면 --force를 붙여 다시 실행하세요", keyPath), nil)
		return exitKeyExists
	}
	dataDir := filepath.Dir(*cfgPath)
	if err := prepareDataDir(dataDir); err != nil {
		return fail(stderr, "데이터 폴더를 준비하지 못했습니다", err)
	}
	id, err := transport.NewIdentity(*name)
	if err != nil {
		return fail(stderr, "키를 만들지 못했습니다", err)
	}
	if err := id.Save(certPath, keyPath); err != nil {
		return fail(stderr, "키를 저장하지 못했습니다", err)
	}
	cfg := config.Agent{Listen: *listen, ControllerFingerprints: []string{controller}, DataDir: dataDir, Name: *name}
	if err := config.Save(*cfgPath, cfg); err != nil {
		return fail(stderr, "agent.json을 쓰지 못했습니다", err)
	}
	b, _ := json.Marshal(map[string]string{"fingerprint": id.Fingerprint, "name": *name})
	fmt.Fprintln(stdout, string(b))
	return 0
}

func cmdInstall(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("install", stderr)
	cfgPath := fs.String("config", config.DefaultPath(), "agent.json 경로")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fail(stderr, "설정을 읽지 못했습니다", err)
	}
	taskConfig, err := taskConfigPath(*cfgPath, config.DefaultPath())
	if err != nil {
		return fail(stderr, "설정 경로를 확인하지 못했습니다", err)
	}
	if err := install(cfg, taskConfig, stdout); err != nil {
		writeInstallLog(cfg.DataDir, "install failed: "+err.Error())
		return fail(stderr, "설치하지 못했습니다", err)
	}
	writeInstallLog(cfg.DataDir, "installed "+version)
	return 0
}

func cmdUninstall(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("uninstall", stderr)
	purge := fs.Bool("purge", false, "데이터 폴더(키·설정·로그·작업 기록)까지 지운다")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := uninstall(config.DefaultDataDir(), *purge, stdout); err != nil {
		return fail(stderr, "제거를 마치지 못했습니다", err)
	}
	return 0
}

// openLogger returns a logger writing to logs/agent.log in dataDir, or to
// stderr when that file cannot be opened.
func openLogger(dataDir string, stderr io.Writer) *log.Logger {
	dir := filepath.Join(dataDir, "logs")
	err := os.MkdirAll(dir, 0o700)
	if err == nil {
		var f *logfile.Log
		if f, err = logfile.Open(filepath.Join(dir, "agent.log"), logfile.MaxBytes, nil); err == nil {
			return log.New(f, "", log.LstdFlags)
		}
	}
	fmt.Fprintf(stderr, "로그 파일을 열지 못해 표준 오류에 씁니다: %v\n", err)
	return log.New(stderr, "", log.LstdFlags)
}

// serveFirstPause and serveMaxPause bound the pause between two attempts to
// serve after a failure.
const (
	serveFirstPause = time.Second
	serveMaxPause   = 5 * time.Second
)

// cmdRun serves the agent until it is interrupted, starting again after a
// failure to listen or accept.
func cmdRun(args []string, stderr io.Writer) int {
	fs := newFlags("run", stderr)
	cfgPath := fs.String("config", config.DefaultPath(), "agent.json 경로")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fail(stderr, "설정을 읽지 못했습니다", err)
	}
	certPath, keyPath := config.KeyPaths(*cfgPath)
	id, err := transport.LoadIdentity(certPath, keyPath)
	if err != nil {
		return fail(stderr, "키를 읽지 못했습니다. deskhand-agent init을 먼저 실행하세요", err)
	}
	lockErr := prepareDataDir(cfg.DataDir)
	logger := openLogger(cfg.DataDir, stderr)
	if lockErr != nil {
		logger.Printf("data folder: %v", lockErr)
	}
	var s *server.Server
	ui := newUI(cfg.DataDir, logger, func() *server.Server { return s })
	s = server.New(server.Options{AllowExec: true, Logger: logger, UI: ui, DataDir: cfg.DataDir, Version: version})
	defer s.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Printf("deskhand-agent %s listening on udp %s for %d controller(s), name %q, key %s", version, cfg.Listen,
		len(cfg.ControllerFingerprints), cfg.Name, id.Fingerprint)
	pause := serveFirstPause
	for {
		err := quicserve.Run(ctx, cfg, id, s, nil)
		if ctx.Err() != nil {
			return 0
		}
		if err == nil {
			err = errors.New("the listener stopped")
		}
		logger.Printf("serve: %v; trying again in %s", err, pause)
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(pause):
		}
		pause = min(2*pause, serveMaxPause)
	}
}

// writeInstallLog appends msg with the time to install.log in dataDir.
func writeInstallLog(dataDir, msg string) {
	f, err := os.OpenFile(filepath.Join(dataDir, "install.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), msg)
}
