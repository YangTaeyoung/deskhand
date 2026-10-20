package client

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
)

// stopTimeout bounds the wait for a stopping daemon.
const stopTimeout = 5 * time.Second

// Stop asks the daemon of the home folder to stop (SIGTERM to the process
// id in run/daemon.lock) and waits until its socket stops answering. A
// daemon that does not answer is left alone.
func Stop(ctx context.Context) error {
	if !Running(ctx, "") {
		return nil
	}
	b, err := os.ReadFile(paths.Lock())
	pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || perr != nil || pid <= 0 {
		return fmt.Errorf("데몬 프로세스 ID를 %s에서 읽지 못했습니다. 활성 상태 보기에서 deskhand 프로세스를 끝내세요", paths.Lock())
	}
	if err := terminate(pid); err != nil {
		return fmt.Errorf("데몬(PID %d)에 종료 신호를 보내지 못했습니다(%v). 활성 상태 보기에서 프로세스를 끝내세요", pid, err)
	}
	ctx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	for Running(ctx, "") {
		select {
		case <-ctx.Done():
			return fmt.Errorf("데몬(PID %d)이 %s 안에 끝나지 않았습니다. %s 로그를 확인하세요", pid, stopTimeout, paths.DaemonLog())
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
}
