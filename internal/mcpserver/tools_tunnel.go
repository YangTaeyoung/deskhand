package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type tunnelOpenInput struct {
	Host       string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	RemotePort int    `json:"remote_port" jsonschema:"TCP port on the PC's loopback (127.0.0.1) to reach, 1 to 65535, for example a Node inspector or DevTools port."`
	LocalPort  int    `json:"local_port,omitempty" jsonschema:"Port to listen on at 127.0.0.1 of this Mac, 1 to 65535; omit it to get a free port (result local_port)."`
}

type tunnelCloseInput struct {
	TunnelID  string `json:"tunnel_id,omitempty" jsonschema:"Tunnel to close, from tunnel_open or tunnel_list. Exactly one of tunnel_id or local_port is required."`
	LocalPort int    `json:"local_port,omitempty" jsonschema:"Local port of the tunnel to close. Exactly one of tunnel_id or local_port is required."`
}

type tunnelListInput struct {
	Host string `json:"host,omitempty" jsonschema:"Only the tunnels of this host: host_id or name from hosts_list. Omit it to list every tunnel."`
}

func portProp(s *jsonschema.Schema, name string) {
	one, max := 1.0, 65535.0
	s.Properties[name].Minimum = &one
	s.Properties[name].Maximum = &max
}

func (s *Server) registerTunnel(srv *mcp.Server) {
	add[tunnelOpenInput](s, srv, "tunnel_open",
		"Open a TCP tunnel from 127.0.0.1 of this Mac to a port on the PC's loopback (127.0.0.1), so that a local client such as a debugger or curl reaches a service that listens only on the PC. Loopback-only on both ends: the Mac side listens on 127.0.0.1 only and the PC side connects to 127.0.0.1 or ::1 only, never to another host, so the tunnel is not reachable from the network. Returns tunnel_id, local_port (connect to 127.0.0.1:local_port), remote_port and local_address. Every connection to local_port becomes one stream to remote_port; the tunnel stays open until tunnel_close, a daemon restart or the removal of the host, and it survives a reconnect of the PC (connections made while the PC is offline are closed at once; tunnel_list shows last_error). A tunnel opened by a debug session belongs to that session and is closed with it. Fails with OFFLINE when the PC is not connected, CAPABILITY_UNSUPPORTED when its agent is too old, PORT_CONFLICT when local_port is taken. Whether the remote port has a listener is only known when a client connects.",
		spec{Method: "tunnel.open"}, func(sc *jsonschema.Schema) {
			portProp(sc, "remote_port")
			portProp(sc, "local_port")
		})
	add[tunnelCloseInput](s, srv, "tunnel_close",
		"Close a tunnel opened by tunnel_open: its listener on 127.0.0.1 and every connection through it end. Name it by tunnel_id or by local_port (see tunnel_list). Tunnels owned by a debug session (owner debug:<debug_id>) are refused; they close with that debug session.",
		spec{Method: "tunnel.close"}, func(sc *jsonschema.Schema) {
			exactlyOne(sc, "tunnel_id", "local_port")
			portProp(sc, "local_port")
		})
	add[tunnelListInput](s, srv, "tunnel_list",
		"List the open tunnels, oldest first: tunnel_id, host_id, local_port, local_address (127.0.0.1:port), remote_port, owner (user, or debug:<debug_id> for a debug session), connections (open now), total_connections, failed_connections, bytes_to_remote and bytes_from_remote, and last_error (why the latest connection could not be carried, for example the PC was offline or nothing listens on remote_port). Read-only.",
		spec{Method: "tunnel.list"}, nil)
}
