package diag

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEventXMLGivesTheSystemFieldsAndTheEventData(t *testing.T) {
	cases := []struct {
		file     string
		record   uint64
		time     string
		level    int
		provider string
		id       int
		data     []string
	}{
		{"time-service.xml", 26759, "2026-10-20T15:29:11.0775261Z", 3, "Microsoft-Windows-Time-Service", 36, []string{"50000", "36042"}},
		{"dokan.xml", 26758, "2026-10-20T13:54:03.8315779Z", 4, "Dokan1", 1, []string{"Mounting disk device."}},
		{"restart-manager.xml", 9518, "2026-10-20T02:30:16.2586906Z", 0, "Microsoft-Windows-RestartManager", 10001, []string{"0", "2026-10-20T02:29:35.3756620Z"}},
	}
	for _, c := range cases {
		e, err := parseEventXML(readFixture(t, filepath.Join("events", c.file)))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		want, _ := time.Parse(time.RFC3339Nano, c.time)
		if e.RecordID != c.record || !e.Time.Equal(want) || e.Level != c.level || e.Provider != c.provider || e.EventID != c.id || !slices.Equal(e.Data, c.data) {
			t.Errorf("%s: %+v", c.file, e)
		}
	}
}

func TestEventXMLWithoutARecordIDOrTimeIsRefused(t *testing.T) {
	for _, doc := range []string{
		`<Event><System><Provider Name='p'/><EventID>1</EventID><Level>2</Level><TimeCreated SystemTime='2026-10-20T15:29:11Z'/></System></Event>`,
		`<Event><System><Provider Name='p'/><EventID>1</EventID><Level>2</Level><EventRecordID>5</EventRecordID></System></Event>`,
		`not xml`,
	} {
		if _, err := parseEventXML([]byte(doc)); err == nil {
			t.Errorf("%s was read", doc)
		}
	}
}

func TestLevelNamesFollowEventViewer(t *testing.T) {
	want := map[int]string{0: "information", 1: "critical", 2: "error", 3: "warning", 4: "information", 5: "verbose", 16: "verbose"}
	for level, name := range want {
		if got := levelName(level); got != name {
			t.Errorf("level %d: %s, expected %s", level, got, name)
		}
	}
}

func TestEventXPathSelectsTheLevelsTheTimeRangeAndTheProvider(t *testing.T) {
	since := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 21, 12, 30, 15, 250e6, time.UTC)
	cases := []struct {
		q    wire.DiagEventsQuery
		want string
	}{
		{wire.DiagEventsQuery{Since: since},
			`*[System[TimeCreated[@SystemTime>='2026-10-20T00:00:00.000Z']]]`},
		{wire.DiagEventsQuery{Since: since, Until: until, Levels: []string{"critical", "error"}},
			`*[System[(Level=1 or Level=2) and TimeCreated[@SystemTime>='2026-10-20T00:00:00.000Z' and @SystemTime<='2026-10-21T12:30:15.250Z']]]`},
		{wire.DiagEventsQuery{Since: since, Levels: []string{"information"}, Provider: "Service Control Manager"},
			`*[System[(Level=0 or Level=4) and TimeCreated[@SystemTime>='2026-10-20T00:00:00.000Z'] and Provider[@Name='Service Control Manager']]]`},
		{wire.DiagEventsQuery{Since: since, Levels: []string{"warning", "verbose"}},
			`*[System[(Level=3 or Level>=5) and TimeCreated[@SystemTime>='2026-10-20T00:00:00.000Z']]]`},
		{wire.DiagEventsQuery{Since: since, Provider: "Bob's Service"},
			`*[System[TimeCreated[@SystemTime>='2026-10-20T00:00:00.000Z'] and Provider[@Name="Bob's Service"]]]`},
	}
	for _, c := range cases {
		got, err := eventXPath(c.q)
		if err != nil || got != c.want {
			t.Errorf("%+v:\n%s (%v)\nexpected\n%s", c.q, got, err, c.want)
		}
	}
	if _, err := eventXPath(wire.DiagEventsQuery{Since: since, Provider: `a'b"c`}); err == nil {
		t.Fatal("a provider with both quote characters was put in the query")
	}
}

func TestEventMessageFallsBackToTheEventDataAndIsBounded(t *testing.T) {
	if got := eventMessage("서비스가 시작되었습니다.\r\n\r\n", []string{"x"}); got != "서비스가 시작되었습니다." {
		t.Fatalf("formatted message %q", got)
	}
	if got := eventMessage("line one\r\nline two", nil); got != "line one\nline two" {
		t.Fatalf("line ends %q", got)
	}
	if got := eventMessage("", []string{"50000", "36042"}); got != "50000; 36042" {
		t.Fatalf("event data %q", got)
	}
	long := eventMessage(strings.Repeat("가", maxMessageChars+10), nil)
	if r := []rune(long); len(r) != maxMessageChars+1 || r[len(r)-1] != '…' {
		t.Fatalf("a long message has %d characters", len(r))
	}
}
