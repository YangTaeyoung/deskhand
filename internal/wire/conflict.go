package wire

import (
	"fmt"
	"slices"
	"strings"
)

// conflictPolicies are the values of on_conflict, the default first.
var conflictPolicies = []string{"fail", "replace", "skip", "rename"}

// ConflictPolicies returns the values of on_conflict, the default ("fail")
// first.
func ConflictPolicies() []string { return slices.Clone(conflictPolicies) }

// IsConflictPolicy reports whether p is a value of on_conflict.
func IsConflictPolicy(p string) bool { return slices.Contains(conflictPolicies, p) }

// RenameMax bounds the numbered names on_conflict rename tries.
const RenameMax = 10000

// NumberedName returns the entry name name with " (k)" added, before the
// extension of a file name that has a name before its extension; on_conflict
// rename tries these names from k = 1.
func NumberedName(name string, k int, folder bool) string {
	ext := ""
	if i := strings.LastIndexByte(name, '.'); !folder && i > 0 {
		ext = name[i:]
	}
	return fmt.Sprintf("%s (%d)%s", name[:len(name)-len(ext)], k, ext)
}
