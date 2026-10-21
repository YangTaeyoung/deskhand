package session

import "testing"

func TestLockStateFollowsTheSessionFlags(t *testing.T) {
	for _, c := range []struct {
		flags int32
		read  bool
		want  string
	}{
		{0, true, "true"},
		{1, true, "false"},
		{-1, true, "unknown"},
		{7, true, "unknown"},
		{0, false, "unknown"},
	} {
		got := "unknown"
		if l := lockState(c.flags, c.read); l != nil {
			got = map[bool]string{true: "true", false: "false"}[*l]
		}
		if got != c.want {
			t.Errorf("lockState(%d, %v) = %s, want %s", c.flags, c.read, got, c.want)
		}
	}
}

func TestIsLockedOnlyForAKnownLock(t *testing.T) {
	yes, no := true, false
	if (Info{}).IsLocked() || (Info{Locked: &no}).IsLocked() || !(Info{Locked: &yes}).IsLocked() {
		t.Fatal("IsLocked does not follow Locked")
	}
}
