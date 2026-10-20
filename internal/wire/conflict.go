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

// Conflict is what on_conflict decides for a destination that exists.
type Conflict int

const (
	// ConflictNone: the operation goes on (skip keeps the destination,
	// replace replaces it, rename takes a free numbered name).
	ConflictNone Conflict = iota
	// ConflictCase: the destination has a name that differs only in letter
	// case, under fail or replace.
	ConflictCase
	// ConflictExists: the destination exists under fail.
	ConflictExists
	// ConflictFolder: replace with a folder on either side.
	ConflictFolder
)

// DecideConflict returns what policy decides for a destination that exists
// (docs/protocol.md §3.1 fs.copy): caseOnly says that its name differs only
// in letter case from the one asked for, folder that a folder is on either
// side. skip and rename always go on; then a case-only name is a conflict,
// then fail, then replace with a folder.
func DecideConflict(policy string, caseOnly, folder bool) Conflict {
	switch {
	case policy == "skip" || policy == "rename":
		return ConflictNone
	case caseOnly:
		return ConflictCase
	case policy == "fail":
		return ConflictExists
	case folder:
		return ConflictFolder
	}
	return ConflictNone
}
