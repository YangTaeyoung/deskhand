package wire

import "slices"

// conflictPolicies are the values of on_conflict, the default first.
var conflictPolicies = []string{"fail", "replace", "skip", "rename"}

// ConflictPolicies returns the values of on_conflict, the default ("fail")
// first.
func ConflictPolicies() []string { return slices.Clone(conflictPolicies) }

// IsConflictPolicy reports whether p is a value of on_conflict.
func IsConflictPolicy(p string) bool { return slices.Contains(conflictPolicies, p) }
