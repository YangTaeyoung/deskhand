package qa

import (
	"context"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/control"
	"github.com/YangTaeyoung/deskhand/internal/daemon/hosts"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/xfer"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// HostsFrom returns the Hosts of the daemon's host registry.
func HostsFrom(reg *hosts.Registry) Hosts { return registryHosts{reg: reg} }

type registryHosts struct{ reg *hosts.Registry }

func (h registryHosts) Resolve(ref string) (Host, error) {
	row, err := h.reg.Resolve(ref)
	if err != nil {
		return Host{}, err
	}
	m := h.reg.Model(row)
	return Host{HostID: row.HostID, Name: row.Name, OS: row.OS, Arch: row.Arch, Address: row.Address, AgentVersion: m.AgentVersion}, nil
}

func (h registryHosts) Agent(hostID string) Agent {
	c, _, err := h.reg.Conn(hostID)
	if err != nil {
		return nil
	}
	return hostAgent{h: c}
}

// hostAgent is the Agent of a host connection.
type hostAgent struct{ h *conn.Host }

func (a hostAgent) Call(ctx context.Context, op string, params proto.Message, opts conn.CallOpts) (proto.Message, error) {
	return a.h.Call(ctx, op, params, opts)
}

// Caps returns the capabilities the agent announced last, nil before it
// has connected.
func (a hostAgent) Caps() map[string]Capability {
	hello := a.h.Hello()
	if hello.Caps == nil {
		return nil
	}
	out := make(map[string]Capability, len(hello.Caps))
	for k, c := range hello.Caps {
		out[k] = Capability{State: c.State, Reason: c.Reason}
	}
	return out
}

// ControlFrom returns the Control of the daemon's control manager: a step
// that drives the UI is AI input of the run's caller (control.CheckAI),
// whoever started the run.
func ControlFrom(m *control.Manager) Control { return managerControl{m: m} }

type managerControl struct{ m *control.Manager }

func (c managerControl) Check(_ context.Context, hostID string, caller model.Caller) (uint64, error) {
	return c.m.CheckAI(hostID, caller)
}

// XferPusher returns the Pusher of the daemon's transfer manager.
func XferPusher(m *xfer.Manager) Pusher { return xferPusher{m: m} }

type xferPusher struct{ m *xfer.Manager }

// pushPoll is the wait between two reads of a transfer that runs.
const pushPoll = 200 * time.Millisecond

// Push starts a push transfer and waits for its end; when ctx ends first
// the transfer is cancelled.
func (p xferPusher) Push(ctx context.Context, hostID, local, remote, onConflict string, c model.Caller) (model.TransferDetail, error) {
	res, err := p.m.Start(ctx, xfer.StartParams{HostID: hostID, Direction: xfer.Push, Local: local, Remote: remote, OnConflict: onConflict}, c)
	if err != nil {
		return model.TransferDetail{}, err
	}
	if res.Err != nil {
		return model.TransferDetail{}, res.Err
	}
	id := res.Transfer.TransferID
	t := time.NewTicker(pushPoll)
	defer t.Stop()
	for {
		td, err := p.m.Get(id)
		if err != nil {
			return model.TransferDetail{}, err
		}
		switch td.State {
		case xfer.StateDone, xfer.StateFailed, xfer.StateCancelled:
			return td, nil
		}
		select {
		case <-ctx.Done():
			if cancelled, err := p.m.Cancel(id, c); err == nil {
				td = cancelled
			}
			return td, ctx.Err()
		case <-t.C:
		}
	}
}
