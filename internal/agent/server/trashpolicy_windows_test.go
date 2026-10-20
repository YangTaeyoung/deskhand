//go:build windows

package server

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestTrashRefusesWhenARecycleBinSettingCannotBeRead(t *testing.T) {
	realPC(t)
	file := filepath.Join(opsDir(t), "kept.txt")
	writeFile(t, file, "kept")
	fi, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalPath(file)
	if err != nil {
		t.Fatal(err)
	}
	read := readDWORD
	t.Cleanup(func() { readDWORD = read })
	settings := func(failing string, failure error) func(registry.Key, string, string) (uint64, error) {
		return func(hive registry.Key, path, name string) (uint64, error) {
			switch {
			case name == failing:
				return 0, failure
			case name == "MaxCapacity":
				return 1024, nil
			}
			return 0, registry.ErrNotExist
		}
	}

	readDWORD = settings("", nil)
	if err := recyclable(canonical, fi); err != nil {
		t.Fatalf("settings that are not set refuse the Recycle Bin: %v", err)
	}

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"NoRecycleFiles", windows.ERROR_ACCESS_DENIED},
		{"NoRecycleFiles", registry.ErrUnexpectedType},
		{"RecycleBinSize", windows.ERROR_ACCESS_DENIED},
		{"NukeOnDelete", windows.ERROR_ACCESS_DENIED},
		{"NukeOnDelete", registry.ErrUnexpectedType},
		{"MaxCapacity", windows.ERROR_ACCESS_DENIED},
	} {
		readDWORD = settings(tc.name, tc.err)
		err := recyclable(canonical, fi)
		if err == nil {
			t.Fatalf("%s failing with %v lets the Recycle Bin take the file", tc.name, tc.err)
		}
		failsWith(t, failure(1, err), wire.CodeCapabilityUnsupported, tc.name)
	}
	if readFile(t, file) != "kept" {
		t.Fatal("the file changed")
	}
}
