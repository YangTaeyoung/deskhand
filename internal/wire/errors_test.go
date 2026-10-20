package wire

import "testing"

func TestErrorCodeValues(t *testing.T) {
	got := map[string]string{
		"OFFLINE": CodeOffline, "AUTH_REQUIRED": CodeAuthRequired, "SESSION_LOCKED": CodeSessionLocked,
		"SESSION_CHANGED": CodeSessionChanged, "CONTROL_HELD_BY_HUMAN": CodeControlHeldByHuman,
		"CONTROL_PREEMPTED": CodeControlPreempted, "STALE_TARGET": CodeStaleTarget,
		"DISPLAY_UNAVAILABLE": CodeDisplayUnavailable, "ELEVATION_REQUIRED": CodeElevationRequired,
		"CAPABILITY_UNSUPPORTED": CodeCapabilityUnsupported, "PORT_CONFLICT": CodePortConflict,
		"TRANSFER_CONFLICT": CodeTransferConflict, "HASH_MISMATCH": CodeHashMismatch,
		"DISK_FULL": CodeDiskFull, "NOT_FOUND": CodeNotFound, "PERMISSION_DENIED": CodePermissionDenied,
		"INVALID_ARGUMENT": CodeInvalidArgument, "IN_PROGRESS": CodeInProgress,
		"EXECUTION_OUTCOME_UNKNOWN": CodeExecutionOutcomeUnknown, "TIMEOUT": CodeTimeout, "INTERNAL": CodeInternal,
	}
	for want, c := range got {
		if c != want {
			t.Errorf("got %q want %q", c, want)
		}
	}
	if len(got) != 21 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestKindValues(t *testing.T) {
	if KindAU != 0x01 || KindVideoInfo != 0x02 || KindVideoStats != 0x03 || KindVideoPaused != 0x04 ||
		KindCursorShape != 0x10 || KindCursorPos != 0x11 || KindPTYData != 0x20 || KindPTYResize != 0x21 ||
		KindPTYExit != 0x22 || KindPTYAttached != 0x23 || KindNotice != 0x30 ||
		KindClientControl != 0x40 || KindClientInput != 0x41 || KindInputAck != 0x42 {
		t.Fatal("kind constant mismatch")
	}
	if ALPN != "deskhand/1" || MaxFrame != 32<<20 || MaxHeader != 64<<10 {
		t.Fatal("constant mismatch")
	}
}
