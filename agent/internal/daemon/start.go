package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

const (
	// startWait bounds how long a new session may take to create its file: the process starts, answers its
	// initialization and takes the prompt, which is when Claude Code writes the file.
	startWait = 60 * time.Second
	// startPoll is how often the expected path of the file is checked.
	startPoll = 100 * time.Millisecond
	// startLocateEvery is how many polls pass between searches of the whole store for the session.
	startLocateEvery = 10
)

// watched returns the watched project that path names, or an error when it is not one.
func (s *store) watched(path string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return "", err
	}
	want := projects.Real(path)
	for _, it := range s.file.Items {
		if projects.Real(it.Path) == want {
			return want, nil
		}
	}
	return "", errors.New("not a watched project")
}

// importNew marks a session the agent started as imported, so it syncs from the next scan on.
func (s *store) importNew(path, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return err
	}
	if err := s.file.Import(path, []string{sessionID}); err != nil {
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

// starter handles runtime.start (docs/spec.md#new-session): it starts the process in a watched project with
// no session to resume, writes the prompt, waits until the vendor created the session file, imports the
// session and returns its ID. A session that could not be completed is stopped and forgotten.
func (b *bridge) starter(st *store) func(context.Context, sync.RuntimeStart) (string, error) {
	return func(ctx context.Context, req sync.RuntimeStart) (string, error) {
		id := req.Configuration
		if id == "" {
			id = req.Provider
		}
		e := b.entry(id)
		if e == nil && req.Configuration == "" {
			return "", fmt.Errorf("unsupported provider %q", req.Provider)
		}
		if e == nil || !e.config().Enabled || e.config().Extends != req.Provider {
			return "", errUnavailable
		}
		conf, mgr := e.config(), e.mgr
		if strings.TrimSpace(req.Text) == "" {
			return "", errors.New("empty prompt")
		}
		path, err := st.watched(req.ProjectPath)
		if err != nil {
			return "", err
		}
		p, err := b.att.prompt(req.Text, req.Attachments)
		if err != nil {
			return "", err
		}
		sessionID, err := mgr.CreateWith(path, p, req.Config)
		if err != nil {
			return "", err
		}
		discard := func() {
			mgr.Discard(sessionID, 15*time.Second)
			_ = b.owners.Forget(sessionID)
		}
		// The configuration is recorded before the session can be resumed: its environment, not the vendor's
		// default one, must run it again.
		if err := b.owners.Set(sessionID, conf.ID); err != nil {
			discard()
			return "", fmt.Errorf("record the session's configuration: %w", err)
		}
		var store provider.Store
		if sp, ok := storeOf(b.cfg, conf).(provider.Store); ok {
			store = sp
		}
		if err := b.waitForSession(ctx, mgr, store, sessionID, path); err != nil {
			discard()
			return "", err
		}
		if err := st.importNew(path, sessionID); err != nil {
			discard()
			return "", fmt.Errorf("import the new session: %w", err)
		}
		return sessionID, nil
	}
}

// waitForSession returns once the session's file exists, or an error when the process failed first.
func (b *bridge) waitForSession(ctx context.Context, mgr *runtime.Manager, store provider.Store, id, path string) error {
	// A native vendor names the file it will write; any other is found by locating the session.
	expected := ""
	if store != nil {
		expected = store.SessionFile(path, id)
	}
	deadline := time.NewTimer(startWait)
	defer deadline.Stop()
	tick := time.NewTicker(startPoll)
	defer tick.Stop()
	for n := 0; ; n++ {
		if _, err := os.Stat(expected); expected != "" && err == nil {
			return nil
		}
		if n%startLocateEvery == 0 {
			if _, err := b.locate(id); err == nil {
				return nil
			}
		}
		if state, msg := mgr.Info(id); state == runtime.StateFailed {
			if msg == "" {
				msg = "the process failed"
			}
			return errors.New(msg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("the process did not create the session in time")
		case <-tick.C:
		}
	}
}
