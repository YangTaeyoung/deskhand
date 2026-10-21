package observe

import (
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestTypeAutoChoosesMethod(t *testing.T) {
	cases := []struct {
		name, text, method, want string
		events                   int
	}{
		{"short ASCII", "hello, world!\n", "auto", "unicode", 1},
		{"short Hangul with tab and CRLF", "안녕하세요\t반가워요\r\n", "", "unicode", 1},
		{"2000 characters", strings.Repeat("가", autoUnicodeMax), "auto", "unicode", 1},
		{"one character past 2000", strings.Repeat("가", autoUnicodeMax+1), "auto", "paste", 1},
		{"CRLF counts two characters", strings.Repeat("\r\n", autoUnicodeMax/2) + "a", "auto", "paste", 1},
		{"emoji outside the BMP", "완료 ✅ 🎉", "auto", "paste", 1},
		{"a control character", "a\x07b", "auto", "paste", 1},
		{"explicit unicode stays", "🎉", "unicode", "unicode", 1},
		{"explicit paste stays", "abc", "paste", "paste", 1},
		{"past the agent's limit splits", strings.Repeat("x", maxTextEvent+5), "auto", "paste", 2},
	}
	for _, c := range cases {
		evs, method, err := TextEvents(c.text, c.method)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if method != c.want || len(evs) != c.events {
			t.Fatalf("%s: method %s with %d events, want %s with %d", c.name, method, len(evs), c.want, c.events)
		}
		var joined strings.Builder
		for _, ev := range evs {
			if ev.K != "text" || ev.Method != c.want {
				t.Fatalf("%s: event %+v", c.name, ev)
			}
			if n := agentChars(ev.Text); n > maxTextEvent {
				t.Fatalf("%s: an event of %d characters", c.name, n)
			}
			joined.WriteString(ev.Text)
		}
		if joined.String() != c.text {
			t.Fatalf("%s: the events do not carry the text", c.name)
		}
	}

	evs, _, err := TextEvents(strings.Repeat("a", maxTextEvent-1)+"\r\n"+"b", "paste")
	if err != nil || len(evs) != 2 || !strings.HasSuffix(evs[0].Text, "a") || !strings.HasPrefix(evs[1].Text, "\r\n") {
		t.Fatalf("a split keeps CRLF together: %d events, %v", len(evs), err)
	}

	for _, bad := range []struct{ text, method string }{{"", "auto"}, {"abc", "keys"}} {
		if _, _, err := TextEvents(bad.text, bad.method); apierr.CodeOf(err) != wire.CodeInvalidArgument {
			t.Fatalf("%q/%q: %v", bad.text, bad.method, err)
		}
	}
}
