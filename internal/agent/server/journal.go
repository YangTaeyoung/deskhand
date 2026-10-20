package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// journalKeep is how long a finished request stays in the journal.
	journalKeep = 24 * time.Hour
	// journalPruneEvery is the least time between two prunes of the journal.
	journalPruneEvery = time.Hour
	// ridMaxLen bounds the length of a rid.
	ridMaxLen = 128
)

const (
	journalRunning = "running"
	journalDone    = "done"
)

// errInterrupted is the stored answer of a request that was running when the
// agent stopped.
const errInterrupted = "deskhand-agent stopped while this request ran, so whether it took effect is unknown; check the PC's state before sending it again with a new rid"

// journalRecord is the file DataDir/journal/<rid>.json. Res holds the fields
// of the answer without t and id.
type journalRecord struct {
	RID      string        `json:"rid"`
	Op       string        `json:"op"`
	State    string        `json:"state"`
	Started  time.Time     `json:"started"`
	Finished time.Time     `json:"finished,omitzero"`
	Res      proto.Message `json:"res,omitempty"`
}

// journalEntry is what the journal keeps in memory of one rid; res is kept
// by a journal without a folder and when the record of the answer could not
// be written.
type journalEntry struct {
	op       string
	running  bool
	started  time.Time
	finished time.Time
	res      proto.Message
}

// journal maps request ids to the answers of the requests that carried them,
// for journalKeep after they finished. With a folder every record is also a
// file there, written before the request runs and again with its answer.
type journal struct {
	dir string
	lg  *log.Logger
	now func() time.Time

	mu      sync.Mutex
	entries map[string]*journalEntry
	pruned  time.Time
	// writeRecord stores a record in dir; it is read with mu held.
	writeRecord func(journalRecord) error
}

// openJournal returns the journal of dir, loading its records; an empty dir
// keeps the journal in memory. A record still running becomes the answer
// errInterrupted with code EXECUTION_OUTCOME_UNKNOWN, a record that cannot
// be read too, and records older than journalKeep are removed.
func openJournal(dir string, lg *log.Logger) *journal {
	j := &journal{dir: dir, lg: lg, now: time.Now, entries: map[string]*journalEntry{}}
	j.writeRecord = j.write
	if dir == "" {
		return j
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		lg.Printf("journal: create %s: %v", dir, err)
		return j
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		lg.Printf("journal: read %s: %v", dir, err)
		return j
	}
	now := j.now()
	for _, e := range names {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		rid, ok := strings.CutSuffix(name, ".json")
		if !ok || !validRID(rid) {
			continue
		}
		rec, err := j.read(rid)
		if err != nil || rec.RID != rid {
			lg.Printf("journal: the record of %s cannot be read (%v); its outcome is unknown", rid, err)
			rec = journalRecord{RID: rid, State: journalRunning, Started: now}
		}
		if rec.State != journalDone {
			rec.State, rec.Finished, rec.Res = journalDone, now, interruptedAnswer()
			if err := j.write(rec); err != nil {
				lg.Printf("journal: %v", err)
			}
		}
		if now.Sub(rec.Finished) > journalKeep {
			_ = os.Remove(j.path(rid))
			continue
		}
		j.entries[rid] = &journalEntry{op: rec.Op, started: rec.Started, finished: rec.Finished}
	}
	j.pruned = now
	return j
}

// interruptedAnswer is the answer fields of a request whose outcome is unknown.
func interruptedAnswer() proto.Message {
	return proto.Message{"ok": false, "code": wire.CodeExecutionOutcomeUnknown, "error": errInterrupted, "retryable": false}
}

// validRID reports whether rid is 1 to ridMaxLen ASCII letters, digits, '-'
// or '_' and is not the name of a Windows device.
func validRID(rid string) bool {
	if rid == "" || len(rid) > ridMaxLen {
		return false
	}
	for _, c := range rid {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return !windowsDeviceName(rid)
}

// windowsDeviceName reports whether name is CON, PRN, AUX, NUL, COM0–COM9 or
// LPT0–LPT9 in any case.
func windowsDeviceName(name string) bool {
	n := strings.ToUpper(name)
	switch n {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(n) == 4 && (strings.HasPrefix(n, "COM") || strings.HasPrefix(n, "LPT")) && n[3] >= '0' && n[3] <= '9'
}

func ridError(rid any) error {
	return coded(wire.CodeInvalidArgument, fmt.Sprintf("rid %s is not a string of 1 to %d letters, digits, '-' or '_', or names a Windows device; use a ULID or UUID", cutChars(fmt.Sprintf("%q", fmt.Sprint(rid)), ridMaxLen+2), ridMaxLen), false)
}

// requestRID returns the rid of req and whether req has one; a rid that is
// present but not a string validRID accepts is an error.
func requestRID(req proto.Message) (string, bool, error) {
	v, ok := req["rid"]
	if !ok {
		return "", false, nil
	}
	rid, isString := v.(string)
	if !isString || !validRID(rid) {
		return "", false, ridError(v)
	}
	return rid, true, nil
}

func (j *journal) path(rid string) string { return filepath.Join(j.dir, rid+".json") }

func (j *journal) read(rid string) (journalRecord, error) {
	var rec journalRecord
	b, err := os.ReadFile(j.path(rid))
	if err != nil {
		return rec, err
	}
	err = json.Unmarshal(b, &rec)
	return rec, err
}

// write stores rec through a temporary file that replaces the old record.
func (j *journal) write(rec journalRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode the record of %s: %w", rec.RID, err)
	}
	return writeFileAtomic(j.path(rec.RID), b)
}

// writeFileAtomic writes data to a temporary file next to path, flushes it to
// disk and renames it over path.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// pruneLocked removes the finished entries older than journalKeep once per
// journalPruneEvery. j.mu is held.
func (j *journal) pruneLocked(now time.Time) {
	if now.Sub(j.pruned) < journalPruneEvery {
		return
	}
	j.pruned = now
	for rid, e := range j.entries {
		if !e.running && now.Sub(e.finished) > journalKeep {
			delete(j.entries, rid)
			if j.dir != "" {
				_ = os.Remove(j.path(rid))
			}
		}
	}
}

// storedLocked returns the answer fields of the finished entry e of rid: the
// ones kept in memory, else the ones of its record, else, for a record
// without an answer, interruptedAnswer. j.mu is held.
func (j *journal) storedLocked(rid string, e *journalEntry) (proto.Message, error) {
	if e.res != nil {
		return e.res, nil
	}
	if j.dir == "" {
		return interruptedAnswer(), nil
	}
	rec, err := j.read(rid)
	if err != nil {
		j.lg.Printf("journal: read the record of %s: %v", rid, err)
		return nil, coded(wire.CodeInternal, fmt.Sprintf("the journal record of rid '%s' cannot be read; see the agent log", rid), false)
	}
	if rec.State != journalDone || rec.Res == nil {
		return interruptedAnswer(), nil
	}
	return rec.Res, nil
}

// begin records rid as running op and returns "", or returns "running"
// while a request with rid runs, or "done" and its answer fields when one
// finished. A rid of another op and a record that cannot be written are
// errors; then nothing is recorded.
func (j *journal) begin(rid, op string) (string, proto.Message, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	j.pruneLocked(now)
	if e, ok := j.entries[rid]; ok {
		if e.op != "" && e.op != op {
			return "", nil, coded(wire.CodeInvalidArgument, fmt.Sprintf("rid '%s' was used for %s; give every request a rid of its own", rid, e.op), false)
		}
		if e.running {
			return journalRunning, nil, nil
		}
		res, err := j.storedLocked(rid, e)
		return journalDone, res, err
	}
	if j.dir != "" {
		if err := j.writeRecord(journalRecord{RID: rid, Op: op, State: journalRunning, Started: now}); err != nil {
			j.lg.Printf("journal: %v", err)
			return "", nil, coded(wire.CodeInternal, fmt.Sprintf("the request was not run because its journal record could not be written in %s; check that the folder is writable", j.dir), true)
		}
	}
	j.entries[rid] = &journalEntry{op: op, running: true, started: now}
	return "", nil, nil
}

// finish records res, an answer without t and id, as the answer of rid. The
// answer stays in memory when the journal has no folder or its record could
// not be written.
func (j *journal) finish(rid string, res proto.Message) {
	j.mu.Lock()
	e := j.entries[rid]
	write := j.writeRecord
	j.mu.Unlock()
	if e == nil {
		return
	}
	now := j.now()
	keep := j.dir == ""
	if !keep {
		if err := write(journalRecord{RID: rid, Op: e.op, State: journalDone, Started: e.started, Finished: now, Res: res}); err != nil {
			j.lg.Printf("journal: %v; the answer of %s is kept in memory until the agent stops", err, rid)
			keep = true
		}
	}
	j.mu.Lock()
	e.running, e.finished = false, now
	if keep {
		e.res = res
	}
	j.mu.Unlock()
}

// status returns the state of rid: "unknown", "running", or "done" with the
// answer fields.
func (j *journal) status(rid string) (string, proto.Message, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pruneLocked(j.now())
	e, ok := j.entries[rid]
	switch {
	case !ok:
		return "unknown", nil, nil
	case e.running:
		return journalRunning, nil, nil
	}
	res, err := j.storedLocked(rid, e)
	return journalDone, res, err
}

// answerFields returns res without t and id.
func answerFields(res proto.Message) proto.Message {
	out := make(proto.Message, len(res))
	for k, v := range res {
		if k != "t" && k != "id" {
			out[k] = v
		}
	}
	return out
}

// answerOf returns the "res" of a request with id made of the answer fields
// fields and extra.
func answerOf(id any, fields proto.Message, extra proto.Message) proto.Message {
	out := make(proto.Message, len(fields)+len(extra)+2)
	for k, v := range fields {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	out["t"], out["id"] = "res", id
	return out
}

// dispatchJournaled runs a journaled request with rid once: a request whose
// rid runs is answered IN_PROGRESS, one whose rid finished gets the stored
// answer with replayed set.
func (s *Server) dispatchJournaled(ctx context.Context, req proto.Message, rid string, send func(m proto.Message, line []byte)) {
	state, stored, err := s.journal.begin(rid, req.String("op"))
	switch {
	case err != nil:
		send(failure(req["id"], err), nil)
		return
	case state == journalRunning:
		send(failure(req["id"], coded(wire.CodeInProgress, fmt.Sprintf("the request with rid '%s' is still running; call req.status with that rid until it is done", rid), true)), nil)
		return
	case state == journalDone:
		send(answerOf(req["id"], stored, proto.Message{"replayed": true}), nil)
		return
	}
	res, line := s.respond(ctx, req)
	s.journal.finish(rid, answerFields(res))
	send(res, line)
}

// reqStatus answers req.status for rid, a valid rid or "" when the request
// has none: the state of the request with rid, and when it is done the fields
// of its answer.
func (s *Server) reqStatus(req proto.Message, rid string) proto.Message {
	if rid == "" {
		return failure(req["id"], coded(wire.CodeInvalidArgument, "req.status needs the rid of the request to look up", false))
	}
	state, stored, err := s.journal.status(rid)
	if err != nil {
		return failure(req["id"], err)
	}
	if state != journalDone {
		return answerOf(req["id"], nil, proto.Message{"ok": true, "state": state})
	}
	return answerOf(req["id"], stored, proto.Message{"state": journalDone})
}

// codedError is an error answered with an error code of docs/protocol.md §8.
type codedError struct {
	code      string
	msg       string
	retryable bool
}

func (e *codedError) Error() string { return e.msg }

func coded(code, msg string, retryable bool) error {
	return &codedError{code: code, msg: msg, retryable: retryable}
}

// failure is the "res" of a request with id that failed with err; a
// codedError adds its code and retryable.
func failure(id any, err error) proto.Message {
	m := proto.Message{"t": "res", "id": id, "ok": false, "error": err.Error()}
	var ce *codedError
	if errors.As(err, &ce) {
		m["code"], m["retryable"] = ce.code, ce.retryable
	}
	return m
}
