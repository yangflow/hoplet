package daemon

import (
	"context"
	"log"
	gosync "sync"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

// store is the locked view of ~/.hoplet/projects.json shared by projects and sessions control.
type store struct {
	cfg  *config.Config
	mu   gosync.Mutex
	file *projects.File
	kick func()
}

func newStore(cfg *config.Config, file *projects.File) *store {
	return &store{cfg: cfg, file: file}
}

func (s *store) setKick(kick func()) { s.kick = kick }

func (s *store) reload() error {
	next, err := projects.Load(s.cfg.HopletHome)
	if err != nil {
		return err
	}
	s.file = next
	return nil
}

// listing builds the device.projects reply snapshot (paths + session rows). A path is reported with symbolic
// links resolved, as the sessions run there record it.
func (s *store) listing() ([]string, []sync.DeviceProject) {
	sources := Scan(s.cfg, context.Background())
	byID := make(map[string]provider.Source, len(sources))
	for _, src := range sources {
		byID[src.ID()] = src
	}
	cands := s.file.Suggest(sources)
	var out []sync.DeviceProject
	for _, it := range s.file.Items {
		pend := s.file.Sessions(it.Path, sources)
		sess := make([]sync.DeviceProjectSession, 0, len(pend))
		for _, p := range pend {
			title := ""
			if src, ok := byID[p.SessionID]; ok {
				title = provider.PeekTitle(context.Background(), src)
			}
			sess = append(sess, sync.DeviceProjectSession{
				SessionID: p.SessionID,
				Cwd:       p.Cwd,
				Imported:  p.Imported,
				Title:     title,
			})
		}
		dir := projects.Real(it.Path)
		out = append(out, sync.DeviceProject{Path: dir, Branch: workspace.Branch(dir), Sessions: sess})
	}
	if cands == nil {
		cands = []string{}
	}
	if out == nil {
		out = []sync.DeviceProject{}
	}
	return cands, out
}

// poll reloads projects.json and returns the allowlisted sources. A bad file syncs nothing.
func (s *store) poll(sources []provider.Source) []provider.Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		log.Printf("reload projects: %v", err)
		return nil
	}
	return s.file.Sync(sources)
}
