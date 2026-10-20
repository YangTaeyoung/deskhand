package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

// Exit codes of the command line.
const (
	exitOK        = 0
	exitFailure   = 1
	exitUsage     = 2
	exitNeedsUser = 3
	exitUnknown   = 4
	exitOffline   = 5
)

// usageError is a mistake in the command line itself.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

// obj is a decoded JSON object whose numbers keep their digits.
type obj map[string]any

func decodeObj(raw []byte) (obj, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m obj
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		m = obj{}
	}
	return m, nil
}

func (o obj) str(key string) string {
	switch v := o[key].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	}
	return ""
}

func (o obj) num(key string) (int64, bool) {
	n, ok := o[key].(json.Number)
	if !ok {
		return 0, false
	}
	if i, err := n.Int64(); err == nil {
		return i, true
	}
	if f, err := n.Float64(); err == nil {
		return int64(f), true
	}
	return 0, false
}

func (o obj) boolean(key string) bool {
	b, _ := o[key].(bool)
	return b
}

func (o obj) sub(key string) obj {
	m, _ := o[key].(map[string]any)
	return obj(m)
}

func (o obj) list(key string) []obj {
	raw, _ := o[key].([]any)
	out := make([]obj, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, obj(m))
		}
	}
	return out
}

// writeJSON prints o as one line, adding schema_version when missing.
func writeJSON(w io.Writer, o obj) {
	if _, ok := o["schema_version"]; !ok {
		o["schema_version"] = json.Number(fmt.Sprint(model.SchemaVersion))
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(o)
	_, _ = w.Write(buf.Bytes())
}

// displayWidth is the number of terminal columns of s.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	switch {
	case r == 0 || r == 0x200d || (r >= 0xfe00 && r <= 0xfe0f) || (r >= 0x300 && r <= 0x36f):
		return 0
	case r >= 0x1100 && r <= 0x115f,
		r >= 0x2e80 && r <= 0xa4cf,
		r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe30 && r <= 0xfe6f,
		r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1faff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

// writeTable prints header and rows with columns aligned to the widest cell.
func writeTable(w io.Writer, header []string, rows [][]string) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = displayWidth(h)
	}
	for _, row := range rows {
		for i, c := range row {
			if i < len(widths) && displayWidth(c) > widths[i] {
				widths[i] = displayWidth(c)
			}
		}
	}
	line := func(cells []string) {
		var b strings.Builder
		for i, c := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(c)
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-displayWidth(c)))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	line(header)
	for _, row := range rows {
		for len(row) < len(header) {
			row = append(row, "")
		}
		line(row)
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// humanSize formats a byte count.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
