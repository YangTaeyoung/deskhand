package wire

import "encoding/json"

// InputEvent is one input stream event. Zero-valued fields are omitted from
// the JSON, except down (button and key events) and ext (scan-code key
// events), which are always present for those kinds.
type InputEvent struct {
	Seq, Gen uint64
	K        string
	X, Y     int
	Display  int
	B        string
	Down     bool
	DX, DY   int
	SC       uint16
	Ext      bool
	VK       uint16
	Text     string
	Method   string
}

type inputEventJSON struct {
	Seq     uint64 `json:"seq"`
	Gen     uint64 `json:"gen"`
	K       string `json:"k"`
	X       int    `json:"x,omitempty"`
	Y       int    `json:"y,omitempty"`
	Display int    `json:"display,omitempty"`
	B       string `json:"b,omitempty"`
	DX      int    `json:"dx,omitempty"`
	DY      int    `json:"dy,omitempty"`
	SC      uint16 `json:"sc,omitempty"`
	Ext     *bool  `json:"ext,omitempty"`
	VK      uint16 `json:"vk,omitempty"`
	Down    *bool  `json:"down,omitempty"`
	Text    string `json:"text,omitempty"`
	Method  string `json:"method,omitempty"`
}

// MarshalJSON implements json.Marshaler.
func (e InputEvent) MarshalJSON() ([]byte, error) {
	j := inputEventJSON{
		Seq: e.Seq, Gen: e.Gen, K: e.K, X: e.X, Y: e.Y, Display: e.Display,
		B: e.B, DX: e.DX, DY: e.DY, SC: e.SC, VK: e.VK, Text: e.Text, Method: e.Method,
	}
	if e.K == "button" || e.K == "key" {
		d := e.Down
		j.Down = &d
	}
	if e.K == "key" && (e.VK == 0 || e.Ext) {
		x := e.Ext
		j.Ext = &x
	} else if e.Ext {
		x := true
		j.Ext = &x
	}
	return json.Marshal(j)
}

// UnmarshalJSON implements json.Unmarshaler with the keys of MarshalJSON;
// down and ext are false when absent.
func (e *InputEvent) UnmarshalJSON(b []byte) error {
	var j inputEventJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	*e = InputEvent{
		Seq: j.Seq, Gen: j.Gen, K: j.K, X: j.X, Y: j.Y, Display: j.Display,
		B: j.B, DX: j.DX, DY: j.DY, SC: j.SC, VK: j.VK, Text: j.Text, Method: j.Method,
	}
	if j.Down != nil {
		e.Down = *j.Down
	}
	if j.Ext != nil {
		e.Ext = *j.Ext
	}
	return nil
}

// InputAck is the agent's reply to one InputEvent.
type InputAck struct {
	Seq        uint64 `json:"seq"`
	OK         bool   `json:"ok"`
	DoneMonoUS uint64 `json:"done_mono_us,omitempty"`
	Code       string `json:"code,omitempty"`
	Error      string `json:"error,omitempty"`
}
