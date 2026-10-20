package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	fsChunkMax    = 262144
	fsTailWindow  = 262144
	fsTailDefault = 200
	fsListMax     = 5000
	// fsCopyBuffer is the buffer fs.copy and fs.hash read files through.
	fsCopyBuffer = 1 << 20
	// fsRenameMax bounds the numbered names on_conflict rename tries.
	fsRenameMax = 10000
)

// resolvePath maps "~", "~/x", relative and absolute paths to an absolute
// path; relative forms resolve against home.
func resolvePath(home, p string) (string, error) {
	if p == "" {
		return "", errors.New("missing path; pass a file or directory path")
	}
	switch {
	case p == "~":
		return filepath.Clean(home), nil
	case strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`):
		return filepath.Join(home, filepath.FromSlash(strings.ReplaceAll(p[2:], `\`, "/"))), nil
	case filepath.IsAbs(p):
		return filepath.Clean(p), nil
	}
	return filepath.Join(home, p), nil
}

// windowsPathFormError refuses the Windows path forms that depend on a
// current folder the caller cannot see: a drive with no separator after it
// ("C:foo") and a path that starts at the root of the current drive ("\x").
// Other paths answer nil.
func windowsPathFormError(p string) error {
	if len(p) >= 2 && p[1] == ':' && (len(p) == 2 || p[2] != '\\' && p[2] != '/') {
		return fmt.Errorf("'%s' is relative to the current folder of drive %s; pass a full path such as %s\\%s", p, p[:2], p[:2], p[2:])
	}
	if p != "" && (p[0] == '\\' || p[0] == '/') && !(len(p) >= 2 && (p[1] == '\\' || p[1] == '/')) {
		return fmt.Errorf("'%s' starts at the root of the agent's current drive; pass a full path with its drive, such as C:%s", p, strings.ReplaceAll(p, "/", `\`))
	}
	return nil
}

func (s *Server) pathArg(req proto.Message, key string) (string, error) {
	return pathArgIn(s.view(), req, key)
}

// pathArgIn is pathArg against the home of v.
func pathArgIn(v homeView, req proto.Message, key string) (string, error) {
	if runtime.GOOS == "windows" {
		if err := windowsPathFormError(req.String(key)); err != nil {
			return "", err
		}
	}
	return resolvePath(v.dir, req.String(key))
}

func mtimeString(fi os.FileInfo) string {
	return fi.ModTime().UTC().Format(time.RFC3339)
}

// notRegular is the error for a path that is neither a regular file nor a
// directory, such as a FIFO.
func notRegular(path string) error {
	return fmt.Errorf("'%s' is not a regular file; fs.read, fs.tail and sha256 read regular files only", path)
}

// isIrregular reports whether path leads to something other than a regular
// file or a directory.
func isIrregular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && !fi.Mode().IsRegular()
}

func fileDigest(path string) (string, error) {
	if isIrregular(path) {
		return "", notRegular(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("cannot read '%s': %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// withoutLongPrefix returns p without a \\?\ prefix before a drive letter.
func withoutLongPrefix(p string) string {
	if strings.HasPrefix(p, `\\?\`) && len(p) >= 6 && p[5] == ':' {
		return p[4:]
	}
	return p
}

// inDeployRoot reports whether path is the deploy root or lies below it;
// root reports whether it is the root itself.
func (s *Server) inDeployRoot(path string) (inside, root bool) {
	r := s.opt.DeployRoot
	if r == nil {
		return false, false
	}
	fold := runtime.GOOS == "windows"
	dir, target := filepath.Clean(r.Path), filepath.Clean(withoutLongPrefix(path))
	if !pathContains(dir, target, filepath.Separator, fold) {
		return false, false
	}
	return true, pathContains(target, dir, filepath.Separator, fold)
}

// ensureDeployRoot creates the deploy root through DeployRoot.Lock when path
// is the root or lies below it and the root does not exist.
func (s *Server) ensureDeployRoot(path string) error {
	if inside, _ := s.inDeployRoot(path); !inside {
		return nil
	}
	r := s.opt.DeployRoot
	s.deployMu.Lock()
	defer s.deployMu.Unlock()
	if _, err := os.Lstat(r.Path); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := r.Lock(); err != nil {
		return fmt.Errorf("%s is missing and could not be created locked (%v); remove what is in its way as an administrator and retry", r.Path, err)
	}
	return nil
}

func (s *Server) registerFiles() {
	s.register("fs.stat", true, s.fsStat)
	s.register("fs.list", true, s.fsList)
	s.register("fs.read", true, s.fsRead)
	s.register("fs.tail", true, s.fsTail)
	s.register("fs.write", true, s.fsWrite)
	s.register("fs.rename", true, s.fsRename)
	s.register("fs.mkdir", true, s.fsMkdir)
	s.register("fs.delete", true, s.fsDelete)
	s.register("fs.copy", true, s.fsCopy)
	s.register("fs.trash", true, s.fsTrash)
	s.register("fs.hash", true, s.fsHash)
}

// isLink reports whether mode, read without following links, is that of a
// symbolic link or of another entry that stands for something else and is not
// a directory, such as a Windows junction.
func isLink(mode os.FileMode) bool {
	return mode&os.ModeSymlink != 0 || mode&os.ModeIrregular != 0 && !mode.IsDir()
}

// entryOwner returns the owner of the entry at path itself, without following
// a link, as ownerName gives it; false means the system reports no owners.
var entryOwner = platformEntryOwner

// ownerName names the owner SID sid: "administrators" for the Administrators
// group, "system" for LocalSystem, "user" for the account the agent runs as
// (userSID), and the SID string for any other owner.
func ownerName(sid, userSID string) string {
	switch {
	case sid == "S-1-5-32-544":
		return "administrators"
	case sid == "S-1-5-18":
		return "system"
	case userSID != "" && sid == userSID:
		return "user"
	}
	return sid
}

// fsStat describes what path leads to. "link" is set when the entry at path
// itself is a link, also when what it points to does not exist. "owner" names
// the owner of the entry at path itself where entryOwner reports one.
func (s *Server) fsStat(_ context.Context, req proto.Message) (proto.Message, error) {
	path, err := s.pathArg(req, "path")
	if err != nil {
		return nil, err
	}
	link, owner, hasOwner := false, "", false
	if li, err := os.Lstat(path); err == nil {
		link = isLink(li.Mode())
		owner, hasOwner = entryOwner(path)
	}
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		out := proto.Message{"exists": false, "path": path}
		if link {
			out["link"] = true
		}
		if hasOwner {
			out["owner"] = owner
		}
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot stat '%s': %v", path, err)
	}
	out := proto.Message{
		"exists": true,
		"dir":    fi.IsDir(),
		"size":   0,
		"mtime":  mtimeString(fi),
		"path":   path,
	}
	if link {
		out["link"] = true
	}
	if hasOwner {
		out["owner"] = owner
	}
	if _, root := s.inDeployRoot(path); root {
		out["locked"] = s.opt.DeployRoot.Locked()
	}
	if !fi.IsDir() {
		out["size"] = fi.Size()
		if req.Bool("sha256") {
			sum, err := fileDigest(path)
			if err != nil {
				return nil, fmt.Errorf("cannot hash '%s': %v", path, err)
			}
			out["sha256"] = sum
		}
	}
	return out, nil
}

func (s *Server) fsList(_ context.Context, req proto.Message) (proto.Message, error) {
	root, err := s.pathArg(req, "path")
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("'%s' does not exist", root)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list '%s': %v", root, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("'%s' is not a directory; use fs.stat or fs.read for files", root)
	}
	if li, err := os.Lstat(root); err == nil && isLink(li.Mode()) {
		return nil, fmt.Errorf("'%s' is a link and fs.list does not follow links; list the folder it leads to", root)
	}
	recursive := req.Bool("recursive")
	withSum := req.Bool("sha256")

	entries := []any{}
	truncated := false
	skipped := 0
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if p == root {
			if err != nil {
				return err
			}
			return nil
		}
		if err != nil {
			skipped++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if len(entries) >= fsListMax {
			truncated = true
			return fs.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			skipped++
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		e := map[string]any{
			"name":  filepath.ToSlash(rel),
			"dir":   d.IsDir(),
			"size":  int64(0),
			"mtime": mtimeString(info),
		}
		if isLink(info.Mode()) {
			e["link"] = true
		}
		if !d.IsDir() {
			e["size"] = info.Size()
			if info.Mode().IsRegular() && runtime.GOOS != "windows" {
				e["executable"] = info.Mode().Perm()&0o100 != 0
			}
			if withSum && info.Mode().IsRegular() {
				if sum, err := fileDigest(p); err == nil {
					e["sha256"] = sum
				}
			}
		}
		entries = append(entries, e)
		if d.IsDir() && !recursive {
			return fs.SkipDir
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("cannot list '%s': %v", root, walkErr)
	}
	out := proto.Message{"entries": entries, "truncated": truncated}
	if skipped > 0 {
		out["skipped"] = skipped
	}
	return out, nil
}

func openRegular(path string) (*os.File, os.FileInfo, error) {
	if isIrregular(path) {
		return nil, nil, notRegular(path)
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("'%s' does not exist", path)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("cannot open '%s': %v", path, err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("cannot stat '%s': %v", path, err)
	}
	if fi.IsDir() {
		f.Close()
		return nil, nil, fmt.Errorf("'%s' is a directory; use fs.list", path)
	}
	return f, fi, nil
}

// startsWithUTF16BOM reports whether the file begins with the bytes FF FE
// and not with FF FE 00 00, the UTF-32LE byte order mark.
func startsWithUTF16BOM(f *os.File) bool {
	var head [4]byte
	n, _ := f.ReadAt(head[:], 0)
	if n < 2 || head[0] != 0xFF || head[1] != 0xFE {
		return false
	}
	return n < 4 || head[2] != 0 || head[3] != 0
}

// withEncoding adds "encoding": "utf-16le" to out for a file that begins with
// the bytes FF FE.
func withEncoding(out proto.Message, utf16 bool) proto.Message {
	if utf16 {
		out["encoding"] = "utf-16le"
	}
	return out
}

func (s *Server) fsRead(_ context.Context, req proto.Message) (proto.Message, error) {
	path, err := s.pathArg(req, "path")
	if err != nil {
		return nil, err
	}
	f, fi, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	offset := int64(req.Int("offset"))
	if offset < 0 {
		return nil, errors.New("offset must not be negative")
	}
	length := req.Int("length")
	if length <= 0 || length > fsChunkMax {
		length = fsChunkMax
	}
	buf := make([]byte, length)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("cannot read '%s': %v", path, err)
	}
	size := fi.Size()
	return withEncoding(proto.Message{
		"data": base64.StdEncoding.EncodeToString(buf[:n]),
		"size": size,
		"eof":  offset+int64(n) >= size,
	}, startsWithUTF16BOM(f)), nil
}

func (s *Server) fsTail(_ context.Context, req proto.Message) (proto.Message, error) {
	path, err := s.pathArg(req, "path")
	if err != nil {
		return nil, err
	}
	f, fi, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	lines := req.Int("lines")
	if lines <= 0 {
		lines = fsTailDefault
	}
	// A file that begins with FF FE is read as UTF-16LE: its lines start
	// after the byte order mark and end with the code unit 0A 00.
	utf16 := startsWithUTF16BOM(f)
	unit, first := int64(1), int64(0)
	if utf16 {
		unit, first = 2, 2
	}
	size := fi.Size()
	start := size - fsTailWindow
	if start < first {
		start = first
	}
	start += (start - first) % unit
	readFrom := start
	if start > first {
		readFrom = start - unit
	}
	buf := make([]byte, size-readFrom)
	n, err := f.ReadAt(buf, readFrom)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("cannot read '%s': %v", path, err)
	}
	buf = buf[:n]
	partialFirst := false
	if start > first && int64(len(buf)) >= unit {
		partialFirst = !isLineFeed(buf, int(unit))
		buf = buf[unit:]
	}
	return withEncoding(proto.Message{
		"data": base64.StdEncoding.EncodeToString(lastLines(buf, lines, partialFirst, int(unit))),
		"size": size,
	}, utf16), nil
}

// isLineFeed reports whether b begins with a line feed of the given code unit
// size: 0A for 1, 0A 00 for 2.
func isLineFeed(b []byte, unit int) bool {
	if len(b) < unit || b[0] != '\n' {
		return false
	}
	return unit == 1 || b[1] == 0
}

// lineFeedIndex returns the offset of the first (last when fromEnd is set)
// line feed in b that starts at a multiple of unit, or -1.
func lineFeedIndex(b []byte, unit int, fromEnd bool) int {
	if unit == 1 {
		if fromEnd {
			return bytes.LastIndexByte(b, '\n')
		}
		return bytes.IndexByte(b, '\n')
	}
	last := len(b) - len(b)%unit - unit
	if fromEnd {
		for i := last; i >= 0; i -= unit {
			if isLineFeed(b[i:], unit) {
				return i
			}
		}
		return -1
	}
	for i := 0; i <= last; i += unit {
		if isLineFeed(b[i:], unit) {
			return i
		}
	}
	return -1
}

// lastLines returns the last n lines of buf, whose code units are unit bytes
// wide. When buf begins in the middle of a line and fewer than n complete
// lines follow, the partial first line is dropped.
func lastLines(buf []byte, n int, partialFirst bool, unit int) []byte {
	body := buf
	if len(buf) >= unit && len(buf)%unit == 0 && isLineFeed(buf[len(buf)-unit:], unit) {
		body = buf[:len(buf)-unit]
	}
	idx := len(body)
	for i := 0; i < n; i++ {
		j := lineFeedIndex(body[:idx], unit, true)
		if j < 0 {
			if partialFirst {
				if k := lineFeedIndex(buf, unit, false); k >= 0 {
					return buf[k+unit:]
				}
				return nil
			}
			return buf
		}
		idx = j
	}
	return buf[idx+unit:]
}

// pastTheEnd is the error of an fs.write whose offset lies past the end of
// the file of size bytes, counted after truncation when truncated is set.
func pastTheEnd(offset int64, path string, size int64, truncated bool) error {
	after, hint := "", "0"
	if truncated {
		after = " after truncate"
	}
	if size > 0 {
		hint = fmt.Sprintf("%d or lower", size)
	}
	return fmt.Errorf("offset %d lies past the end of '%s' (%d bytes%s); write from offset %s", offset, path, size, after, hint)
}

// fsWrite writes at most fsChunkMax bytes at an offset within the file or at
// its end, or appends them.
func (s *Server) fsWrite(_ context.Context, req proto.Message) (proto.Message, error) {
	v := s.view()
	path, err := pathArgIn(v, req, "path")
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(req.String("data"))
	if err != nil {
		return nil, errors.New("data is not valid base64; send the bytes base64-encoded")
	}
	if len(data) > fsChunkMax {
		return nil, fmt.Errorf("data is %d bytes; fs.write takes at most %d bytes, so write the rest with further calls", len(data), fsChunkMax)
	}
	offset := int64(req.Int("offset"))
	if offset < 0 {
		return nil, errors.New("offset must not be negative")
	}
	appendMode, truncate := req.Bool("append"), req.Bool("truncate")
	if appendMode && truncate {
		return nil, errors.New("pass append or truncate, not both")
	}
	fsys, name, err := s.writerIn(v, path)
	if err != nil {
		return nil, err
	}
	defer fsys.Close()
	if !appendMode {
		var size int64
		if fi, err := fsys.Stat(name); err == nil && !truncate {
			size = fi.Size()
		}
		if offset > size {
			return nil, pastTheEnd(offset, path, size, truncate)
		}
	}
	dir := filepath.Dir(path)
	if _, err := fsys.Stat(filepath.Dir(name)); errors.Is(err, fs.ErrNotExist) {
		if !req.Bool("mkdirs") {
			return nil, fmt.Errorf("directory '%s' does not exist; pass mkdirs to create it", dir)
		}
		if err := s.ensureDeployRoot(dir); err != nil {
			return nil, err
		}
		if err := fsys.MkdirAll(filepath.Dir(name)); err != nil {
			return nil, fmt.Errorf("cannot create '%s': %v", dir, fsys.explain(filepath.Dir(name), err))
		}
	}
	flags := os.O_WRONLY | os.O_CREATE
	switch {
	case appendMode:
		flags |= os.O_APPEND
	case truncate:
		flags |= os.O_TRUNC
	}
	f, err := fsys.OpenFile(name, flags, 0o644)
	if err != nil {
		return nil, fmt.Errorf("cannot open '%s' for writing: %v", path, fsys.explain(name, err))
	}
	if appendMode {
		_, err = f.Write(data)
	} else {
		_, err = f.WriteAt(data, offset)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("cannot write '%s': %v", path, err)
	}
	if req.Has("executable") && runtime.GOOS != "windows" {
		var fi os.FileInfo
		if fi, err = f.Stat(); err == nil {
			mode := fi.Mode().Perm() &^ 0o111
			if req.Bool("executable") {
				mode = executableMode(mode)
			}
			if mode != fi.Mode().Perm() {
				err = f.Chmod(mode)
			}
		}
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("cannot set the execute bits of '%s': %v; the data is written, so set them with exec.run (chmod)", path, err)
		}
	}
	fi, statErr := f.Stat()
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("cannot write '%s': %v", path, err)
	}
	if statErr != nil {
		return nil, fmt.Errorf("cannot stat '%s': %v", path, statErr)
	}
	return proto.Message{"size": fi.Size()}, nil
}

// executableMode adds the execute bit for each of owner, group and others
// that has the read bit.
func executableMode(m os.FileMode) os.FileMode {
	return m | (m&0o444)>>2
}

func (s *Server) fsRename(_ context.Context, req proto.Message) (proto.Message, error) {
	v := s.view()
	from, err := pathArgIn(v, req, "from")
	if err != nil {
		return nil, err
	}
	to, err := pathArgIn(v, req, "to")
	if err != nil {
		return nil, err
	}
	fsys, fromName, err := s.writerIn(v, from)
	if err != nil {
		return nil, err
	}
	defer fsys.Close()
	toName, sameSide := to, !v.contains(to)
	if h, ok := fsys.(homeFS); ok {
		toName, sameSide = homeRelative(h.home, to)
	}
	if !sameSide {
		return nil, fmt.Errorf("'%s' and '%s' lie on different sides of the home folder of the desktop user; rename within it or outside it, or copy the file", from, to)
	}
	if _, err := fsys.Lstat(fromName); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("'%s' does not exist", from)
		}
		return nil, fmt.Errorf("cannot stat '%s': %v", from, fsys.explain(fromName, err))
	}
	if err := s.ensureDeployRoot(to); err != nil {
		return nil, err
	}
	if dst, err := fsys.Lstat(toName); err == nil {
		if !req.Bool("overwrite") {
			return nil, fmt.Errorf("'%s' exists; pass overwrite to replace it", to)
		}
		if dst.IsDir() {
			return nil, fmt.Errorf("'%s' is a directory; delete it first with fs.delete", to)
		}
	}
	if err := fsys.Rename(fromName, toName); err != nil {
		return nil, fmt.Errorf("cannot rename '%s' to '%s': %v", from, to, explainEither(fsys, fromName, toName, err))
	}
	return proto.Message{"path": to}, nil
}

func (s *Server) fsMkdir(_ context.Context, req proto.Message) (proto.Message, error) {
	v := s.view()
	path, err := pathArgIn(v, req, "path")
	if err != nil {
		return nil, err
	}
	if err := s.ensureDeployRoot(path); err != nil {
		return nil, err
	}
	fsys, name, err := s.writerIn(v, path)
	if err != nil {
		return nil, err
	}
	defer fsys.Close()
	if err := fsys.MkdirAll(name); err != nil {
		return nil, fmt.Errorf("cannot create '%s': %v", path, fsys.explain(name, err))
	}
	return proto.Message{"path": path}, nil
}

// isFileSystemRoot reports whether the cleaned path is the root of its volume:
// the separator alone, or a volume name followed by the separator.
func isFileSystemRoot(path, volume string, sep byte) bool {
	return strings.HasPrefix(path, volume) && path[len(volume):] == string(sep)
}

// pathContains reports whether target equals dir or lies below it, comparing
// whole components split on sep; fold ignores letter case.
func pathContains(dir, target string, sep byte, fold bool) bool {
	if fold {
		dir, target = strings.ToLower(dir), strings.ToLower(target)
	}
	split := func(p string) []string { return strings.Split(strings.TrimRight(p, string(sep)), string(sep)) }
	d, t := split(dir), split(target)
	if len(d) > len(t) {
		return false
	}
	for i := range d {
		if d[i] != t[i] {
			return false
		}
	}
	return true
}

// protectedFromDelete reports whether path is a file-system root, homeDir
// or one of its ancestors, compared with homeDir as
// given and with its links resolved.
func protectedFromDelete(homeDir, path string) bool {
	if isFileSystemRoot(path, filepath.VolumeName(path), filepath.Separator) {
		return true
	}
	if homeDir == "" {
		return false
	}
	fold := runtime.GOOS == "windows"
	home := filepath.Clean(homeDir)
	if pathContains(path, home, filepath.Separator, fold) {
		return true
	}
	real, err := filepath.EvalSymlinks(home)
	return err == nil && pathContains(path, real, filepath.Separator, fold)
}

// fileID identifies a file or folder on its volume.
type fileID struct{ volume, index uint64 }

// readFileID returns the identity of path; follow says whether a link at the
// end of path is followed.
var readFileID = platformFileID

// homeRefusal is the error of fs.delete for path, which is the home
// directory or contains it.
func homeRefusal(path string) error {
	return fmt.Errorf("refusing to delete '%s': it is the home directory or contains it; delete its contents instead, or use exec.run if this is intended", path)
}

// identityRefusal returns the refusal of fs.delete for path, whose Lstat is
// fi, when it is a folder whose identity equals that of the root of its
// volume, homeDir or one of its ancestors, homeDir
// taken as given and with its links resolved. A folder whose identity, or
// whose volume root, cannot be read is refused, and so is every folder while
// the identity of one of those existing folders cannot be read. Other paths
// answer nil.
func (s *Server) identityRefusal(homeDir, path string, fi os.FileInfo) error {
	if !fi.IsDir() {
		return nil
	}
	const next = "delete its contents instead, or use exec.run if this is intended"
	target, err := readFileID(path, false)
	if err != nil {
		return fmt.Errorf("refusing to delete '%s': its identity could not be read (%v), so it cannot be told apart from the home directory; %s", path, err, next)
	}
	folders, err := volumeRoots(path)
	if err != nil {
		return fmt.Errorf("refusing to delete '%s': the root of its volume could not be read (%v), so it cannot be told apart from that root; %s", path, err, next)
	}
	if homeDir != "" {
		home := filepath.Clean(homeDir)
		homes := []string{home}
		if real, err := filepath.EvalSymlinks(home); err == nil {
			homes = append(homes, real)
		}
		for _, p := range homes {
			for {
				folders = append(folders, p)
				parent := filepath.Dir(p)
				if parent == p {
					break
				}
				p = parent
			}
		}
	}
	for _, p := range folders {
		id, err := readFileID(p, true)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("refusing to delete '%s': the identity of %s could not be read (%v), so it cannot be told apart from that folder; %s", path, p, err, next)
		}
		if id == target {
			return homeRefusal(path)
		}
	}
	return nil
}

// resolveParent returns path without a \\?\ prefix before a drive letter and
// with the links, junctions and short names of its parent folder resolved
// and its last element kept; it returns path when the parent cannot be
// resolved.
func resolveParent(path string) string {
	p := filepath.Clean(withoutLongPrefix(path))
	dir, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return p
	}
	return filepath.Join(dir, filepath.Base(p))
}

func (s *Server) fsDelete(_ context.Context, req proto.Message) (proto.Message, error) {
	v := s.view()
	path, err := pathArgIn(v, req, "path")
	if err != nil {
		return nil, err
	}
	if protectedFromDelete(v.dir, path) || protectedFromDelete(v.dir, resolveParent(path)) {
		return nil, homeRefusal(path)
	}
	fsys, name, err := s.writerIn(v, path)
	if err != nil {
		return nil, err
	}
	defer fsys.Close()
	fi, err := fsys.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return proto.Message{"deleted": false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot stat '%s': %v", path, fsys.explain(name, err))
	}
	if err := s.identityRefusal(v.dir, path, fi); err != nil {
		return nil, err
	}
	if fi.IsDir() && !req.Bool("recursive") {
		children, err := fsys.ReadDir(name)
		if err != nil {
			return nil, fmt.Errorf("cannot read '%s': %v", path, fsys.explain(name, err))
		}
		if len(children) > 0 {
			return nil, fmt.Errorf("'%s' is not empty; pass recursive to delete it with its contents", path)
		}
	}
	if err := fsys.RemoveAll(name); err != nil {
		return nil, fmt.Errorf("cannot delete '%s': %v", path, fsys.explain(name, err))
	}
	return proto.Message{"deleted": true}, nil
}

// conflictPolicies are the values of on_conflict.
var conflictPolicies = []string{"fail", "replace", "skip", "rename"}

// conflictPolicy returns the on_conflict of req, "fail" when it has none.
func conflictPolicy(req proto.Message) (string, error) {
	v, ok := req["on_conflict"]
	if !ok || v == nil || v == "" {
		return "fail", nil
	}
	if p, ok := v.(string); ok && slices.Contains(conflictPolicies, p) {
		return p, nil
	}
	return "", invalidArgument(fmt.Sprintf("on_conflict %v is not one of fail, replace, skip or rename; pass one of them", v))
}

// ctxReader reads r until ctx ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// tempName returns a new name in dir for an entry being written there.
func tempName(dir string) string {
	return filepath.Join(dir, ".deskhand-copy-"+hex.EncodeToString(randomBytes(8)))
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

// numberedName returns path with " (k)" added to its name, before the
// extension of a file name that has a name before its extension.
func numberedName(path string, k int, folder bool) string {
	dir, base := filepath.Split(path)
	ext := ""
	if !folder {
		ext = filepath.Ext(base)
		if ext == base {
			ext = ""
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base[:len(base)-len(ext)], k, ext))
}

// caseCollision is the error of a folder copy that met two names in the
// source folder from that differ only in letter case, the second at rel.
func caseCollision(from, rel string) error {
	return coded(wire.CodeTransferConflict, fmt.Sprintf("'%s' holds names that differ only in letter case, such as '%s', and the destination folder takes them for one name; rename one of them and retry", from, rel), false)
}

// copyFile copies the regular file src, whose Lstat is fi, to the new file
// dst with the modification time and permission bits of fi (on Windows,
// whether it is read-only), flushed to disk. from and rel name the copied
// folder and src in it for the error of a name dst already has.
func copyFile(ctx context.Context, src string, fi fs.FileInfo, dst, from, rel string) error {
	in, err := os.Open(src)
	if err != nil {
		return fsFailure(fmt.Sprintf("cannot open '%s'", src), err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return caseCollision(from, rel)
	}
	if err != nil {
		return fsFailure(fmt.Sprintf("cannot create the copy of '%s'", src), err)
	}
	_, err = io.CopyBuffer(out, ctxReader{ctx, in}, make([]byte, fsCopyBuffer))
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	}
	if err == nil {
		err = os.Chmod(dst, fi.Mode().Perm())
	}
	if err != nil {
		return fsFailure(fmt.Sprintf("cannot copy '%s'", src), err)
	}
	return nil
}

// copyEntry copies the file or folder from, whose Lstat is fi, to the new
// path dst and returns the number of files it copied. A folder is copied
// with everything in it; a link or a special file in it is an
// INVALID_ARGUMENT error.
func copyEntry(ctx context.Context, from string, fi fs.FileInfo, dst string) (int, error) {
	if !fi.IsDir() {
		return 1, copyFile(ctx, from, fi, dst, from, filepath.Base(from))
	}
	type folder struct {
		path string
		info fs.FileInfo
	}
	var folders []folder
	files := 0
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fsFailure(fmt.Sprintf("cannot read '%s'", p), err)
		}
		info, err := d.Info()
		if err != nil {
			return fsFailure(fmt.Sprintf("cannot read '%s'", p), err)
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case isLink(info.Mode()):
			return invalidArgument(fmt.Sprintf("'%s' holds the link '%s', and fs.copy does not follow links; remove the link or copy what it leads to on its own", from, p))
		case info.IsDir():
			err := os.Mkdir(target, 0o700)
			if errors.Is(err, fs.ErrExist) {
				return caseCollision(from, rel)
			}
			if err != nil {
				return fsFailure(fmt.Sprintf("cannot create the copy of '%s'", p), err)
			}
			folders = append(folders, folder{target, info})
		case info.Mode().IsRegular():
			if err := copyFile(ctx, p, info, target, from, rel); err != nil {
				return err
			}
			files++
		default:
			return invalidArgument(fmt.Sprintf("'%s' holds '%s', which is not a regular file or folder; fs.copy copies regular files and folders only", from, p))
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for i := len(folders) - 1; i >= 0; i-- {
		f := folders[i]
		err := os.Chtimes(f.path, f.info.ModTime(), f.info.ModTime())
		if err == nil && runtime.GOOS != "windows" {
			err = os.Chmod(f.path, f.info.Mode().Perm())
		}
		if err != nil {
			return 0, fsFailure(fmt.Sprintf("cannot copy the folder attributes of '%s'", from), err)
		}
	}
	return files, nil
}

// copiesIntoItself reports whether to lies below the folder from, compared
// as given and with to's folder and from resolved.
func copiesIntoItself(from, to string) bool {
	fold := runtime.GOOS == "windows"
	below := func(dir, p string) bool {
		return pathContains(dir, p, filepath.Separator, fold) && !pathContains(p, dir, filepath.Separator, fold)
	}
	if below(from, to) {
		return true
	}
	rf, err1 := resolvedPath(from, true)
	rt, err2 := resolvedPath(to, false)
	return err1 == nil && err2 == nil && below(rf, rt)
}

// fsCopy copies the file or folder from to the path to (copyEntry) under a
// temporary name in the folder of to and then renames it there: to itself,
// or with on_conflict rename the first free numbered name. When to exists,
// on_conflict decides: fail (the default) and a name that differs only in
// letter case under replace are TRANSFER_CONFLICT errors, skip copies
// nothing, replace replaces a file with a file, rename picks a numbered
// name. The answer carries the path of the copy, the number of files copied
// and the outcome written, skipped or renamed.
func (s *Server) fsCopy(ctx context.Context, req proto.Message) (proto.Message, error) {
	v := s.view()
	roots, err := rootsArg(req)
	if err != nil {
		return nil, err
	}
	from, err := checkedPathArg(v, req, "from", roots)
	if err != nil {
		return nil, err
	}
	to, err := checkedPathArg(v, req, "to", roots)
	if err != nil {
		return nil, err
	}
	policy, err := conflictPolicy(req)
	if err != nil {
		return nil, err
	}
	src, err := os.Lstat(from)
	if missing(err) {
		return nil, coded(wire.CodeNotFound, fmt.Sprintf("'%s' does not exist; check the path with fs.stat", from), false)
	}
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot read '%s'", from), err)
	}
	switch {
	case isLink(src.Mode()):
		return nil, invalidArgument(fmt.Sprintf("'%s' is a link and fs.copy does not follow links; copy the path it leads to", from))
	case src.IsDir() && !req.Bool("recursive"):
		return nil, invalidArgument(fmt.Sprintf("'%s' is a folder; pass recursive to copy it with everything in it", from))
	case !src.IsDir() && !src.Mode().IsRegular():
		return nil, invalidArgument(fmt.Sprintf("'%s' is not a regular file or folder; fs.copy copies regular files and folders only", from))
	}
	dir := filepath.Dir(to)
	if dir == to {
		return nil, invalidArgument(fmt.Sprintf("'%s' is the root of a volume; pass the path the copy gets, such as %s", to, filepath.Join(to, filepath.Base(from))))
	}
	if v.contains(to) {
		return nil, coded(wire.CodeCapabilityUnsupported, fmt.Sprintf("fs.copy does not write into the home folder of the desktop user on this system ('%s'); copy with fs.read and fs.write instead", to), false)
	}
	if src.IsDir() && copiesIntoItself(from, to) {
		return nil, invalidArgument(fmt.Sprintf("cannot copy the folder '%s' into itself ('%s'); pass a destination outside it", from, to))
	}
	if fi, err := os.Stat(dir); missing(err) || err == nil && !fi.IsDir() {
		return nil, coded(wire.CodeNotFound, fmt.Sprintf("the folder '%s' does not exist; create it with fs.mkdir, then retry", dir), false)
	} else if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot read '%s'", dir), err)
	}
	if dst, err := os.Lstat(to); err == nil {
		name, err := actualName(to)
		if err != nil {
			name = filepath.Base(to)
		}
		existing := filepath.Join(dir, name)
		caseOnly := name != filepath.Base(to)
		conflict := func(msg string) error { return coded(wire.CodeTransferConflict, msg, false) }
		switch {
		case policy == "skip":
			return proto.Message{"path": existing, "copied": 0, "outcome": "skipped"}, nil
		case caseOnly && policy != "rename":
			return nil, conflict(fmt.Sprintf("'%s' exists as '%s', a name that differs only in letter case, which Windows takes for the same name; pass that name, another name, or on_conflict skip or rename", to, name))
		case policy == "fail":
			return nil, conflict(fmt.Sprintf("'%s' exists; pass on_conflict replace, skip or rename to copy anyway", to))
		case policy == "replace" && (dst.IsDir() || src.IsDir()):
			return nil, conflict(fmt.Sprintf("'%s' or '%s' is a folder, and on_conflict replace replaces a file with a file; trash the folder with fs.trash first, or pass on_conflict rename", from, to))
		}
	} else if !missing(err) {
		return nil, fsFailure(fmt.Sprintf("cannot read '%s'", to), err)
	}
	tmp := tempName(dir)
	defer os.RemoveAll(tmp)
	copied, err := copyEntry(ctx, from, src, tmp)
	if err != nil && ctx.Err() != nil {
		return nil, coded(wire.CodeInternal, fmt.Sprintf("the copy of '%s' stopped because the connection closed, and nothing was put at '%s'; send the request again with a new rid", from, to), true)
	}
	if err != nil {
		return nil, err
	}
	dest := to
	switch policy {
	case "replace":
		if src.IsDir() {
			err = renameNoReplace(tmp, to)
		} else {
			err = os.Rename(tmp, to)
		}
	case "rename":
		dest, err = renameToFreeName(tmp, to, src.IsDir())
	default:
		err = renameNoReplace(tmp, to)
	}
	if errors.Is(err, fs.ErrExist) {
		return nil, coded(wire.CodeTransferConflict, fmt.Sprintf("'%s' appeared while the copy was made; pass on_conflict replace, skip or rename to copy anyway", to), false)
	}
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot put the copy of '%s' at '%s'", from, to), err)
	}
	outcome := "written"
	if dest != to {
		outcome = "renamed"
	}
	return proto.Message{"path": dest, "copied": copied, "outcome": outcome}, nil
}

// renameToFreeName renames tmp to to, or to the first numbered name of to
// (numberedName) that does not exist, and returns the name it took.
func renameToFreeName(tmp, to string, folder bool) (string, error) {
	for k := 0; k <= fsRenameMax; k++ {
		name := to
		if k > 0 {
			name = numberedName(to, k, folder)
		}
		err := renameNoReplace(tmp, name)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", coded(wire.CodeTransferConflict, fmt.Sprintf("'%s' and its numbered names up to (%d) exist; pass another destination", to, fsRenameMax), false)
}

// fsTrash moves the file or folder path to the Recycle Bin (platformTrash).
// It refuses what fs.delete refuses: a file-system root, the home directory
// and its ancestors. A path that does not exist answers trashed false.
func (s *Server) fsTrash(_ context.Context, req proto.Message) (proto.Message, error) {
	v := s.view()
	roots, err := rootsArg(req)
	if err != nil {
		return nil, err
	}
	path, err := checkedPathArg(v, req, "path", roots)
	if err != nil {
		return nil, err
	}
	if protectedFromDelete(v.dir, path) || protectedFromDelete(v.dir, resolveParent(path)) {
		return nil, homeRefusal(path)
	}
	fi, err := os.Lstat(path)
	if missing(err) {
		return proto.Message{"trashed": false}, nil
	}
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot read '%s'", path), err)
	}
	if err := s.identityRefusal(v.dir, path, fi); err != nil {
		return nil, err
	}
	if err := platformTrash(path, fi); err != nil {
		return nil, err
	}
	return proto.Message{"trashed": true}, nil
}

// fsHash returns the SHA-256 of the bytes of the regular file path from
// offset, length of them or, with length 0, up to the end, with the number
// of bytes it read and the size of the file.
func (s *Server) fsHash(ctx context.Context, req proto.Message) (proto.Message, error) {
	roots, err := rootsArg(req)
	if err != nil {
		return nil, err
	}
	path, err := checkedPathArg(s.view(), req, "path", roots)
	if err != nil {
		return nil, err
	}
	offset, length := int64(req.Int("offset")), int64(req.Int("length"))
	if offset < 0 {
		return nil, invalidArgument("offset must not be negative; pass 0 or more")
	}
	if length < 0 {
		return nil, invalidArgument("length must not be negative; pass 0 to read to the end of the file")
	}
	fi, err := os.Stat(path)
	if missing(err) {
		return nil, coded(wire.CodeNotFound, fmt.Sprintf("'%s' does not exist; check the path with fs.stat", path), false)
	}
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot read '%s'", path), err)
	}
	if fi.IsDir() {
		return nil, invalidArgument(fmt.Sprintf("'%s' is a folder; fs.hash reads regular files only", path))
	}
	if !fi.Mode().IsRegular() {
		return nil, invalidArgument(fmt.Sprintf("'%s' is not a regular file; fs.hash reads regular files only", path))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot open '%s'", path), err)
	}
	defer f.Close()
	if fi, err = f.Stat(); err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot read '%s'", path), err)
	}
	size := fi.Size()
	if offset > size {
		return nil, invalidArgument(fmt.Sprintf("offset %d lies past the end of '%s' (%d bytes); pass %d or lower", offset, path, size, size))
	}
	n := size - offset
	if length > 0 && length < n {
		n = length
	}
	h := sha256.New()
	read, err := io.CopyBuffer(h, ctxReader{ctx, io.NewSectionReader(f, offset, n)}, make([]byte, fsCopyBuffer))
	if err != nil {
		return nil, fsFailure(fmt.Sprintf("cannot read '%s'", path), err)
	}
	return proto.Message{"sha256": hex.EncodeToString(h.Sum(nil)), "offset": offset, "length": read, "size": size}, nil
}
