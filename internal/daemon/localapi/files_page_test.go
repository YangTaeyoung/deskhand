package localapi

import (
	"fmt"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

func names(es []model.FileEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

func sample() []model.FileEntry {
	var out []model.FileEntry
	for _, e := range []model.FileEntry{{Name: "b.txt"}, {Name: "Zeta", Dir: true}, {Name: "A.txt"}, {Name: "alpha", Dir: true}, {Name: "c.txt"}} {
		e.Path = "/p/" + e.Name
		out = append(out, e)
	}
	return out
}

func TestPageEntriesOrdersFoldersFirstThenNameIgnoringCase(t *testing.T) {
	page, next, total, err := pageEntries(sample(), "", 0)
	if err != nil || next != "" || total != 5 {
		t.Fatalf("next=%q total=%d err=%v", next, total, err)
	}
	want := []string{"alpha", "Zeta", "A.txt", "b.txt", "c.txt"}
	for i, n := range names(page) {
		if n != want[i] {
			t.Fatalf("order = %v, want %v", names(page), want)
		}
	}
}

func TestPageEntriesWalksPagesWithCursor(t *testing.T) {
	var got []string
	cursor := ""
	pages := 0
	for {
		page, next, total, err := pageEntries(sample(), cursor, 2)
		if err != nil || total != 5 {
			t.Fatalf("total=%d err=%v", total, err)
		}
		got = append(got, names(page)...)
		pages++
		if next == "" {
			break
		}
		cursor = next
	}
	if pages != 3 || len(got) != 5 || got[0] != "alpha" || got[4] != "c.txt" {
		t.Fatalf("pages=%d got=%v", pages, got)
	}
}

func TestPageEntriesCursorSurvivesRemovedEntry(t *testing.T) {
	_, next, _, _ := pageEntries(sample(), "", 2)
	rest := []model.FileEntry{{Name: "b.txt", Path: "/p/b.txt"}, {Name: "c.txt", Path: "/p/c.txt"}, {Name: "A.txt", Path: "/p/A.txt"}}
	page, _, _, err := pageEntries(rest, next, 10)
	if err != nil || len(page) != 3 || page[0].Name != "A.txt" {
		t.Fatalf("page=%v err=%v", names(page), err)
	}
}

func TestPageEntriesRejectsBadCursorAndLimit(t *testing.T) {
	if _, _, _, err := pageEntries(sample(), "not-a-cursor", 2); apierr.CodeOf(err) != "INVALID_ARGUMENT" {
		t.Fatalf("bad cursor err = %v", err)
	}
	empty := encodeCursor(listCursor{Dir: true})
	if _, _, _, err := pageEntries(sample(), empty, 2); apierr.CodeOf(err) != "INVALID_ARGUMENT" {
		t.Fatalf("empty-key cursor err = %v", err)
	}
	if _, _, _, err := pageEntries(sample(), "", 5001); apierr.CodeOf(err) != "INVALID_ARGUMENT" {
		t.Fatalf("limit 5001 err = %v", err)
	}
	if _, _, _, err := pageEntries(sample(), "", -1); apierr.CodeOf(err) != "INVALID_ARGUMENT" {
		t.Fatalf("limit -1 err = %v", err)
	}
}

func TestPageEntriesDefaultLimitIs500(t *testing.T) {
	var es []model.FileEntry
	for i := 0; i < 700; i++ {
		es = append(es, model.FileEntry{Name: string(rune('a'+i%26)) + string(rune(0x4e00+i)), Path: fmt.Sprint("/p/", i)})
	}
	page, next, total, err := pageEntries(es, "", 0)
	if err != nil || len(page) != 500 || next == "" || total != 700 {
		t.Fatalf("len=%d next=%q total=%d err=%v", len(page), next, total, err)
	}
}
