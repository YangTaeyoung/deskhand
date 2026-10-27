package input

import (
	"errors"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

type atomicWriter interface {
	WithWriter(uint64, func() error) error
}

func TestWriterChangeWaitsForAShortAdmittedAction(t *testing.T) {
	j := newTestInjector(t, &fakeSystem{}, userSession, oneDisplay, Options{})
	w, ok := any(j).(atomicWriter)
	if !ok {
		t.Fatal("input cannot serialize a short action with writer changes")
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- w.WithWriter(1, func() error { close(started); <-release; return nil })
	}()
	<-started
	changed := make(chan uint64, 1)
	go func() { changed <- j.SetWriter(2) }()
	select {
	case gen := <-changed:
		close(release)
		t.Fatalf("writer changed to %d while its prior action was still running", gen)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case gen := <-changed:
		if gen != 2 {
			t.Fatalf("writer became %d", gen)
		}
	case <-time.After(time.Second):
		t.Fatal("writer change remained blocked after the action")
	}
	ran := false
	err := w.WithWriter(1, func() error { ran = true; return nil })
	var ie *Error
	if ran || !errors.As(err, &ie) || ie.Code != wire.CodeControlPreempted {
		t.Fatalf("stale action ran=%t err=%v", ran, err)
	}
}

func TestShortWriterActionAllowsReentrantPreemptionCallbacks(t *testing.T) {
	var j *Injector
	j = newTestInjector(t, &fakeSystem{}, userSession, oneDisplay, Options{Preempted: func(p Preemption) {
		if p.Gen == 1 {
			j.SetWriter(2)
		}
	}})
	w, ok := any(j).(atomicWriter)
	if !ok {
		t.Fatal("input cannot serialize a short action with writer changes")
	}
	done := make(chan error, 1)
	ran := false
	go func() { done <- w.WithWriter(1, func() error { ran = true; return nil }) }()
	select {
	case err := <-done:
		var ie *Error
		if ran || !errors.As(err, &ie) || ie.Code != wire.CodeControlPreempted {
			t.Fatalf("action after reentrant takeover ran=%t err=%v", ran, err)
		}
	case <-time.After(time.Second):
		t.Fatal("preemption callback deadlocked")
	}
	marker := errors.New("action result")
	if err := w.WithWriter(2, func() error { return marker }); err != marker {
		t.Fatalf("action result %v", err)
	}
}
