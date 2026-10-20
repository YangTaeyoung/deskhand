package wire

import "testing"

func TestCheckWindowsNameFindsWhatWindowsCannotHold(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault WindowsNameFault
		r     rune
	}{
		{"report.txt", NameOK, 0},
		{"보고서 (1).hwp", NameOK, 0},
		{"CONSOLE.txt", NameOK, 0},
		{"COM10", NameOK, 0},
		{"a:b", NameBadChar, ':'},
		{"what?", NameBadChar, '?'},
		{`a\b`, NameOK, 0},
		{"tab\there", NameBadChar, '\t'},
		{"x<y>", NameBadChar, '<'},
		{"ends.", NameTrailing, 0},
		{"ends ", NameTrailing, 0},
		{"CON", NameDevice, 0},
		{"con.txt", NameDevice, 0},
		{"NUL .log", NameDevice, 0},
		{"Com1.tar.gz", NameDevice, 0},
		{"LPT9", NameDevice, 0},
		{"LPT0", NameDevice, 0},
		{"COM¹", NameDevice, 0},
		{"CONIN$", NameDevice, 0},
	} {
		fault, r := CheckWindowsName(tc.name)
		if fault != tc.fault || r != tc.r {
			t.Errorf("CheckWindowsName(%q) = %v %q, want %v %q", tc.name, fault, r, tc.fault, tc.r)
		}
	}
}
