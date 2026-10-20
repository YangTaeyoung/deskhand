// Package xfer is the daemon's transfer manager: it pushes files and
// folders from this Mac to a host and pulls them from it over the agent's
// put and get streams and xfer ops (docs/protocol.md §2, §3.1), with
// resume, conflict policies, verified moves and progress events, and keeps
// every transfer in the store (docs/local-api.md §5, §8).
package xfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Directions of a transfer.
const (
	Push     = "push"
	Pull     = "pull"
	MovePush = "move_push"
	MovePull = "move_pull"
)

// States of a transfer and of its files.
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StatePaused    = "paused"
	StateVerifying = "verifying"
	StateDone      = "done"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// Outcomes of a transfer and of its files.
const (
	OutcomeWritten        = "written"
	OutcomeSkipped        = "skipped"
	OutcomeRenamed        = "renamed"
	OutcomeSourceRetained = "copied_source_retained"
	OutcomePartial        = "partial"
	OutcomeFailed         = "failed"
)

// Options configure a Manager.
type Options struct {
	Store *store.Store
	Bus   *events.Bus
	Conns *conn.Manager
	// Logger receives the manager's log; nil discards it.
	Logger *log.Logger
	Hooks  Hooks
}

// Hooks are called by the file workers; nil hooks are skipped.
type Hooks struct {
	// Opened runs when a put or get stream of the file rel of a transfer
	// starts at offset.
	Opened func(transferID, rel string, offset int64)
	// Wrote runs after a chunk of the file rel went to the put stream or
	// into the part file of a pull; done bytes of the file are written.
	Wrote func(transferID, rel string, done int64)
	// Step runs when a transfer whose files are all finished reaches step:
	// "remove" before a move removes its source, "finish" before the
	// transfer ends. ctx is the context of its run.
	Step func(ctx context.Context, transferID, step string)
}

// StartParams are the parameters of transfer.start: Local and Remote are
// the full paths of the item on this Mac and on the host.
type StartParams struct {
	HostID     string
	Direction  string
	Local      string
	Remote     string
	OnConflict string
}

// Started is what Start did: the new transfer and the request that checked
// the host, or, with Err set, why no transfer started.
type Started struct {
	Transfer model.Transfer
	Info     conn.CallInfo
	Err      *apierr.Error
}

// Manager runs the transfers.
type Manager struct {
	opt Options
	lg  *log.Logger

	// base ends with Close; every run of a transfer runs under it.
	base context.Context
	stop context.CancelFunc

	// lifeMu guards closed and the additions to wg.
	lifeMu sync.Mutex
	closed bool
	wg     sync.WaitGroup

	// mu guards live and is taken before the lock of a transfer.
	mu   sync.Mutex
	live map[string]*transfer
}

// New returns a Manager; Load restores the stored transfers.
func New(opt Options) *Manager {
	lg := opt.Logger
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	m := &Manager{opt: opt, lg: lg, live: map[string]*transfer{}}
	m.base, m.stop = context.WithCancel(context.Background())
	return m
}

// Load restores the transfers the daemon left unfinished when it stopped:
// a queued, running or verifying transfer becomes queued and goes on once
// its host is ready, a paused one stays paused, and a cancelled one whose
// part files on the host are not removed yet removes them once its host is
// ready. It returns how many transfers go on.
func (m *Manager) Load() int {
	rows, err := m.opt.Store.ListTransfers(store.TransferFilter{States: []string{StateQueued, StateRunning, StateVerifying}, Limit: -1})
	if err != nil {
		m.lg.Printf("transfers: load: %v", err)
		return 0
	}
	n := 0
	for _, row := range rows {
		t, err := m.acquire(row.TransferID)
		if err != nil {
			m.lg.Printf("transfers: load %s: %v", row.TransferID, err)
			continue
		}
		t.mu.Lock()
		t.interruptedLocked()
		t.launchLocked()
		t.mu.Unlock()
		m.release(t)
		n++
	}
	m.cleanupCancelled("")
	return n
}

// Close stops every run, stores what the transfers did and keeps them for
// the next Load.
func (m *Manager) Close() {
	m.lifeMu.Lock()
	m.closed = true
	m.lifeMu.Unlock()
	m.mu.Lock()
	live := make([]*transfer, 0, len(m.live))
	for _, t := range m.live {
		live = append(live, t)
	}
	m.mu.Unlock()
	for _, t := range live {
		t.mu.Lock()
		r := t.run
		t.mu.Unlock()
		if r != nil {
			r.cancel(errClosing)
		}
	}
	m.stop()
	m.wg.Wait()
	m.mu.Lock()
	live = live[:0]
	for _, t := range m.live {
		live = append(live, t)
	}
	m.mu.Unlock()
	for _, t := range live {
		t.mu.Lock()
		if t.timer != nil {
			t.flushLocked()
		}
		t.closed = true
		t.mu.Unlock()
	}
}

// HostReady removes, once the host hostID is ready, the part files that
// cancelled transfers of it left on the host.
func (m *Manager) HostReady(hostID string) {
	m.cleanupCancelled(hostID)
}

// cleanupCancelled starts the removal of the part files that cancelled
// transfers of hostID (of every host when "") left on the host.
func (m *Manager) cleanupCancelled(hostID string) {
	ids, err := m.opt.Store.TransfersWithParts(hostID, StateCancelled)
	if err != nil {
		m.lg.Printf("transfers: find part files to remove: %v", err)
		return
	}
	for _, id := range ids {
		t, err := m.acquire(id)
		if err != nil {
			continue
		}
		t.mu.Lock()
		if t.row.State == StateCancelled && pushing(t.row.Direction) {
			t.launchLocked()
		}
		t.mu.Unlock()
		m.release(t)
	}
}

// anyPart reports whether a file of files may have a part file.
func anyPart(files []store.TransferFile) bool {
	for _, f := range files {
		if f.Part && !f.Dir {
			return true
		}
	}
	return false
}

// acquire returns the transfer transferID, loading it from the store when
// it is not live, and keeps it live until release.
func (m *Manager) acquire(transferID string) (*transfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := m.live[transferID]; t != nil {
		t.refs.Add(1)
		return t, nil
	}
	row, files, err := m.opt.Store.GetTransfer(transferID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound(transferID)
	}
	if err != nil {
		return nil, apierr.Internal(fmt.Sprintf("전송 기록을 읽지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	t := newTransfer(m, row, files)
	t.refs.Add(1)
	m.live[transferID] = t
	return t, nil
}

// release gives back a transfer acquire returned; a transfer nobody holds
// and that does not run stops being live once its changes are stored.
func (m *Manager) release(t *transfer) {
	if t.refs.Add(-1) > 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.refs.Load() > 0 || m.live[t.row.TransferID] != t {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.run != nil {
		return
	}
	if t.timer != nil {
		t.flushLocked()
	}
	delete(m.live, t.row.TransferID)
}

func notFound(transferID string) *apierr.Error {
	return apierr.NotFound(fmt.Sprintf("전송 %s가 없습니다. transfer.list로 transfer_id를 확인하세요", transferID))
}

// Start checks a transfer and starts it: the source has to exist, and the
// folder of the destination; the host is asked through the agent, which
// has to be connected. A problem of the parameters is an error; a transfer
// that cannot start is a Started with Err, no transfer and a timeline
// entry.
func (m *Manager) Start(ctx context.Context, p StartParams, c model.Caller) (Started, error) {
	if err := p.check(); err != nil {
		return Started{}, err
	}
	if p.OnConflict == "" {
		p.OnConflict = "fail"
	}
	row, err := m.opt.Store.GetHost(p.HostID)
	if errors.Is(err, store.ErrNotFound) {
		return Started{}, apierr.NotFound(fmt.Sprintf("'%s' PC가 등록돼 있지 않습니다. host.list로 이름을 확인하세요", p.HostID))
	}
	if err != nil {
		return Started{}, apierr.Internal(fmt.Sprintf("PC 목록을 읽지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	h := m.opt.Conns.Get(row.HostID)
	if h == nil {
		return Started{Err: apierr.New(wire.CodeOffline, fmt.Sprintf("%s PC에 연결돼 있지 않습니다. deskhand host connect %s로 연결한 뒤 다시 시도하세요", row.Name, row.Name),
			true, apierr.NextHostConnect)}, nil
	}
	sep := separator(h, row)
	folder, info, failure := checkStart(ctx, h, p, sep)
	if failure != nil {
		_, _ = m.opt.Bus.Record(c.Entry(row.HostID, model.TimelineFile, fmt.Sprintf("%s 시작 못 함: %s (%s)", verb(p.Direction),
			route(p.Direction, p.Local, row.HostID, p.Remote), failure.Message), model.TimelineFailed, ""))
		return Started{Info: info, Err: failure}, nil
	}
	now := time.Now()
	tr := store.Transfer{TransferID: ids.New(ids.Transfer), HostID: row.HostID, Direction: p.Direction, Local: filepath.Clean(p.Local),
		Remote: p.Remote, OnConflict: p.OnConflict, Sep: sep, State: StateQueued, Folder: folder,
		Actor: c.Actor, ClientID: c.ClientID, ClientName: c.ClientName, CreatedAt: now, UpdatedAt: now}
	if err := m.opt.Store.CreateTransfer(tr); err != nil {
		return Started{}, apierr.Internal(fmt.Sprintf("전송을 저장하지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	t := newTransfer(m, tr, nil)
	t.refs.Add(1)
	m.mu.Lock()
	m.live[tr.TransferID] = t
	m.mu.Unlock()
	defer m.release(t)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordLocked(c, t.verbLocked()+" 시작: "+t.routeLocked(), model.TimelineOK)
	t.publishLocked()
	t.launchLocked()
	return Started{Transfer: t.snapshotLocked(), Info: info}, nil
}

// Get returns the transfer transferID.
func (m *Manager) Get(transferID string) (model.Transfer, error) {
	m.mu.Lock()
	t := m.live[transferID]
	m.mu.Unlock()
	if t != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.snapshotLocked(), nil
	}
	row, files, err := m.opt.Store.GetTransfer(transferID)
	if errors.Is(err, store.ErrNotFound) {
		return model.Transfer{}, notFound(transferID)
	}
	if err != nil {
		return model.Transfer{}, apierr.Internal(fmt.Sprintf("전송 기록을 읽지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	return snapshot(row, files, 0), nil
}

// List returns the transfers of hostID, of every host when "", newest
// first.
func (m *Manager) List(hostID string) ([]model.Transfer, error) {
	rows, err := m.opt.Store.ListTransfers(store.TransferFilter{HostID: hostID})
	if err != nil {
		return nil, apierr.Internal(fmt.Sprintf("전송 기록을 읽지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	out := make([]model.Transfer, 0, len(rows))
	for _, row := range rows {
		tr, err := m.Get(row.TransferID)
		if err != nil {
			continue
		}
		out = append(out, tr)
	}
	return out, nil
}

// Pause stops a queued or running transfer and keeps it paused, also across
// daemon restarts, until Resume. A paused transfer is returned as it is; a
// finished one, one that finished while its run stopped and a move that
// removes its source are errors.
func (m *Manager) Pause(transferID string, c model.Caller) (model.Transfer, error) {
	t, err := m.acquire(transferID)
	if err != nil {
		return model.Transfer{}, err
	}
	defer m.release(t)
	t.opMu.Lock()
	defer t.opMu.Unlock()
	t.mu.Lock()
	switch {
	case t.removing:
		defer t.mu.Unlock()
		return model.Transfer{}, removingRefusal(transferID, "일시정지")
	case t.row.State == StatePaused:
		defer t.mu.Unlock()
		return t.snapshotLocked(), nil
	case finishedState(t.row.State):
		defer t.mu.Unlock()
		return model.Transfer{}, pauseRefusal(transferID, t.row.State)
	}
	t.mu.Unlock()
	t.stopRun(errPaused)
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.removing:
		t.launchLocked()
		return model.Transfer{}, removingRefusal(transferID, "일시정지")
	case finishedState(t.row.State):
		return model.Transfer{}, pauseRefusal(transferID, t.row.State)
	}
	for i := range t.files {
		if f := &t.files[i]; f.State == StateRunning || f.State == StateVerifying {
			f.State = StatePaused
			t.dirty[i] = true
		}
	}
	t.row.State = StatePaused
	t.recordLocked(c, t.verbLocked()+" 일시정지: "+t.routeLocked(), model.TimelineOK)
	t.publishLocked()
	return t.snapshotLocked(), nil
}

// Resume starts a paused, failed or cancelled transfer again; its files
// that are done stay done. A failed file takes the fingerprint of its
// source again, and a transfer with a failed folder lists its files again.
// A queued or running transfer is returned as it is; a done one is an
// error.
func (m *Manager) Resume(transferID string, c model.Caller) (model.Transfer, error) {
	t, err := m.acquire(transferID)
	if err != nil {
		return model.Transfer{}, err
	}
	defer m.release(t)
	t.opMu.Lock()
	defer t.opMu.Unlock()
	t.mu.Lock()
	switch t.row.State {
	case StateQueued, StateRunning, StateVerifying:
		defer t.mu.Unlock()
		return t.snapshotLocked(), nil
	case StateDone:
		defer t.mu.Unlock()
		return model.Transfer{}, apierr.Invalid(fmt.Sprintf("전송 %s는 이미 끝났습니다. 다시 보내려면 transfer.start로 새 전송을 시작하세요", transferID))
	}
	t.mu.Unlock()
	t.stopRun(errPaused)
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.files {
		f := &t.files[i]
		if f.Dir && f.State == StateFailed {
			t.row.Planned = false
		}
		switch f.State {
		case StateFailed:
			f.Refresh = !f.Dir
			fallthrough
		case StateCancelled, StatePaused, StateRunning, StateVerifying:
			f.State, f.Error, f.Outcome = StateQueued, nil, ""
			t.dirty[i] = true
		}
	}
	t.strikes = map[int]int{}
	t.restarts = map[int]int{}
	t.row.State, t.row.Outcome, t.row.Error, t.row.FinishedAt = StateQueued, "", nil, nil
	t.recordLocked(c, t.verbLocked()+" 재개: "+t.routeLocked(), model.TimelineOK)
	t.publishLocked()
	t.launchLocked()
	return t.snapshotLocked(), nil
}

// Cancel stops a transfer for good: its finished files stay, its other
// files are cancelled and their part files removed (on the host once it is
// ready). A cancelled transfer is returned as it is; a done one, one that
// ended done while its run stopped and a move that removes its source are
// errors.
func (m *Manager) Cancel(transferID string, c model.Caller) (model.Transfer, error) {
	t, err := m.acquire(transferID)
	if err != nil {
		return model.Transfer{}, err
	}
	defer m.release(t)
	t.opMu.Lock()
	defer t.opMu.Unlock()
	t.mu.Lock()
	switch {
	case t.removing:
		defer t.mu.Unlock()
		return model.Transfer{}, removingRefusal(transferID, "취소")
	case t.row.State == StateCancelled:
		defer t.mu.Unlock()
		return t.snapshotLocked(), nil
	case t.row.State == StateDone:
		defer t.mu.Unlock()
		return model.Transfer{}, cancelRefusal(transferID)
	}
	t.mu.Unlock()
	t.stopRun(errCancelled)
	t.mu.Lock()
	switch {
	case t.removing:
		t.launchLocked()
		defer t.mu.Unlock()
		return model.Transfer{}, removingRefusal(transferID, "취소")
	case t.row.State == StateDone:
		defer t.mu.Unlock()
		return model.Transfer{}, cancelRefusal(transferID)
	}
	t.mu.Unlock()
	t.cancelFiles()
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.row.State, t.row.Outcome, t.row.Error, t.row.FinishedAt = StateCancelled, "", nil, &now
	t.recordLocked(c, fmt.Sprintf("%s 취소: %s (끝난 파일 %d개는 남김)", t.verbLocked(), t.routeLocked(), t.doneFilesLocked()), model.TimelineOK)
	t.publishLocked()
	if anyPart(t.files) {
		t.launchLocked()
	}
	return t.snapshotLocked(), nil
}

// finishedState reports whether state is that of a transfer that ended.
func finishedState(state string) bool {
	return state == StateDone || state == StateFailed || state == StateCancelled
}

func pauseRefusal(transferID, state string) *apierr.Error {
	return apierr.Invalid(fmt.Sprintf("전송 %s는 이미 %s 상태로 끝나 일시정지할 수 없습니다. 다시 보내려면 transfer.resume이나 transfer.start를 쓰세요", transferID, state))
}

func cancelRefusal(transferID string) *apierr.Error {
	return apierr.Invalid(fmt.Sprintf("전송 %s는 이미 끝나 취소할 것이 없습니다. 받은 파일을 지우려면 files.trash를 쓰세요", transferID))
}

// removingRefusal is the error of what (일시정지, 취소) asked of a move that
// removes its source.
func removingRefusal(transferID, what string) *apierr.Error {
	return apierr.Invalid(fmt.Sprintf("전송 %s는 받은 쪽 검증을 마치고 원본 정리 중이라 %s할 수 없습니다. 정리가 끝날 때까지 기다리세요", transferID, what))
}

// Causes that end a run.
var (
	errPaused    = errors.New("the transfer was paused")
	errCancelled = errors.New("the transfer was cancelled")
	errClosing   = errors.New("the daemon stops")
	// errRetry ends an attempt whose host stopped answering.
	errRetry = errors.New("the host stopped answering")
)

// runner is one run of a transfer: its attempts until it finishes, is
// stopped or its host goes away.
type runner struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}
}
