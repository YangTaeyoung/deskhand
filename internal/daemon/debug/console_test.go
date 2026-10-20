package debug

import "testing"

func fillConsole(b *consoleBuffer, n int) {
	for i := 0; i < n; i++ {
		b.add(ConsoleEntry{Source: exceptionSourceConsole, Level: "log", Text: "line"})
	}
}

func TestConsoleBufferDropsOldestBeyondKeep(t *testing.T) {
	b := &consoleBuffer{keep: 3}
	fillConsole(b, 5)
	if b.dropped != 2 || b.first() != 3 || b.last != 5 {
		t.Fatalf("dropped=%d first=%d last=%d", b.dropped, b.first(), b.last)
	}
	p := b.page(ConsoleQuery{})
	if len(p.Entries) != 3 || p.Entries[0].Seq != 3 || p.Entries[2].Seq != 5 {
		t.Fatalf("latest page = %+v", p.Entries)
	}
	if p.NextCursor != 5 || p.HasMore || p.HasEarlier || p.Dropped != 2 || p.FirstSeq != 3 {
		t.Fatalf("latest page meta = %+v", p)
	}
}

func TestConsoleCursorPageAndGap(t *testing.T) {
	b := &consoleBuffer{keep: 3}
	fillConsole(b, 5) // seqs 3,4,5 kept
	zero := int64(0)
	p := b.page(ConsoleQuery{Cursor: &zero, Limit: 2})
	if !p.Gap {
		t.Fatalf("a cursor before the kept window must report a gap: %+v", p)
	}
	if len(p.Entries) != 2 || p.Entries[0].Seq != 3 || p.NextCursor != 4 || !p.HasMore {
		t.Fatalf("cursor page = %+v", p)
	}
	next := p.NextCursor
	p2 := b.page(ConsoleQuery{Cursor: &next, Limit: 2})
	if len(p2.Entries) != 1 || p2.Entries[0].Seq != 5 || p2.HasMore || p2.Gap {
		t.Fatalf("second cursor page = %+v", p2)
	}
}

func TestConsoleBeforePage(t *testing.T) {
	b := &consoleBuffer{keep: 10}
	fillConsole(b, 6)
	before := int64(5)
	p := b.page(ConsoleQuery{Before: &before, Limit: 2})
	if len(p.Entries) != 2 || p.Entries[0].Seq != 3 || p.Entries[1].Seq != 4 || !p.HasEarlier {
		t.Fatalf("before page = %+v", p)
	}
}

func TestConsoleClearAdvancesFirstSeq(t *testing.T) {
	b := &consoleBuffer{keep: 10}
	fillConsole(b, 4)
	if through := b.clear(); through != 4 {
		t.Fatalf("clear through = %d", through)
	}
	fillConsole(b, 1)
	p := b.page(ConsoleQuery{})
	if len(p.Entries) != 1 || p.Entries[0].Seq != 5 || p.FirstSeq != 5 {
		t.Fatalf("after clear page = %+v", p)
	}
}
