package wire

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestAURoundTrip(t *testing.T) {
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i * 7)
	}
	in := AU{Key: true, HasConfig: true, Width: 3840, Height: 2160, Seq: 42,
		CaptureMonoUS: 1_000_000, EncodeDoneMonoUS: 1_004_000, Data: data}
	out, err := UnmarshalAU(in.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatal("AU mismatch")
	}
}

func TestAUMarshalLayout(t *testing.T) {
	p := AU{Key: true, Width: 0x01020304, Height: 5, Seq: 6, CaptureMonoUS: 7, EncodeDoneMonoUS: 8, Data: []byte{9}}.Marshal()
	want := []byte{
		0x01,
		1, 2, 3, 4,
		0, 0, 0, 5,
		0, 0, 0, 0, 0, 0, 0, 6,
		0, 0, 0, 0, 0, 0, 0, 7,
		0, 0, 0, 0, 0, 0, 0, 8,
		9,
	}
	if !bytes.Equal(p, want) {
		t.Fatalf("got %v", p)
	}
	p = AU{HasConfig: true}.Marshal()
	if p[0] != 0x02 {
		t.Fatalf("flags = %#x", p[0])
	}
}

func TestUnmarshalAUShort(t *testing.T) {
	if _, err := UnmarshalAU(make([]byte, 32)); err == nil {
		t.Fatal("expected error")
	}
}

func TestVideoMessagesJSON(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{VideoInfo{Codec: "h264", Width: 1920, Height: 1080, SrcWidth: 3840, SrcHeight: 2160, FPS: 60, Bitrate: 6_000_000,
			Encoder: "NVIDIA H.264 Encoder MFT", Hardware: true, Capture: "dd", Display: 1},
			`{"codec":"h264","width":1920,"height":1080,"src_width":3840,"src_height":2160,"fps":60,"bitrate":6000000,"encoder":"NVIDIA H.264 Encoder MFT","hardware":true,"capture":"dd","display":1}`},
		{VideoStats{FPS: 59.5, Bitrate: 5_900_000, Dropped: 2, EncodeMSP50: 4.25, EncodeMSP95: 6.5, CaptureMSP50: 3},
			`{"fps":59.5,"bitrate":5900000,"dropped":2,"encode_ms_p50":4.25,"encode_ms_p95":6.5,"capture_ms_p50":3}`},
		{VideoPaused{Reason: PausedNoEncoder}, `{"reason":"no_encoder"}`},
		{VideoControl{Op: ControlKeyframe}, `{"op":"keyframe"}`},
		{VideoControl{Op: ControlBitrate, BPS: 3_000_000}, `{"op":"bitrate","bps":3000000}`},
		{VideoControl{Op: ControlConfig, MaxFPS: 30, MaxWidth: 1280}, `{"op":"config","max_fps":30,"max_width":1280}`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil || string(b) != c.want {
			t.Fatalf("got %s err=%v want %s", b, err, c.want)
		}
	}
	var ctl VideoControl
	if err := json.Unmarshal([]byte(`{"op":"config","max_fps":24,"max_width":0}`), &ctl); err != nil || ctl != (VideoControl{Op: ControlConfig, MaxFPS: 24}) {
		t.Fatalf("config control %+v, %v", ctl, err)
	}
	for got, want := range map[string]string{
		PausedNoViewer: "no_viewer", PausedNoEncoder: "no_encoder", ControlKeyframe: "keyframe", ControlBitrate: "bitrate",
		ControlConfig: "config", ControlPause: "pause", ControlResume: "resume",
	} {
		if got != want {
			t.Errorf("constant %q, want %q", got, want)
		}
	}
}
