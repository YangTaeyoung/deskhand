package input

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var (
	enterDown = raw{vk: vkReturn, scan: scanReturn}
	enterUp   = raw{vk: vkReturn, scan: scanReturn, flags: keyeventfKeyUp}
	tabDown   = raw{vk: vkTab, scan: scanTab}
	tabUp     = raw{vk: vkTab, scan: scanTab, flags: keyeventfKeyUp}
)

func TestUnicodeSurrogatePairs(t *testing.T) {
	if got, want := unicodeInputs("😀"), []raw{
		unicodeRaw(0xD83D, false), unicodeRaw(0xD83D, true), unicodeRaw(0xDE00, false), unicodeRaw(0xDE00, true),
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("😀 gives %+v, want %+v", got, want)
	}
	if got, want := unicodeInputs("가\n"), []raw{
		unicodeRaw('가', false), unicodeRaw('가', true), enterDown, enterUp,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("가\\n gives %+v, want %+v", got, want)
	}
}

func TestLineBreaksAndTabsBecomeKeys(t *testing.T) {
	got := unicodeInputs("a\r\nb\tc\rd")
	want := []raw{
		unicodeRaw('a', false), unicodeRaw('a', true), enterDown, enterUp,
		unicodeRaw('b', false), unicodeRaw('b', true), tabDown, tabUp,
		unicodeRaw('c', false), unicodeRaw('c', true), enterDown, enterUp,
		unicodeRaw('d', false), unicodeRaw('d', true),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestTextIsSentInChunksThatWaitForTheWindowToReadThem(t *testing.T) {
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	text := strings.Repeat("한", 2*chunkChars) + "\r\n" + strings.Repeat("😀", chunkChars-2)
	mustOK(t, j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: text}))
	calls := sys.sent()
	if len(calls) != 3 || len(calls[0]) != 2*chunkChars || len(calls[2]) != 2+4*(chunkChars-2) {
		t.Fatalf("%d sends", len(calls))
	}
	var all []raw
	for _, c := range calls {
		all = append(all, c...)
	}
	if !reflect.DeepEqual(all, unicodeInputs(text)) {
		t.Fatal("the chunks do not add up to the text")
	}
	if n := sys.drainCount(); n != 2 {
		t.Fatalf("%d drains for three chunks, want one before each chunk after the first", n)
	}
	mustOK(t, j.Apply(wire.InputEvent{Seq: 2, K: "text", Text: "a"}))
	if n := sys.drainCount(); n != 3 {
		t.Fatalf("%d drains; a text right after another waits for it to be read", n)
	}
	j.lastText.Store(time.Now().Add(-2 * drainAfter).UnixNano())
	mustOK(t, j.Apply(wire.InputEvent{Seq: 3, K: "text", Text: "b"}))
	if n := sys.drainCount(); n != 3 {
		t.Fatalf("%d drains; a text long after the last does not wait", n)
	}
	if p := j.Pressed(); len(p) != 0 {
		t.Fatalf("pressed %+v", p)
	}
}

func TestTextStopsWhenTheWindowStopsReading(t *testing.T) {
	sys := &fakeSystem{drainErr: func(call int) error {
		if call == 1 {
			return errors.New("busy")
		}
		return nil
	}}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	ack := j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: strings.Repeat("가", 3*chunkChars)})
	mustFail(t, ack, wire.CodeTimeout)
	if !strings.Contains(ack.Error, "typed 200 of 300") {
		t.Fatalf("ack %+v", ack)
	}
	if n := len(sys.sent()); n != 2 {
		t.Fatalf("%d sends", n)
	}
}

func TestLongTextIsCutByAPreemption(t *testing.T) {
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	j.SetWriter(1)
	sys.hook = func(call int, in []raw) {
		if call == 2 {
			go j.SetWriter(2)
			for j.Gen() != 2 {
				time.Sleep(time.Millisecond)
			}
		}
	}
	ack := j.Apply(wire.InputEvent{Seq: 2, Gen: 1, K: "text", Text: strings.Repeat("가", 6000)})
	mustFail(t, ack, wire.CodeControlPreempted)
	if !strings.Contains(ack.Error, "300 of 6000") {
		t.Fatalf("ack %+v", ack)
	}
	downs := 0
	for _, r := range sys.all() {
		if r.flags == keyeventfUnicode {
			downs++
		}
	}
	if downs != 3*chunkChars {
		t.Fatalf("%d characters typed after the preemption, want %d", downs, 3*chunkChars)
	}
}

func TestPasteRestoresTheClipboardAroundCtrlV(t *testing.T) {
	clip := &fakeClipboard{text: "사용자의 클립보드"}
	sys := &fakeSystem{clip: clip}
	sys.hook = func(call int, in []raw) {
		for _, r := range in {
			clip.record(rawName(r))
		}
	}
	written := 0
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{ClipboardWritten: func() { written++ }})
	mustOK(t, j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: "붙여넣기 😀", Method: "paste"}))
	want := []string{"save 사용자의 클립보드", "set 붙여넣기 😀", "vk 11 down", "vk 56 down", "vk 56 up", "vk 11 up", "restore 사용자의 클립보드"}
	if got := clip.entries(); !reflect.DeepEqual(got, want) {
		t.Fatalf("clipboard and keys %q, want %q", got, want)
	}
	if written != 2 {
		t.Fatalf("ClipboardWritten ran %d times, want 2", written)
	}
	if p := j.Pressed(); len(p) != 0 {
		t.Fatalf("pressed %+v", p)
	}
}

func TestPasteWaitsForTheTargetToDrainBeforeRestoring(t *testing.T) {
	clip := &fakeClipboard{text: "old"}
	sys := &fakeSystem{clip: clip}
	entered, release := make(chan struct{}), make(chan struct{})
	sys.drainErr = func(int) error {
		close(entered)
		<-release
		return nil
	}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	done := make(chan wire.InputAck, 1)
	go func() { done <- j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: "new", Method: "paste"}) }()
	<-entered
	if clip.current() != "new" {
		t.Fatalf("clipboard %q while the drain runs", clip.current())
	}
	for _, e := range clip.entries() {
		if strings.HasPrefix(e, "restore") {
			t.Fatalf("restored before the drain finished: %q", clip.entries())
		}
	}
	close(release)
	mustOK(t, <-done)
	if clip.current() != "old" {
		t.Fatalf("clipboard %q", clip.current())
	}
}

func TestPasteWaitsTheMinimumBeforeDrainingAndRestoring(t *testing.T) {
	clip := &fakeClipboard{text: "old"}
	sys := &fakeSystem{clip: clip}
	var mu sync.Mutex
	var pressed, drained, restored time.Time
	sys.hook = func(call int, in []raw) {
		mu.Lock()
		pressed = time.Now()
		mu.Unlock()
	}
	sys.drainErr = func(int) error {
		mu.Lock()
		drained = time.Now()
		mu.Unlock()
		return nil
	}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{ClipboardWritten: func() {
		if clip.current() == "old" {
			mu.Lock()
			restored = time.Now()
			mu.Unlock()
		}
	}})
	j.pasteMin = 40 * time.Millisecond
	mustOK(t, j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: "new", Method: "paste"}))
	mu.Lock()
	defer mu.Unlock()
	if pressed.IsZero() || drained.IsZero() || restored.IsZero() {
		t.Fatalf("Ctrl+V at %v, drain at %v, restore at %v", pressed, drained, restored)
	}
	if d := drained.Sub(pressed); d < j.pasteMin {
		t.Fatalf("the drain began %v after Ctrl+V, before the minimum wait %v", d, j.pasteMin)
	}
	if restored.Before(drained) {
		t.Fatal("the clipboard was restored before the drain")
	}
	if got := newInjector(sys, Options{}).pasteMin; got != pasteMinWait || pasteMinWait < 120*time.Millisecond {
		t.Fatalf("default minimum wait %v (pasteMinWait %v), want at least 120 ms", got, pasteMinWait)
	}
}

func TestPasteRestoresAtOnceWhenItsStreamContextEnds(t *testing.T) {
	clip := &fakeClipboard{text: "old"}
	sys := &fakeSystem{clip: clip}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sys.hook = func(call int, in []raw) { cancel() }
	restored := false
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{ClipboardWritten: func() {
		if clip.current() == "old" {
			restored = true
		}
	}})
	j.pasteMin = 10 * time.Second
	start := time.Now()
	j.apply(ctx, 7, wire.InputEvent{Seq: 1, K: "text", Text: "new", Method: "paste"})
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the paste took %v after its context ended during the minimum wait of %v", d, j.pasteMin)
	}
	if len(sys.sent()) != 1 || clip.current() != "old" || !restored {
		t.Fatalf("%d sends, clipboard %q, restore reported %v", len(sys.sent()), clip.current(), restored)
	}
}

func TestAPreemptedPasteRestoresWithoutCtrlV(t *testing.T) {
	clip := &fakeClipboard{text: "old"}
	sys := &fakeSystem{clip: clip}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{ClipboardWritten: func() {}})
	j.SetWriter(1)
	j.opt.ClipboardWritten = func() {
		if clip.text == "new" {
			j.gen.Store(2)
		}
	}
	mustFail(t, j.Apply(wire.InputEvent{Seq: 1, Gen: 1, K: "text", Text: "new", Method: "paste"}), wire.CodeControlPreempted)
	if got := sys.all(); len(got) != 0 {
		t.Fatalf("sent %+v", got)
	}
	if clip.text != "old" {
		t.Fatalf("clipboard %q", clip.text)
	}
}

func TestPasteWithoutAClipboardIsUnsupported(t *testing.T) {
	j := newTestInjector(t, &fakeSystem{}, userSession, oneDisplay, Options{})
	mustFail(t, j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: "a", Method: "paste"}), wire.CodeCapabilityUnsupported)
}

func TestTextStopsWhenItsStreamContextEnds(t *testing.T) {
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	sys.hook = func(call int, in []raw) {
		if call == 0 {
			cancel()
		}
	}
	ack := j.apply(ctx, 7, wire.InputEvent{Seq: 1, K: "text", Text: strings.Repeat("가", 5*chunkChars)})
	if ack.OK || !strings.Contains(ack.Error, "typed 100 of 500") {
		t.Fatalf("ack %+v", ack)
	}
	if n := len(sys.sent()); n != 1 {
		t.Fatalf("%d sends after the context ended, want 1", n)
	}
}

// rawName names a keyboard input by its virtual key and direction.
func rawName(r raw) string {
	dir := "down"
	if r.flags&keyeventfKeyUp != 0 {
		dir = "up"
	}
	return "vk " + strings.ToLower(strings.TrimLeft(hex4(r.vk), "0")) + " " + dir
}

func hex4(v uint16) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[v>>12&15], digits[v>>8&15], digits[v>>4&15], digits[v&15]})
}
