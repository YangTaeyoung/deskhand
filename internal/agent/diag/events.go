package diag

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// maxMessageChars bounds the characters of an event message.
const maxMessageChars = 4000

// levelNumbers are the event levels of each level name of diag.events.
var levelNumbers = map[string][]int{
	"critical":    {1},
	"error":       {2},
	"warning":     {3},
	"information": {0, 4},
	"verbose":     {5},
}

// levelName returns the diag.events name of an event level: 0 (log always)
// and 4 are information, levels above 5 verbose.
func levelName(level int) string {
	switch level {
	case 1:
		return "critical"
	case 2:
		return "error"
	case 3:
		return "warning"
	case 0, 4:
		return "information"
	}
	return "verbose"
}

// eventXPath returns the event log query of q: its levels, its time range
// and its provider. A provider name holding both quote characters cannot be
// written in the query.
func eventXPath(q wire.DiagEventsQuery) (string, error) {
	var parts []string
	if len(q.Levels) > 0 {
		var terms []string
		for _, l := range q.Levels {
			for _, n := range levelNumbers[l] {
				terms = append(terms, "Level="+strconv.Itoa(n))
			}
		}
		parts = append(parts, "("+strings.Join(terms, " or ")+")")
	}
	span := "@SystemTime>='" + wire.FormatDiagTime(q.Since) + "'"
	if !q.Until.IsZero() {
		span += " and @SystemTime<='" + wire.FormatDiagTime(q.Until) + "'"
	}
	parts = append(parts, "TimeCreated["+span+"]")
	if q.Provider != "" {
		lit, err := xpathLiteral(q.Provider)
		if err != nil {
			return "", err
		}
		parts = append(parts, "Provider[@Name="+lit+"]")
	}
	return "*[System[" + strings.Join(parts, " and ") + "]]", nil
}

// xpathLiteral quotes s for the query: in single quotes, or in double quotes
// when s holds a single quote.
func xpathLiteral(s string) (string, error) {
	switch {
	case !strings.Contains(s, "'"):
		return "'" + s + "'", nil
	case !strings.Contains(s, `"`):
		return `"` + s + `"`, nil
	}
	return "", &InvalidError{Text: "the provider name holds both ' and \", which the event log query cannot match; pass a provider name without one of them"}
}

// rawEvent is an event as its XML rendering describes it; Data holds the
// values of its EventData or UserData.
type rawEvent struct {
	RecordID uint64
	Time     time.Time
	Level    int
	Provider string
	EventID  int
	Data     []string
}

type eventXML struct {
	System struct {
		Provider struct {
			Name string `xml:"Name,attr"`
		} `xml:"Provider"`
		EventID     string `xml:"EventID"`
		Level       string `xml:"Level"`
		TimeCreated struct {
			SystemTime string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
		EventRecordID string `xml:"EventRecordID"`
	} `xml:"System"`
	EventData *dataXML `xml:"EventData"`
	UserData  *dataXML `xml:"UserData"`
}

// dataXML keeps the inner XML of EventData or UserData.
type dataXML struct {
	Inner []byte `xml:",innerxml"`
}

// parseEventXML reads the XML rendering of an event. It needs the record
// id and the creation time.
func parseEventXML(data []byte) (rawEvent, error) {
	var x eventXML
	if err := xml.Unmarshal(data, &x); err != nil {
		return rawEvent{}, fmt.Errorf("the event XML could not be read: %v", err)
	}
	sys := x.System
	id, err := strconv.ParseUint(strings.TrimSpace(sys.EventRecordID), 10, 64)
	if err != nil {
		return rawEvent{}, errors.New("the event XML has no EventRecordID")
	}
	at, err := time.Parse(time.RFC3339Nano, sys.TimeCreated.SystemTime)
	if err != nil {
		return rawEvent{}, errors.New("the event XML has no TimeCreated")
	}
	e := rawEvent{RecordID: id, Time: at, Provider: sys.Provider.Name}
	e.Level, _ = strconv.Atoi(strings.TrimSpace(sys.Level))
	e.EventID, _ = strconv.Atoi(strings.TrimSpace(sys.EventID))
	for _, d := range []*dataXML{x.EventData, x.UserData} {
		if d != nil {
			e.Data = append(e.Data, dataValues(d.Inner)...)
		}
	}
	return e, nil
}

// dataValues returns the non-empty texts of the elements of inner, except
// those of Binary elements.
func dataValues(inner []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(inner))
	var out []string
	binary := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "Binary" {
				binary++
			}
		case xml.EndElement:
			if t.Name.Local == "Binary" {
				binary--
			}
		case xml.CharData:
			if s := strings.TrimSpace(string(t)); s != "" && binary == 0 {
				out = append(out, s)
			}
		}
	}
}

// eventMessage returns the message of an event: formatted, with its line
// ends as "\n" and without surrounding space, or the event's data values
// joined when formatted is empty, cut to maxMessageChars characters and
// "…".
func eventMessage(formatted string, data []string) string {
	msg := strings.TrimSpace(strings.ReplaceAll(formatted, "\r\n", "\n"))
	if msg == "" {
		msg = strings.Join(data, "; ")
	}
	if r := []rune(msg); len(r) > maxMessageChars {
		msg = string(r[:maxMessageChars]) + "…"
	}
	return msg
}

// newEvent returns the diag.events record of e with message.
func newEvent(e rawEvent, message string) Event {
	return Event{RecordID: e.RecordID, Time: wire.FormatDiagTime(e.Time), Level: levelName(e.Level), Provider: e.Provider,
		EventID: e.EventID, Message: message}
}
