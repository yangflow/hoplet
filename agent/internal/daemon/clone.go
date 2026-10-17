package daemon

import (
	"context"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

// newCloner is the cloner of device.clone.*: it clones inside the browse root and makes a finished clone a
// watched project.
func newCloner(cfg *config.Config, st *store) *workspace.Cloner {
	return &workspace.Cloner{
		Home:     cfg.HopletHome,
		Root:     cfg.BrowseRoot,
		Watched:  st.watchedPaths,
		Register: st.watchProject,
	}
}

// watchProject makes path a watched project, with no session imported (the clone of a handoff target).
func (s *store) watchProject(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return err
	}
	if err := s.file.Add(projects.Real(path)); err != nil {
		return err
	}
	if err := s.file.Save(s.cfg.HopletHome); err != nil {
		return err
	}
	if s.kick != nil {
		s.kick()
	}
	return nil
}

// cloneCheck handles device.clone.check.
func cloneCheck(cl *workspace.Cloner) func(context.Context, string, string) sync.CloneChecked {
	return func(ctx context.Context, origin, cloneInto string) sync.CloneChecked {
		r := cl.Check(ctx, origin, cloneInto)
		return sync.CloneChecked{Reason: r.Reason, Detail: r.Detail, CloneInto: r.CloneInto}
	}
}

// cloneStart handles device.clone.start.
func cloneStart(cl *workspace.Cloner) func(context.Context, string, string, string, func(int64, *int64)) (string, error) {
	return func(ctx context.Context, handoffID, origin, cloneInto string, progress func(int64, *int64)) (string, error) {
		path, err := cl.Clone(ctx, handoffID, origin, cloneInto, func(received, total int64) {
			if total >= 0 {
				progress(received, &total)
				return
			}
			progress(received, nil)
		})
		if err != nil {
			if f, ok := err.(*workspace.CloneFailure); ok {
				return "", &sync.CloneError{Reason: f.Reason, Detail: f.Detail}
			}
			return "", &sync.CloneError{Reason: workspace.CloneGitFailed}
		}
		return path, nil
	}
}
