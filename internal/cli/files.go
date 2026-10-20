package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func (r *run) files(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("deskhand files ls|stat|cat|mkdir|mv|rm|cp|trash|hash|push|pull|move 중 하나를 고르세요"))
	}
	rest := args[1:]
	switch args[0] {
	case "ls":
		return r.filesLs(ctx, rest)
	case "push", "pull", "move":
		return r.filesTransfer(ctx, args[0], rest)
	case "cp":
		return r.filesCp(ctx, rest)
	case "trash":
		return r.filesRm(ctx, rest, true)
	case "hash":
		return r.filesHash(ctx, rest)
	case "stat":
		return r.eachPath(ctx, "files stat", rest, r.filesStat)
	case "cat":
		return r.eachPath(ctx, "files cat", rest, r.filesCat)
	case "mkdir":
		return r.eachPath(ctx, "files mkdir", rest, r.filesMkdir)
	case "mv":
		return r.filesMv(ctx, rest)
	case "rm":
		return r.filesRm(ctx, rest, false)
	}
	return r.fail(usagef("알 수 없는 files 명령 %q입니다. ls, stat, cat, mkdir, mv, rm, cp, trash, hash, push, pull, move 중 하나를 고르세요", args[0]))
}

// eachPath parses every positional argument as <host>:<path> and runs fn on
// each, returning the first non-zero exit code.
func (r *run) eachPath(ctx context.Context, name string, args []string, fn func(context.Context, *client.Client, remotePath) int) int {
	pos, _, err := parseArgs(newFlags(name), args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) == 0 {
		return r.fail(usagef("deskhand %s <호스트>:<경로> ... 형식으로 주세요", name))
	}
	targets, err := parseRemotes(pos)
	if err != nil {
		return r.fail(err)
	}
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	worst := exitOK
	for _, t := range targets {
		if code := fn(ctx, c, t); code != exitOK && worst == exitOK {
			worst = code
		}
	}
	return worst
}

func parseRemotes(args []string) ([]remotePath, error) {
	out := make([]remotePath, 0, len(args))
	for _, a := range args {
		t, err := parseRemote(a)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// listPageSize is the page size of files ls.
const listPageSize = 1000

func (r *run) filesLs(ctx context.Context, args []string) int {
	fs := newFlags("files ls")
	limit := fs.Int("limit", 0, "보여 줄 최대 항목 수(기본: 전부)")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) == 0 {
		return r.fail(usagef("deskhand files ls [--limit N] <호스트>:<경로> ... 형식으로 주세요"))
	}
	if *limit < 0 {
		return r.fail(usagef("--limit은 0 이상이어야 합니다. 0이면 전부 보여 줍니다"))
	}
	targets, err := parseRemotes(pos)
	if err != nil {
		return r.fail(err)
	}
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	worst := exitOK
	for _, t := range targets {
		if code := r.lsOne(ctx, c, t, *limit); code != exitOK && worst == exitOK {
			worst = code
		}
	}
	return worst
}

// listAll reads the pages of files.list of t until the listing ends or
// limit entries (0: no limit) are read, and returns the first page's result
// with all entries read; next_cursor stays when entries remain.
func (r *run) listAll(ctx context.Context, c *client.Client, t remotePath, limit int) (obj, error) {
	var out obj
	var entries []any
	cursor := ""
	for {
		page := listPageSize
		if limit > 0 && limit-len(entries) < page {
			page = limit - len(entries)
		}
		params := map[string]any{"host_id": t.Host, "path": t.Path, "limit": page}
		if cursor != "" {
			params["cursor"] = cursor
		}
		res, err := r.call(ctx, c, "files.list", params, callTimeout)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = res
		}
		got, _ := res["entries"].([]any)
		entries = append(entries, got...)
		cursor = res.str("next_cursor")
		if cursor == "" || (limit > 0 && len(entries) >= limit) {
			if cursor == "" {
				delete(out, "next_cursor")
			} else {
				out["next_cursor"] = cursor
			}
			out["entries"] = entries
			return out, nil
		}
	}
}

func (r *run) lsOne(ctx context.Context, c *client.Client, t remotePath, limit int) int {
	res, err := r.listAll(ctx, c, t, limit)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	entries := res.list("entries")
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		name, size := e.str("name"), ""
		if e.boolean("dir") {
			name += "/"
			size = "-"
		} else {
			n, _ := e.num("size")
			size = humanSize(n)
		}
		rows = append(rows, []string{name, size, e.str("mtime")})
	}
	if len(rows) == 0 {
		fmt.Fprintln(r.Stdout, "비어 있습니다")
		return exitOK
	}
	writeTable(r.Stdout, []string{"이름", "크기", "수정 시각"}, rows)
	if res.str("next_cursor") != "" {
		total, _ := res.num("total")
		r.info("항목 %d개 중 %d개만 보였습니다. --limit을 늘리거나 빼고 다시 조회하세요", total, len(rows))
	}
	if res.boolean("truncated") {
		r.info("목록이 잘렸습니다. 하위 폴더를 지정해 다시 조회하세요")
	}
	return exitOK
}

func (r *run) filesStat(ctx context.Context, c *client.Client, t remotePath) int {
	res, err := r.call(ctx, c, "files.stat", map[string]any{"host_id": t.Host, "path": t.Path}, callTimeout)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	if !res.boolean("exists") {
		fmt.Fprintf(r.Stdout, "경로: %s\n존재: 아니오\n", t.Path)
		return exitFailure
	}
	kind := "파일"
	if res.boolean("dir") {
		kind = "폴더"
	}
	size, _ := res.num("size")
	fmt.Fprintf(r.Stdout, "경로: %s\n종류: %s\n크기: %s (%d바이트)\n수정: %s\n", res.str("path"), kind, humanSize(size), size, dash(res.str("mtime")))
	if o := res.str("owner"); o != "" {
		fmt.Fprintf(r.Stdout, "소유자: %s\n", o)
	}
	return exitOK
}

// filesCat writes the file's bytes to stdout; a UTF-16 LE file is written
// as UTF-8.
func (r *run) filesCat(ctx context.Context, c *client.Client, t remotePath) int {
	var offset int64
	var utf16Buf []byte
	isUTF16 := false
	w := bufio.NewWriter(r.Stdout)
	defer w.Flush()
	for {
		res, err := r.call(ctx, c, "files.read", map[string]any{"host_id": t.Host, "path": t.Path, "offset": offset}, callTimeout)
		if err != nil {
			w.Flush()
			return r.fail(err)
		}
		data, derr := base64.StdEncoding.DecodeString(res.str("data"))
		if derr != nil {
			return r.fail(fmt.Errorf("파일 데이터를 해석하지 못했습니다. deskhand와 데몬 버전을 확인하세요"))
		}
		if res.str("encoding") == "utf-16le" {
			isUTF16 = true
		}
		offset += int64(len(data))
		if isUTF16 {
			utf16Buf = append(utf16Buf, data...)
		} else {
			_, _ = w.Write(data)
		}
		if res.boolean("eof") || len(data) == 0 {
			break
		}
	}
	if isUTF16 {
		_, _ = w.WriteString(decodeUTF16LE(utf16Buf))
	}
	return exitOK
}

func decodeUTF16LE(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	if len(u) > 0 && u[0] == 0xfeff {
		u = u[1:]
	}
	return string(utf16.Decode(u))
}

func (r *run) filesMkdir(ctx context.Context, c *client.Client, t remotePath) int {
	res, err := r.call(ctx, c, "files.mkdir", map[string]any{"host_id": t.Host, "path": t.Path}, callTimeout)
	if err != nil {
		return r.failMut(err, t.Host)
	}
	code := r.settle(res, "폴더 만들기")
	if code == exitOK {
		r.say("폴더를 만들었습니다: %s", t.Path)
	}
	return code
}

func (r *run) filesMv(ctx context.Context, args []string) int {
	fs := newFlags("files mv")
	overwrite := fs.Bool("overwrite", false, "대상이 있으면 덮어쓰기")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 2 {
		return r.fail(usagef("deskhand files mv <호스트>:<원본> <호스트>:<대상> 형식으로 주세요"))
	}
	ts, err := parseRemotes(pos)
	if err != nil {
		return r.fail(err)
	}
	if ts[0].Host != ts[1].Host {
		return r.fail(usagef("files mv는 같은 호스트 안에서만 옮깁니다. 호스트 간 복사는 지원하지 않습니다"))
	}
	params := map[string]any{"host_id": ts[0].Host, "from": ts[0].Path, "to": ts[1].Path}
	if *overwrite {
		params["overwrite"] = true
	}
	res, err := r.simple(ctx, "files.rename", params)
	if err != nil {
		return r.failMut(err, ts[0].Host)
	}
	code := r.settle(res, "이동")
	if code == exitOK {
		r.say("옮겼습니다: %s → %s", ts[0].Path, ts[1].Path)
	}
	return code
}

// filesRm trashes (files rm, files trash) or permanently deletes (files rm
// --permanent) each path, and reports success only for a path the agent
// says it trashed or deleted.
func (r *run) filesRm(ctx context.Context, args []string, trashOnly bool) int {
	name := "files rm"
	if trashOnly {
		name = "files trash"
	}
	fs := newFlags(name)
	var permanent, yes, recursive *bool
	if !trashOnly {
		permanent = fs.Bool("permanent", false, "휴지통을 거치지 않고 영구 삭제")
		yes = fs.Bool("yes", false, "확인 없이 진행")
		recursive = fs.Bool("recursive", false, "폴더를 안의 내용까지 삭제(--permanent)")
		fs.BoolVar(recursive, "r", false, "--recursive와 같음")
	}
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) == 0 {
		return r.fail(usagef("deskhand %s%s <호스트>:<경로> ... 형식으로 주세요", name, map[bool]string{true: "", false: " [--permanent [--yes]]"}[trashOnly]))
	}
	ts, err := parseRemotes(pos)
	if err != nil {
		return r.fail(err)
	}
	perm := permanent != nil && *permanent
	if perm && !*yes {
		if !r.Interactive() {
			return r.fail(usagef("영구 삭제는 되돌릴 수 없습니다. 확인할 사람이 없으면 --yes를 붙여 다시 실행하세요"))
		}
		fmt.Fprintf(r.Stderr, "다음 %d개를 영구 삭제합니다(되돌릴 수 없음):\n", len(ts))
		for _, t := range ts {
			fmt.Fprintf(r.Stderr, "  %s:%s\n", t.Host, t.Path)
		}
		fmt.Fprint(r.Stderr, "계속할까요? [y/N] ")
		line, _ := bufio.NewReader(r.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(r.Stderr, "취소했습니다")
			return exitFailure
		}
	}
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	worst := exitOK
	for _, t := range ts {
		method, params, done, flag := "files.trash", map[string]any{"host_id": t.Host, "path": t.Path}, "휴지통으로 보냈습니다", "trashed"
		if perm {
			method, done, flag = "files.delete", "영구 삭제했습니다", "deleted"
			params["permanent"] = true
			if *recursive {
				params["recursive"] = true
			}
		}
		res, err := r.call(ctx, c, method, params, callTimeout)
		code := exitOK
		if err != nil {
			code = r.failMut(err, t.Host)
		} else if code = r.settle(res, "삭제"); code == exitOK {
			if res.boolean(flag) {
				r.say("%s: %s", done, t.Path)
			} else {
				fmt.Fprintf(r.Stderr, "삭제: %s:%s에서 아무것도 지우지 않았습니다(경로가 없거나 에이전트가 처리하지 않았습니다). deskhand files stat 으로 경로를 확인하세요\n", t.Host, t.Path)
				code = exitFailure
			}
		}
		if code != exitOK && worst == exitOK {
			worst = code
		}
	}
	return worst
}

// filesCp copies a file or folder inside one PC.
func (r *run) filesCp(ctx context.Context, args []string) int {
	fs := newFlags("files cp")
	onConflict := fs.String("on-conflict", "fail", "대상이 이미 있을 때")
	recursive := fs.Bool("recursive", false, "폴더를 안의 내용까지 복사")
	fs.BoolVar(recursive, "r", false, "--recursive와 같음")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 2 {
		return r.fail(usagef("deskhand files cp [-r] [--on-conflict fail|replace|skip|rename] <호스트>:<원본> <호스트>:<대상> 형식으로 주세요"))
	}
	if !wire.IsConflictPolicy(*onConflict) {
		return r.fail(usagef("--on-conflict는 %s 중 하나여야 합니다", strings.Join(wire.ConflictPolicies(), ", ")))
	}
	ts, err := parseRemotes(pos)
	if err != nil {
		return r.fail(err)
	}
	if ts[0].Host != ts[1].Host {
		return r.fail(usagef("files cp는 같은 호스트 안에서만 복사합니다. Mac과 PC 사이는 files push·pull을 쓰세요"))
	}
	params := map[string]any{"host_id": ts[0].Host, "from": ts[0].Path, "to": ts[1].Path, "on_conflict": *onConflict}
	if *recursive {
		params["recursive"] = true
	}
	res, err := r.simple(ctx, "files.copy", params)
	if err != nil {
		return r.failMut(err, ts[0].Host)
	}
	code := r.settle(res, "복사")
	if code != exitOK {
		return code
	}
	n, _ := res.num("copied")
	switch res.str("outcome") {
	case "skipped":
		r.say("복사하지 않았습니다: 대상 %s이(가) 이미 있어 건너뛰었습니다", res.str("path"))
	case "renamed":
		r.say("복사했습니다: %s → %s (이름을 바꿔 둠, 파일 %d개)", ts[0].Path, res.str("path"), n)
	default:
		r.say("복사했습니다: %s → %s (파일 %d개)", ts[0].Path, res.str("path"), n)
	}
	return exitOK
}

// filesHash prints the SHA-256 of each file.
func (r *run) filesHash(ctx context.Context, args []string) int {
	fs := newFlags("files hash")
	offset := fs.Int64("offset", 0, "읽기 시작할 바이트 위치")
	length := fs.Int64("length", 0, "읽을 바이트 수(0: 끝까지)")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) == 0 {
		return r.fail(usagef("deskhand files hash [--offset N] [--length N] <호스트>:<경로> ... 형식으로 주세요"))
	}
	if *offset < 0 || *length < 0 {
		return r.fail(usagef("--offset과 --length는 0 이상이어야 합니다"))
	}
	ts, err := parseRemotes(pos)
	if err != nil {
		return r.fail(err)
	}
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	worst := exitOK
	for _, t := range ts {
		params := map[string]any{"host_id": t.Host, "path": t.Path}
		if *offset > 0 {
			params["offset"] = *offset
		}
		if *length > 0 {
			params["length"] = *length
		}
		res, err := r.call(ctx, c, "files.hash", params, callTimeout)
		code := exitOK
		if err != nil {
			code = r.fail(err)
		} else if code = r.settle(res, "해시"); code == exitOK {
			r.say("%s  %s", res.str("sha256"), t.Path)
		}
		if code != exitOK && worst == exitOK {
			worst = code
		}
	}
	return worst
}
