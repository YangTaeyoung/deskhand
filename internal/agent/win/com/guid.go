// Package com calls COM interfaces through their vtables with
// syscall.SyscallN and describes their results: GUIDs and HRESULTs.
package com

import (
	"fmt"
	"strconv"
	"strings"
)

// GUID is the in-memory layout of a Windows GUID.
type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// ParseGUID reads a GUID written as xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx in
// hex of either case, optionally inside braces.
func ParseGUID(s string) (GUID, error) {
	in := s
	if strings.HasPrefix(s, "{") || strings.HasSuffix(s, "}") {
		if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
			return GUID{}, fmt.Errorf("GUID %q has an unmatched brace", in)
		}
		s = s[1 : len(s)-1]
	}
	parts := strings.Split(s, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		return GUID{}, fmt.Errorf("GUID %q is not of the form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", in)
	}
	hex := func(x string) (uint64, error) {
		v, err := strconv.ParseUint(x, 16, 64)
		if err != nil {
			return 0, fmt.Errorf("GUID %q has a non-hex digit", in)
		}
		return v, nil
	}
	var g GUID
	d1, err := hex(parts[0])
	if err != nil {
		return GUID{}, err
	}
	d2, err := hex(parts[1])
	if err != nil {
		return GUID{}, err
	}
	d3, err := hex(parts[2])
	if err != nil {
		return GUID{}, err
	}
	g.Data1, g.Data2, g.Data3 = uint32(d1), uint16(d2), uint16(d3)
	tail := parts[3] + parts[4]
	for i := range g.Data4 {
		b, err := hex(tail[2*i : 2*i+2])
		if err != nil {
			return GUID{}, err
		}
		g.Data4[i] = byte(b)
	}
	return g, nil
}

// MustGUID is ParseGUID for constant strings; it panics on a malformed one.
func MustGUID(s string) GUID {
	g, err := ParseGUID(s)
	if err != nil {
		panic(err)
	}
	return g
}

// String writes g as lower-case xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx.
func (g GUID) String() string {
	return fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%x", g.Data1, g.Data2, g.Data3, g.Data4[0], g.Data4[1], g.Data4[2:])
}
