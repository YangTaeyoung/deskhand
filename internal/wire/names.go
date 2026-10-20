package wire

import "strings"

// WindowsNameFault is why Windows does not hold an entry name as written.
type WindowsNameFault int

const (
	// NameOK: Windows holds the name as written.
	NameOK WindowsNameFault = iota
	// NameBadChar: the name has a control character or one of < > : " | ? *.
	NameBadChar
	// NameTrailing: the name ends with a dot or a space, which Windows drops.
	NameTrailing
	// NameDevice: Windows takes the name for a device
	// (ReservedWindowsName).
	NameDevice
)

// CheckWindowsName returns the fault of the entry name name, one element of
// a path, and for NameBadChar the character.
func CheckWindowsName(name string) (WindowsNameFault, rune) {
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`<>:"|?*`, r) {
			return NameBadChar, r
		}
	}
	switch {
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "):
		return NameTrailing, 0
	case ReservedWindowsName(name):
		return NameDevice, 0
	}
	return NameOK, 0
}

// ReservedWindowsName reports whether Windows takes the name for a device:
// CON, PRN, AUX, NUL, CONIN$, CONOUT$, COM0–COM9, LPT0–LPT9 and COM or LPT
// with ¹, ² or ³, in any letter case, alone or before an extension and
// spaces.
func ReservedWindowsName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if !strings.HasPrefix(stem, "COM") && !strings.HasPrefix(stem, "LPT") {
		return false
	}
	switch d := stem[3:]; d {
	case "¹", "²", "³":
		return true
	default:
		return len(d) == 1 && d[0] >= '0' && d[0] <= '9'
	}
}
