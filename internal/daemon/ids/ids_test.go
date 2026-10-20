package ids

import (
	"regexp"
	"testing"
	"time"
)

var ulidPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

func TestULIDFormatAndOrder(t *testing.T) {
	now := time.Date(2026, 10, 20, 1, 2, 3, 4_000_000, time.UTC)
	prev := ""
	for i := 0; i < 1000; i++ {
		id := ULID(now)
		if !ulidPattern.MatchString(id) {
			t.Fatalf("ULID %q", id)
		}
		if id <= prev {
			t.Fatalf("ULID %s does not follow %s", id, prev)
		}
		prev = id
	}
	if later := ULID(now.Add(time.Millisecond)); later <= prev {
		t.Fatalf("ULID of a later millisecond %s <= %s", later, prev)
	}
	if id := New(Request); len(id) != 28 || id[:2] != "r-" {
		t.Fatalf("request id %q", id)
	}
}

func TestULIDTimePrefix(t *testing.T) {
	if got := ULID(time.UnixMilli(0))[:10]; got != "0000000000" {
		t.Fatalf("time part of the epoch %s", got)
	}
	if got := ULID(time.UnixMilli(1<<48 - 1))[:10]; got != "7ZZZZZZZZZ" {
		t.Fatalf("time part of the largest time %s", got)
	}
}
