package qa

import "testing"

func TestByteSizeUsesOneDecimalOnBase1024(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{563, "563 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1280, "1.2 KB"},
		{2184, "2.1 KB"},
		{1048575, "1024.0 KB"},
		{2516992, "2.4 MB"},
		{1 << 30, "1.0 GB"},
		{1<<40 - 1, "1024.0 GB"},
		{3 << 40, "3.0 TB"},
	} {
		if got := byteSize(tc.n); got != tc.want {
			t.Errorf("byteSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
