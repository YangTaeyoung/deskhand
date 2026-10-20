package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// receipt is the record of a committed transfer: what xfer.commit answered
// for the part file whose partKey is Key.
type receipt struct {
	Key        string    `json:"key"`
	TransferID string    `json:"transfer_id"`
	Request    string    `json:"request"`
	Path       string    `json:"path"`
	Outcome    string    `json:"outcome"`
	SHA256     string    `json:"sha256"`
	Committed  time.Time `json:"committed"`
}

// receiptStore keeps the receipts of the commits of the last journalKeep:
// each as a file in dir written like a journal record, or in memory when
// there is no dir or the file could not be written.
type receiptStore struct {
	dir string
	lg  *log.Logger
	now func() time.Time

	mu     sync.Mutex
	mem    map[string]receipt
	pruned time.Time
}

// openReceipts returns the receipts kept in dir, removing those older than
// journalKeep; an empty dir keeps them in memory.
func openReceipts(dir string, lg *log.Logger) *receiptStore {
	r := &receiptStore{dir: dir, lg: lg, now: time.Now, mem: map[string]receipt{}}
	if dir == "" {
		return r
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		lg.Printf("receipts: create %s: %v; receipts are kept in memory", dir, err)
		r.dir = ""
		return r
	}
	r.mu.Lock()
	r.pruneLocked()
	r.mu.Unlock()
	return r
}

// file returns the file of the receipt of key.
func (r *receiptStore) file(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(r.dir, hex.EncodeToString(sum[:16])+".json")
}

// get returns the receipt of key when one of the last journalKeep exists.
func (r *receiptStore) get(key string) (receipt, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	fresh := func(rc receipt) bool { return rc.Key == key && r.now().Sub(rc.Committed) <= journalKeep }
	if rc, ok := r.mem[key]; ok && fresh(rc) {
		return rc, true
	}
	if r.dir == "" {
		return receipt{}, false
	}
	b, err := os.ReadFile(r.file(key))
	if err != nil {
		if !os.IsNotExist(err) {
			r.lg.Printf("receipts: %v", err)
		}
		return receipt{}, false
	}
	var rc receipt
	if err := json.Unmarshal(b, &rc); err != nil || !fresh(rc) {
		return receipt{}, false
	}
	return rc, true
}

// put stores rc, in memory until the agent stops when its file cannot be
// written.
func (r *receiptStore) put(rc receipt) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dir != "" {
		b, err := json.Marshal(rc)
		if err == nil {
			err = writeFileAtomic(r.file(rc.Key), b)
		}
		if err == nil {
			delete(r.mem, rc.Key)
			return
		}
		r.lg.Printf("receipts: %v; the receipt of transfer %s is kept in memory until the agent stops", err, rc.TransferID)
	}
	r.mem[rc.Key] = rc
}

// pruneLocked removes, once per journalPruneEvery, the receipts older than
// journalKeep, those in dir by the time their file was written, and the
// temporary files a write left. r.mu is held.
func (r *receiptStore) pruneLocked() {
	now := r.now()
	if !r.pruned.IsZero() && now.Sub(r.pruned) < journalPruneEvery {
		return
	}
	r.pruned = now
	for k, rc := range r.mem {
		if now.Sub(rc.Committed) > journalKeep {
			delete(r.mem, k)
		}
	}
	if r.dir == "" {
		return
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		r.lg.Printf("receipts: read %s: %v", r.dir, err)
		return
	}
	for _, e := range entries {
		name := e.Name()
		info, err := e.Info()
		if strings.HasSuffix(name, ".tmp") || err == nil && strings.HasSuffix(name, ".json") && now.Sub(info.ModTime()) > journalKeep {
			_ = os.Remove(filepath.Join(r.dir, name))
		}
	}
}
