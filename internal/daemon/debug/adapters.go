package debug

import (
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/hosts"
)

// HostsFrom returns the Hosts of the daemon's host registry.
func HostsFrom(reg *hosts.Registry) Hosts { return registryHosts{reg: reg} }

type registryHosts struct{ reg *hosts.Registry }

func (h registryHosts) Conn(ref string) (Agent, HostInfo, error) {
	c, row, err := h.reg.Conn(ref)
	if err != nil {
		return nil, HostInfo{}, err
	}
	return c, HostInfo{HostID: row.HostID, Name: row.Name}, nil
}

func (h registryHosts) SessionID(hostID string) string { return h.reg.SessionID(hostID) }

// ensure *conn.Host satisfies Agent.
var _ Agent = (*conn.Host)(nil)
