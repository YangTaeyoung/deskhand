package server

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
)

// recycleRecord is one item of a Recycle Bin folder: its $I record, the $R
// entry that holds the item, the path it was deleted from and when.
type recycleRecord struct {
	info, data string
	original   string
	deleted    time.Time
}

// fileTimeEpoch is the Unix epoch as a FILETIME: 100 ns intervals since
// 1601-01-01 UTC.
const fileTimeEpoch = 116444736000000000

// readRecycleInfo returns the path and the deletion time a $I record b
// holds. Both versions keep the deletion time as a FILETIME at offset 16;
// version 1 keeps the path in 260 UTF-16 code units from offset 24, version
// 2 keeps its length in code units at offset 24 and the path from offset 28.
func readRecycleInfo(b []byte) (string, time.Time, bool) {
	if len(b) < 28 {
		return "", time.Time{}, false
	}
	var units []uint16
	switch binary.LittleEndian.Uint64(b) {
	case 1:
		if len(b) < 24+2*260 {
			return "", time.Time{}, false
		}
		units = make([]uint16, 260)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(b[24+2*i:])
		}
	case 2:
		n := int(binary.LittleEndian.Uint32(b[24:]))
		if n < 0 || 28+2*n > len(b) {
			return "", time.Time{}, false
		}
		units = make([]uint16, n)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(b[28+2*i:])
		}
	default:
		return "", time.Time{}, false
	}
	if i := slices.Index(units, 0); i >= 0 {
		units = units[:i]
	}
	ft := int64(binary.LittleEndian.Uint64(b[16:]))
	return string(utf16.Decode(units)), time.Unix(0, (ft-fileTimeEpoch)*100).UTC(), true
}

// findRecycleRecord returns the item of the Recycle Bin folder bin whose
// $I record names original, compared without letter case, with a deletion
// time at since or later; since is taken in whole milliseconds, the
// precision of the deletion times the shell records.
func findRecycleRecord(bin, original string, since time.Time) (recycleRecord, bool, error) {
	ents, err := os.ReadDir(bin)
	if err != nil {
		return recycleRecord{}, false, err
	}
	from := since.Truncate(time.Millisecond)
	for _, e := range ents {
		name := e.Name()
		if len(name) < 3 || !strings.EqualFold(name[:2], "$I") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(bin, name))
		if err != nil {
			continue
		}
		path, deleted, ok := readRecycleInfo(b)
		if !ok || !strings.EqualFold(path, original) || deleted.Before(from) {
			continue
		}
		return recycleRecord{info: filepath.Join(bin, name), data: filepath.Join(bin, "$R"+name[2:]), original: path, deleted: deleted}, true, nil
	}
	return recycleRecord{}, false, nil
}
