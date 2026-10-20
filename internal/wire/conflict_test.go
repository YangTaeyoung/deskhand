package wire

import (
	"slices"
	"testing"
)

func TestConflictPoliciesAreFailReplaceSkipRename(t *testing.T) {
	want := []string{"fail", "replace", "skip", "rename"}
	if got := ConflictPolicies(); !slices.Equal(got, want) {
		t.Fatalf("ConflictPolicies %v", got)
	}
	for _, p := range want {
		if !IsConflictPolicy(p) {
			t.Errorf("%s is not a policy", p)
		}
	}
	for _, p := range []string{"", "merge", "Fail"} {
		if IsConflictPolicy(p) {
			t.Errorf("%q is a policy", p)
		}
	}
	ConflictPolicies()[0] = "changed"
	if ConflictPolicies()[0] != "fail" {
		t.Fatal("the caller changed the list")
	}
}
