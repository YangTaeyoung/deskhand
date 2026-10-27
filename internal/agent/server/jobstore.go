package server

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// jobFlushInterval is the time between two writes of a running job's output.
var jobFlushInterval = 2 * time.Second

// jobMeta is the file meta.json of a job folder. Code is set once the job
// exited; OutTotal and ErrTotal count every byte the job wrote to stdout and
// stderr, whose last jobBufferBytes the files stdout and stderr hold.
type jobMeta struct {
	Job      string    `json:"job"`
	PID      int       `json:"pid"`
	Cmd      string    `json:"cmd"`
	Started  time.Time `json:"started"`
	State    string    `json:"state"`
	Code     *int      `json:"code,omitempty"`
	OutTotal int       `json:"out_total"`
	ErrTotal int       `json:"err_total"`
}

// jobStore keeps each exec.start job in a folder of dir named by its id. A
// nil jobStore keeps nothing.
type jobStore struct {
	dir string
	lg  *log.Logger
}

func (st *jobStore) folder(id string) string { return filepath.Join(st.dir, id) }

// save writes the job's output and then its meta.json, with the exit code
// when exitCode is not nil. Once the files of an exit are written, save
// writes nothing more.
func (st *jobStore) save(j *job, exitCode *int) error {
	if st == nil {
		return nil
	}
	j.saveMu.Lock()
	defer j.saveMu.Unlock()
	if j.exitSaved {
		return nil
	}
	dir := st.folder(j.id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	out, outTotal := j.out.tail()
	errOut, errTotal := j.err.tail()
	if err := writeFileAtomic(filepath.Join(dir, "stdout"), out); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "stderr"), errOut); err != nil {
		return err
	}
	m := jobMeta{Job: j.id, PID: j.processID(), Cmd: j.cmdline, Started: j.started, State: "running", OutTotal: outTotal, ErrTotal: errTotal}
	if exitCode != nil {
		m.State, m.Code = "exited", exitCode
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "meta.json"), b); err != nil {
		return err
	}
	j.exitSaved = exitCode != nil
	return nil
}

// remove deletes the folder of job id.
func (st *jobStore) remove(id string) {
	if st == nil {
		return
	}
	if err := os.RemoveAll(st.folder(id)); err != nil {
		st.lg.Printf("jobs: remove %s: %v", st.folder(id), err)
	}
}

// jobNumber returns n of a job id "j<n>".
func jobNumber(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, "j")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n > 0 && strconv.Itoa(n) == rest
}

// load returns the stored jobs ordered by id number. A job whose meta.json
// records no exit is lost.
func (st *jobStore) load() []*job {
	if st == nil {
		return nil
	}
	entries, err := os.ReadDir(st.dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			st.lg.Printf("jobs: read %s: %v", st.dir, err)
		}
		return nil
	}
	var jobs []*job
	for _, e := range entries {
		if _, ok := jobNumber(e.Name()); !ok || !e.IsDir() {
			continue
		}
		j, err := st.loadJob(e.Name())
		if err != nil {
			st.lg.Printf("jobs: the job %s cannot be read (%v); its outcome is unknown", e.Name(), err)
			j = &job{id: e.Name(), out: newRing(jobBufferBytes), err: newRing(jobBufferBytes),
				done: make(chan struct{}), stdinOff: true, running: true, exited: true, lost: true, code: -1}
			close(j.done)
		}
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(a, b int) bool {
		na, _ := jobNumber(jobs[a].id)
		nb, _ := jobNumber(jobs[b].id)
		return na < nb
	})
	return jobs
}

func (st *jobStore) loadJob(id string) (*job, error) {
	dir := st.folder(id)
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var m jobMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	out, _ := os.ReadFile(filepath.Join(dir, "stdout"))
	errOut, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	j := &job{id: id, pid: m.PID, cmdline: m.Cmd, started: m.Started, out: restoredRing(jobBufferBytes, out, m.OutTotal),
		err: restoredRing(jobBufferBytes, errOut, m.ErrTotal), done: make(chan struct{}), stdinOff: true, running: true, exited: true}
	close(j.done)
	if m.State == "exited" && m.Code != nil {
		j.code = *m.Code
	} else {
		j.code, j.lost = -1, true
	}
	return j, nil
}

// restore puts stored jobs into the table, keeping the newest maxJobs and
// removing the folders of the others, and continues the ids after the
// highest one.
func (t *jobTable) restore(jobs []*job) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if extra := len(jobs) - maxJobs; extra > 0 {
		for _, j := range jobs[:extra] {
			t.store.remove(j.id)
		}
		jobs = jobs[extra:]
	}
	for _, j := range jobs {
		t.jobs[j.id] = j
		t.order = append(t.order, j.id)
		if n, _ := jobNumber(j.id); n > t.next {
			t.next = n
		}
	}
}

// saveJob writes j through the job store, with exitCode as in
// jobStore.save, and logs a failure.
func (s *Server) saveJob(j *job, exitCode *int) {
	if err := s.jobs.store.save(j, exitCode); err != nil {
		s.opt.Logger.Printf("jobs: save %s: %v", j.id, err)
	}
}

// flushJob writes j's output every jobFlushInterval while it grows, until
// the job exits.
func (s *Server) flushJob(j *job) {
	if s.jobs.store == nil {
		return
	}
	tick := time.NewTicker(jobFlushInterval)
	defer tick.Stop()
	out, errOut := j.out.written(), j.err.written()
	for {
		select {
		case <-j.done:
			return
		case <-tick.C:
			if o, e := j.out.written(), j.err.written(); o != out || e != errOut {
				out, errOut = o, e
				s.saveJob(j, nil)
			}
		}
	}
}
