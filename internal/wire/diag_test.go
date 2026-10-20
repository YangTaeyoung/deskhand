package wire

import (
	"slices"
	"testing"
	"time"
)

func intp(n int) *int { return &n }

func TestDiagEventsDefaultsAreTheSystemLogOfTheLastDayWithEveryLevel(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	q, fault := DiagEventsParams{}.Check(now)
	if fault != nil {
		t.Fatalf("fault %+v", fault)
	}
	if q.Log != "System" || !q.Since.Equal(now.Add(-24*time.Hour)) || !q.Until.IsZero() || q.Levels != nil || q.Provider != "" || q.Max != 200 {
		t.Fatalf("defaults %+v", q)
	}
}

func TestDiagEventsParamsAreCanonicalized(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	q, fault := DiagEventsParams{Log: "application", Since: "2026-10-21T20:00:00+09:00", Until: "2026-10-21T11:30:00.5Z",
		Levels: []string{"Warning", "critical", "warning"}, Provider: "Service Control Manager", Max: intp(1000)}.Check(now)
	if fault != nil {
		t.Fatalf("fault %+v", fault)
	}
	if q.Log != "Application" || !q.Since.Equal(time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC)) ||
		!q.Until.Equal(time.Date(2026, 10, 21, 11, 30, 0, 5e8, time.UTC)) || !slices.Equal(q.Levels, []string{"critical", "warning"}) ||
		q.Provider != "Service Control Manager" || q.Max != 1000 {
		t.Fatalf("query %+v", q)
	}
}

func TestDiagEventsParamFaultsNameTheField(t *testing.T) {
	now := time.Now()
	cases := []struct {
		p     DiagEventsParams
		field string
	}{
		{DiagEventsParams{Log: "Security"}, "log"},
		{DiagEventsParams{Since: "yesterday"}, "since"},
		{DiagEventsParams{Until: "2026-10-21"}, "until"},
		{DiagEventsParams{Since: "2026-10-21T10:00:00Z", Until: "2026-10-21T09:00:00Z"}, "until"},
		{DiagEventsParams{Levels: []string{"error", "fatal"}}, "levels"},
		{DiagEventsParams{Max: intp(0)}, "max"},
		{DiagEventsParams{Max: intp(1001)}, "max"},
	}
	for _, c := range cases {
		_, fault := c.p.Check(now)
		if fault == nil || fault.Field != c.field {
			t.Errorf("%+v: fault %+v, expected field %s", c.p, fault, c.field)
		}
	}
	if _, fault := (DiagEventsParams{Levels: []string{"error", "fatal"}}).Check(now); fault.Value != "fatal" {
		t.Fatalf("the levels fault names %q", fault.Value)
	}
}

func TestDiagCrashesSinceDefaultsToThirtyDaysAgo(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	since, err := DiagCrashesSince("", now)
	if err != nil || !since.Equal(now.AddDate(0, 0, -30)) {
		t.Fatalf("since %v %v", since, err)
	}
	if since, err = DiagCrashesSince("2026-10-01T00:00:00+09:00", now); err != nil || !since.Equal(time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("since %v %v", since, err)
	}
	if _, err = DiagCrashesSince("last week", now); err == nil {
		t.Fatal("a since that is not RFC 3339 was taken")
	}
}

func TestDiagTimesAreUTCWithMilliseconds(t *testing.T) {
	at := time.Date(2026, 10, 21, 21, 4, 5, 123456789, time.FixedZone("KST", 9*3600))
	if got := FormatDiagTime(at); got != "2026-10-21T12:04:05.123Z" {
		t.Fatalf("formatted %q", got)
	}
}
