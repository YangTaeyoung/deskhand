package cli

import (
	"fmt"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Transfer states and outcomes of the local API (docs/local-api.md §5).
const (
	stateQueued    = "queued"
	stateRunning   = "running"
	statePaused    = "paused"
	stateVerifying = "verifying"
	stateDone      = "done"
	stateFailed    = "failed"
	stateCancelled = "cancelled"

	outcomeSourceRetained = "copied_source_retained"
	outcomeSkipped        = "skipped"
)

const progressBarWidth = 24

func isMove(direction string) bool { return direction == "move_push" || direction == "move_pull" }

// transferEnded reports whether a transfer state is final.
func transferEnded(state string) bool {
	return state == stateDone || state == stateFailed || state == stateCancelled
}

// formatRate formats bytes per second as MB/s.
func formatRate(bps int64) string { return fmt.Sprintf("%.1f MB/s", float64(bps)/1e6) }

// formatETA formats the time left to move remaining bytes at rate bytes per
// second.
func formatETA(remaining, rate int64) string {
	if rate <= 0 {
		return "계산 중"
	}
	if remaining <= 0 {
		return "곧 끝남"
	}
	secs := (remaining + rate - 1) / rate
	switch {
	case secs >= 3600:
		return fmt.Sprintf("%d시간 %d분", secs/3600, secs%3600/60)
	case secs >= 60:
		return fmt.Sprintf("%d분 %d초", secs/60, secs%60)
	}
	return fmt.Sprintf("%d초", secs)
}

func stateKorean(s string) string {
	switch s {
	case stateQueued:
		return "대기 중"
	case stateRunning:
		return "전송 중"
	case statePaused:
		return "일시 정지"
	case stateVerifying:
		return "검증 중"
	case stateDone:
		return "완료"
	case stateFailed:
		return "실패"
	case stateCancelled:
		return "취소됨"
	}
	return dash(s)
}

func directionKorean(d string) string {
	switch d {
	case "push":
		return "올리기"
	case "pull":
		return "받기"
	case "move_push":
		return "옮기기(Mac→PC)"
	case "move_pull":
		return "옮기기(PC→Mac)"
	}
	return dash(d)
}

// progressLine renders a transfer as a bar of width cells with percent,
// rate, time left and file count.
func progressLine(t obj, width int) string {
	done, _ := t.num("bytes_done")
	total, _ := t.num("bytes_total")
	rate, _ := t.num("rate_bps")
	fd, _ := t.num("files_done")
	ft, _ := t.num("files_total")
	pct := int64(0)
	if total > 0 {
		pct = done * 100 / total
		if pct > 100 {
			pct = 100
		}
	}
	filled := int(pct) * width / 100
	bar := "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
	state := t.str("state")
	if state == stateQueued || state == statePaused || state == stateVerifying {
		return fmt.Sprintf("%s %3d%% %s %d/%d개 %s/%s", bar, pct, stateKorean(state), fd, ft, humanSize(done), humanSize(total))
	}
	return fmt.Sprintf("%s %3d%% %s 남은 시간 %s %d/%d개 %s/%s", bar, pct, formatRate(rate), formatETA(total-done, rate), fd, ft, humanSize(done), humanSize(total))
}

// notMoved counts the files of a transfer detail that were not put on the
// destination: failed, cancelled, unfinished or skipped.
func notMoved(t obj) int {
	n := 0
	for _, f := range t.list("files") {
		if f.str("state") != stateDone || f.str("outcome") == outcomeSkipped {
			n++
		}
	}
	return n
}

// sourceOutcome says what became of the source of a move: "trashed" (on the
// PC), "deleted" (on this Mac), "retained", or "pending" while the transfer
// runs. It is "" for a transfer that is not a move.
func sourceOutcome(t obj) string {
	dir := t.str("direction")
	if !isMove(dir) {
		return ""
	}
	state := t.str("state")
	switch {
	case !transferEnded(state):
		return "pending"
	case state != stateDone || t.str("outcome") == outcomeSourceRetained:
		return "retained"
	case dir == "move_pull":
		return "trashed"
	}
	return "deleted"
}

// moveSentence states what became of the source of an ended move.
func moveSentence(t obj) string {
	switch sourceOutcome(t) {
	case "trashed":
		return "원본을 휴지통으로 옮겼습니다"
	case "deleted":
		return "원본을 지웠습니다"
	case "retained":
		if n := notMoved(t); n > 0 {
			return fmt.Sprintf("원본을 남겼습니다: %d개 파일이 옮겨지지 않았습니다", n)
		}
		if m := t.sub("error").str("message"); m != "" {
			return "원본을 남겼습니다: " + m
		}
		if t.str("state") == stateDone {
			return "원본을 남겼습니다: 원본을 정리하지 못했습니다"
		}
		return "원본을 남겼습니다: 전송이 끝나기 전에 멈췄습니다"
	}
	return ""
}

// transferExit is the exit code of a transfer in its last seen state: 0 when
// everything asked for happened, 1 when files failed, were cancelled or a
// move kept its source, 5 when the PC was unreachable.
func transferExit(t obj) int {
	switch t.sub("error").str("code") {
	case wire.CodeOffline, wire.CodeAuthRequired:
		if t.str("state") != stateDone {
			return exitOffline
		}
	}
	if t.str("state") == stateDone && t.str("outcome") != outcomeSourceRetained {
		return exitOK
	}
	return exitFailure
}
