package wire

import (
	"encoding/json"
	"reflect"
	"testing"
)

func jsonMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInputEventJSONKeys(t *testing.T) {
	b, err := json.Marshal(InputEvent{Seq: 1, Gen: 2, K: "key", SC: 30, Down: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"seq":1,"gen":2,"k":"key","sc":30,"ext":false,"down":true}`
	if string(b) != want {
		t.Fatalf("got %s want %s", b, want)
	}
}

func TestInputEventKeyUpKeepsDown(t *testing.T) {
	b, _ := json.Marshal(InputEvent{Seq: 3, Gen: 2, K: "key", SC: 30})
	want := `{"seq":3,"gen":2,"k":"key","sc":30,"ext":false,"down":false}`
	if string(b) != want {
		t.Fatalf("got %s want %s", b, want)
	}
	m := jsonMap(t, InputEvent{Seq: 4, Gen: 2, K: "button", B: "left"})
	if v, ok := m["down"]; !ok || v != false {
		t.Fatalf("button up: %v", m)
	}
}

func TestInputEventOmitsIrrelevantFields(t *testing.T) {
	cases := map[string]InputEvent{
		"move":        {Seq: 1, K: "move", X: 10, Y: 20, Display: 1},
		"wheel":       {Seq: 1, K: "wheel", DX: 0, DY: -120},
		"text":        {Seq: 1, K: "text", Text: "a", Method: "unicode"},
		"release_all": {Seq: 1, K: "release_all"},
	}
	for name, ev := range cases {
		m := jsonMap(t, ev)
		for _, k := range []string{"down", "ext", "sc", "vk", "b"} {
			if _, ok := m[k]; ok {
				t.Errorf("%s: unexpected key %q in %v", name, k, m)
			}
		}
	}
	m := jsonMap(t, cases["move"])
	if m["x"] != float64(10) || m["y"] != float64(20) || m["display"] != float64(1) {
		t.Fatalf("move: %v", m)
	}
}

func TestInputEventRoundTrip(t *testing.T) {
	for _, in := range []InputEvent{
		{Seq: 1, Gen: 2, K: "key", SC: 30, Ext: true, Down: true},
		{Seq: 2, Gen: 2, K: "key", VK: 0x41},
		{Seq: 3, Gen: 2, K: "button", B: "x1", Down: true},
		{Seq: 4, Gen: 2, K: "move", X: 5, Y: 6, Display: 1},
		{Seq: 5, Gen: 2, K: "wheel", DX: 30, DY: -120},
		{Seq: 6, Gen: 2, K: "text", Text: "한", Method: "paste"},
	} {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out InputEvent
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in, out) {
			t.Fatalf("got %+v want %+v (json %s)", out, in, b)
		}
	}
}

func TestInputEventDecodesTheProtocolKeys(t *testing.T) {
	for in, want := range map[string]InputEvent{
		`{"seq":1,"gen":2,"k":"key","sc":30,"ext":true,"down":true}`: {Seq: 1, Gen: 2, K: "key", SC: 30, Ext: true, Down: true},
		`{"seq":2,"gen":2,"k":"key","vk":21,"down":false}`:           {Seq: 2, Gen: 2, K: "key", VK: 21},
		`{"seq":3,"gen":0,"k":"move","x":-5,"y":6,"display":1}`:      {Seq: 3, K: "move", X: -5, Y: 6, Display: 1},
		`{"seq":4,"k":"wheel","dx":30,"dy":-120}`:                    {Seq: 4, K: "wheel", DX: 30, DY: -120},
		`{"seq":5,"k":"text","text":"가\n","method":"paste"}`:         {Seq: 5, K: "text", Text: "가\n", Method: "paste"},
		`{"seq":6,"k":"button","b":"x2","down":true,"extra":1}`:      {Seq: 6, K: "button", B: "x2", Down: true},
	} {
		var got InputEvent
		if err := json.Unmarshal([]byte(in), &got); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v want %+v", in, got, want)
		}
	}
	var ev InputEvent
	if err := json.Unmarshal([]byte(`{"seq":"one","k":"key"}`), &ev); err == nil {
		t.Fatal("a seq that is a string decoded")
	}
}

func TestInputAckJSON(t *testing.T) {
	b, _ := json.Marshal(InputAck{Seq: 5, OK: true, DoneMonoUS: 99})
	if want := `{"seq":5,"ok":true,"done_mono_us":99}`; string(b) != want {
		t.Fatalf("got %s", b)
	}
	b, _ = json.Marshal(InputAck{Seq: 6, Code: CodeControlPreempted, Error: "x"})
	if want := `{"seq":6,"ok":false,"code":"CONTROL_PREEMPTED","error":"x"}`; string(b) != want {
		t.Fatalf("got %s", b)
	}
}
