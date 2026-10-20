package debug

import (
	"fmt"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
)

const (
	// consoleKeep is how many console entries a session keeps.
	consoleKeep = 5000
	// consoleDefault and consoleMax are the page sizes of debug.console.
	consoleDefault = 200
	consoleMax     = 1000
	// consoleQueue bounds the console entries waiting for the pump.
	consoleQueue = 4096
)

// ConsoleEntry is a console message, an uncaught exception or a browser
// log entry of a session.
type ConsoleEntry struct {
	Seq      int64        `json:"seq"`
	Time     string       `json:"time"`
	TargetID string       `json:"target_id"`
	Source   string       `json:"source"`
	Level    string       `json:"level"`
	Text     string       `json:"text"`
	ScriptID string       `json:"script_id,omitempty"`
	URL      string       `json:"url,omitempty"`
	Line     *int         `json:"line,omitempty"`
	Column   *int         `json:"column,omitempty"`
	Stack    []StackFrame `json:"stack,omitempty"`
}

// ConsoleQuery selects a page of debug.console: after Cursor, before
// Before, or the newest entries.
type ConsoleQuery struct {
	Cursor *int64
	Before *int64
	Limit  int
}

// ConsolePage is one page of debug.console.
type ConsolePage struct {
	Entries    []ConsoleEntry `json:"entries"`
	NextCursor int64          `json:"next_cursor"`
	FirstSeq   int64          `json:"first_seq"`
	HasMore    bool           `json:"has_more"`
	HasEarlier bool           `json:"has_earlier"`
	Gap        bool           `json:"gap"`
	Dropped    int64          `json:"dropped"`
}

// consoleBuffer keeps the newest consoleKeep entries of a session; their
// seq values follow each other.
type consoleBuffer struct {
	keep    int
	entries []ConsoleEntry
	last    int64
	dropped int64
}

// add numbers e, keeps it and returns it. It trims only when the buffer
// reaches twice keep, so steady-state appends do not re-copy every entry.
func (b *consoleBuffer) add(e ConsoleEntry) ConsoleEntry {
	b.last++
	e.Seq = b.last
	b.entries = append(b.entries, e)
	keep := b.keep
	if keep <= 0 {
		keep = consoleKeep
	}
	if len(b.entries) > 2*keep {
		over := len(b.entries) - keep
		b.entries = append(b.entries[:0:0], b.entries[over:]...)
		b.dropped += int64(over)
	}
	return e
}

// drop records entries dropped before the buffer, so paging reports them.
func (b *consoleBuffer) drop(n int64) { b.dropped += n }

// clear removes every entry and returns the seq of the last one.
func (b *consoleBuffer) clear() int64 {
	b.entries = nil
	return b.last
}

// first returns the oldest seq kept, or last+1 when nothing is kept.
func (b *consoleBuffer) first() int64 {
	if len(b.entries) == 0 {
		return b.last + 1
	}
	return b.entries[0].Seq
}

// checkQuery refuses a query with both cursor and before, a negative
// position or a limit outside 0–consoleMax.
func checkQuery(q ConsoleQuery) error {
	switch {
	case q.Cursor != nil && q.Before != nil:
		return apierr.Invalid("debug.console에는 cursor와 before 중 하나만 주세요")
	case q.Cursor != nil && *q.Cursor < 0:
		return apierr.Invalid("cursor는 0 이상이어야 합니다. 이전 결과의 next_cursor를 주세요")
	case q.Before != nil && *q.Before < 1:
		return apierr.Invalid("before는 1 이상의 seq여야 합니다")
	case q.Limit < 0 || q.Limit > consoleMax:
		return apierr.Invalid(fmt.Sprintf("limit은 1부터 %d까지입니다. 값을 고쳐 다시 보내세요", consoleMax))
	}
	return nil
}

// page returns the entries q selects, oldest first.
func (b *consoleBuffer) page(q ConsoleQuery) ConsolePage {
	limit := int64(q.Limit)
	if limit == 0 {
		limit = consoleDefault
	}
	first := b.first()
	var from, to int64
	var next int64
	switch {
	case q.Cursor != nil:
		from, to = max(*q.Cursor+1, first), b.last
		to = min(to, from+limit-1)
		next = *q.Cursor
	case q.Before != nil:
		to = min(*q.Before-1, b.last)
		from = max(first, to-limit+1)
		next = *q.Before - 1
	default:
		to = b.last
		from = max(first, to-limit+1)
		next = b.last
	}
	out := ConsolePage{Entries: []ConsoleEntry{}, FirstSeq: first, Dropped: b.dropped}
	if len(b.entries) > 0 && from <= to {
		out.Entries = append(out.Entries, b.entries[from-first:to-first+1]...)
		next = to
	}
	out.NextCursor = next
	if len(b.entries) > 0 {
		lastKept := b.entries[len(b.entries)-1].Seq
		out.HasMore = lastKept > next
		if len(out.Entries) > 0 {
			out.HasEarlier = first < out.Entries[0].Seq
		} else {
			out.HasEarlier = first <= next
		}
	}
	out.Gap = q.Cursor != nil && *q.Cursor < first-1
	return out
}
