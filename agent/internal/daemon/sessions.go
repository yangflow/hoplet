package daemon

import (
	"context"
	"errors"

	"github.com/yangflow/hoplet/agent/internal/sync"
)

// sessionsImport handles device.sessions.import (CLI: sessions import).
func (s *store) sessionsImport(_ context.Context, path string, sessionIDs []string) ([]string, []sync.DeviceProject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return nil, nil, err
	}
	if err := s.file.Import(path, sessionIDs); err != nil {
		return nil, nil, err
	}
	if err := s.file.Save(s.cfg.HopletHome); err != nil {
		return nil, nil, err
	}
	cands, listed := s.listing()
	if s.kick != nil {
		s.kick()
	}
	return cands, listed, nil
}

// sessionsRemove handles device.sessions.remove (CLI: sessions remove).
func (s *store) sessionsRemove(_ context.Context, path string, sessionIDs []string) ([]string, []sync.DeviceProject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return nil, nil, err
	}
	if len(sessionIDs) == 0 {
		return nil, nil, errors.New("missing session ids")
	}
	if err := s.file.ClearImports(path, sessionIDs); err != nil {
		return nil, nil, err
	}
	if err := s.file.Save(s.cfg.HopletHome); err != nil {
		return nil, nil, err
	}
	cands, listed := s.listing()
	if s.kick != nil {
		s.kick()
	}
	return cands, listed, nil
}
