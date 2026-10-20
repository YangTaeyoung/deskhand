package input

import (
	"context"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// chunkChars is the most characters of a text one SendInput sends.
	chunkChars = 100
	// drainTimeout bounds the wait for the foreground window's thread to
	// read the input of the chunk before.
	drainTimeout = 5 * time.Second
	// drainAfter is how recent the last chunk of text has to be for the
	// first chunk of the next text to wait for it to be read.
	drainAfter = time.Second
)

// textItem is the inputs of one character of a text, or of a "\r\n", and
// the characters it stands for.
type textItem struct {
	in    []raw
	chars int
}

// textItems splits text into its characters: "\r\n", "\r" and "\n"
// become Enter, "\t" Tab, and any other character its UTF-16 units, each
// pressed and released as a KEYEVENTF_UNICODE input.
func textItems(text string) []textItem {
	items := make([]textItem, 0, len(text))
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		switch r {
		case '\r':
			if i < len(text) && text[i] == '\n' {
				i++
				items = append(items, textItem{in: keyPress(vkReturn, scanReturn), chars: 2})
				continue
			}
			items = append(items, textItem{in: keyPress(vkReturn, scanReturn), chars: 1})
		case '\n':
			items = append(items, textItem{in: keyPress(vkReturn, scanReturn), chars: 1})
		case '\t':
			items = append(items, textItem{in: keyPress(vkTab, scanTab), chars: 1})
		default:
			var units [2]uint16
			n := 1
			if r1, r2 := utf16.EncodeRune(r); r1 != utf8.RuneError {
				units[0], units[1], n = uint16(r1), uint16(r2), 2
			} else {
				units[0] = uint16(r)
			}
			in := make([]raw, 0, 2*n)
			for _, u := range units[:n] {
				in = append(in, raw{scan: u, flags: keyeventfUnicode}, raw{scan: u, flags: keyeventfUnicode | keyeventfKeyUp})
			}
			items = append(items, textItem{in: in, chars: 1})
		}
	}
	return items
}

// keyPress presses and releases virtual key vk with scan code scan.
func keyPress(vk, scan uint16) []raw {
	return []raw{{vk: vk, scan: scan}, {vk: vk, scan: scan, flags: keyeventfKeyUp}}
}

// unicodeInputs is the inputs that type text.
func unicodeInputs(text string) []raw {
	var in []raw
	for _, it := range textItems(text) {
		in = append(in, it.in...)
	}
	return in
}

// textChunk is the inputs of consecutive characters of a text.
type textChunk struct {
	in    []raw
	chars int
}

// textChunks groups the characters of text, a "\r\n" counting as one,
// into chunks of at most chunkChars, and returns them with the number of
// characters of text.
func textChunks(text string) ([]textChunk, int) {
	items := textItems(text)
	total := 0
	for _, it := range items {
		total += it.chars
	}
	var chunks []textChunk
	for len(items) > 0 {
		n := min(chunkChars, len(items))
		var c textChunk
		for _, it := range items[:n] {
			c.in = append(c.in, it.in...)
			c.chars += it.chars
		}
		chunks = append(chunks, c)
		items = items[n:]
	}
	return chunks, total
}

// typeText types ev.Text with KEYEVENTF_UNICODE inputs, chunk by chunk.
// Before a chunk that follows another within drainAfter it waits, with mu
// free, until the foreground window's thread has read the chunk before. It
// stops when the writer generation has risen past ev.Gen or ctx ends (the
// input stream closed), with an ack that names how many characters it
// typed.
func (j *Injector) typeText(ctx context.Context, owner uint64, ev wire.InputEvent) error {
	chunks, total := textChunks(ev.Text)
	typed := 0
	for i, c := range chunks {
		if ctx.Err() != nil {
			return streamClosed("typed %d of %d characters, then the input stream closed before the rest", typed, total)
		}
		if i > 0 || time.Since(time.Unix(0, j.lastText.Load())) < drainAfter {
			if err := j.sys.drain(ctx, drainTimeout); err != nil {
				return fail(wire.CodeTimeout, "typed %d of %d characters, then the window in front stopped reading its input (%v); check that it responds, then send the rest", typed, total, err)
			}
		}
		if ctx.Err() != nil {
			return streamClosed("typed %d of %d characters, then the input stream closed before the rest", typed, total)
		}
		err := j.locked(ev.Gen, func() error { return j.inject(owner, c.in) })
		j.lastText.Store(time.Now().UnixNano())
		if err != nil {
			if e, ok := err.(*Error); ok && i > 0 {
				return fail(e.Code, "typed %d of %d characters, then: %s", typed, total, e.Msg)
			}
			return err
		}
		typed += c.chars
	}
	return nil
}

// paste puts ev.Text on the clipboard, presses Ctrl+V and restores the
// clipboard pasteMin later once the paste target has drained, waiting at
// most pasteDelay more for it; when ctx ends during those waits it
// restores at once.
func (j *Injector) paste(ctx context.Context, owner uint64, ev wire.InputEvent) error {
	cb := j.sys.clipboard()
	if cb == nil {
		return fail(wire.CodeCapabilityUnsupported, "this agent cannot paste; send the text with method unicode")
	}
	j.pasteMu.Lock()
	defer j.pasteMu.Unlock()
	restore, err := cb.save()
	if err != nil {
		return fail(wire.CodeInternal, "the clipboard could not be saved before the paste (%v); try again, or send the text with method unicode", err)
	}
	restored := false
	putBack := func() {
		if restored {
			return
		}
		restored = true
		if err := restore(); err != nil {
			j.opt.Logger.Printf("input: restoring the clipboard after a paste: %v", err)
		}
		j.clipboardWritten()
	}
	defer putBack()
	if ctx.Err() != nil {
		return streamClosed("the input stream closed before the paste")
	}
	if err := cb.setText(ev.Text); err != nil {
		return fail(wire.CodeInternal, "the clipboard could not be set for the paste (%v); try again, or send the text with method unicode", err)
	}
	j.clipboardWritten()
	ctrlScan, _ := j.sys.keyScan(vkControl)
	vScan, _ := j.sys.keyScan(vkV)
	ctrlV := []raw{{vk: vkControl, scan: ctrlScan}, {vk: vkV, scan: vScan},
		{vk: vkV, scan: vScan, flags: keyeventfKeyUp}, {vk: vkControl, scan: ctrlScan, flags: keyeventfKeyUp}}
	if err := j.locked(ev.Gen, func() error { return j.inject(owner, ctrlV) }); err != nil {
		return err
	}
	if sleep(ctx, j.pasteMin) {
		_ = j.sys.drain(ctx, j.pasteDelay)
	}
	putBack()
	return nil
}

// sleep waits d and reports whether it did before ctx ended.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (j *Injector) clipboardWritten() {
	if j.opt.ClipboardWritten != nil {
		j.opt.ClipboardWritten()
	}
}
