package server

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// desktopFake answers Options.SessionInfo and Options.Displays with values
// a test changes.
type desktopFake struct {
	mu       sync.Mutex
	info     session.Info
	infoErr  error
	displays []display.Info
	dispErr  error
}

func (f *desktopFake) sessionInfo() (session.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info, f.infoErr
}

func (f *desktopFake) list() ([]display.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]display.Info(nil), f.displays...), f.dispErr
}

func (f *desktopFake) set(change func(f *desktopFake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// feed reads the messages of r into a channel until r ends.
func feed(r *proto.Reader) <-chan proto.Message {
	ch := make(chan proto.Message, 256)
	go func() {
		defer close(ch)
		for {
			m, err := r.Next()
			if err != nil {
				return
			}
			ch <- m
		}
	}()
	return ch
}

// nextEvent returns the next event named name within 5 s.
func nextEvent(t *testing.T, ch <-chan proto.Message, name string) proto.Message {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-ch:
			if !ok {
				t.Fatalf("the stream ended before %s", name)
			}
			if m.Type() == "ev" && m.String("name") == name {
				return m
			}
		case <-timeout:
			t.Fatalf("no %s within 5 s", name)
		}
	}
}

// noDesktopEvent fails the test when a session.changed or display.changed
// arrives within d.
func noDesktopEvent(t *testing.T, ch <-chan proto.Message, d time.Duration, while string) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case m, ok := <-ch:
			if !ok {
				t.Fatalf("the stream ended %s", while)
			}
			if n := m.String("name"); m.Type() == "ev" && (n == "session.changed" || n == "display.changed") {
				t.Fatalf("%s %s: %v", n, while, m)
			}
		case <-timeout:
			return
		}
	}
}

// asJSON is v as the JSON values a decoded message holds.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// desktopClient connects to a server whose session and displays come from
// f and whose watcher checks every 20 ms, and returns its hello and the
// messages after it.
func desktopClient(t *testing.T, f *desktopFake) (proto.Message, <-chan proto.Message) {
	t.Helper()
	old := capsCheckInterval
	capsCheckInterval = 20 * time.Millisecond
	t.Cleanup(func() { capsCheckInterval = old })
	_, r, _, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), SessionInfo: f.sessionInfo, Displays: f.list})
	hello := expect(t, r, "hello")
	return hello, feed(r)
}

func TestDisplayAndSessionChangesAreSentAsEvents(t *testing.T) {
	f := &desktopFake{info: testSession, displays: append([]display.Info(nil), testDisplays...)}
	hello, ch := desktopClient(t, f)
	if !reflect.DeepEqual(hello["displays"], wantDisplays(t)) {
		t.Fatalf("hello displays %v", hello["displays"])
	}
	noDesktopEvent(t, ch, 150*time.Millisecond, "while nothing changed since the hello")

	wider := append([]display.Info(nil), testDisplays...)
	wider[1].Width, wider[1].Height = 2560, 1440
	f.set(func(f *desktopFake) { f.displays = wider })
	ev := nextEvent(t, ch, "display.changed")
	if !reflect.DeepEqual(ev["displays"], asJSON(t, wider)) {
		t.Fatalf("display.changed %v", ev)
	}
	noDesktopEvent(t, ch, 150*time.Millisecond, "after the display change was sent")

	locked := testSession
	locked.Locked, locked.InputDesktop = boolPtr(true), session.DesktopWinlogon
	f.set(func(f *desktopFake) { f.info = locked })
	ev = nextEvent(t, ch, "session.changed")
	want := asJSON(t, sessionFields(locked)).(map[string]any)
	if got := onlyKeys(ev, want); !reflect.DeepEqual(got, want) || ev.Has("displays") {
		t.Fatalf("session.changed %v, want the fields %v", ev, want)
	}
	noDesktopEvent(t, ch, 150*time.Millisecond, "after the session change was sent")
}

func TestUnreadableSessionAndDisplaysSendNothingUntilTheyDiffer(t *testing.T) {
	f := &desktopFake{info: testSession, displays: append([]display.Info(nil), testDisplays...)}
	_, ch := desktopClient(t, f)
	f.set(func(f *desktopFake) {
		f.infoErr = errors.New("WTSQuerySessionInformation failed")
		f.dispErr = errors.New("EnumDisplayMonitors failed")
	})
	noDesktopEvent(t, ch, 150*time.Millisecond, "while neither can be read")
	f.set(func(f *desktopFake) { f.infoErr, f.dispErr = nil, nil })
	noDesktopEvent(t, ch, 150*time.Millisecond, "once both read as in the hello again")
	f.set(func(f *desktopFake) { f.displays = f.displays[:1] })
	if ev := nextEvent(t, ch, "display.changed"); !reflect.DeepEqual(ev["displays"], asJSON(t, testDisplays[:1])) {
		t.Fatalf("display.changed %v", ev)
	}
}

func TestEachConnectionIsComparedWithItsOwnHello(t *testing.T) {
	f := &desktopFake{info: testSession, displays: append([]display.Info(nil), testDisplays...)}
	old := capsCheckInterval
	capsCheckInterval = 20 * time.Millisecond
	t.Cleanup(func() { capsCheckInterval = old })
	_, r, _, first := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), SessionInfo: f.sessionInfo, Displays: f.list})
	expect(t, r, "hello")
	chA := feed(r)
	f.set(func(f *desktopFake) { f.displays = f.displays[:1] })
	nextEvent(t, chA, "display.changed")
	_, rB, _ := dialAgent(t, first.RemoteAddr().String())
	helloB := expect(t, rB, "hello")
	if !reflect.DeepEqual(helloB["displays"], asJSON(t, testDisplays[:1])) {
		t.Fatalf("the second hello's displays %v", helloB["displays"])
	}
	noDesktopEvent(t, feed(rB), 150*time.Millisecond, "on a connection whose hello carried the displays")
}
