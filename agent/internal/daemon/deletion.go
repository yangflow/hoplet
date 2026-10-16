package daemon

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/provider"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

// maxVendorCopies bounds the removal of a session's files from the vendor store: a session can be there
// twice, and each Find answers one copy.
const maxVendorCopies = 8

// forgetSession removes the session from the imported list of every project (step 1 of a deletion).
func (s *store) forgetSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return err
	}
	if !s.file.ForgetSession(sessionID) {
		return nil
	}
	return s.file.Save(s.cfg.HopletHome)
}

// sessionCwd is the directory the session's file records, "" when this machine has no file for it.
func sessionCwd(cfg *config.Config, sessionID string) string {
	for _, f := range finders(cfg) {
		if found, ok := f.Find(sessionID); ok {
			return found.Cwd
		}
	}
	return ""
}

// vendorStores lists the native vendors that can find and remove a session's file.
func vendorStores(cfg *config.Config) []interface {
	provider.Finder
	provider.Store
} {
	var out []interface {
		provider.Finder
		provider.Store
	}
	for _, v := range vendors(cfg) {
		if s, ok := v.(interface {
			provider.Finder
			provider.Store
		}); ok {
			out = append(out, s)
		}
	}
	return out
}

// sessionInspect finds what deleting the session would remove here. It finds the file by the vendor's own
// lookup and the worktree by the naming of a handoff; nothing it reports came from the core.
func sessionInspect(cfg *config.Config) func(context.Context, string) (sync.SessionInspection, error) {
	return func(ctx context.Context, sessionID string) (sync.SessionInspection, error) {
		var out sync.SessionInspection
		if !provider.ValidSessionID(sessionID) {
			return out, errors.New("invalid-session-id")
		}
		cwd := ""
		for _, v := range vendorStores(cfg) {
			if found, ok := v.Find(sessionID); ok {
				cwd = found.Cwd
				if found.NativePath != "" {
					out.VendorFile = &sync.DeletionFile{Path: found.NativePath}
				}
				break
			}
		}
		if w, ok := workspace.FindSessionWorktree(ctx, cwd, watchedPaths(cfg.HopletHome), sessionID); ok {
			dirty, unpushed, err := workspace.InspectWorktree(ctx, w)
			if err != nil {
				return out, err
			}
			out.Worktree = &sync.DeletionWorktree{
				Path:     w.Path,
				Branch:   w.Branch,
				Dirty:    sync.DeletionDirty{Modified: dirty.Modified, Untracked: dirty.Untracked},
				Unpushed: unpushed,
			}
		}
		return out, nil
	}
}

// sessionDelete runs the steps of a deletion, each idempotent: a target that is gone counts as done. The
// worktree and the branch go before the vendor file because the file records the directory the worktree is
// found from. A failed step makes the later ones skipped.
func sessionDelete(cfg *config.Config, st *store) func(context.Context, string, bool, bool, bool) []sync.DeletionStep {
	return func(ctx context.Context, sessionID string, vendorFile, worktree, worktreeDirty bool) []sync.DeletionStep {
		var steps []sync.DeletionStep
		failed := false
		run := func(step string, do func() string) {
			if failed {
				steps = append(steps, sync.DeletionStep{Step: step, Status: sync.StatusSkipped})
				return
			}
			if reason := do(); reason != "" {
				failed = true
				log.Printf("[agent] delete %s: %s failed: %s", shortIDPrefix(sessionID), step, reason)
				steps = append(steps, sync.DeletionStep{Step: step, Status: sync.StatusFailed, Reason: reason})
				return
			}
			steps = append(steps, sync.DeletionStep{Step: step, Status: sync.StatusDone})
		}
		if !provider.ValidSessionID(sessionID) {
			return []sync.DeletionStep{{Step: sync.StepStopSyncing, Status: sync.StatusFailed, Reason: "invalid-session-id"}}
		}
		run(sync.StepStopSyncing, func() string {
			if st.forgetSession(sessionID) != nil {
				return "forget-failed"
			}
			return ""
		})
		var found workspace.SessionWorktree
		var haveWorktree bool
		if worktree {
			found, haveWorktree = workspace.FindSessionWorktree(ctx, sessionCwd(cfg, sessionID), watchedPaths(cfg.HopletHome), sessionID)
			run(sync.StepWorktree, func() string {
				if !haveWorktree || !found.Present {
					return ""
				}
				if !found.IsOurs(ctx) {
					return "not-ours"
				}
				// Uncommitted work goes only when the user ticked the worktree knowing it was dirty.
				if !worktreeDirty {
					d, _, err := workspace.InspectWorktree(ctx, found)
					if err != nil {
						return "remove-failed"
					}
					if d.Modified+d.Untracked > 0 {
						return "dirty"
					}
				}
				workspace.RemoveWorktreeKeepBranch(ctx, found.Clone, found.Path)
				if _, err := os.Lstat(found.Path); err == nil {
					return "remove-failed"
				}
				return ""
			})
			run(sync.StepBranch, func() string {
				if !haveWorktree || !found.HasBranch {
					return ""
				}
				workspace.DeleteBranch(ctx, found.Clone, found.Branch)
				if workspace.BranchExists(ctx, found.Clone, found.Branch) {
					return "remove-failed"
				}
				return ""
			})
		}
		if vendorFile {
			run(sync.StepVendorFile, func() string {
				for _, v := range vendorStores(cfg) {
					last := ""
					for range maxVendorCopies {
						f, ok := v.Find(sessionID)
						if !ok || f.NativePath == "" {
							break
						}
						if f.NativePath == last {
							return "remove-failed"
						}
						last = f.NativePath
						v.Remove(f.NativePath)
					}
					if f, ok := v.Find(sessionID); ok && f.NativePath != "" {
						return "remove-failed"
					}
				}
				return ""
			})
		}
		return steps
	}
}
