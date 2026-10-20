//go:build windows

package clipboard

import (
	"errors"
	"fmt"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	gdi32                          = syscall.NewLazyDLL("gdi32.dll")
	procEnumClipboardFormats       = user32.NewProc("EnumClipboardFormats")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")
	procGetEnhMetaFileBits         = gdi32.NewProc("GetEnhMetaFileBits")
	procSetEnhMetaFileBits         = gdi32.NewProc("SetEnhMetaFileBits")
	procDeleteEnhMetaFile          = gdi32.NewProc("DeleteEnhMetaFile")
	errSnapshotTooLarge            = errors.New("the clipboard holds more than 256 MiB")
	errClipboardEnumerationStopped = errors.New("EnumClipboardFormats failed")
)

// Standard clipboard formats whose data is not a global memory block.
const (
	cfBitmap          = 2
	cfMetafilePict    = 3
	cfPalette         = 9
	cfEnhMetafile     = 14
	cfOwnerDisplay    = 0x80
	cfDspBitmap       = 0x82
	cfDspMetafilePict = 0x83
	cfDspEnhMetafile  = 0x8E
	cfPrivateFirst    = 0x200
	cfGDIObjLast      = 0x3FF
)

// maxSnapshotBytes bounds the data Save copies.
const maxSnapshotBytes = 256 << 20

// Names of the registered formats that keep a clipboard content out of the
// clipboard history, the cloud clipboard and clipboard monitors.
const (
	formatExcludeMonitors     = "ExcludeClipboardContentFromMonitorProcessing"
	formatCanIncludeInHistory = "CanIncludeInClipboardHistory"
	formatCanUploadToCloud    = "CanUploadToCloudClipboard"
)

// Snapshot is a copy of the clipboard in the formats Restore can put back:
// every format whose data is a global memory block, and enhanced
// metafiles. Skipped lists the formats on the clipboard it did not copy:
// bitmaps, palettes and metafile pictures held as GDI objects (Windows
// makes CF_BITMAP again from CF_DIB), owner-drawn and private formats.
type Snapshot struct {
	items   []savedFormat
	Skipped []uint32
}

type savedFormat struct {
	format uint32
	data   []byte
}

// Formats returns the formats the snapshot holds, in clipboard order.
func (s *Snapshot) Formats() []uint32 {
	out := make([]uint32, len(s.items))
	for i, it := range s.items {
		out[i] = it.format
	}
	return out
}

// Data returns the bytes the snapshot holds for format, or nil.
func (s *Snapshot) Data(format uint32) []byte {
	for _, it := range s.items {
		if it.format == format {
			return it.data
		}
	}
	return nil
}

// Save copies the clipboard through serial. A clipboard whose owner
// renders formats on request renders them all.
func Save() (*Snapshot, error) { return serial(save) }

func save() (*Snapshot, error) {
	if err := open(); err != nil {
		return nil, err
	}
	defer procCloseClipboard.Call()
	s := &Snapshot{}
	total := 0
	var f uintptr
	for {
		var err error
		f, _, err = procEnumClipboardFormats.Call(f)
		if f == 0 {
			if errno, ok := err.(syscall.Errno); ok && errno != 0 {
				return nil, fmt.Errorf("%w: %v", errClipboardEnumerationStopped, err)
			}
			return s, nil
		}
		format := uint32(f)
		data, ok := formatData(format)
		if !ok {
			s.Skipped = append(s.Skipped, format)
			continue
		}
		total += len(data)
		if total > maxSnapshotBytes {
			return nil, errSnapshotTooLarge
		}
		s.items = append(s.items, savedFormat{format: format, data: data})
	}
}

// memoryFormat reports whether the data of format is a global memory
// block.
func memoryFormat(format uint32) bool {
	switch format {
	case cfBitmap, cfMetafilePict, cfPalette, cfEnhMetafile, cfOwnerDisplay, cfDspBitmap, cfDspMetafilePict, cfDspEnhMetafile:
		return false
	}
	return format < cfPrivateFirst || format > cfGDIObjLast
}

// formatData copies the clipboard data of format with the clipboard open.
func formatData(format uint32) ([]byte, bool) {
	if format != cfEnhMetafile && !memoryFormat(format) {
		return nil, false
	}
	h, _, _ := procGetClipboardData.Call(uintptr(format))
	if h == 0 {
		return nil, false
	}
	if format == cfEnhMetafile {
		n, _, _ := procGetEnhMetaFileBits.Call(h, 0, 0)
		if n == 0 {
			return nil, false
		}
		b := make([]byte, n)
		if r, _, _ := procGetEnhMetaFileBits.Call(h, n, uintptr(unsafe.Pointer(&b[0]))); r == 0 {
			return nil, false
		}
		return b, true
	}
	size, _, _ := procGlobalSize.Call(h)
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(h)
	b := make([]byte, size)
	copy(b, unsafe.Slice((*byte)(pointerAt(p)), size))
	return b, true
}

// Restore replaces the clipboard with the snapshot through serial, marked
// to stay out of the clipboard history, unless the snapshot is empty,
// which empties the clipboard.
func (s *Snapshot) Restore() error {
	_, err := serial(func() (struct{}, error) { return struct{}{}, s.restore() })
	return err
}

func (s *Snapshot) restore() error {
	items := s.items
	if len(items) > 0 {
		items = withPrivacyMarks(items)
	}
	return replace(items)
}

// SetPrivateText replaces the clipboard with text through serial, marked
// to stay out of the clipboard history, the cloud clipboard and clipboard
// monitors.
func SetPrivateText(text string) error {
	_, err := serial(func() (struct{}, error) {
		u := utf16.Encode([]rune(text))
		b := make([]byte, 2*len(u)+2)
		for i, c := range u {
			b[2*i], b[2*i+1] = byte(c), byte(c>>8)
		}
		return struct{}{}, replace(withPrivacyMarks([]savedFormat{{format: cfUnicodeText, data: b}}))
	})
	return err
}

// withPrivacyMarks adds the formats that keep the content out of the
// clipboard history, the cloud clipboard and monitors, unless items has
// one of them already.
func withPrivacyMarks(items []savedFormat) []savedFormat {
	marks := []struct {
		name string
		data []byte
	}{{formatExcludeMonitors, []byte{0, 0, 0, 0}}, {formatCanIncludeInHistory, []byte{0, 0, 0, 0}}, {formatCanUploadToCloud, []byte{0, 0, 0, 0}}}
	var add []savedFormat
	for _, m := range marks {
		f := registeredFormat(m.name)
		if f == 0 {
			continue
		}
		for _, it := range items {
			if it.format == f {
				return items
			}
		}
		add = append(add, savedFormat{format: f, data: m.data})
	}
	return append(append([]savedFormat(nil), items...), add...)
}

// registeredFormat returns the id of the registered clipboard format name,
// or 0.
func registeredFormat(name string) uint32 {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	f, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	return uint32(f)
}

// replace empties the clipboard and puts items on it; the data handles of
// the formats Windows took belong to it, the others are freed.
func replace(items []savedFormat) error {
	handles := make([]uintptr, len(items))
	free := func(i int) {
		if handles[i] == 0 {
			return
		}
		if items[i].format == cfEnhMetafile {
			procDeleteEnhMetaFile.Call(handles[i])
		} else {
			procGlobalFree.Call(handles[i])
		}
		handles[i] = 0
	}
	for i, it := range items {
		h, err := dataHandle(it)
		if err != nil {
			for k := range i {
				free(k)
			}
			return err
		}
		handles[i] = h
	}
	if err := open(); err != nil {
		for i := range items {
			free(i)
		}
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	var failed []uint32
	for i, it := range items {
		if r, _, _ := procSetClipboardData.Call(uintptr(it.format), handles[i]); r == 0 {
			failed = append(failed, it.format)
			free(i)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("SetClipboardData failed for the formats %v", failed)
	}
	return nil
}

// dataHandle makes the data handle of it: an enhanced metafile for
// CF_ENHMETAFILE, a movable global memory block otherwise.
func dataHandle(it savedFormat) (uintptr, error) {
	if it.format == cfEnhMetafile {
		if len(it.data) == 0 {
			return 0, errors.New("SetEnhMetaFileBits: no data")
		}
		h, _, _ := procSetEnhMetaFileBits.Call(uintptr(len(it.data)), uintptr(unsafe.Pointer(&it.data[0])))
		if h == 0 {
			return 0, errors.New("SetEnhMetaFileBits failed")
		}
		return h, nil
	}
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(max(len(it.data), 1)))
	if h == 0 {
		return 0, errors.New("GlobalAlloc failed")
	}
	if len(it.data) > 0 {
		p, _, _ := procGlobalLock.Call(h)
		if p == 0 {
			procGlobalFree.Call(h)
			return 0, errors.New("GlobalLock failed")
		}
		copy(unsafe.Slice((*byte)(pointerAt(p)), len(it.data)), it.data)
		procGlobalUnlock.Call(h)
	}
	return h, nil
}
