package observe

import (
	"fmt"
	"slices"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// maxChords bounds the chords of one input.key.
const maxChords = 50

// keyDef is how a key is pressed: by set 1 scan code (ext for an E0 key)
// or, for vk, by virtual key.
type keyDef struct {
	sc       uint16
	ext      bool
	vk       uint16
	modifier bool
}

// keyNames maps a canonical key name of input.key to its key.
var keyNames = map[string]keyDef{
	"ctrl": {sc: 0x1d, modifier: true}, "lctrl": {sc: 0x1d, modifier: true}, "rctrl": {sc: 0x1d, ext: true, modifier: true},
	"shift": {sc: 0x2a, modifier: true}, "lshift": {sc: 0x2a, modifier: true}, "rshift": {sc: 0x36, modifier: true},
	"alt": {sc: 0x38, modifier: true}, "lalt": {sc: 0x38, modifier: true}, "ralt": {sc: 0x38, ext: true, modifier: true},
	"win": {sc: 0x5b, ext: true, modifier: true}, "lwin": {sc: 0x5b, ext: true, modifier: true}, "rwin": {sc: 0x5c, ext: true, modifier: true},

	"esc": {sc: 0x01}, "backspace": {sc: 0x0e}, "tab": {sc: 0x0f}, "enter": {sc: 0x1c}, "space": {sc: 0x39}, "capslock": {sc: 0x3a},
	"scrolllock": {sc: 0x46}, "menu": {sc: 0x5d, ext: true},
	"insert": {sc: 0x52, ext: true}, "delete": {sc: 0x53, ext: true}, "home": {sc: 0x47, ext: true}, "end": {sc: 0x4f, ext: true},
	"pageup": {sc: 0x49, ext: true}, "pagedown": {sc: 0x51, ext: true},
	"up": {sc: 0x48, ext: true}, "down": {sc: 0x50, ext: true}, "left": {sc: 0x4b, ext: true}, "right": {sc: 0x4d, ext: true},

	"f1": {sc: 0x3b}, "f2": {sc: 0x3c}, "f3": {sc: 0x3d}, "f4": {sc: 0x3e}, "f5": {sc: 0x3f}, "f6": {sc: 0x40}, "f7": {sc: 0x41},
	"f8": {sc: 0x42}, "f9": {sc: 0x43}, "f10": {sc: 0x44}, "f11": {sc: 0x57}, "f12": {sc: 0x58},
	"f13": {vk: 0x7c}, "f14": {vk: 0x7d}, "f15": {vk: 0x7e}, "f16": {vk: 0x7f}, "f17": {vk: 0x80}, "f18": {vk: 0x81},
	"f19": {vk: 0x82}, "f20": {vk: 0x83}, "f21": {vk: 0x84}, "f22": {vk: 0x85}, "f23": {vk: 0x86}, "f24": {vk: 0x87},

	"q": {sc: 0x10}, "w": {sc: 0x11}, "e": {sc: 0x12}, "r": {sc: 0x13}, "t": {sc: 0x14}, "y": {sc: 0x15}, "u": {sc: 0x16},
	"i": {sc: 0x17}, "o": {sc: 0x18}, "p": {sc: 0x19}, "a": {sc: 0x1e}, "s": {sc: 0x1f}, "d": {sc: 0x20}, "f": {sc: 0x21},
	"g": {sc: 0x22}, "h": {sc: 0x23}, "j": {sc: 0x24}, "k": {sc: 0x25}, "l": {sc: 0x26}, "z": {sc: 0x2c}, "x": {sc: 0x2d},
	"c": {sc: 0x2e}, "v": {sc: 0x2f}, "b": {sc: 0x30}, "n": {sc: 0x31}, "m": {sc: 0x32},
	"1": {sc: 0x02}, "2": {sc: 0x03}, "3": {sc: 0x04}, "4": {sc: 0x05}, "5": {sc: 0x06}, "6": {sc: 0x07}, "7": {sc: 0x08},
	"8": {sc: 0x09}, "9": {sc: 0x0a}, "0": {sc: 0x0b},
	"-": {sc: 0x0c}, "=": {sc: 0x0d}, "[": {sc: 0x1a}, "]": {sc: 0x1b}, ";": {sc: 0x27}, "'": {sc: 0x28}, "`": {sc: 0x29},
	"\\": {sc: 0x2b}, ",": {sc: 0x33}, ".": {sc: 0x34}, "/": {sc: 0x35},

	"numpad0": {sc: 0x52}, "numpad1": {sc: 0x4f}, "numpad2": {sc: 0x50}, "numpad3": {sc: 0x51}, "numpad4": {sc: 0x4b},
	"numpad5": {sc: 0x4c}, "numpad6": {sc: 0x4d}, "numpad7": {sc: 0x47}, "numpad8": {sc: 0x48}, "numpad9": {sc: 0x49},
	"numpaddecimal": {sc: 0x53}, "numpadadd": {sc: 0x4e}, "numpadsubtract": {sc: 0x4a}, "numpadmultiply": {sc: 0x37},
	"numpaddivide": {sc: 0x35, ext: true}, "numpadenter": {sc: 0x1c, ext: true},

	"printscreen": {vk: 0x2c}, "pause": {vk: 0x13}, "numlock": {vk: 0x90},
	"hangul": {vk: 0x15}, "hanja": {vk: 0x19},
	"volumemute": {vk: 0xad}, "volumedown": {vk: 0xae}, "volumeup": {vk: 0xaf},
	"medianext": {vk: 0xb0}, "mediaprev": {vk: 0xb1}, "mediastop": {vk: 0xb2}, "mediaplaypause": {vk: 0xb3},
}

// keyAliases maps other names of a key to its canonical name.
var keyAliases = map[string]string{
	"control": "ctrl", "lcontrol": "lctrl", "rcontrol": "rctrl", "altgr": "ralt", "windows": "win",
	"escape": "esc", "return": "enter", "del": "delete", "ins": "insert", "pgup": "pageup", "pgdn": "pagedown",
	"apps": "menu", "contextmenu": "menu", "prtsc": "printscreen", "break": "pause",
	"minus": "-", "equal": "=", "bracketleft": "[", "bracketright": "]", "semicolon": ";", "quote": "'", "backquote": "`",
	"backslash": "\\", "comma": ",", "period": ".", "slash": "/",
	"한영": "hangul", "한/영": "hangul", "한자": "hanja",
}

// keyName returns the canonical name of a key name of input.key: lower
// case, without "_" in names longer than one character, aliases resolved.
func keyName(s string) string {
	n := strings.ToLower(s)
	if len([]rune(n)) > 1 {
		n = strings.ReplaceAll(n, "_", "")
	}
	if a, ok := keyAliases[n]; ok {
		return a
	}
	return n
}

// knownKeyNames returns the canonical key names in sorted order.
func knownKeyNames() string {
	names := make([]string, 0, len(keyNames))
	for n := range keyNames {
		names = append(names, n)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func keysInvalid(format string, a ...any) error {
	return apierr.Invalid(fmt.Sprintf(format, a...))
}

// ParseKeys turns keys, chords separated by spaces such as "ctrl+shift+esc
// enter", into key events: each chord presses its keys in order and
// releases them in reverse. A chord is modifiers (ctrl, shift, alt, win and
// their left and right forms) followed by at most one other key. It also
// returns the keys with canonical names.
func ParseKeys(keys string) ([]wire.InputEvent, string, error) {
	chords := strings.Fields(keys)
	switch {
	case len(chords) == 0:
		return nil, "", keysInvalid("keys가 비어 있습니다. ctrl+shift+esc나 enter처럼 누를 키를 주세요")
	case len(chords) > maxChords:
		return nil, "", keysInvalid("keys의 조합이 %d개로 한 번에 보낼 수 있는 %d개를 넘습니다. 나눠 보내세요", len(chords), maxChords)
	}
	var evs []wire.InputEvent
	canon := make([]string, 0, len(chords))
	for _, chord := range chords {
		defs, names, err := parseChord(chord)
		if err != nil {
			return nil, "", err
		}
		for _, d := range defs {
			evs = append(evs, d.event(true))
		}
		for i := len(defs) - 1; i >= 0; i-- {
			evs = append(evs, defs[i].event(false))
		}
		canon = append(canon, strings.Join(names, "+"))
	}
	return evs, strings.Join(canon, " "), nil
}

// parseChord returns the keys of one chord in pressing order and their
// canonical names.
func parseChord(chord string) ([]keyDef, []string, error) {
	parts := strings.Split(chord, "+")
	defs := make([]keyDef, 0, len(parts))
	names := make([]string, 0, len(parts))
	for i, p := range parts {
		if p == "" {
			return nil, nil, keysInvalid("%q에 빈 키 이름이 있습니다. 키는 +로 잇고, + 글자는 = 키(shift+=)나 numpad_add로 주세요", chord)
		}
		n := keyName(p)
		d, ok := keyNames[n]
		if !ok {
			return nil, nil, keysInvalid("키 이름 %q를 모릅니다(Windows 키 이름을 씁니다). 다음 이름 중에서 고르세요: %s", p, knownKeyNames())
		}
		if slices.Contains(names, n) {
			return nil, nil, keysInvalid("%q에 %s가 두 번 있습니다. 키마다 한 번만 주세요", chord, n)
		}
		if i > 0 && !defs[i-1].modifier {
			return nil, nil, keysInvalid("%q에서 %s 뒤에 다른 키가 있습니다. 한 조합은 ctrl·shift·alt·win 뒤에 키 하나만 둡니다", chord, names[i-1])
		}
		defs = append(defs, d)
		names = append(names, n)
	}
	return defs, names, nil
}

// event is the key event that presses (down) or releases d.
func (d keyDef) event(down bool) wire.InputEvent {
	if d.vk != 0 {
		return wire.InputEvent{K: "key", VK: d.vk, Down: down}
	}
	return wire.InputEvent{K: "key", SC: d.sc, Ext: d.ext, Down: down}
}
