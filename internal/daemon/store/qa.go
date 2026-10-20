package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// QARun is a row of qa_runs: a QA run with its full record in Data.
type QARun struct {
	RunID      string
	JobID      string
	HostID     string
	Name       string
	Status     string
	Rev        int64
	Data       json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt *time.Time
}

const qaColumns = `run_id, job_id, host_id, name, status, rev, run_json, created_at, updated_at, finished_at`

func scanQARun(sc interface{ Scan(...any) error }) (QARun, error) {
	var r QARun
	var data, created, updated, finished string
	err := sc.Scan(&r.RunID, &r.JobID, &r.HostID, &r.Name, &r.Status, &r.Rev, &data, &created, &updated, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return QARun{}, ErrNotFound
	}
	if err != nil {
		return QARun{}, err
	}
	r.Data = json.RawMessage(data)
	r.CreatedAt, r.UpdatedAt = parseTime(created), parseTime(updated)
	if finished != "" {
		t := parseTime(finished)
		r.FinishedAt = &t
	}
	return r, nil
}

// CreateQARun inserts r.
func (s *Store) CreateQARun(r QARun) error {
	_, err := s.db.Exec(`INSERT INTO qa_runs (`+qaColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RunID, r.JobID, r.HostID, r.Name, r.Status, r.Rev, rawOr(r.Data, "{}"), FormatTime(r.CreatedAt), FormatTime(r.UpdatedAt),
		finishedText(r.FinishedAt))
	return err
}

// SaveQARun stores the status, rev, record and times of r.
func (s *Store) SaveQARun(r QARun) error {
	res, err := s.db.Exec(`UPDATE qa_runs SET status = ?, rev = ?, run_json = ?, updated_at = ?, finished_at = ? WHERE run_id = ?`,
		r.Status, r.Rev, rawOr(r.Data, "{}"), FormatTime(r.UpdatedAt), finishedText(r.FinishedAt), r.RunID)
	return affected(res, err)
}

// GetQARun returns the run runID.
func (s *Store) GetQARun(runID string) (QARun, error) {
	return scanQARun(s.db.QueryRow(`SELECT `+qaColumns+` FROM qa_runs WHERE run_id = ?`, runID))
}

// QARunFilter selects runs: HostID matches every host when "", Before
// keeps the runs whose id sorts before it, Unfinished the runs that have
// not finished; Limit 0 lifts the limit.
type QARunFilter struct {
	HostID     string
	Before     string
	Unfinished bool
	Limit      int
}

// ListQARuns returns the runs f selects, newest first.
func (s *Store) ListQARuns(f QARunFilter) ([]QARun, error) {
	q := `SELECT ` + qaColumns + ` FROM qa_runs WHERE 1 = 1`
	var args []any
	if f.HostID != "" {
		q += ` AND host_id = ?`
		args = append(args, f.HostID)
	}
	if f.Before != "" {
		q += ` AND run_id < ?`
		args = append(args, f.Before)
	}
	if f.Unfinished {
		q += ` AND finished_at = ''`
	}
	limit := f.Limit
	if limit <= 0 {
		limit = -1
	}
	q += ` ORDER BY run_id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QARun{}
	for rows.Next() {
		r, err := scanQARun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
