package wire

import (
	"strings"
	"time"
)

// Defaults and bounds of diag.events and diag.crashes (docs/protocol.md
// §3.1).
const (
	DiagEventsDefaultMax  = 200
	DiagEventsMaxLimit    = 1000
	DiagEventsDefaultSpan = 24 * time.Hour
	DiagCrashesDefaultAge = 30
)

// DiagEventLogs are the logs diag.events reads, the first one by default.
var DiagEventLogs = []string{"System", "Application"}

// DiagEventLevels are the levels of diag.events from the most severe.
var DiagEventLevels = []string{"critical", "error", "warning", "information", "verbose"}

// diagTimeLayout is RFC 3339 in UTC with milliseconds.
const diagTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// FormatDiagTime formats t in UTC with milliseconds, the form of every time
// of the diag ops and of proc.list.
func FormatDiagTime(t time.Time) string { return t.UTC().Format(diagTimeLayout) }

// ParseDiagTime reads an RFC 3339 time, with or without fractional seconds.
func ParseDiagTime(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

// DiagEventsParams are the parameters of diag.events as they arrive; Max is
// nil when absent.
type DiagEventsParams struct {
	Log      string
	Since    string
	Until    string
	Levels   []string
	Provider string
	Max      *int
}

// DiagEventsQuery is a checked diag.events request with the defaults
// applied: Log is a name of DiagEventLogs, Until zero means no upper bound,
// Levels holds names of DiagEventLevels in that order without repeats and
// nil means every level.
type DiagEventsQuery struct {
	Log          string
	Since, Until time.Time
	Levels       []string
	Provider     string
	Max          int
}

// DiagEventsFault names the parameter of diag.events that Check refused
// and, for levels, the value.
type DiagEventsFault struct {
	Field string
	Value string
}

// Check returns the query of p at now: log matched without regard to case
// (default System), since and until RFC 3339 (since by default
// DiagEventsDefaultSpan before now, until not after since), levels of
// DiagEventLevels without regard to case, and max from 1 to
// DiagEventsMaxLimit (default DiagEventsDefaultMax).
func (p DiagEventsParams) Check(now time.Time) (DiagEventsQuery, *DiagEventsFault) {
	q := DiagEventsQuery{Log: DiagEventLogs[0], Since: now.Add(-DiagEventsDefaultSpan), Provider: p.Provider, Max: DiagEventsDefaultMax}
	if p.Log != "" {
		q.Log = ""
		for _, l := range DiagEventLogs {
			if strings.EqualFold(l, p.Log) {
				q.Log = l
			}
		}
		if q.Log == "" {
			return DiagEventsQuery{}, &DiagEventsFault{Field: "log", Value: p.Log}
		}
	}
	if p.Since != "" {
		t, err := ParseDiagTime(p.Since)
		if err != nil {
			return DiagEventsQuery{}, &DiagEventsFault{Field: "since", Value: p.Since}
		}
		q.Since = t
	}
	if p.Until != "" {
		t, err := ParseDiagTime(p.Until)
		if err != nil || t.Before(q.Since) {
			return DiagEventsQuery{}, &DiagEventsFault{Field: "until", Value: p.Until}
		}
		q.Until = t
	}
	q.Since, q.Until = q.Since.UTC(), q.Until.UTC()
	if len(p.Levels) > 0 {
		want := map[string]bool{}
		for _, l := range p.Levels {
			l = strings.ToLower(l)
			known := false
			for _, k := range DiagEventLevels {
				known = known || k == l
			}
			if !known {
				return DiagEventsQuery{}, &DiagEventsFault{Field: "levels", Value: l}
			}
			want[l] = true
		}
		for _, k := range DiagEventLevels {
			if want[k] {
				q.Levels = append(q.Levels, k)
			}
		}
	}
	if p.Max != nil {
		if *p.Max < 1 || *p.Max > DiagEventsMaxLimit {
			return DiagEventsQuery{}, &DiagEventsFault{Field: "max"}
		}
		q.Max = *p.Max
	}
	return q, nil
}

// DiagCrashesSince returns the since of diag.crashes: s as RFC 3339, or
// DiagCrashesDefaultAge days before now when s is "".
func DiagCrashesSince(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return now.AddDate(0, 0, -DiagCrashesDefaultAge).UTC(), nil
	}
	t, err := ParseDiagTime(s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
