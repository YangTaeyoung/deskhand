package observe

import (
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// press is one key event of a parsed sequence: a scan code (ext for E0
// keys) or a virtual key, down or up.
type press struct {
	sc   uint16
	ext  bool
	vk   uint16
	down bool
}

func pressesOf(evs []wire.InputEvent) []press {
	out := make([]press, 0, len(evs))
	for _, ev := range evs {
		out = append(out, press{sc: ev.SC, ext: ev.Ext, vk: ev.VK, down: ev.Down})
	}
	return out
}

func samePresses(a, b []press) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestKeysParsing(t *testing.T) {
	cases := []struct {
		keys  string
		want  []press
		canon string
	}{
		{"ctrl+shift+esc", []press{{sc: 0x1d, down: true}, {sc: 0x2a, down: true}, {sc: 0x01, down: true},
			{sc: 0x01}, {sc: 0x2a}, {sc: 0x1d}}, "ctrl+shift+esc"},
		{"enter", []press{{sc: 0x1c, down: true}, {sc: 0x1c}}, "enter"},
		{"Alt+F4", []press{{sc: 0x38, down: true}, {sc: 0x3e, down: true}, {sc: 0x3e}, {sc: 0x38}}, "alt+f4"},
		{"win+r", []press{{sc: 0x5b, ext: true, down: true}, {sc: 0x13, down: true}, {sc: 0x13}, {sc: 0x5b, ext: true}}, "win+r"},
		{"한영", []press{{vk: 0x15, down: true}, {vk: 0x15}}, "hangul"},
		{"한/영", []press{{vk: 0x15, down: true}, {vk: 0x15}}, "hangul"},
		{"한자", []press{{vk: 0x19, down: true}, {vk: 0x19}}, "hanja"},
		{"ctrl+a delete", []press{{sc: 0x1d, down: true}, {sc: 0x1e, down: true}, {sc: 0x1e}, {sc: 0x1d},
			{sc: 0x53, ext: true, down: true}, {sc: 0x53, ext: true}}, "ctrl+a delete"},
		{"Control+Page_Down", []press{{sc: 0x1d, down: true}, {sc: 0x51, ext: true, down: true}, {sc: 0x51, ext: true}, {sc: 0x1d}}, "ctrl+pagedown"},
		{"rctrl+ralt+up", []press{{sc: 0x1d, ext: true, down: true}, {sc: 0x38, ext: true, down: true}, {sc: 0x48, ext: true, down: true},
			{sc: 0x48, ext: true}, {sc: 0x38, ext: true}, {sc: 0x1d, ext: true}}, "rctrl+ralt+up"},
		{"ctrl+=", []press{{sc: 0x1d, down: true}, {sc: 0x0d, down: true}, {sc: 0x0d}, {sc: 0x1d}}, "ctrl+="},
		{"ctrl+-", []press{{sc: 0x1d, down: true}, {sc: 0x0c, down: true}, {sc: 0x0c}, {sc: 0x1d}}, "ctrl+-"},
		{"shift+/", []press{{sc: 0x2a, down: true}, {sc: 0x35, down: true}, {sc: 0x35}, {sc: 0x2a}}, "shift+/"},
		{"f12", []press{{sc: 0x58, down: true}, {sc: 0x58}}, "f12"},
		{"f13", []press{{vk: 0x7c, down: true}, {vk: 0x7c}}, "f13"},
		{"printscreen", []press{{vk: 0x2c, down: true}, {vk: 0x2c}}, "printscreen"},
		{"numpad_enter", []press{{sc: 0x1c, ext: true, down: true}, {sc: 0x1c, ext: true}}, "numpadenter"},
		{"shift", []press{{sc: 0x2a, down: true}, {sc: 0x2a}}, "shift"},
		{"  tab   space ", []press{{sc: 0x0f, down: true}, {sc: 0x0f}, {sc: 0x39, down: true}, {sc: 0x39}}, "tab space"},
	}
	for _, c := range cases {
		evs, canon, err := ParseKeys(c.keys)
		if err != nil {
			t.Fatalf("%q: %v", c.keys, err)
		}
		for _, ev := range evs {
			if ev.K != "key" {
				t.Fatalf("%q: event kind %q", c.keys, ev.K)
			}
		}
		if got := pressesOf(evs); !samePresses(got, c.want) {
			t.Fatalf("%q: presses %+v, want %+v", c.keys, got, c.want)
		}
		if canon != c.canon {
			t.Fatalf("%q: canonical %q, want %q", c.keys, canon, c.canon)
		}
	}

	for _, bad := range []string{"", "   ", "cmd+c", "ctrl+", "ctrl++", "a+b", "ctrl+ctrl+a", "ctrl+shift+nosuchkey", "enter+ctrl"} {
		_, _, err := ParseKeys(bad)
		e := apierr.As(err)
		if err == nil || e.Code != wire.CodeInvalidArgument || e.NextAction != apierr.NextFixParams {
			t.Fatalf("%q: %+v", bad, err)
		}
		if bad == "cmd+c" && (!strings.Contains(e.Message, "cmd") || !strings.Contains(e.Message, "ctrl") || !strings.Contains(e.Message, "pagedown") ||
			!strings.Contains(e.Message, "hangul")) {
			t.Fatalf("an unknown name lists the names it knows: %s", e.Message)
		}
	}
	if _, _, err := ParseKeys(strings.Repeat("a ", maxChords+1)); apierr.CodeOf(err) != wire.CodeInvalidArgument {
		t.Fatalf("%d chords: %v", maxChords+1, err)
	}
}
