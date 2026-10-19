package daemon

import (
	"errors"
	"log"

	"github.com/yangflow/hoplet/agent/internal/ports"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// newPortWatcher connects the port scan to the core connection (ADR 0026): it scans for the sessions the core
// names, scopes each by the session's directory and by the processes Hoplet started for it (its Claude Code
// process and its terminal shell), and reports on the control lane.
func newPortWatcher(scan ports.Scanner, locate runtime.Locator, bridge *bridge, terms *terminals, client func() *sync.Client) *ports.Watcher {
	return ports.New(ports.Config{
		Scanner: scan,
		Dir: func(sessionID string) (string, error) {
			loc, err := locate(sessionID)
			return loc.Dir, err
		},
		Roots: func(sessionID string) []int {
			return []int{bridge.managerOf(sessionID).PID(sessionID), terms.mgr.ShellPID(sessionID)}
		},
		Send: func(sessionID string, found []ports.Port) {
			c := client()
			if c == nil {
				return
			}
			out := make([]sync.SessionPort, 0, len(found))
			for _, p := range found {
				out = append(out, sync.SessionPort{Port: p.Port, Address: p.Address, Process: p.Process})
			}
			if err := c.SendSessionPorts(sessionID, out); err != nil && !errors.Is(err, sync.ErrNotConnected) {
				log.Printf("[ports] send: %v", err)
			}
		},
	})
}
