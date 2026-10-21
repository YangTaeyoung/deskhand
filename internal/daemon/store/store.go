// Package store keeps the daemon's hosts, jobs, events and requests in
// SQLite (modernc.org/sqlite).
package store

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// schemaVersion is the PRAGMA user_version of the schema in schema.sql.
const schemaVersion = 1

// TimeLayout is the layout of every stored time and of the times of the
// local API: RFC 3339 in UTC with milliseconds.
const TimeLayout = "2006-01-02T15:04:05.000Z07:00"

// FormatTime formats t in UTC with TimeLayout.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// Request states.
const (
	RequestSent    = "sent"
	RequestDone    = "done"
	RequestUnknown = "unknown"
	RequestNotSent = "not_sent"
)

var (
	// ErrNotFound is returned for a row that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrNameTaken is returned by PutHost for a name another host has.
	ErrNameTaken = errors.New("host name taken")
)

// Store is the daemon database.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path with mode 0600 and brings its
// schema to schemaVersion.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("데이터베이스 %s를 열지 못했습니다. 파일 권한을 확인하세요: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("데이터베이스 %s를 준비하지 못했습니다. 파일이 손상됐다면 옮겨 두고 데몬을 다시 시작하세요: %w", path, err)
	}
	_ = os.Chmod(path, 0o600)
	return s, nil
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v > schemaVersion {
		return fmt.Errorf("schema version %d is newer than %d", v, schemaVersion)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range strings.Split(schema, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	for _, c := range addedColumns {
		have := false
		rows, err := tx.Query(`SELECT name FROM pragma_table_info(?)`, c.table)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			have = have || name == c.column
		}
		rows.Close()
		if !have {
			if _, err := tx.Exec(`ALTER TABLE ` + c.table + ` ADD COLUMN ` + c.column + ` ` + c.definition); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// addedColumns are the columns of schema.sql that a table made by an older
// schema lacks; migrate adds them.
var addedColumns = []struct{ table, column, definition string }{
	{"transfers", "job_id", "TEXT NOT NULL DEFAULT ''"},
	{"hosts", "ai_scope", "TEXT NOT NULL DEFAULT ''"},
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Host is a row of hosts.
type Host struct {
	HostID    string
	Name      string
	Address   string
	Port      int
	AgentFP   string
	OS        string
	Arch      string
	SSHAlias  string
	CreatedAt time.Time
	LastSeen  time.Time
}

const hostColumns = `host_id, name, address, port, agent_fp, os, arch, ssh_alias, created_at, last_seen`

func scanHost(sc interface{ Scan(...any) error }) (Host, error) {
	var h Host
	var created, seen string
	err := sc.Scan(&h.HostID, &h.Name, &h.Address, &h.Port, &h.AgentFP, &h.OS, &h.Arch, &h.SSHAlias, &created, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return Host{}, ErrNotFound
	}
	h.CreatedAt = parseTime(created)
	if seen != "" {
		h.LastSeen = parseTime(seen)
	}
	return h, err
}

func optTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return FormatTime(t)
}

// PutHost inserts h; a name another host has is ErrNameTaken.
func (s *Store) PutHost(h Host) error {
	if other, err := s.HostByName(h.Name); err == nil && other.HostID != h.HostID {
		return ErrNameTaken
	}
	_, err := s.db.Exec(`INSERT INTO hosts (`+hostColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.HostID, h.Name, h.Address, h.Port, h.AgentFP, h.OS, h.Arch, h.SSHAlias, FormatTime(h.CreatedAt), optTime(h.LastSeen))
	return err
}

// UpdateHost replaces the fields of the host h.HostID except its creation time.
func (s *Store) UpdateHost(h Host) error {
	return s.UpdateHostWithScope(h, nil)
}

// UpdateHostWithScope changes the host and, when scope is non-nil, its AI
// scope in one SQLite statement. An empty scope clears the stored limit.
func (s *Store) UpdateHostWithScope(h Host, scope *string) error {
	var value any
	if scope != nil {
		value = *scope
	}
	res, err := s.db.Exec(`UPDATE hosts SET name = ?, address = ?, port = ?, agent_fp = ?, os = ?, arch = ?, ssh_alias = ?, ai_scope = COALESCE(?, ai_scope) WHERE host_id = ?`,
		h.Name, h.Address, h.Port, h.AgentFP, h.OS, h.Arch, h.SSHAlias, value, h.HostID)
	return affected(res, err)
}

// HostAIScope returns the stored AI scope JSON of the host; "" while none
// was set.
func (s *Store) HostAIScope(hostID string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT ai_scope FROM hosts WHERE host_id = ?`, hostID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetHostAIScope stores the AI scope JSON of the host; "" clears it.
func (s *Store) SetHostAIScope(hostID, scope string) error {
	res, err := s.db.Exec(`UPDATE hosts SET ai_scope = ? WHERE host_id = ?`, scope, hostID)
	return affected(res, err)
}

// TouchHost records when the host was last seen connected.
func (s *Store) TouchHost(hostID string, seen time.Time) error {
	res, err := s.db.Exec(`UPDATE hosts SET last_seen = ? WHERE host_id = ?`, FormatTime(seen), hostID)
	return affected(res, err)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteHost removes the host.
func (s *Store) DeleteHost(hostID string) error {
	res, err := s.db.Exec(`DELETE FROM hosts WHERE host_id = ?`, hostID)
	return affected(res, err)
}

// GetHost returns the host with hostID.
func (s *Store) GetHost(hostID string) (Host, error) {
	return scanHost(s.db.QueryRow(`SELECT `+hostColumns+` FROM hosts WHERE host_id = ?`, hostID))
}

// HostByName returns the host named name.
func (s *Store) HostByName(name string) (Host, error) {
	return scanHost(s.db.QueryRow(`SELECT `+hostColumns+` FROM hosts WHERE name = ?`, name))
}

// ListHosts returns every host in creation order.
func (s *Store) ListHosts() ([]Host, error) {
	rows, err := s.db.Query(`SELECT ` + hostColumns + ` FROM hosts ORDER BY created_at, host_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Job is a row of jobs. Result is empty while the job has none.
type Job struct {
	JobID      string
	HostID     string
	Kind       string
	RequestID  string
	Status     string
	Summary    string
	Params     json.RawMessage
	Result     json.RawMessage
	RemoteJob  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt *time.Time
}

const jobColumns = `job_id, host_id, kind, request_id, status, summary, params_json, result_json, remote_job, created_at, updated_at, finished_at`

func scanJob(sc interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var params, result, created, updated, finished string
	err := sc.Scan(&j.JobID, &j.HostID, &j.Kind, &j.RequestID, &j.Status, &j.Summary, &params, &result, &j.RemoteJob,
		&created, &updated, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	j.Params = json.RawMessage(params)
	if result != "" {
		j.Result = json.RawMessage(result)
	}
	j.CreatedAt, j.UpdatedAt = parseTime(created), parseTime(updated)
	if finished != "" {
		t := parseTime(finished)
		j.FinishedAt = &t
	}
	return j, nil
}

func rawOr(b json.RawMessage, empty string) string {
	if len(b) == 0 {
		return empty
	}
	return string(b)
}

// CreateJob inserts j.
func (s *Store) CreateJob(j Job) error {
	finished := ""
	if j.FinishedAt != nil {
		finished = FormatTime(*j.FinishedAt)
	}
	_, err := s.db.Exec(`INSERT INTO jobs (`+jobColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.JobID, j.HostID, j.Kind, j.RequestID, j.Status, j.Summary, rawOr(j.Params, "{}"), rawOr(j.Result, ""), j.RemoteJob,
		FormatTime(j.CreatedAt), FormatTime(j.UpdatedAt), finished)
	return err
}

// ErrJobFinished is returned for a change to a job that has finished.
var ErrJobFinished = errors.New("job finished")

// unfinished returns ErrJobFinished when the update of res touched no row
// and the job exists, and ErrNotFound when it does not exist.
func (s *Store) unfinished(jobID string, res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		return err
	}
	if _, err := s.GetJob(jobID); err != nil {
		return err
	}
	return ErrJobFinished
}

// SetJobRunning records the remote job id and result of a running job; a
// finished job is left as it is (ErrJobFinished).
func (s *Store) SetJobRunning(jobID, remoteJob string, result json.RawMessage, now time.Time) error {
	res, err := s.db.Exec(`UPDATE jobs SET status = 'running', remote_job = ?, result_json = ?, updated_at = ? WHERE job_id = ? AND finished_at = ''`,
		remoteJob, rawOr(result, ""), FormatTime(now), jobID)
	return s.unfinished(jobID, res, err)
}

// FinishJob sets the final status and result of a job that has not
// finished; a finished job is left as it is (ErrJobFinished).
func (s *Store) FinishJob(jobID, status string, result json.RawMessage, now time.Time) error {
	ts := FormatTime(now)
	res, err := s.db.Exec(`UPDATE jobs SET status = ?, result_json = ?, updated_at = ?, finished_at = ? WHERE job_id = ? AND finished_at = ''`,
		status, rawOr(result, ""), ts, ts, jobID)
	return s.unfinished(jobID, res, err)
}

// SetJobState sets the status and result of the job jobID, finished or
// not, also when it had finished.
func (s *Store) SetJobState(jobID, status string, result json.RawMessage, finished bool, now time.Time) error {
	fin := ""
	if finished {
		fin = FormatTime(now)
	}
	res, err := s.db.Exec(`UPDATE jobs SET status = ?, result_json = ?, updated_at = ?, finished_at = ? WHERE job_id = ?`,
		status, rawOr(result, ""), FormatTime(now), fin, jobID)
	return affected(res, err)
}

// GetJob returns the job with jobID.
func (s *Store) GetJob(jobID string) (Job, error) {
	return scanJob(s.db.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE job_id = ?`, jobID))
}

// JobByRemote returns the job of host whose remote job id is remote.
func (s *Store) JobByRemote(hostID, remote string) (Job, error) {
	return scanJob(s.db.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE host_id = ? AND remote_job = ? ORDER BY created_at DESC LIMIT 1`,
		hostID, remote))
}

// JobFilter selects jobs; empty fields match every job. Unfinished selects
// the jobs that have not finished; All lifts the limit.
type JobFilter struct {
	HostID, Kind, Status string
	Unfinished           bool
	All                  bool
	Limit                int
}

// ListJobs returns the jobs that f selects, newest first, at most f.Limit
// (500 when 0) unless f.All.
func (s *Store) ListJobs(f JobFilter) ([]Job, error) {
	q := `SELECT ` + jobColumns + ` FROM jobs WHERE 1 = 1`
	var args []any
	for _, c := range []struct{ col, v string }{{"host_id", f.HostID}, {"kind", f.Kind}, {"status", f.Status}} {
		if c.v != "" {
			q += ` AND ` + c.col + ` = ?`
			args = append(args, c.v)
		}
	}
	if f.Unfinished {
		q += ` AND finished_at = ''`
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 500
	}
	if f.All {
		limit = -1
	}
	q += ` ORDER BY created_at DESC, job_id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// AppendEvent stores an event and returns its sequence number.
func (s *Store) AppendEvent(ts time.Time, hostID, jobID, kind string, payload json.RawMessage) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO events (ts, host_id, job_id, kind, payload_json) VALUES (?, ?, ?, ?, ?)`,
		FormatTime(ts), hostID, jobID, kind, rawOr(payload, "{}"))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Event is a row of events.
type Event struct {
	Seq     int64
	TS      time.Time
	HostID  string
	JobID   string
	Kind    string
	Payload json.RawMessage
}

// GetEvent returns the stored event seq.
func (s *Store) GetEvent(seq int64) (Event, error) {
	var e Event
	var ts, payload string
	err := s.db.QueryRow(`SELECT seq, ts, host_id, job_id, kind, payload_json FROM events WHERE seq = ?`, seq).
		Scan(&e.Seq, &ts, &e.HostID, &e.JobID, &e.Kind, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	e.TS, e.Payload = parseTime(ts), json.RawMessage(payload)
	return e, err
}

// Request is a row of requests.
type Request struct {
	RequestID string
	HostID    string
	Op        string
	State     string
	Result    json.RawMessage
	CreatedAt time.Time
}

// RequestSent records a request with a request id as sent.
func (s *Store) RequestSent(rid, hostID, op string) error {
	_, err := s.db.Exec(`INSERT INTO requests (request_id, host_id, op, state, created_at) VALUES (?, ?, ?, ?, ?)`,
		rid, hostID, op, RequestSent, FormatTime(time.Now()))
	return err
}

// SetRequestState records the state of a request and, when result is not
// empty, its answer.
func (s *Store) SetRequestState(rid, state string, result json.RawMessage) error {
	res, err := s.db.Exec(`UPDATE requests SET state = ?, result_json = CASE WHEN ? = '' THEN result_json ELSE ? END WHERE request_id = ?`,
		state, rawOr(result, ""), rawOr(result, ""), rid)
	return affected(res, err)
}

// GetRequest returns the request with rid.
func (s *Store) GetRequest(rid string) (Request, error) {
	var r Request
	var result, created string
	err := s.db.QueryRow(`SELECT request_id, host_id, op, state, result_json, created_at FROM requests WHERE request_id = ?`, rid).
		Scan(&r.RequestID, &r.HostID, &r.Op, &r.State, &result, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if result != "" {
		r.Result = json.RawMessage(result)
	}
	r.CreatedAt = parseTime(created)
	return r, err
}

// MarkSentUnknown sets every sent request to unknown and returns how many
// there were; the daemon calls it when it starts.
func (s *Store) MarkSentUnknown() (int64, error) {
	res, err := s.db.Exec(`UPDATE requests SET state = ? WHERE state = ?`, RequestUnknown, RequestSent)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Timeline is a row of timeline: one action of a person, an AI client or
// the daemon. Artifacts is a JSON array.
type Timeline struct {
	Seq        int64
	TS         time.Time
	HostID     string
	Actor      string
	ClientID   string
	ClientName string
	Kind       string
	Summary    string
	Outcome    string
	JobID      string
	Artifacts  json.RawMessage
}

const timelineColumns = `seq, ts, host_id, actor, client_id, client_name, kind, summary, outcome, job_id, artifacts_json`

// AppendTimeline stores e and returns it with its sequence number.
func (s *Store) AppendTimeline(e Timeline) (Timeline, error) {
	res, err := s.db.Exec(`INSERT INTO timeline (ts, host_id, actor, client_id, client_name, kind, summary, outcome, job_id, artifacts_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		FormatTime(e.TS), e.HostID, e.Actor, e.ClientID, e.ClientName, e.Kind, e.Summary, e.Outcome, e.JobID, rawOr(e.Artifacts, "[]"))
	if err != nil {
		return Timeline{}, err
	}
	e.Seq, err = res.LastInsertId()
	return e, err
}

// TimelineFilter selects timeline rows: HostID when set, rows at or after
// Since when set, rows before the sequence number Before when it is above 0.
type TimelineFilter struct {
	HostID string
	Since  time.Time
	Before int64
	Limit  int
}

// ListTimeline returns the rows f selects, newest first, at most f.Limit
// (100 when 0).
func (s *Store) ListTimeline(f TimelineFilter) ([]Timeline, error) {
	q := `SELECT ` + timelineColumns + ` FROM timeline WHERE 1 = 1`
	var args []any
	if f.HostID != "" {
		q += ` AND host_id = ?`
		args = append(args, f.HostID)
	}
	if !f.Since.IsZero() {
		q += ` AND ts >= ?`
		args = append(args, FormatTime(f.Since))
	}
	if f.Before > 0 {
		q += ` AND seq < ?`
		args = append(args, f.Before)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q += ` ORDER BY seq DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Timeline{}
	for rows.Next() {
		var e Timeline
		var ts, arts string
		if err := rows.Scan(&e.Seq, &ts, &e.HostID, &e.Actor, &e.ClientID, &e.ClientName, &e.Kind, &e.Summary, &e.Outcome, &e.JobID, &arts); err != nil {
			return nil, err
		}
		e.TS, e.Artifacts = parseTime(ts), json.RawMessage(arts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Prune removes the events older than events and the settled requests
// (done or not sent) older than requests.
func (s *Store) Prune(events, requests time.Time) error {
	if _, err := s.db.Exec(`DELETE FROM events WHERE ts < ?`, FormatTime(events)); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM requests WHERE state IN (?, ?) AND created_at < ?`, RequestDone, RequestNotSent, FormatTime(requests))
	return err
}
