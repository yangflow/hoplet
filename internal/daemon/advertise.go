package daemon

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/libp2p/zeroconf/v2"

	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/proto"
)

// bonjourService is the DNS-SD service type of the direct listener.
const bonjourService = "_hoplet._tcp"

// advertisable reports whether a listener at addr can be reached from the
// local network.
func advertisable(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	return ok && !tcp.IP.IsLoopback()
}

// advertise announces the direct listener on the local network under the
// host's ID, which says nothing about the machine; Noise authenticates
// whoever answers. It returns the function that ends the announcement. A
// listener only reachable from this machine is not announced, and a
// network that refuses multicast costs a warning, not the daemon.
func advertise(addr net.Addr, id noise.HostID, log *slog.Logger) (stop func()) {
	stop = func() {}
	if !advertisable(addr) {
		return stop
	}
	text := []string{fmt.Sprintf("v=%d", proto.ProtocolMajor)}
	srv, err := zeroconf.Register(string(id), bonjourService, "local.", addr.(*net.TCPAddr).Port, text, nil)
	if err != nil {
		log.Warn("announce the direct listener on the local network", "err", err)
		return stop
	}
	return srv.Shutdown
}
