//go:build !windows

package server

import (
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestDiagOpsOutsideWindowsAreUnsupported(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	for _, op := range []string{"diag.system", "diag.services", "diag.events", "diag.crashes"} {
		if res := c.call(op, nil); res["ok"] != false || res.String("code") != wire.CodeCapabilityUnsupported {
			t.Errorf("%s: %v", op, res)
		}
	}
}
