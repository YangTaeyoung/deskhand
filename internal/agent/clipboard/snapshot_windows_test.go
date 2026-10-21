package clipboard

import (
	"bytes"
	"os"
	"slices"
	"testing"
	"unicode/utf16"
)

// utf16Bytes is text as CF_UNICODETEXT data.
func utf16Bytes(text string) []byte {
	u := append(utf16.Encode([]rune(text)), 0)
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return b
}

// keepUsersClipboard saves the clipboard and puts it back when the test
// ends; the test does not touch a clipboard it cannot save.
func keepUsersClipboard(t *testing.T) {
	t.Helper()
	if os.Getenv("DESKHAND_WINTEST") == "" {
		t.Skip("needs the signed-in user's session; run through scripts/wintest.sh")
	}
	user, err := Save()
	if err != nil {
		t.Fatalf("the user's clipboard could not be saved, so the test does not touch it: %v", err)
	}
	t.Cleanup(func() {
		if err := user.Restore(); err != nil {
			t.Errorf("restoring the user's clipboard: %v", err)
		}
	})
}

func TestSnapshotRestoresTheFormatsItSaved(t *testing.T) {
	keepUsersClipboard(t)
	custom := registeredFormat("DeskhandTestFormat")
	if custom == 0 {
		t.Fatal("RegisterClipboardFormat failed")
	}
	data := []byte{1, 2, 3, 4, 5}
	if _, err := serial(func() (struct{}, error) {
		return struct{}{}, replace([]savedFormat{{format: cfUnicodeText, data: utf16Bytes("원래 텍스트 😀")}, {format: custom, data: data}})
	}); err != nil {
		t.Fatal(err)
	}
	s, err := Save()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.Formats(), custom) || !bytes.HasPrefix(s.Data(custom), data) {
		t.Fatalf("the snapshot holds %v, custom data %v", s.Formats(), s.Data(custom))
	}
	if err := SetPrivateText("다른 글"); err != nil {
		t.Fatal(err)
	}
	if text, err := New().Text(); err != nil || text != "다른 글" {
		t.Fatalf("text %q %v", text, err)
	}
	private, err := Save()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{formatExcludeMonitors, formatCanIncludeInHistory, formatCanUploadToCloud} {
		if !slices.Contains(private.Formats(), registeredFormat(name)) {
			t.Fatalf("SetPrivateText left out %s: %v", name, private.Formats())
		}
	}
	if slices.Contains(private.Formats(), custom) {
		t.Fatal("SetPrivateText kept the custom format")
	}
	if err := s.Restore(); err != nil {
		t.Fatal(err)
	}
	if text, err := New().Text(); err != nil || text != "원래 텍스트 😀" {
		t.Fatalf("restored text %q %v", text, err)
	}
	again, err := Save()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(again.Data(custom), data) {
		t.Fatalf("restored custom data %v", again.Data(custom))
	}
	if !slices.Contains(again.Formats(), registeredFormat(formatCanIncludeInHistory)) {
		t.Fatalf("the restored content is not kept out of the clipboard history: %v", again.Formats())
	}
	t.Logf("saved %v (skipped %v); restored %v", s.Formats(), s.Skipped, again.Formats())
}

func TestAnEmptySnapshotEmptiesTheClipboard(t *testing.T) {
	keepUsersClipboard(t)
	if _, err := serial(func() (struct{}, error) { return struct{}{}, replace(nil) }); err != nil {
		t.Fatal(err)
	}
	empty, err := Save()
	if err != nil || len(empty.Formats()) != 0 {
		t.Fatalf("empty snapshot %v %v", empty, err)
	}
	if err := SetPrivateText("x"); err != nil {
		t.Fatal(err)
	}
	if err := empty.Restore(); err != nil {
		t.Fatal(err)
	}
	if after, err := Save(); err != nil || len(after.Formats()) != 0 {
		t.Fatalf("after restoring an empty snapshot %v %v", after.Formats(), err)
	}
}
