package store

import (
	"database/sql"
	"errors"
	"time"
)

// ErrGenDecrease is returned by PutControl for a gen below the stored one.
var ErrGenDecrease = errors.New("control gen would decrease")

// Control is a row of control: who may drive a host's input. OwnerClient
// and OwnerName are the client id and name of the owner, LeaseUntil the end
// of an AI owner's lease (zero for other owners).
type Control struct {
	HostID      string
	Owner       string
	OwnerClient string
	OwnerName   string
	Gen         uint64
	AIPaused    bool
	LeaseUntil  time.Time
	UpdatedAt   time.Time
}

const controlColumns = `host_id, owner, owner_client, owner_name, gen, ai_paused, lease_until, updated_at`

func scanControl(sc interface{ Scan(...any) error }) (Control, error) {
	var c Control
	var gen int64
	var paused int
	var lease, updated string
	err := sc.Scan(&c.HostID, &c.Owner, &c.OwnerClient, &c.OwnerName, &gen, &paused, &lease, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Control{}, ErrNotFound
	}
	if err != nil {
		return Control{}, err
	}
	c.Gen, c.AIPaused = uint64(gen), paused != 0
	if lease != "" {
		c.LeaseUntil = parseTime(lease)
	}
	c.UpdatedAt = parseTime(updated)
	return c, nil
}

// PutControl stores c in place of the host's row; a gen below the stored
// one is ErrGenDecrease and changes nothing.
func (s *Store) PutControl(c Control) error {
	lease := ""
	if !c.LeaseUntil.IsZero() {
		lease = FormatTime(c.LeaseUntil)
	}
	paused := 0
	if c.AIPaused {
		paused = 1
	}
	res, err := s.db.Exec(`INSERT INTO control (`+controlColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (host_id) DO UPDATE SET owner = excluded.owner, owner_client = excluded.owner_client,
			owner_name = excluded.owner_name, gen = excluded.gen, ai_paused = excluded.ai_paused,
			lease_until = excluded.lease_until, updated_at = excluded.updated_at
		WHERE excluded.gen >= control.gen`,
		c.HostID, c.Owner, c.OwnerClient, c.OwnerName, int64(c.Gen), paused, lease, FormatTime(c.UpdatedAt))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrGenDecrease
	}
	return nil
}

// GetControl returns the row of hostID.
func (s *Store) GetControl(hostID string) (Control, error) {
	return scanControl(s.db.QueryRow(`SELECT `+controlColumns+` FROM control WHERE host_id = ?`, hostID))
}

// ListControls returns every row by host id.
func (s *Store) ListControls() ([]Control, error) {
	rows, err := s.db.Query(`SELECT ` + controlColumns + ` FROM control ORDER BY host_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Control
	for rows.Next() {
		c, err := scanControl(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
