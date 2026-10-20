package diag

import (
	"fmt"
	"testing"
)

func TestServiceStatesAndStartTypesUseTheProtocolNames(t *testing.T) {
	states := map[uint32]string{1: "stopped", 2: "start_pending", 3: "stop_pending", 4: "running", 5: "start_pending", 6: "stop_pending", 7: "paused", 0: "unknown", 9: "unknown"}
	for code, want := range states {
		if got := serviceState(code); got != want {
			t.Errorf("state %d: %s, expected %s", code, got, want)
		}
	}
	starts := []struct {
		code    uint32
		delayed bool
		want    string
	}{
		{0, false, "auto"}, {1, false, "auto"}, {2, false, "auto"}, {2, true, "delayed_auto"}, {3, false, "manual"}, {3, true, "manual"},
		{4, false, "disabled"}, {7, false, "unknown"},
	}
	for _, s := range starts {
		if got := startType(s.code, s.delayed); got != s.want {
			t.Errorf("start %d delayed %v: %s, expected %s", s.code, s.delayed, got, s.want)
		}
	}
}

func TestServicesMatchTheNameOrDisplayNameAndAreSortedByName(t *testing.T) {
	all := []Service{
		{Name: "wuauserv", DisplayName: "Windows Update"},
		{Name: "BITS", DisplayName: "Background Intelligent Transfer Service"},
		{Name: "UsoSvc", DisplayName: "Update Orchestrator Service"},
		{Name: "Audiosrv", DisplayName: "Windows Audio"},
	}
	cases := map[string]string{
		"":       "[Audiosrv BITS UsoSvc wuauserv]",
		"UPDATE": "[UsoSvc wuauserv]",
		"bits":   "[BITS]",
		"svc":    "[UsoSvc]",
		"none":   "[]",
	}
	for filter, want := range cases {
		var names []string
		for _, s := range selectServices(all, filter) {
			names = append(names, s.Name)
		}
		if names == nil {
			names = []string{}
		}
		if got := fmt.Sprint(names); got != want {
			t.Errorf("filter %q: %s, expected %s", filter, got, want)
		}
	}
}
