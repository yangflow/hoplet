package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yangflow/hoplet/agent/internal/projects"

	"github.com/yangflow/hoplet/agent/internal/sync"
)

// projectsList handles device.projects.list (CLI: projects list).
func (s *store) projectsList(_ context.Context) ([]string, []sync.DeviceProject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return nil, nil, err
	}
	cands, listed := s.listing()
	return cands, listed, nil
}

// existingDir resolves symlinks in an absolute path and requires the result to be a directory.
func existingDir(path string) (string, error) {
	p := projects.Clean(path)
	if !filepath.IsAbs(p) {
		return "", errors.New("path must be absolute")
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%s does not exist", p)
		}
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", p)
	}
	return real, nil
}

// projectsAdd handles device.projects.add (the same file change as CLI: projects add). The path must be an
// existing directory; it is stored with symlinks resolved.
func (s *store) projectsAdd(_ context.Context, path string) ([]string, []sync.DeviceProject, error) {
	path, err := existingDir(path)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return nil, nil, err
	}
	if err := s.file.Add(path); err != nil {
		return nil, nil, err
	}
	if err := s.file.Save(s.cfg.HopletHome); err != nil {
		return nil, nil, err
	}
	cands, listed := s.listing()
	return cands, listed, nil
}

// projectsRemove handles device.projects.remove (CLI: projects remove).
func (s *store) projectsRemove(_ context.Context, path string) ([]string, []sync.DeviceProject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return nil, nil, err
	}
	if err := s.file.RemovePath(path); err != nil {
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
