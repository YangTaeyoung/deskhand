package debug

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// previewMax bounds value_preview in characters.
	previewMax = 200
	// consoleTextMax bounds the text of a console entry in characters.
	consoleTextMax = 10000
	// stackMax bounds the frames of a console or exception stack.
	stackMax = 20
)

// remoteObject is a CDP Runtime.RemoteObject.
type remoteObject struct {
	Type                string          `json:"type"`
	Subtype             string          `json:"subtype,omitempty"`
	ClassName           string          `json:"className,omitempty"`
	Value               json.RawMessage `json:"value,omitempty"`
	UnserializableValue string          `json:"unserializableValue,omitempty"`
	Description         string          `json:"description,omitempty"`
	ObjectID            string          `json:"objectId,omitempty"`
	Preview             *objectPreview  `json:"preview,omitempty"`
}

// objectPreview is a CDP Runtime.ObjectPreview.
type objectPreview struct {
	Type        string            `json:"type"`
	Subtype     string            `json:"subtype,omitempty"`
	Description string            `json:"description,omitempty"`
	Overflow    bool              `json:"overflow"`
	Properties  []propertyPreview `json:"properties"`
	Entries     []entryPreview    `json:"entries,omitempty"`
}

type propertyPreview struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Value   string `json:"value,omitempty"`
	Subtype string `json:"subtype,omitempty"`
}

type entryPreview struct {
	Key   *objectPreview `json:"key,omitempty"`
	Value objectPreview  `json:"value"`
}

// RemoteValue is a value of the debuggee (local-api.md §5).
type RemoteValue struct {
	Type         string `json:"type"`
	Subtype      string `json:"subtype,omitempty"`
	ClassName    string `json:"class_name,omitempty"`
	ValuePreview string `json:"value_preview"`
	ObjectID     string `json:"object_id,omitempty"`
	HasChildren  bool   `json:"has_children"`
}

// Property is a property of an object or scope.
type Property struct {
	RemoteValue
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Writable *bool  `json:"writable,omitempty"`
}

// StackFrame is a frame of a console or exception stack.
type StackFrame struct {
	Function string `json:"function"`
	ScriptID string `json:"script_id,omitempty"`
	URL      string `json:"url"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

// ExceptionInfo describes a thrown value.
type ExceptionInfo struct {
	Text     string       `json:"text"`
	ScriptID string       `json:"script_id,omitempty"`
	URL      string       `json:"url,omitempty"`
	Line     *int         `json:"line,omitempty"`
	Column   *int         `json:"column,omitempty"`
	Stack    []StackFrame `json:"stack"`
}

// cut returns s cut to max characters, ending in "…" when it was longer.
func cut(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

// quote returns s as a JSON string without HTML escapes.
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n  "); i >= 0 {
		return s[:i]
	}
	return s
}

// stringValue returns the string the value of o holds.
func (o remoteObject) stringValue() string {
	var s string
	if json.Unmarshal(o.Value, &s) == nil {
		return s
	}
	return o.Description
}

// scalar returns the text of a number, boolean, bigint or symbol.
func (o remoteObject) scalar() string {
	switch {
	case o.UnserializableValue != "":
		return o.UnserializableValue
	case o.Description != "":
		return o.Description
	case len(o.Value) > 0:
		return string(o.Value)
	}
	return o.Type
}

// preview returns the value_preview of o, uncut.
func (o remoteObject) preview() string {
	switch o.Type {
	case "string":
		return quote(o.stringValue())
	case "undefined":
		return "undefined"
	case "number", "boolean", "bigint", "symbol":
		return o.scalar()
	case "function":
		return functionPreview(o.Description)
	case "object":
		if o.Subtype == "null" {
			return "null"
		}
		d := firstLine(o.Description)
		if d == "" {
			d = o.ClassName
		}
		if o.Preview != nil && o.Subtype != "error" {
			d += " " + previewText(o.Preview)
		}
		return d
	}
	return firstLine(o.Description)
}

// value returns o as a RemoteValue.
func (o remoteObject) value() RemoteValue {
	v := RemoteValue{Type: o.Type, Subtype: o.Subtype, ClassName: o.ClassName, ValuePreview: cut(o.preview(), previewMax), ObjectID: o.ObjectID}
	if v.Type == "" {
		v.Type = "undefined"
	}
	v.HasChildren = o.ObjectID != "" && !(o.Preview != nil && len(o.Preview.Properties) == 0 && len(o.Preview.Entries) == 0 && !o.Preview.Overflow)
	return v
}

// plain is the text of a console argument: a string as it is, any other
// value as its preview.
func (o remoteObject) plain() string {
	if o.Type == "string" {
		return o.stringValue()
	}
	return cut(o.preview(), previewMax)
}

func isIndex(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// previewText is the {…} or […] part of an object preview.
func previewText(p *objectPreview) string {
	var parts []string
	switch {
	case p.Subtype == "array" || p.Subtype == "typedarray":
		for _, pp := range p.Properties {
			if isIndex(pp.Name) {
				parts = append(parts, propertyText(pp))
			} else {
				parts = append(parts, pp.Name+": "+propertyText(pp))
			}
		}
		if p.Overflow {
			parts = append(parts, "…")
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case len(p.Entries) > 0:
		for _, e := range p.Entries {
			val := entryText(e.Value)
			if e.Key != nil {
				val = entryText(*e.Key) + " => " + val
			}
			parts = append(parts, val)
		}
	default:
		for _, pp := range p.Properties {
			parts = append(parts, pp.Name+": "+propertyText(pp))
		}
	}
	if p.Overflow {
		parts = append(parts, "…")
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func entryText(p objectPreview) string {
	switch p.Type {
	case "string":
		return quote(p.Description)
	case "object":
		if p.Subtype == "null" {
			return "null"
		}
	}
	return p.Description
}

func propertyText(pp propertyPreview) string {
	switch pp.Type {
	case "string":
		return quote(pp.Value)
	case "function":
		return "ƒ"
	case "accessor":
		return "(...)"
	case "undefined":
		return "undefined"
	case "object":
		if pp.Subtype == "null" {
			return "null"
		}
	}
	return pp.Value
}

// functionPreview returns "ƒ name(params)" for the source text of a
// function, or "class Name" for a class.
func functionPreview(desc string) string {
	s := strings.TrimSpace(desc)
	if strings.HasPrefix(s, "class") {
		rest := strings.TrimSpace(strings.TrimPrefix(s, "class"))
		end := strings.IndexAny(rest, " {")
		if end < 0 {
			end = len(rest)
		}
		return strings.TrimSpace("class " + rest[:end])
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "async "))
	name := ""
	params := ""
	switch {
	case strings.HasPrefix(s, "function"):
		s = strings.TrimSpace(strings.TrimPrefix(s, "function"))
		s = strings.TrimSpace(strings.TrimPrefix(s, "*"))
		if i := strings.IndexByte(s, '('); i >= 0 {
			name = strings.TrimSpace(s[:i])
			params = parenthesised(s[i:])
		}
	case strings.HasPrefix(s, "("):
		params = parenthesised(s)
	default:
		arrow := strings.Index(s, "=>")
		paren := strings.IndexByte(s, '(')
		switch {
		case arrow >= 0 && (paren < 0 || arrow < paren):
			params = strings.TrimSpace(s[:arrow])
		case paren >= 0:
			name = strings.TrimSpace(strings.TrimPrefix(s[:paren], "*"))
			params = parenthesised(s[paren:])
		}
	}
	return "ƒ " + name + "(" + strings.Join(strings.Fields(params), " ") + ")"
}

// parenthesised returns what the parenthesis that opens s encloses.
func parenthesised(s string) string {
	depth := 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[1:i]
			}
		}
	}
	return strings.TrimPrefix(s, "(")
}

// number returns the numeric value of o for %d, %i and %f.
func (o remoteObject) number() (float64, bool) {
	if o.Type != "number" {
		return 0, false
	}
	var f float64
	if json.Unmarshal(o.Value, &f) == nil {
		return f, true
	}
	switch o.UnserializableValue {
	case "Infinity":
		return math.Inf(1), true
	case "-Infinity":
		return math.Inf(-1), true
	case "-0":
		return 0, true
	}
	return 0, false
}

// consoleText joins the console arguments as the console shows them,
// applying the format specifiers of a first string argument.
func consoleText(args []remoteObject) string {
	var parts []string
	rest := args
	if len(args) > 0 && args[0].Type == "string" {
		format := args[0].stringValue()
		rest = args[1:]
		var b strings.Builder
		for i := 0; i < len(format); i++ {
			c := format[i]
			if c != '%' || i+1 == len(format) {
				b.WriteByte(c)
				continue
			}
			spec := format[i+1]
			if spec == '%' {
				b.WriteByte('%')
				i++
				continue
			}
			if !strings.ContainsRune("sdifoOc", rune(spec)) {
				b.WriteByte(c)
				continue
			}
			i++
			if len(rest) == 0 {
				b.WriteByte('%')
				b.WriteByte(spec)
				continue
			}
			a := rest[0]
			rest = rest[1:]
			switch spec {
			case 's':
				b.WriteString(a.plain())
			case 'd', 'i':
				if f, ok := a.number(); ok && !math.IsInf(f, 0) {
					b.WriteString(strconv.FormatFloat(math.Trunc(f), 'f', -1, 64))
				} else {
					b.WriteString("NaN")
				}
			case 'f':
				if f, ok := a.number(); ok {
					b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
				} else {
					b.WriteString("NaN")
				}
			case 'o', 'O':
				b.WriteString(cut(a.preview(), previewMax))
			}
		}
		parts = append(parts, b.String())
	}
	for _, a := range rest {
		parts = append(parts, a.plain())
	}
	return cut(strings.Join(parts, " "), consoleTextMax)
}

// cdpCallFrame is a frame of a CDP Runtime.StackTrace.
type cdpCallFrame struct {
	FunctionName string `json:"functionName"`
	ScriptID     string `json:"scriptId"`
	URL          string `json:"url"`
	LineNumber   int    `json:"lineNumber"`
	ColumnNumber int    `json:"columnNumber"`
}

type stackTrace struct {
	CallFrames []cdpCallFrame `json:"callFrames"`
}

// stack returns the first stackMax frames of st with 1-based lines and
// columns.
func (st *stackTrace) stack() []StackFrame {
	out := []StackFrame{}
	if st == nil {
		return out
	}
	for i, f := range st.CallFrames {
		if i == stackMax {
			break
		}
		out = append(out, StackFrame{Function: f.FunctionName, ScriptID: f.ScriptID, URL: f.URL, Line: f.LineNumber + 1, Column: f.ColumnNumber + 1})
	}
	return out
}

// exceptionDetails is a CDP Runtime.ExceptionDetails.
type exceptionDetails struct {
	Text         string        `json:"text"`
	LineNumber   int           `json:"lineNumber"`
	ColumnNumber int           `json:"columnNumber"`
	ScriptID     string        `json:"scriptId"`
	URL          string        `json:"url"`
	StackTrace   *stackTrace   `json:"stackTrace"`
	Exception    *remoteObject `json:"exception"`
}

// headline is the one-line text of a thrown value: the first line of an
// error's description, or the details' text and the value's preview.
func (d exceptionDetails) headline() string {
	if d.Exception != nil {
		if d.Exception.Subtype == "error" && d.Exception.Description != "" {
			return firstLine(d.Exception.Description)
		}
		v := d.Exception.plain()
		if d.Text != "" {
			return d.Text + " " + v
		}
		return v
	}
	return d.Text
}

// info returns d as an ExceptionInfo.
func (d exceptionDetails) info() *ExceptionInfo {
	line, col := d.LineNumber+1, d.ColumnNumber+1
	e := &ExceptionInfo{Text: cut(d.headline(), consoleTextMax), ScriptID: d.ScriptID, URL: d.URL, Line: &line, Column: &col, Stack: d.StackTrace.stack()}
	if e.URL == "" && len(e.Stack) > 0 {
		e.URL = e.Stack[0].URL
	}
	return e
}
