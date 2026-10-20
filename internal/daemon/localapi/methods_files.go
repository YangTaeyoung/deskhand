package localapi

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func (s *Server) registerFiles() {
	s.methods["files.list"] = s.filesList
	s.methods["files.stat"] = s.filesStat
	s.methods["files.read"] = s.filesRead
	s.methods["files.write"] = s.filesWrite
	s.methods["files.mkdir"] = s.filesMkdir
	s.methods["files.rename"] = s.filesRename
	s.methods["files.delete"] = s.filesDelete
	s.methods["files.copy"] = s.filesCopy
	s.methods["files.trash"] = s.filesTrash
	s.methods["files.hash"] = s.filesHash
}

type fileParams struct {
	HostID     string `json:"host_id"`
	Path       string `json:"path"`
	From       string `json:"from"`
	To         string `json:"to"`
	Recursive  bool   `json:"recursive"`
	SHA256     bool   `json:"sha256"`
	Offset     int64  `json:"offset"`
	Length     int64  `json:"length"`
	Data       string `json:"data"`
	Append     bool   `json:"append"`
	Truncate   bool   `json:"truncate"`
	Mkdirs     bool   `json:"mkdirs"`
	Overwrite  bool   `json:"overwrite"`
	Permanent  bool   `json:"permanent"`
	OnConflict string `json:"on_conflict"`
	Cursor     string `json:"cursor"`
	Limit      int    `json:"limit"`
}

func (p fileParams) needPath(method string) error {
	if err := hostRequired(p.HostID); err != nil {
		return err
	}
	if p.Path == "" {
		return required("path", method)
	}
	return nil
}

// separator returns the path separator of the host's OS.
func separator(osName string) string {
	if osName == "windows" {
		return `\`
	}
	return "/"
}

// joinRemote joins a folder path and a slash-separated relative path with sep.
func joinRemote(dir, rel, sep string) string {
	rel = strings.ReplaceAll(rel, "/", sep)
	if strings.HasSuffix(dir, sep) {
		return dir + rel
	}
	return dir + sep + rel
}

// baseName returns the last element of a remote path.
func baseName(p, sep string) string {
	p = strings.TrimRight(p, sep)
	if i := strings.LastIndex(p, sep); i >= 0 {
		return p[i+1:]
	}
	return p
}

// apiTime turns an agent time (RFC 3339) into the API's form.
func apiTime(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return model.Time(t)
}

// hostOS returns the OS of a connected host from its hello, else the stored one.
func hostOS(h *conn.Host, stored string) string {
	if o := h.Hello().OS; o != "" {
		return o
	}
	return stored
}

func (s *Server) filesList(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p fileParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.needPath("files.list"); err != nil {
		return nil, err
	}
	if p.Limit < 0 || p.Limit > maxListLimit {
		return nil, apierr.Invalid("limit은 0 이상 5000 이하여야 합니다. 0이거나 없으면 기본값 500입니다")
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	st, err := h.Call(ctx, "fs.stat", proto.Message{"path": p.Path}, conn.CallOpts{})
	if err != nil {
		return nil, err
	}
	root := st.String("path")
	if root == "" {
		root = p.Path
	}
	res, err := h.Call(ctx, "fs.list", proto.Message{"path": root, "recursive": p.Recursive}, conn.CallOpts{})
	if err != nil {
		return nil, err
	}
	sep := separator(hostOS(h, row.OS))
	raws, _ := res["entries"].([]any)
	entries := make([]model.FileEntry, 0, len(raws))
	for _, r := range raws {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		e := proto.Message(m)
		rel := e.String("name")
		size, _ := m["size"].(float64)
		entries = append(entries, model.FileEntry{Name: path.Base(rel), Path: joinRemote(root, rel, sep), Dir: e.Bool("dir"),
			Size: int64(size), MTime: apiTime(e.String("mtime")), Link: e.Bool("link")})
	}
	page, next, total, err := pageEntries(entries, p.Cursor, p.Limit)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"host_id": row.HostID, "path": root, "entries": page, "total": total, "truncated": res.Bool("truncated")}
	if next != "" {
		out["next_cursor"] = next
	}
	if res.Has("skipped") {
		out["skipped"] = res.Int("skipped")
	}
	return out, nil
}

func (s *Server) filesStat(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p fileParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.needPath("files.stat"); err != nil {
		return nil, err
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	params := proto.Message{"path": p.Path}
	if p.SHA256 {
		params["sha256"] = true
	}
	res, err := h.Call(ctx, "fs.stat", params, conn.CallOpts{})
	if err != nil {
		return nil, err
	}
	full := res.String("path")
	size, _ := res["size"].(float64)
	out := map[string]any{"host_id": row.HostID, "exists": res.Bool("exists"), "path": full,
		"name": baseName(full, separator(hostOS(h, row.OS))), "dir": res.Bool("dir"), "size": int64(size), "link": res.Bool("link")}
	if mt := res.String("mtime"); mt != "" {
		out["mtime"] = apiTime(mt)
	}
	for _, k := range []string{"owner", "sha256"} {
		if v := res.String(k); v != "" {
			out[k] = v
		}
	}
	if res.Has("locked") {
		out["locked"] = res.Bool("locked")
	}
	return out, nil
}

func (s *Server) filesRead(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p fileParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.needPath("files.read"); err != nil {
		return nil, err
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	params := proto.Message{"path": p.Path, "offset": p.Offset}
	if p.Length > 0 {
		params["length"] = p.Length
	}
	res, err := h.Call(ctx, "fs.read", params, conn.CallOpts{})
	if err != nil {
		return nil, err
	}
	size, _ := res["size"].(float64)
	out := map[string]any{"host_id": row.HostID, "path": p.Path, "data": res.String("data"), "size": int64(size), "eof": res.Bool("eof")}
	if enc := res.String("encoding"); enc != "" {
		out["encoding"] = enc
	}
	return out, nil
}

// fileChange runs a journaled fs op and returns its envelope with the
// answer's fields, recording a timeline entry.
func (s *Server) fileChange(ctx context.Context, sess *session, hostRef, op string, params proto.Message, summary string,
	keep ...string) (any, error) {
	h, row, err := s.d.Hosts.Conn(hostRef)
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	info := conn.CallInfo{}
	res, err := h.Call(cctx, op, params, conn.CallOpts{Info: &info})
	if pe := requestProblem(err); pe != nil {
		return nil, pe
	}
	o := outcomeOf(info, err)
	fields := map[string]any{}
	if err == nil {
		for _, k := range keep {
			if v, ok := res[k]; ok {
				fields[k] = v
			}
		}
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(row.HostID, model.TimelineFile, summary, timelineOutcome(o.status), ""))
	return s.envelope(row.HostID, "", info, o, nil).Map(fields), nil
}

func (s *Server) filesWrite(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p fileParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.needPath("files.write"); err != nil {
		return nil, err
	}
	params := proto.Message{"path": p.Path, "data": p.Data}
	if p.Offset > 0 {
		params["offset"] = p.Offset
	}
	for k, v := range map[string]bool{"append": p.Append, "truncate": p.Truncate, "mkdirs": p.Mkdirs} {
		if v {
			params[k] = true
		}
	}
	return s.fileChange(ctx, sess, p.HostID, "fs.write", params, "파일 쓰기: "+p.Path, "size")
}

func (s *Server) filesMkdir(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p fileParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.needPath("files.mkdir"); err != nil {
		return nil, err
	}
	return s.fileChange(ctx, sess, p.HostID, "fs.mkdir", proto.Message{"path": p.Path}, "폴더 만들기: "+p.Path, "path")
}

func (s *Server) filesRename(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p fileParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, err
	}
	if p.From == "" || p.To == "" {
		return nil, required("from과 to", "files.rename")
	}
	params := proto.Message{"from": p.From, "to": p.To}
	if p.Overwrite {
		params["overwrite"] = true
	}
	return s.fileChange(ctx, sess, p.HostID, "fs.rename", params, "이름 바꾸기: "+p.From+" → "+p.To, "path")
}

func (s *Server) filesDelete(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p fileParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.needPath("files.delete"); err != nil {
		return nil, err
	}
	if !p.Permanent {
		return nil, apierr.Invalid("files.delete는 영구 삭제만 합니다. 영구 삭제하려면 permanent:true를 주세요")
	}
	params := proto.Message{"path": p.Path}
	if p.Recursive {
		params["recursive"] = true
	}
	return s.fileChange(ctx, sess, p.HostID, "fs.delete", params, "영구 삭제: "+p.Path, "deleted")
}

func (s *Server) filesCopy(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p fileParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, err
	}
	if p.From == "" || p.To == "" {
		return nil, required("from과 to", "files.copy")
	}
	params := proto.Message{"from": p.From, "to": p.To}
	if p.OnConflict != "" {
		if !wire.IsConflictPolicy(p.OnConflict) {
			return nil, apierr.Invalid("on_conflict는 fail, replace, skip, rename 중 하나여야 합니다. 그중 하나를 주세요")
		}
		params["on_conflict"] = p.OnConflict
	}
	if p.Recursive {
		params["recursive"] = true
	}
	return s.fileChange(ctx, sess, p.HostID, "fs.copy", params, "복사: "+p.From+" → "+p.To, "path", "copied", "outcome")
}

func (s *Server) filesTrash(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p fileParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.needPath("files.trash"); err != nil {
		return nil, err
	}
	return s.fileChange(ctx, sess, p.HostID, "fs.trash", proto.Message{"path": p.Path}, "휴지통으로 옮기기: "+p.Path, "trashed")
}

// filesHash runs fs.hash and returns its envelope with the sha256, offset,
// length and size of the answer; it records no timeline entry.
func (s *Server) filesHash(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p fileParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.needPath("files.hash"); err != nil {
		return nil, err
	}
	if p.Offset < 0 || p.Length < 0 {
		return nil, apierr.Invalid("offset과 length는 0 이상이어야 합니다. length 0은 파일 끝까지입니다")
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	info := conn.CallInfo{}
	res, err := h.Call(ctx, "fs.hash", proto.Message{"path": p.Path, "offset": p.Offset, "length": p.Length}, conn.CallOpts{Info: &info})
	if pe := requestProblem(err); pe != nil {
		return nil, pe
	}
	fields := map[string]any{}
	if err == nil {
		for _, k := range []string{"sha256", "offset", "length", "size"} {
			if v, ok := res[k]; ok {
				fields[k] = v
			}
		}
	}
	return s.envelope(row.HostID, "", info, outcomeOf(info, err), nil).Map(fields), nil
}
