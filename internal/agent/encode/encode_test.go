package encode

import (
	"bytes"
	"testing"
)

func TestNALTypesOfAnnexB(t *testing.T) {
	au := []byte{
		0, 0, 0, 1, 0x09, 0xf0,
		0, 0, 0, 1, 0x67, 0x64, 0x00, 0x34,
		0, 0, 1, 0x68, 0xee, 0x3c, 0x80,
		0, 0, 1, 0x65, 0x88, 0x84, 0x00, 0x00, 0x03, 0x00, 0x01,
	}
	if got := NALTypes(au); !bytes.Equal(got, []byte{9, 7, 8, 5}) {
		t.Fatalf("NAL types %v", got)
	}
	if key, config := describe(au); !key || !config {
		t.Fatalf("IDR with SPS and PPS: key %v config %v", key, config)
	}
	p := []byte{0, 0, 0, 1, 0x09, 0x30, 0, 0, 0, 1, 0x41, 0x9a, 0x00}
	if key, config := describe(p); key || config {
		t.Fatalf("P slice: key %v config %v", key, config)
	}
	if key, config := describe([]byte{0, 0, 1, 0x65, 0x88}); !key || config {
		t.Fatalf("IDR without parameter sets: key %v config %v", key, config)
	}
	if key, config := describe([]byte{0, 0, 1, 0x67, 0, 0, 1, 0x65}); !key || config {
		t.Fatalf("IDR with an SPS but no PPS: key %v config %v", key, config)
	}
	if got := NALTypes([]byte{0, 0, 1}); len(got) != 0 {
		t.Fatalf("a start code without a NAL header: %v", got)
	}
}

func TestH264LevelAdmitsTheFormat(t *testing.T) {
	for _, c := range []struct {
		w, h, fps, bps int
		want           uint32
	}{
		{3840, 2160, 60, 20_000_000, 52},
		{3840, 2160, 30, 20_000_000, 51},
		{2560, 1440, 60, 12_000_000, 51},
		{3008, 1692, 60, 20_000_000, 52},
		{1920, 1080, 60, 6_000_000, 42},
		{1920, 1080, 30, 6_000_000, 40},
		{1920, 1080, 30, 40_000_000, 41},
		{1280, 720, 60, 6_000_000, 32},
		{1280, 720, 60, 40_000_000, 41},
		{640, 360, 30, 2_000_000, 30},
		{7680, 4320, 60, 20_000_000, 0},
	} {
		if got := h264Level(c.w, c.h, c.fps, c.bps); got != c.want {
			t.Errorf("%dx%d@%d %d bps: level %d, want %d", c.w, c.h, c.fps, c.bps, got, c.want)
		}
	}
}

func TestCheckSizes(t *testing.T) {
	if err := checkSizes(3840, 2160, 1920, 1080, 60, 6_000_000); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][6]int{
		{3840, 2160, 1921, 1080, 60, 1}, {3840, 2160, 1920, 1081, 60, 1}, {0, 2160, 1920, 1080, 60, 1},
		{3840, 2160, 3842, 2160, 60, 1}, {3840, 2160, 1920, 1080, 0, 1}, {3840, 2160, 1920, 1080, 60, 0},
	} {
		if err := checkSizes(c[0], c[1], c[2], c[3], c[4], c[5]); err == nil {
			t.Errorf("checkSizes%v accepted", c)
		}
	}
}

func TestSampleTimesIncrease(t *testing.T) {
	var c sampleClock
	got := []int64{c.next(5_000_000), c.next(5_016_667), c.next(5_016_667), c.next(4_000_000), c.next(5_050_000)}
	want := []int64{0, 166_670, 166_671, 166_672, 500_000}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample times %v, want %v", got, want)
		}
	}
	if m := c.mono(166_670); m != 5_016_667 {
		t.Fatalf("mono(166670) = %d", m)
	}
}
