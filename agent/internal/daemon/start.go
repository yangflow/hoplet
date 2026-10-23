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
	"github.com/yangflow/hoplet/agent/internal/rules"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
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

// addProject makes path a watched project and reports whether it was not one before.
func (s *store) addProject(path string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return false, err
	}
	created := true
	for _, it := range s.file.Items {
		if projects.Real(it.Path) == projects.Real(path) {
			created = false
		}
	}
	if err := s.file.Add(path); err != nil {
		return false, err
	}
	if err := s.file.Save(s.cfg.HopletHome); err != nil {
		return false, err
	}
	return created, nil
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
//
// With req.Worktree the session runs in a new git worktree of the project's repository, made before the provider
// starts (workspace.CreateNewWorktree). The worktree directory becomes a watched project like the one a handoff
// installs, and the worktree is tied to the session by the repository's configuration so that deleting the session
// finds it (ADR 0023). A start that fails takes the worktree, its branch and the project away again.
func (b *bridge) starter(st *store) func(context.Context, sync.RuntimeStart) (sync.RuntimeStartResult, error) {
	return func(ctx context.Context, req sync.RuntimeStart) (sync.RuntimeStartResult, error) {
		var none sync.RuntimeStartResult
		id := req.Configuration
		if id == "" {
			id = req.Provider
		}
		e := b.entry(id)
		if e == nil && req.Configuration == "" {
			return none, fmt.Errorf("unsupported provider %q", req.Provider)
		}
		if e == nil || !e.config().Enabled || e.config().Extends != req.Provider {
			return none, errUnavailable
		}
		conf, mgr := e.config(), e.mgr
		if strings.TrimSpace(req.Text) == "" {
			return none, errors.New("empty prompt")
		}
		path, err := st.watched(req.ProjectPath)
		if err != nil {
			return none, err
		}
		p, err := b.att.prompt(req.Text, req.Attachments)
		if err != nil {
			return none, err
		}
		var made *workspace.NewWorktree
		dir := path
		if req.Worktree != nil {
			w, err := workspace.CreateNewWorktree(ctx, path, req.Worktree.Branch)
			if err != nil {
				return none, err
			}
			made, dir = &w, w.Dir
		}
		// undo takes back what this start made.
		undo := func(projectAdded bool) {
			if made == nil {
				return
			}
			if projectAdded {
				_ = st.dropProject(dir)
			}
			made.Remove(context.WithoutCancel(ctx))
		}
		projectAdded := false
		if made != nil {
			added, err := st.addProject(dir)
			if err != nil {
				undo(false)
				return none, fmt.Errorf("watch the worktree: %w", err)
			}
			projectAdded = added
		}
		// A session another session started obeys that session's deny and ask rules from its first tool call,
		// before the core knows its ID (ADR 0029); an allow rule among them is refused.
		var carry *rules.Carry
		if len(req.InheritedRules) > 0 {
			c, err := b.rules.Inherit(dir, req.InheritedRules)
			if err != nil {
				undo(projectAdded)
				return none, err
			}
			carry = c
			defer carry.Drop()
		}
		sessionID, err := mgr.CreateWith(dir, p, req.Config)
		if err != nil {
			undo(projectAdded)
			return none, err
		}
		if carry != nil {
			carry.Bind(sessionID)
		}
		discard := func() {
			mgr.Discard(sessionID, 15*time.Second)
			_ = b.owners.Forget(sessionID)
			undo(projectAdded)
		}
		// The configuration is recorded before the session can be resumed: its environment, not the vendor's
		// default one, must run it again.
		if err := b.owners.Set(sessionID, conf.ID); err != nil {
			discard()
			return none, fmt.Errorf("record the session's configuration: %w", err)
		}
		if made != nil {
			if err := made.Mark(ctx, sessionID); err != nil {
				discard()
				return none, fmt.Errorf("record the session's worktree: %w", err)
			}
		}
		var store provider.Store
		if sp, ok := storeOf(b.cfg, conf).(provider.Store); ok {
			store = sp
		}
		if err := b.waitForSession(ctx, mgr, store, sessionID, dir); err != nil {
			discard()
			return none, err
		}
		if err := st.importNew(dir, sessionID); err != nil {
			discard()
			return none, fmt.Errorf("import the new session: %w", err)
		}
		res := sync.RuntimeStartResult{SessionID: sessionID}
		if made != nil {
			res.Worktree = &sync.StartedWorktree{Clone: made.Clone, Path: made.Path, Branch: made.Branch}
		}
		return res, nil
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
