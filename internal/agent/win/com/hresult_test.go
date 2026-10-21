package com

import (
	"errors"
	"fmt"
	"testing"
)

func TestHRESULTFormat(t *testing.T) {
	for _, c := range []struct {
		hr   HRESULT
		want string
	}{
		{0x80070005, "E_ACCESSDENIED (0x80070005)"},
		{0x887A0022, "DXGI_ERROR_NOT_CURRENTLY_AVAILABLE (0x887A0022)"},
		{0, "S_OK (0x00000000)"},
		{0x8badf00d, "HRESULT 0x8BADF00D"},
	} {
		if got := c.hr.Error(); got != c.want {
			t.Errorf("HRESULT(0x%08X).Error() = %q, want %q", uint32(c.hr), got, c.want)
		}
	}
}

func TestHRESULTFailedFollowsTheSeverityBit(t *testing.T) {
	if !E_ACCESSDENIED.Failed() || E_ACCESSDENIED.Succeeded() {
		t.Fatal("E_ACCESSDENIED is not a failure")
	}
	if S_OK.Failed() || S_FALSE.Failed() || !S_FALSE.Succeeded() {
		t.Fatal("S_OK or S_FALSE is a failure")
	}
}

func TestHRESULTErrIsNilOnSuccess(t *testing.T) {
	if S_OK.Err() != nil || S_FALSE.Err() != nil {
		t.Fatal("a success has an error")
	}
	err := fmt.Errorf("DuplicateOutput: %w", E_ACCESSDENIED.Err())
	var hr HRESULT
	if !errors.As(err, &hr) || hr != E_ACCESSDENIED {
		t.Fatalf("errors.As(%v) = %v", err, hr)
	}
}
