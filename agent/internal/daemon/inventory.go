package daemon

import (
	"errors"
	"log"

	"github.com/yangflow/hoplet/agent/internal/inventory"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// newInventory connects the detection of the machine's tools, GPUs and watched-project remotes to the core
// connection: it reports on the control lane when it starts, after every connection, every hour and soon after
// the watched projects change. Detection runs in its own goroutine, never on the connection's read loop.
func newInventory(st *store, client func() *sync.Client) *inventory.Collector {
	return inventory.New(inventory.Config{
		Projects: st.paths,
		Send: func(r inventory.Report) {
			c := client()
			if c == nil {
				return
			}
			out := sync.DeviceInventory{
				At:           r.At.UnixMilli(),
				Toolchains:   make([]sync.InventoryToolchain, 0, len(r.Toolchains)),
				Gpus:         make([]sync.InventoryGpu, 0, len(r.GPUs)),
				Repositories: make([]sync.InventoryRepository, 0, len(r.Repositories)),
			}
			for _, t := range r.Toolchains {
				out.Toolchains = append(out.Toolchains, sync.InventoryToolchain{Name: t.Name, Version: t.Version})
			}
			for _, g := range r.GPUs {
				out.Gpus = append(out.Gpus, sync.InventoryGpu{Name: g.Name, Memory: g.Memory})
			}
			for _, p := range r.Repositories {
				out.Repositories = append(out.Repositories, sync.InventoryRepository{Path: p.Path, Origin: p.Origin})
			}
			if err := c.SendDeviceInventory(out); err != nil && !errors.Is(err, sync.ErrNotConnected) {
				log.Printf("[inventory] send: %v", err)
			}
		},
	})
}

// paths are the watched projects, symbolic links resolved as the project list reports them.
func (s *store) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.file.Items))
	for _, it := range s.file.Items {
		out = append(out, projects.Real(it.Path))
	}
	return out
}
