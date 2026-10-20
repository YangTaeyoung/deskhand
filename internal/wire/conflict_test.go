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

func TestNumberedNameKeepsTheExtensionOfAFile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		k      int
		folder bool
		want   string
	}{
		{"a.txt", 1, false, "a (1).txt"},
		{"a.tar.gz", 2, false, "a.tar (2).gz"},
		{".bashrc", 1, false, ".bashrc (1)"},
		{"noext", 3, false, "noext (3)"},
		{"v1.2", 1, true, "v1.2 (1)"},
		{"보고서.hwp", 1, false, "보고서 (1).hwp"},
	} {
		if got := NumberedName(tc.name, tc.k, tc.folder); got != tc.want {
			t.Errorf("NumberedName(%q, %d, %v) = %q, want %q", tc.name, tc.k, tc.folder, got, tc.want)
		}
	}
	if RenameMax != 10000 {
		t.Fatalf("RenameMax %d", RenameMax)
	}
}

func TestDecideConflictIsOneTableForAnExistingDestination(t *testing.T) {
	for _, tc := range []struct {
		policy           string
		caseOnly, folder bool
		want             Conflict
	}{
		{"fail", false, false, ConflictExists},
		{"fail", true, false, ConflictCase},
		{"fail", false, true, ConflictExists},
		{"replace", false, false, ConflictNone},
		{"replace", true, false, ConflictCase},
		{"replace", false, true, ConflictFolder},
		{"replace", true, true, ConflictCase},
		{"skip", true, true, ConflictNone},
		{"rename", true, true, ConflictNone},
		{"rename", false, false, ConflictNone},
	} {
		if got := DecideConflict(tc.policy, tc.caseOnly, tc.folder); got != tc.want {
			t.Errorf("DecideConflict(%s, case %v, folder %v) = %v, want %v", tc.policy, tc.caseOnly, tc.folder, got, tc.want)
		}
	}
}
