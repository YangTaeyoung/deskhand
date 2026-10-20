package com

import "testing"

func TestMustGUIDLayout(t *testing.T) {
	g := MustGUID("{770AAE78-F26F-4DBA-A829-253C83D1B387}")
	want := GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba, Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
	if g != want {
		t.Fatalf("MustGUID = %+v, want %+v", g, want)
	}
	if s := g.String(); s != "770aae78-f26f-4dba-a829-253c83d1b387" {
		t.Fatalf("String() = %q", s)
	}
	if g2 := MustGUID(g.String()); g2 != g {
		t.Fatalf("round trip %+v", g2)
	}
}

func TestParseGUIDRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "770aae78-f26f-4dba-a829", "770aae78-f26f-4dba-a829-253c83d1b38z", "770aae78f26f-4dba-a829-253c83d1b387-", "{770aae78-f26f-4dba-a829-253c83d1b387"} {
		if _, err := ParseGUID(s); err == nil {
			t.Errorf("ParseGUID(%q) accepted", s)
		}
	}
}
