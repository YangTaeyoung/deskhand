package store

import (
	"errors"
	"testing"
	"time"
)

func TestControlRoundTripAndGenNeverDecreases(t *testing.T) {
	s := openTemp(t)
	if _, err := s.GetControl("pc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("control of a host without a row: %v", err)
	}
	lease := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	c := Control{HostID: "pc", Owner: "ai", OwnerClient: "c-1", OwnerName: "claude", Gen: 3, LeaseUntil: lease, UpdatedAt: lease}
	if err := s.PutControl(c); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetControl("pc"); err != nil || got != c {
		t.Fatalf("control %+v, %v, want %+v", got, err, c)
	}
	c = Control{HostID: "pc", Owner: "human", OwnerClient: "c-2", OwnerName: "app", Gen: 4, AIPaused: true, UpdatedAt: lease}
	if err := s.PutControl(c); err != nil {
		t.Fatal(err)
	}
	if err := s.PutControl(Control{HostID: "pc", Owner: "none", Gen: 2, UpdatedAt: lease}); !errors.Is(err, ErrGenDecrease) {
		t.Fatalf("a lower gen was stored: %v", err)
	}
	if err := s.PutControl(Control{HostID: "other", Owner: "none", Gen: 1, UpdatedAt: lease}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListControls()
	if err != nil || len(list) != 2 || list[0].HostID != "other" || list[1] != c {
		t.Fatalf("controls %+v, %v", list, err)
	}
}
