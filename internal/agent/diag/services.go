package diag

import (
	"sort"
	"strings"
)

// serviceState returns the diag.services state of a SERVICE_STATUS
// dwCurrentState: a service continuing is start_pending and one pausing
// stop_pending.
func serviceState(state uint32) string {
	switch state {
	case 1:
		return "stopped"
	case 2, 5:
		return "start_pending"
	case 3, 6:
		return "stop_pending"
	case 4:
		return "running"
	case 7:
		return "paused"
	}
	return "unknown"
}

// startType returns the diag.services start type of a QUERY_SERVICE_CONFIG
// dwStartType: boot, system and automatic start are auto, delayed_auto with
// delayed set on an automatic start.
func startType(start uint32, delayed bool) string {
	switch start {
	case 0, 1:
		return "auto"
	case 2:
		if delayed {
			return "delayed_auto"
		}
		return "auto"
	case 3:
		return "manual"
	case 4:
		return "disabled"
	}
	return "unknown"
}

// serviceMatches reports whether the name or display name contains filter
// without regard to case; an empty filter matches every service.
func serviceMatches(filter, name, display string) bool {
	f := strings.ToLower(filter)
	return strings.Contains(strings.ToLower(name), f) || strings.Contains(strings.ToLower(display), f)
}

// selectServices returns the services of all that serviceMatches filter,
// sorted by name without regard to case.
func selectServices(all []Service, filter string) []Service {
	out := []Service{}
	for _, s := range all {
		if serviceMatches(filter, s.Name, s.DisplayName) {
			out = append(out, s)
		}
	}
	sortServices(out)
	return out
}

// sortServices sorts services by name without regard to case.
func sortServices(services []Service) {
	sort.SliceStable(services, func(i, j int) bool {
		return strings.ToLower(services[i].Name) < strings.ToLower(services[j].Name)
	})
}
