package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// Projects is how an installation changes the watched projects (projects.json), which the daemon owns.
type Projects interface {
	// Watch makes sure path is a watched project with the session imported; created says whether path was
	// not watched before.
	Watch(path, sessionID string) (created bool, err error)
	// Unwatch undoes Watch: the session is no longer imported and, when the path was created by it, the path
	// is no longer watched.
	Unwatch(path, sessionID string, removePath bool) error
}

// Installer puts handed-off sessions on this machine (docs/spec.md#handoff): a worktree next to the clone,
// the snapshot's files in it, the session file in the session's vendor's store, and the worktree in projects.json. It
// keeps a record of every installation in the state root before it changes anything, so an installation
// that was interrupted is removed again.
type Installer struct {
	Home     string                               // agent state root; records are in <Home>/handoff
	Stores   func(provider string) provider.Store // the store of the vendor named, nil when it has none
	Watched  func() []string
	Projects Projects

	mu sync.Mutex
}

// Record is the journal of one installation.
type Record struct {
	HandoffID string `json:"handoffId"`
	SessionID string `json:"sessionId"`
	// Provider is the vendor whose store holds the session file; verify, rollback and recovery use the same one.
	Provider string `json:"provider"`
	// State is "installing" until everything is in place, then "installed" until the handoff commits or aborts.
	State          string `json:"state"`
	Clone          string `json:"clone"`
	Worktree       string `json:"worktree"`
	Branch         string `json:"branch"`
	Cwd            string `json:"cwd"`
	SessionFile    string `json:"sessionFile,omitempty"`
	ProjectPath    string `json:"projectPath,omitempty"`
	ProjectCreated bool   `json:"projectCreated,omitempty"`
}

// The states of a Record.
const (
	StateInstalling = "installing"
	StateInstalled  = "installed"
)

func (i *Installer) recordPath(handoffID string) string {
	return filepath.Join(i.Home, "handoff", handoffID+".json")
}

func (i *Installer) save(r Record) error {
	dir := filepath.Join(i.Home, "handoff")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".record-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), i.recordPath(r.HandoffID))
}

func (i *Installer) load(path string) (Record, error) {
	var r Record
	raw, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	if !opaqueID.MatchString(r.HandoffID) || !provider.ValidSessionID(r.SessionID) {
		return r, errors.New("invalid record")
	}
	return r, nil
}

// Records lists the installations that have a record.
func (i *Installer) Records() []Record {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.records()
}

func (i *Installer) records() []Record {
	entries, _ := os.ReadDir(filepath.Join(i.Home, "handoff"))
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), cloneJournalSuffix) {
			continue
		}
		r, err := i.load(filepath.Join(i.Home, "handoff", e.Name()))
		if err != nil {
			log.Printf("[agent] ignoring handoff record %s: %v", e.Name(), err)
			continue
		}
		out = append(out, r)
	}
	return out
}

// worktreeIsOurs reports whether the worktree is registered in the clone on the branch the installation
// made: the only directory a rollback is allowed to remove.
func worktreeIsOurs(ctx context.Context, r Record) bool {
	out, err := git(ctx, r.Clone, "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	want, err := filepath.EvalSymlinks(r.Worktree)
	if err != nil {
		return false
	}
	var current string
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			if real, err := filepath.EvalSymlinks(p); err == nil {
				current = real
			} else {
				current = p
			}
		}
		if b, ok := strings.CutPrefix(line, "branch refs/heads/"); ok && current == want && b == r.Branch {
			return true
		}
	}
	return false
}

// rollback undoes an installation as far as its record says it got, then forgets it. It is safe to repeat.
func (i *Installer) rollback(ctx context.Context, r Record) error {
	var errs []error
	if r.ProjectPath != "" {
		if err := i.Projects.Unwatch(r.ProjectPath, r.SessionID, r.ProjectCreated); err != nil {
			errs = append(errs, err)
		}
	}
	if st := i.store(r.Provider); r.SessionFile != "" && st != nil {
		st.Remove(r.SessionFile)
	}
	if _, err := os.Lstat(r.Worktree); err == nil {
		if worktreeIsOurs(ctx, r) {
			RemoveWorktree(ctx, r.Clone, r.Worktree, r.Branch)
		} else {
			log.Printf("[agent] handoff %s: %s is not the worktree it made; left in place", r.HandoffID, r.Worktree)
		}
	} else {
		// The worktree is gone; its registration may not be. The branch stays: it may be one the user made.
		_, _ = git(ctx, r.Clone, "worktree", "prune")
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if err := os.Remove(i.recordPath(r.HandoffID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// store is the store of the vendor, nil when there is none.
func (i *Installer) store(name string) provider.Store {
	if i.Stores == nil {
		return nil
	}
	return i.Stores(name)
}

// Installed is the result of an installation.
type Installed struct {
	Record
}

// Install installs a completed transfer. On any failure it removes what it installed and the transfer's
// staging, and returns the reason; on success the record stays in state "installed" until Finish or Abort.
func (i *Installer) Install(ctx context.Context, t *Transfer) (*Installed, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	defer t.Close()
	if !t.Complete() {
		return nil, fmt.Errorf("%w: incomplete", ErrTransfer)
	}
	h := t.Header
	if !provider.ValidSessionID(h.SessionID) {
		return nil, errors.New("invalid-session-id")
	}
	store := i.store(h.Provider)
	if store == nil {
		return nil, errors.New("the session's vendor does not support handoff")
	}
	// Nothing on this machine changes until the whole archive has been inspected.
	stage := filepath.Join(t.Dir(), "archive")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return nil, err
	}
	archive, err := OpenArchive(t.Path(PartSnapshot), stage)
	if err != nil {
		return nil, err
	}
	clone, err := ResolveClone(ctx, i.Watched(), h.TargetPath, archive.Meta.OriginURL)
	if err != nil {
		return nil, err
	}
	short := h.SessionID
	if len(short) > 8 {
		short = short[:8]
	}
	rec := Record{
		HandoffID: h.HandoffID,
		SessionID: h.SessionID,
		Provider:  h.Provider,
		State:     StateInstalling,
		Clone:     clone,
		Worktree:  filepath.Join(filepath.Dir(clone), filepath.Base(clone)+"-"+short),
		Branch:    "hoplet/" + short,
	}
	if err := i.save(rec); err != nil {
		return nil, err
	}
	if err := Restore(ctx, archive, RestoreSpec{Clone: rec.Clone, Worktree: rec.Worktree, Branch: rec.Branch}); err != nil {
		// Restore removed what it made; a refusal because the path or branch was taken made nothing.
		_ = os.Remove(i.recordPath(rec.HandoffID))
		return nil, err
	}
	rec.Cwd = rec.Worktree
	if archive.Meta.Subdir != "" {
		rec.Cwd = filepath.Join(rec.Worktree, filepath.FromSlash(archive.Meta.Subdir))
	}
	fail := func(err error) (*Installed, error) {
		if rbErr := i.rollback(context.WithoutCancel(ctx), rec); rbErr != nil {
			return nil, fmt.Errorf("%w (rollback incomplete: %v)", err, rbErr)
		}
		return nil, err
	}
	if info, err := os.Stat(rec.Cwd); err != nil || !info.IsDir() {
		return fail(errors.New("the session's directory is not in the snapshot"))
	}
	// The file is named in the record only once it exists: a rollback removes what the record names, and a
	// session file that was already there is not ours to remove.
	file, err := store.Install(rec.Cwd, h.SessionID, t.Path(PartSession))
	if err != nil {
		return fail(err)
	}
	rec.SessionFile = file
	rec.ProjectPath = rec.Cwd
	if err := i.save(rec); err != nil {
		return fail(err)
	}
	created, err := i.Projects.Watch(rec.Cwd, h.SessionID)
	if err != nil {
		return fail(err)
	}
	rec.ProjectCreated = created
	rec.State = StateInstalled
	if err := i.save(rec); err != nil {
		return fail(err)
	}
	return &Installed{Record: rec}, nil
}

// Verification is what a target proves about its copy before ownership moves.
type Verification struct {
	SessionBytes  int64
	SessionSHA256 string
	TreeHash      string
}

// Verify reads back the installed session file and the worktree.
func (i *Installer) Verify(ctx context.Context, handoffID string) (Verification, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	rec, err := i.load(i.recordPath(handoffID))
	if err != nil || rec.State != StateInstalled {
		return Verification{}, errors.New("not-installed")
	}
	f, err := os.Open(rec.SessionFile)
	if err != nil {
		return Verification{}, err
	}
	defer f.Close()
	sum := sha256.New()
	n, err := io.Copy(sum, f)
	if err != nil {
		return Verification{}, err
	}
	tree, err := TreeHash(ctx, rec.Worktree)
	if err != nil {
		return Verification{}, err
	}
	return Verification{SessionBytes: n, SessionSHA256: hex.EncodeToString(sum.Sum(nil)), TreeHash: tree}, nil
}

// Find returns the record of an installation.
func (i *Installer) Find(handoffID string) (Record, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	r, err := i.load(i.recordPath(handoffID))
	return r, err == nil
}

// Finish forgets an installation whose handoff committed: the files stay, the record goes.
func (i *Installer) Finish(handoffID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := os.Remove(i.recordPath(handoffID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Abort removes an installation whose handoff did not commit. Without a record it does nothing.
func (i *Installer) Abort(ctx context.Context, handoffID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	rec, err := i.load(i.recordPath(handoffID))
	if err != nil {
		return nil
	}
	return i.rollback(ctx, rec)
}

// Recover removes installations that were interrupted before they finished. An installation that did finish
// waits for the core's word on whether the handoff committed; its record is returned.
func (i *Installer) Recover(ctx context.Context) (pending []Record, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	// Staging of transfers cannot be resumed.
	entries, _ := os.ReadDir(filepath.Join(i.Home, "handoff"))
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "transfer-") {
			_ = os.RemoveAll(filepath.Join(i.Home, "handoff", e.Name()))
		}
	}
	var errs []error
	for _, r := range i.records() {
		if r.State == StateInstalled {
			pending = append(pending, r)
			continue
		}
		if err := i.rollback(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	return pending, errors.Join(errs...)
}
