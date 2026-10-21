package observe

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// autoUnicodeMax is the longest text, in the agent's characters, that
	// method auto types with unicode.
	autoUnicodeMax = 2000
	// maxTextEvent is the most characters one text event carries
	// (docs/protocol.md §6).
	maxTextEvent = 100_000
)

// Text methods.
const (
	MethodAuto    = "auto"
	MethodUnicode = "unicode"
	MethodPaste   = "paste"
)

// agentChars counts the characters of text as the agent does: code
// points, "\r\n" as two.
func agentChars(text string) int { return utf8.RuneCountInString(text) }

// unicodeTypes reports whether KEYEVENTF_UNICODE input types r as one
// character: a character of the Basic Multilingual Plane other than a
// control character, or tab, carriage return or line feed.
func unicodeTypes(r rune) bool {
	switch r {
	case '\t', '\r', '\n':
		return true
	}
	return r <= 0xffff && !unicode.IsControl(r) && r != utf8.RuneError
}

// autoMethod is the method auto picks for text: unicode for a text of at
// most autoUnicodeMax characters that unicode types character by
// character, paste otherwise.
func autoMethod(text string) string {
	if agentChars(text) > autoUnicodeMax {
		return MethodPaste
	}
	for _, r := range text {
		if !unicodeTypes(r) {
			return MethodPaste
		}
	}
	return MethodUnicode
}

// TextEvents returns the text events that type text with method (auto
// when ""), in parts of at most maxTextEvent characters that keep "\r\n"
// together, and the method they use.
func TextEvents(text, method string) ([]wire.InputEvent, string, error) {
	if text == "" {
		return nil, "", apierr.Invalid("text가 비어 있습니다. 입력할 글을 주세요")
	}
	switch method {
	case "", MethodAuto:
		method = autoMethod(text)
	case MethodUnicode, MethodPaste:
	default:
		return nil, "", apierr.Invalid(fmt.Sprintf("method %q를 모릅니다. auto, unicode, paste 중 하나로 주세요", method))
	}
	var evs []wire.InputEvent
	for _, part := range splitText(text, maxTextEvent) {
		evs = append(evs, wire.InputEvent{K: "text", Text: part, Method: method})
	}
	return evs, method, nil
}

// splitText cuts text into parts of at most n characters without cutting
// a "\r\n".
func splitText(text string, n int) []string {
	var parts []string
	for agentChars(text) > n {
		cut, count := 0, 0
		for i := range text {
			if count == n {
				cut = i
				break
			}
			count++
		}
		if text[cut-1] == '\r' && text[cut] == '\n' {
			cut--
		}
		parts = append(parts, text[:cut])
		text = text[cut:]
	}
	return append(parts, text)
}
