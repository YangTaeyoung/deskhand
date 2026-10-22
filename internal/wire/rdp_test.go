package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseRDPSessionRequiresEveryFieldAndValidIdentity(t *testing.T) {
	raw := `{"enabled":false,"port":3397,"certificate_sha256":"` + strings.Repeat("ab", 32) + `","session_id":7,"user":"PC\\desk","sid":"S-1-5-21-1","protocol":"rdp","connect_state":"active","locked":false}`
	r, err := ParseRDPSession([]byte(raw))
	if err != nil || r.CertificateSHA256 != strings.Repeat("AB", 32) || r.Locked || r.Enabled {
		t.Fatalf("valid metadata: %+v %v", r, err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	for key := range fields {
		for _, null := range []bool{false, true} {
			copy := map[string]any{}
			for k, v := range fields {
				copy[k] = v
			}
			if null {
				copy[key] = nil
			} else {
				delete(copy, key)
			}
			b, _ := json.Marshal(copy)
			if _, err := ParseRDPSession(b); err == nil {
				t.Fatalf("accepted missing/null %s: %s", key, b)
			}
		}
	}
	for key, values := range map[string][]any{
		"session_id": {0, -1, 1.5, 4294967296}, "user": {""}, "sid": {"", "another-user"}, "port": {0, -1, 65536},
		"protocol": {"unknown"}, "connect_state": {"unknown"}, "enabled": {"false"}, "locked": {0},
		"certificate_sha256": {strings.Repeat("AB", 20), strings.Repeat("ZZ", 32), strings.Repeat("AB:", 32)},
	} {
		for _, value := range values {
			copy := map[string]any{}
			for k, v := range fields {
				copy[k] = v
			}
			copy[key] = value
			b, _ := json.Marshal(copy)
			if _, err := ParseRDPSession(b); err == nil {
				t.Fatalf("accepted invalid %s: %s", key, b)
			}
		}
	}
}
