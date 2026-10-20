package xfer

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// statWait bounds a fs.stat or fs.list of a check or a plan.
	statWait = time.Minute
	// planHashers is how many files of a push are hashed at once.
	planHashers = 4
)

// check returns the problem of the parameters of a start, or nil.
func (p StartParams) check() error {
	switch {
	case p.HostID == "":
		return apierr.Invalid("host_id가 없습니다. host.list로 PC의 host_id나 이름을 확인해 주세요")
	case p.Direction != Push && p.Direction != Pull && p.Direction != MovePush && p.Direction != MovePull:
		return apierr.Invalid(fmt.Sprintf("direction %q는 쓸 수 없습니다. push, pull, move_push, move_pull 중 하나를 주세요", p.Direction))
	case !filepath.IsAbs(p.Local):
		return apierr.Invalid("local은 이 Mac의 절대 경로여야 합니다. /로 시작하는 전체 경로를 주세요")
	case filepath.Clean(p.Local) == "/":
		return apierr.Invalid("local이 볼륨의 루트입니다. 옮길 파일이나 폴더의 전체 경로를 주세요")
	case strings.TrimSpace(p.Remote) == "":
		return apierr.Invalid("remote가 없습니다. PC 쪽 항목의 전체 경로를 주세요")
	case dotElements(p.Remote):
		return apierr.Invalid(fmt.Sprintf("remote %s에 . 이나 .. 원소가 있습니다. 그 원소 없이 항목의 전체 경로를 주세요", p.Remote))
	case p.OnConflict != "" && !wire.IsConflictPolicy(p.OnConflict):
		return apierr.Invalid("on_conflict는 fail, replace, skip, rename 중 하나여야 합니다. 그중 하나를 주세요")
	}
	return nil
}

// dotElements reports whether the host path p has a "." or ".." element
// between its separators ('/' and '\').
func dotElements(p string) bool {
	for _, e := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if e == "." || e == ".." {
			return true
		}
	}
	return false
}

// separator returns the path separator of the host h, whose stored row is
// row.
func separator(h *conn.Host, row store.Host) string {
	osName := h.Hello().OS
	if osName == "" {
		osName = row.OS
	}
	if osName == "windows" {
		return `\`
	}
	return "/"
}

// lastSep returns the index of the last separator in the host path p:
// on Windows hosts '\' or '/'.
func lastSep(p, sep string) int {
	if sep == `\` {
		return strings.LastIndexAny(p, `\/`)
	}
	return strings.LastIndex(p, sep)
}

// trimSeps returns p without separators at its end, keeping a root.
func trimSeps(p, sep string) string {
	cut := sep
	if sep == `\` {
		cut = `\/`
	}
	if t := strings.TrimRight(p, cut); t != "" && !strings.HasSuffix(t, ":") {
		return t
	}
	return p
}

// remoteJoin joins the folder dir of the host with the slash-separated
// relative path rel.
func remoteJoin(dir, rel, sep string) string {
	if rel == "" {
		return dir
	}
	rel = strings.ReplaceAll(rel, "/", sep)
	if strings.HasSuffix(dir, sep) || sep == `\` && strings.HasSuffix(dir, "/") {
		return dir + rel
	}
	return dir + sep + rel
}

// remoteDir returns the folder of the host path p, "." when p names no
// folder.
func remoteDir(p, sep string) string {
	p = trimSeps(p, sep)
	i := lastSep(p, sep)
	switch {
	case i < 0:
		return "."
	case i == 0:
		return p[:1]
	case sep == `\` && p[i-1] == ':':
		return p[:i+1]
	}
	return p[:i]
}

// remoteBase returns the name of the host path p.
func remoteBase(p, sep string) string {
	p = trimSeps(p, sep)
	return p[lastSep(p, sep)+1:]
}

// remoteNumbered returns the host path p with the name wire.NumberedName
// gives it.
func remoteNumbered(p, sep string, k int) string {
	return remoteJoin(remoteDir(p, sep), wire.NumberedName(remoteBase(p, sep), k, false), sep)
}

// busyWait bounds how long callBusy tries again while a part file is busy.
const busyWait = 10 * time.Second

// callBusy is call that tries again, for up to busyWait, while the agent
// answers IN_PROGRESS: another operation holds the part file.
func callBusy(ctx context.Context, h *conn.Host, op string, params proto.Message, wait time.Duration) (proto.Message, conn.CallInfo, error) {
	deadline := time.Now().Add(busyWait)
	delay := 50 * time.Millisecond
	for {
		res, info, err := call(ctx, h, op, params, wait)
		if apierr.CodeOf(err) != wire.CodeInProgress || time.Now().After(deadline) || sleep(ctx, delay) != nil {
			return res, info, err
		}
		delay = min(2*delay, time.Second)
	}
}

// call sends op to the host h within wait and returns its answer.
func call(ctx context.Context, h *conn.Host, op string, params proto.Message, wait time.Duration) (proto.Message, conn.CallInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	info := conn.CallInfo{}
	res, err := h.Call(cctx, op, params, conn.CallOpts{Info: &info})
	return res, info, err
}

// checkStart checks the source and the folder of the destination of the
// transfer p and returns whether its item is a folder. failure says why it
// cannot start; info is what happened to the request to the host.
func checkStart(ctx context.Context, h *conn.Host, p StartParams, sep string) (folder bool, info conn.CallInfo, failure *apierr.Error) {
	local := filepath.Clean(p.Local)
	if pushing(p.Direction) {
		fi, err := os.Lstat(local)
		if err != nil {
			return false, info, localError(err, fmt.Sprintf("이 Mac의 %s를 읽지 못했습니다", local), "경로를 확인한 뒤 다시 시도하세요")
		}
		if !fi.IsDir() && !fi.Mode().IsRegular() {
			return false, info, apierr.Invalid(fmt.Sprintf("이 Mac의 %s는 링크나 특수 파일이라 보내지 않습니다. 링크가 가리키는 항목의 경로를 주세요", local))
		}
		parent := remoteDir(p.Remote, sep)
		res, cinfo, err := call(ctx, h, "fs.stat", proto.Message{"path": parent}, statWait)
		if err != nil {
			return false, cinfo, apierr.As(err)
		}
		if !res.Bool("exists") || !res.Bool("dir") {
			return false, cinfo, apierr.NotFound(fmt.Sprintf("PC에 폴더 %s가 없습니다. files.mkdir로 만든 뒤 다시 시도하세요", parent))
		}
		return fi.IsDir(), cinfo, nil
	}
	res, cinfo, err := call(ctx, h, "fs.stat", proto.Message{"path": p.Remote}, statWait)
	if err != nil {
		return false, cinfo, apierr.As(err)
	}
	switch {
	case !res.Bool("exists"):
		return false, cinfo, apierr.NotFound(fmt.Sprintf("PC에 %s가 없습니다. 경로를 확인한 뒤 다시 시도하세요", p.Remote))
	case res.Bool("link"):
		return false, cinfo, apierr.Invalid(fmt.Sprintf("PC의 %s는 링크라 받지 않습니다. 링크가 가리키는 항목의 경로를 주세요", p.Remote))
	}
	dir := filepath.Dir(local)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return false, cinfo, apierr.NotFound(fmt.Sprintf("이 Mac에 폴더 %s가 없습니다. 폴더를 만든 뒤 다시 시도하세요", dir))
	}
	return res.Bool("dir"), cinfo, nil
}

// fileTime formats a modification time as transfers keep it.
func fileTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// manifest collects the files of a plan in order.
type manifest struct {
	id    string
	files []store.TransferFile
	index map[string]int
}

func (m *manifest) add(f store.TransferFile) {
	f.TransferID, f.Seq = m.id, len(m.files)
	if f.State == "" {
		f.State = StateQueued
	}
	if m.index == nil {
		m.index = map[string]int{}
	}
	m.index[f.Rel] = f.Seq
	m.files = append(m.files, f)
}

// failed adds the entry rel, a folder when dir is set, as failed with e.
func (m *manifest) failed(rel string, dir bool, e *apierr.Error) {
	if i, ok := m.index[rel]; ok {
		m.files[i].State, m.files[i].Error = StateFailed, encodeError(e)
		return
	}
	m.add(store.TransferFile{Rel: rel, Dir: dir, State: StateFailed, Error: encodeError(e)})
}

func linkRefused(path string) *apierr.Error {
	return apierr.Invalid(fmt.Sprintf("%s는 링크라 옮기지 않았습니다. 링크가 가리키는 항목을 따로 옮기세요", path))
}

func specialRefused(path string) *apierr.Error {
	return apierr.Invalid(fmt.Sprintf("%s는 일반 파일이 아니라 옮기지 않았습니다. 필요하면 내용을 파일로 저장해 옮기세요", path))
}

// plan lists the files of the transfer and stores them as its manifest.
// A file of the manifest before stays done when its fingerprint is the
// same, keeps its commit whose outcome is not known with its fingerprint
// (taken again after the commit settles when it changed), and keeps its
// part file.
func (t *transfer) plan(ctx context.Context, h *conn.Host) error {
	var files []store.TransferFile
	var err error
	if pushing(t.row.Direction) {
		files, err = t.planPush(ctx)
	} else {
		files, err = t.planPull(ctx, h)
	}
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	before := map[string]store.TransferFile{}
	for _, f := range t.files {
		if !f.Dir {
			before[f.Rel] = f
		}
	}
	for i := range files {
		f := &files[i]
		o, ok := before[f.Rel]
		if !ok || f.Dir || f.State != StateQueued {
			continue
		}
		same := o.Size == f.Size && sameSecond(o.MTime, f.MTime) && (f.SHA256 == "" || o.SHA256 == f.SHA256)
		switch {
		case o.State == StateDone && same:
			o.Seq = f.Seq
			*f = o
		case o.CommitRID != "":
			o.Seq, o.State, o.Error, o.Refresh = f.Seq, StateQueued, nil, !same
			*f = o
		case o.Part:
			f.Part, f.BytesDone = true, o.BytesDone
		}
	}
	t.row.Planned = true
	if err := t.m.save(t.row, files, true); err != nil {
		t.row.Planned = false
		return apierr.Internal(fmt.Sprintf("전송할 파일 목록을 저장하지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	t.files, t.dirty = files, map[int]bool{}
	t.changedLocked()
	return nil
}

// planPush lists the item of a push on this Mac: its files with their
// size, modification time and SHA-256 and its folders. A link, a special
// file, an entry that cannot be read and, in a folder, a name the host
// cannot hold (manifest.screen; for a Windows host also the later of two
// names that differ only in letter case) are failed entries.
func (t *transfer) planPush(ctx context.Context) ([]store.TransferFile, error) {
	root := t.row.Local
	m := &manifest{id: t.row.TransferID}
	if !t.row.Folder {
		fi, err := os.Lstat(root)
		if err != nil {
			return nil, localError(err, fmt.Sprintf("이 Mac의 %s를 읽지 못했습니다", root), "경로를 확인한 뒤 transfer.resume으로 다시 시작하세요")
		}
		if !fi.Mode().IsRegular() {
			return nil, sourceChanged(root)
		}
		m.add(store.TransferFile{Rel: filepath.Base(root)})
	} else {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if p == root {
				if err == nil && !d.IsDir() {
					return sourceChanged(root)
				}
				return err
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			switch {
			case err != nil:
				m.failed(rel, d != nil && d.IsDir(), localError(err, fmt.Sprintf("이 Mac의 %s를 읽지 못했습니다", p), "권한을 확인한 뒤 transfer.resume으로 다시 시도하세요"))
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
			case d.IsDir():
				m.add(store.TransferFile{Rel: rel, Dir: true})
			case d.Type().IsRegular():
				m.add(store.TransferFile{Rel: rel})
			case d.Type()&fs.ModeSymlink != 0:
				m.failed(rel, false, linkRefused(p))
			default:
				m.failed(rel, false, specialRefused(p))
			}
			return nil
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if e, ok := err.(*apierr.Error); ok {
				return nil, e
			}
			return nil, localError(err, fmt.Sprintf("이 Mac의 폴더 %s를 읽지 못했습니다", root), "권한을 확인한 뒤 transfer.resume으로 다시 시작하세요")
		}
	}
	if t.row.Folder {
		windows := t.row.Sep == `\`
		m.screen(func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }, windows, windows)
	}
	if err := t.fingerprintAll(ctx, m.files); err != nil {
		return nil, err
	}
	return m.files, nil
}

// fingerprintAll takes the fingerprint of every queued file of a push:
// size, modification time and SHA-256. A file that cannot be read fails.
func (t *transfer) fingerprintAll(ctx context.Context, files []store.TransferFile) error {
	work := make(chan int)
	var wg sync.WaitGroup
	for range planHashers {
		wg.Go(func() {
			for i := range work {
				f := &files[i]
				size, mtime, sum, e := fingerprint(ctx, t.localPath(*f))
				if e != nil {
					f.State, f.Error = StateFailed, encodeError(e)
					continue
				}
				f.Size, f.MTime, f.SHA256 = size, mtime, sum
			}
		})
	}
	for i, f := range files {
		if f.Dir || f.State != StateQueued {
			continue
		}
		select {
		case work <- i:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
	return ctx.Err()
}

// planPull lists the item of a pull on the host: its files with their size
// and modification time, and its folders. A link, a name this Mac cannot
// take (manifest.screen) and, on a volume that ignores letter case, the
// later of two names that differ only in letter case are failed entries.
func (t *transfer) planPull(ctx context.Context, h *conn.Host) ([]store.TransferFile, error) {
	m := &manifest{id: t.row.TransferID}
	sep := t.row.Sep
	if !t.row.Folder {
		res, _, err := call(ctx, h, "fs.stat", proto.Message{"path": t.row.Remote}, statWait)
		if err != nil {
			return nil, err
		}
		if !res.Bool("exists") || res.Bool("dir") || res.Bool("link") {
			return nil, sourceChanged(t.row.Remote)
		}
		m.add(store.TransferFile{Rel: remoteBase(t.row.Remote, sep), Size: int64(res.Int("size")), MTime: res.String("mtime")})
		return m.files, nil
	}
	entries, err := listRemote(ctx, h, t.row.Remote, sep)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		full := remoteJoin(t.row.Remote, e.rel, sep)
		switch {
		case e.link:
			m.failed(e.rel, false, linkRefused(full))
		case e.dir:
			m.add(store.TransferFile{Rel: e.rel, Dir: true})
		default:
			m.add(store.TransferFile{Rel: e.rel, Size: e.size, MTime: e.mtime})
		}
	}
	m.screen(func(rel string) string { return remoteJoin(t.row.Remote, rel, sep) }, false, caseInsensitive(filepath.Dir(t.row.Local)))
	return m.files, nil
}

// remoteEntry is an entry of a folder on the host.
type remoteEntry struct {
	rel       string
	dir, link bool
	size      int64
	mtime     string
}

// listRemote returns every entry below the folder dir of the host: one
// recursive fs.list, or one fs.list per folder when that one stops early.
// Entries the agent could not read are an error.
func listRemote(ctx context.Context, h *conn.Host, dir, sep string) ([]remoteEntry, error) {
	list := func(path, prefix string, recursive bool) ([]remoteEntry, bool, error) {
		res, _, err := call(ctx, h, "fs.list", proto.Message{"path": path, "recursive": recursive}, statWait)
		if err != nil {
			return nil, false, err
		}
		if n := res.Int("skipped"); n > 0 {
			return nil, false, apierr.New(wire.CodePermissionDenied, fmt.Sprintf("PC의 %s 안에 읽지 못한 항목이 %d개 있어 폴더를 받지 않았습니다. 권한을 확인한 뒤 transfer.resume으로 다시 시도하세요", path, n),
				false, apierr.NextCheckState)
		}
		raws, _ := res["entries"].([]any)
		out := make([]remoteEntry, 0, len(raws))
		for _, r := range raws {
			m, ok := r.(map[string]any)
			if !ok {
				continue
			}
			e := proto.Message(m)
			rel := e.String("name")
			if prefix != "" {
				rel = prefix + "/" + rel
			}
			out = append(out, remoteEntry{rel: rel, dir: e.Bool("dir"), link: e.Bool("link"), size: int64(e.Int("size")), mtime: e.String("mtime")})
		}
		return out, res.Bool("truncated"), nil
	}
	all, truncated, err := list(dir, "", true)
	if err != nil || !truncated {
		return all, err
	}
	all = nil
	queue := []string{""}
	for len(queue) > 0 {
		rel := queue[0]
		queue = queue[1:]
		path := remoteJoin(dir, rel, sep)
		entries, truncated, err := list(path, rel, false)
		if err != nil {
			return nil, err
		}
		if truncated {
			return nil, apierr.Invalid(fmt.Sprintf("PC의 폴더 %s에 항목이 너무 많아 한 번에 받을 수 없습니다. 하위 폴더로 나눠 받으세요", path))
		}
		for _, e := range entries {
			if e.dir && !e.link {
				queue = append(queue, e.rel)
			}
		}
		all = append(all, entries...)
	}
	return all, nil
}

// localPath returns where the file f is on this Mac.
func (t *transfer) localPath(f store.TransferFile) string {
	if !t.row.Folder {
		return t.row.Local
	}
	return filepath.Join(t.row.Local, filepath.FromSlash(f.Rel))
}

// remotePath returns where the file f is on the host.
func (t *transfer) remotePath(f store.TransferFile) string {
	if !t.row.Folder {
		return t.row.Remote
	}
	return remoteJoin(t.row.Remote, f.Rel, t.row.Sep)
}
