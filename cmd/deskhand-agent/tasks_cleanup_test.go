package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestRemoveManagedTasksContinuesAfterFailure(t *testing.T) {
	wantErr := errors.New("access denied")
	var removed []string
	err := removeManagedTasks(func(name string) error {
		removed = append(removed, name)
		if name == taskName {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("lost removal failure: %v", err)
	}
	if want := []string{"Deskhand Agent", "Deskhand Console Handoff"}; !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed %q; want only app-owned tasks %q", removed, want)
	}
}

func TestRemoveManagedTasksSuccess(t *testing.T) {
	if err := removeManagedTasks(func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
