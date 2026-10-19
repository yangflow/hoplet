package daemon

import (
	"errors"
	"log"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/resources"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// newResourceSampler connects the measurement of the machine to the core connection: every 15 seconds it
// reports the machine's load and that of the processes Hoplet started for each session (its Claude Code or
// Codex process and its terminal shell) on the control lane. It is nil on a platform that is not measured.
func newResourceSampler(cfg *config.Config, bridge *bridge, terms *terminals, client func() *sync.Client) *resources.Sampler {
	reader := resources.NewReader()
	if reader == nil {
		return nil
	}
	return resources.New(resources.Config{
		Reader:   reader,
		DiskPath: cfg.BrowseRoot,
		Roots: func() map[string][]int {
			roots := map[string][]int{}
			for _, m := range bridge.managers() {
				for id, pid := range m.PIDs() {
					roots[id] = append(roots[id], pid)
				}
			}
			for id, pid := range terms.mgr.ShellPIDs() {
				roots[id] = append(roots[id], pid)
			}
			return roots
		},
		Send: func(s resources.Sample) {
			c := client()
			if c == nil {
				return
			}
			out := sync.DeviceResources{
				At: s.At.UnixMilli(), CPU: s.CPU, MemUsed: s.MemUsed, MemTotal: s.MemTotal,
				DiskFree: s.DiskFree, DiskTotal: s.DiskTotal,
				Sessions: make([]sync.SessionResources, 0, len(s.Sessions)),
			}
			for _, u := range s.Sessions {
				out.Sessions = append(out.Sessions, sync.SessionResources{SessionID: u.SessionID, CPU: u.CPU, RSS: u.RSS, Children: u.Children})
			}
			if err := c.SendDeviceResources(out); err != nil && !errors.Is(err, sync.ErrNotConnected) {
				log.Printf("[resources] send: %v", err)
			}
		},
	})
}
