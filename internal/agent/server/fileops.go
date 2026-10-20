package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// fsCopyBuffer is the buffer fs.copy and fs.hash read files through.
	fsCopyBuffer = 1 << 20
	// fsRenameMax bounds the numbered names on_conflict rename tries.
	fsRenameMax = 10000
)

// conflictPolicy returns the on_conflict of req, "fail" when it has none.
func conflictPolicy(req proto.Message) (string, error) {
	v, ok := req["on_conflict"]
	if !ok || v == nil || v == "" {
		return "fail", nil
	}
	if p, ok := v.(string); ok && wire.IsConflictPolicy(p) {
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

// intoItself returns an INVALID_ARGUMENT error when the folder that holds
// to, or one of its ancestors, is the folder from itself (os.SameFile, with
// links followed), and when from or one of those folders cannot be read to
// tell; it returns nil otherwise.
func intoItself(from, to string) error {
	src, err := os.Stat(from)
	if err != nil {
		return invalidArgument(fmt.Sprintf("'%s' cannot be read (%v), so '%s' cannot be shown to lie outside it; check the path with fs.stat", from, err, to))
	}
	for p := filepath.Dir(to); ; {
		fi, err := os.Stat(p)
		switch {
		case err == nil && os.SameFile(src, fi):
			return invalidArgument(fmt.Sprintf("cannot copy the folder '%s' into itself ('%s'); pass a destination outside it", from, to))
		case err != nil && !missing(err):
			return invalidArgument(fmt.Sprintf("'%s' cannot be read (%v), so '%s' cannot be shown to lie outside '%s'; pass a destination whose folders can be read", p, err, to, from))
		}
		parent := filepath.Dir(p)
		if parent == p {
			return nil
		}
		p = parent
	}
}

// fsCopy copies the file or folder from to the path to (copyEntry) under a
// temporary name in the folder of to, which it creates through
// ensureDeployRoot when to lies in a missing deploy root, and then renames
// it there: to itself,
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
	if src.IsDir() {
		if err := intoItself(from, to); err != nil {
			return nil, err
		}
	}
	if err := s.ensureDeployRoot(to); err != nil {
		return nil, err
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
// It refuses with PERMISSION_DENIED what fs.delete refuses: a file-system
// root, the home directory and its ancestors. A path that does not exist
// answers trashed false.
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
