package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Transfer is a row of transfers: a file or folder transfer between this
// Mac and a host. Planned says that its files are listed in
// transfer_files; Folder that its item is a folder. Sep is the path
// separator of the host. Actor, ClientID and ClientName name who started it;
// JobID is the job that mirrors it.
type Transfer struct {
	TransferID string
	HostID     string
	Direction  string
	Local      string
	Remote     string
	OnConflict string
	Sep        string
	State      string
	Planned    bool
	Folder     bool
	Outcome    string
	Error      json.RawMessage
	Rev        int64
	Actor      string
	ClientID   string
	ClientName string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt *time.Time
	JobID      string
}

// TransferFile is a row of transfer_files: one file or folder of a
// transfer, at position Seq of its manifest. MTime and SHA256 are the
// fingerprint of the source, Path where the file was put, CommitRID the
// request id of a commit whose answer is not known, Part says that a part
// file of it may exist and Refresh that the fingerprint is taken again when
// the file starts next.
type TransferFile struct {
	TransferID string
	Seq        int
	Rel        string
	Dir        bool
	Size       int64
	MTime      string
	SHA256     string
	State      string
	BytesDone  int64
	Outcome    string
	Path       string
	Error      json.RawMessage
	CommitRID  string
	Part       bool
	Refresh    bool
}

const transferColumns = `transfer_id, host_id, direction, local_path, remote_path, on_conflict, sep, state, planned, folder, outcome,
	error_json, rev, actor, client_id, client_name, created_at, updated_at, finished_at, job_id`

const transferFileColumns = `transfer_id, seq, rel, dir, size, mtime, sha256, state, bytes_done, outcome, path, error_json, commit_rid, part,
	refresh`

func scanTransfer(sc interface{ Scan(...any) error }) (Transfer, error) {
	var t Transfer
	var errJSON, created, updated, finished string
	err := sc.Scan(&t.TransferID, &t.HostID, &t.Direction, &t.Local, &t.Remote, &t.OnConflict, &t.Sep, &t.State, &t.Planned, &t.Folder,
		&t.Outcome, &errJSON, &t.Rev, &t.Actor, &t.ClientID, &t.ClientName, &created, &updated, &finished, &t.JobID)
	if errors.Is(err, sql.ErrNoRows) {
		return Transfer{}, ErrNotFound
	}
	if err != nil {
		return Transfer{}, err
	}
	if errJSON != "" {
		t.Error = json.RawMessage(errJSON)
	}
	t.CreatedAt, t.UpdatedAt = parseTime(created), parseTime(updated)
	if finished != "" {
		f := parseTime(finished)
		t.FinishedAt = &f
	}
	return t, nil
}

func finishedText(t *time.Time) string {
	if t == nil {
		return ""
	}
	return FormatTime(*t)
}

// CreateTransfer inserts t without files.
func (s *Store) CreateTransfer(t Transfer) error {
	_, err := s.db.Exec(`INSERT INTO transfers (`+transferColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TransferID, t.HostID, t.Direction, t.Local, t.Remote, t.OnConflict, t.Sep, t.State, t.Planned, t.Folder, t.Outcome,
		rawOr(t.Error, ""), t.Rev, t.Actor, t.ClientID, t.ClientName, FormatTime(t.CreatedAt), FormatTime(t.UpdatedAt), finishedText(t.FinishedAt),
		t.JobID)
	return err
}

// SaveTransfer replaces the fields of the transfer t.TransferID other than
// its creation and stores files in one transaction: with replace, files
// become all the files of the transfer; otherwise each of them is inserted
// or replaced by its Seq.
func (s *Store) SaveTransfer(t Transfer, files []TransferFile, replace bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE transfers SET host_id = ?, direction = ?, local_path = ?, remote_path = ?, on_conflict = ?, sep = ?, state = ?,
		planned = ?, folder = ?, outcome = ?, error_json = ?, rev = ?, actor = ?, client_id = ?, client_name = ?, updated_at = ?, finished_at = ?,
		job_id = ? WHERE transfer_id = ?`,
		t.HostID, t.Direction, t.Local, t.Remote, t.OnConflict, t.Sep, t.State, t.Planned, t.Folder, t.Outcome, rawOr(t.Error, ""), t.Rev,
		t.Actor, t.ClientID, t.ClientName, FormatTime(t.UpdatedAt), finishedText(t.FinishedAt), t.JobID, t.TransferID)
	if err := affected(res, err); err != nil {
		return err
	}
	if replace {
		if _, err := tx.Exec(`DELETE FROM transfer_files WHERE transfer_id = ?`, t.TransferID); err != nil {
			return err
		}
	}
	if len(files) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO transfer_files (` + transferFileColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (transfer_id, seq) DO UPDATE SET rel = excluded.rel, dir = excluded.dir, size = excluded.size, mtime = excluded.mtime,
			sha256 = excluded.sha256, state = excluded.state, bytes_done = excluded.bytes_done, outcome = excluded.outcome, path = excluded.path,
			error_json = excluded.error_json, commit_rid = excluded.commit_rid, part = excluded.part, refresh = excluded.refresh`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, f := range files {
			if _, err := stmt.Exec(t.TransferID, f.Seq, f.Rel, f.Dir, f.Size, f.MTime, f.SHA256, f.State, f.BytesDone, f.Outcome, f.Path,
				rawOr(f.Error, ""), f.CommitRID, f.Part, f.Refresh); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// GetTransfer returns the transfer transferID and its files in manifest
// order.
func (s *Store) GetTransfer(transferID string) (Transfer, []TransferFile, error) {
	t, err := scanTransfer(s.db.QueryRow(`SELECT `+transferColumns+` FROM transfers WHERE transfer_id = ?`, transferID))
	if err != nil {
		return Transfer{}, nil, err
	}
	files, err := s.TransferFiles(transferID)
	if err != nil {
		return Transfer{}, nil, err
	}
	return t, files, nil
}

// TransferFiles returns the files of a transfer in manifest order.
func (s *Store) TransferFiles(transferID string) ([]TransferFile, error) {
	rows, err := s.db.Query(`SELECT `+transferFileColumns+` FROM transfer_files WHERE transfer_id = ? ORDER BY seq`, transferID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TransferFile{}
	for rows.Next() {
		var f TransferFile
		var errJSON string
		if err := rows.Scan(&f.TransferID, &f.Seq, &f.Rel, &f.Dir, &f.Size, &f.MTime, &f.SHA256, &f.State, &f.BytesDone, &f.Outcome, &f.Path,
			&errJSON, &f.CommitRID, &f.Part, &f.Refresh); err != nil {
			return nil, err
		}
		if errJSON != "" {
			f.Error = json.RawMessage(errJSON)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// TransferFilter selects transfers; empty fields match every transfer.
type TransferFilter struct {
	HostID string
	States []string
	Limit  int
}

// ListTransfers returns the transfers f selects, newest first, at most
// f.Limit (500 when 0, all when below 0), without their files.
func (s *Store) ListTransfers(f TransferFilter) ([]Transfer, error) {
	q := `SELECT ` + transferColumns + ` FROM transfers WHERE 1 = 1`
	var args []any
	if f.HostID != "" {
		q += ` AND host_id = ?`
		args = append(args, f.HostID)
	}
	if len(f.States) > 0 {
		q += ` AND state IN (?` + repeatComma(len(f.States)-1) + `)`
		for _, st := range f.States {
			args = append(args, st)
		}
	}
	limit := f.Limit
	if limit == 0 {
		limit = 500
	}
	q += ` ORDER BY created_at DESC, transfer_id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Transfer{}
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// repeatComma returns n times ", ?".
func repeatComma(n int) string {
	b := make([]byte, 0, 3*n)
	for range n {
		b = append(b, ", ?"...)
	}
	return string(b)
}

// TransfersWithParts returns the transfers of hostID (of every host when
// "") in state that have a file with a part file.
func (s *Store) TransfersWithParts(hostID, state string) ([]string, error) {
	q := `SELECT DISTINCT t.transfer_id FROM transfers t JOIN transfer_files f ON f.transfer_id = t.transfer_id
		WHERE t.state = ? AND f.part = 1 AND f.dir = 0`
	args := []any{state}
	if hostID != "" {
		q += ` AND t.host_id = ?`
		args = append(args, hostID)
	}
	rows, err := s.db.Query(q+` ORDER BY t.transfer_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// TransferTotal counts the files (folders left out) of a transfer: all of
// them, those done and those failed, and their bytes and bytes done.
type TransferTotal struct {
	Files, Done, Failed int
	Bytes, BytesDone    int64
}

// TransferTotals returns the totals of the transfers ids that have files.
func (s *Store) TransferTotals(ids []string) (map[string]TransferTotal, error) {
	out := map[string]TransferTotal{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT transfer_id, COUNT(*), COALESCE(SUM(state = 'done'), 0), COALESCE(SUM(state = 'failed'), 0),
		COALESCE(SUM(size), 0), COALESCE(SUM(bytes_done), 0) FROM transfer_files WHERE dir = 0 AND transfer_id IN (?`+repeatComma(len(ids)-1)+`)
		GROUP BY transfer_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var t TransferTotal
		if err := rows.Scan(&id, &t.Files, &t.Done, &t.Failed, &t.Bytes, &t.BytesDone); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// PruneTransfers removes the transfers that finished (done, failed or
// cancelled) before before and have no file with a part file, with their
// files and jobs, and returns how many it removed.
func (s *Store) PruneTransfers(before time.Time) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	const old = `SELECT transfer_id FROM transfers WHERE state IN ('done', 'failed', 'cancelled') AND finished_at != '' AND finished_at < ?
		AND transfer_id NOT IN (SELECT transfer_id FROM transfer_files WHERE part = 1 AND dir = 0)`
	at := FormatTime(before)
	if _, err := tx.Exec(`DELETE FROM jobs WHERE job_id != '' AND job_id IN (SELECT job_id FROM transfers WHERE transfer_id IN (`+old+`))`, at); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM transfer_files WHERE transfer_id IN (`+old+`)`, at); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM transfers WHERE transfer_id IN (`+old+`)`, at)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
