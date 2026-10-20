package localapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

var errBadCursor = errors.New("bad cursor")

const (
	defaultListLimit = 500
	maxListLimit     = 5000
)

// listCursor is the sort key of the last entry of a page.
type listCursor struct {
	Dir  bool   `json:"d"`
	Fold string `json:"f"`
	Name string `json:"n"`
	Path string `json:"p"`
}

func entryKey(e model.FileEntry) listCursor {
	return listCursor{Dir: e.Dir, Fold: strings.ToLower(e.Name), Name: e.Name, Path: e.Path}
}

// before reports whether a sorts before b: folders first, then the name
// without regard to case, then the exact name, then the path.
func (a listCursor) before(b listCursor) bool {
	if a.Dir != b.Dir {
		return a.Dir
	}
	if a.Fold != b.Fold {
		return a.Fold < b.Fold
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.Path < b.Path
}

func encodeCursor(c listCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (listCursor, error) {
	var c listCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if err == nil && (c.Fold == "" || c.Path == "") {
		err = errBadCursor
	}
	if err != nil {
		return c, apierr.Invalid("cursor를 읽지 못했습니다. 이전 결과의 next_cursor를 그대로 주거나 cursor 없이 처음부터 다시 조회하세요")
	}
	return c, nil
}

// pageEntries sorts entries (folders first, then name without regard to
// case) and returns the page of up to limit entries after cursor, the cursor
// of the next page ("" on the last page) and the number of entries listed.
func pageEntries(entries []model.FileEntry, cursor string, limit int) ([]model.FileEntry, string, int, error) {
	if limit < 0 || limit > maxListLimit {
		return nil, "", 0, apierr.Invalid("limit은 0 이상 5000 이하여야 합니다. 0이거나 없으면 기본값 500입니다")
	}
	if limit == 0 {
		limit = defaultListLimit
	}
	var after *listCursor
	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", 0, err
		}
		after = &c
	}
	sorted := append([]model.FileEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return entryKey(sorted[i]).before(entryKey(sorted[j])) })
	start := 0
	if after != nil {
		start = sort.Search(len(sorted), func(i int) bool { return after.before(entryKey(sorted[i])) })
	}
	end := start + limit
	next := ""
	if end < len(sorted) {
		next = encodeCursor(entryKey(sorted[end-1]))
	} else {
		end = len(sorted)
	}
	return sorted[start:end], next, len(sorted), nil
}
