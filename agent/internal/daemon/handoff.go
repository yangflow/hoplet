package daemon

import (
	"context"
	"errors"
	"log"
	gosync "sync"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

// Watch and Unwatch let an installation change projects.json (workspace.Projects).

// Watch makes path a watched project with the session imported.
func (s *store) Watch(path, sessionID string) (bool, error) {
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
	if err := s.file.Import(path, []string{sessionID}); err != nil {
		return false, err
	}
	if err := s.file.Save(s.cfg.HopletHome); err != nil {
		return false, err
	}
	return created, nil
}

// Unwatch undoes Watch.
func (s *store) Unwatch(path, sessionID string, removePath bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return err
	}
	// A path that is not watched has nothing to undo.
	_ = s.file.ClearImports(path, []string{sessionID})
	if removePath {
		_ = s.file.RemovePath(path)
	}
	return s.file.Save(s.cfg.HopletHome)
}

// watchedPaths reads the watched project paths from projects.json; a file that cannot be read has none.
func watchedPaths(home string) []string {
	f, err := projects.Load(home)
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(f.Items))
	for _, it := range f.Items {
		paths = append(paths, it.Path)
	}
	return paths
}

// watchedPaths lists the watched project paths.
func (s *store) watchedPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return nil
	}
	paths := make([]string, 0, len(s.file.Items))
	for _, it := range s.file.Items {
		paths = append(paths, it.Path)
	}
	return paths
}

// handoffs is the device side of a handoff (docs/spec.md#handoff): as the owner it stops the session and
// takes the last snapshot; as the target it receives, installs and verifies, and after the commit it syncs.
// Until then the installed session is held back from sync: the core does not accept it from this device yet.
type handoffs struct {
	cfg    *config.Config
	inst   *workspace.Installer
	bridge *bridge
	snaps  *snapshotter
	client *sync.Client
	locate runtime.Locator

	mu        gosync.Mutex
	transfers map[string]*workspace.Transfer // by handoff ID
	held      map[string]bool                // sessions installed here and not yet committed
}

func newHandoffs(cfg *config.Config, st *store, bridge *bridge, snaps *snapshotter, locate runtime.Locator) *handoffs {
	return &handoffs{
		cfg: cfg,
		inst: &workspace.Installer{
			Home:     cfg.HopletHome,
			Store:    vendorStore(cfg, bridge.mgr.Driver().Name()),
			Watched:  st.watchedPaths,
			Projects: st,
		},
		bridge:    bridge,
		snaps:     snaps,
		locate:    locate,
		transfers: map[string]*workspace.Transfer{},
		held:      map[string]bool{},
	}
}

// recover removes installations that were interrupted and holds back the ones that wait for the core's word.
func (h *handoffs) recover(ctx context.Context) {
	if _, err := h.inst.Recover(ctx); err != nil {
		log.Printf("[agent] handoff recovery: %v", err)
	}
	for _, r := range h.inst.Records() {
		h.hold(r.SessionID)
	}
}

func (h *handoffs) hold(sessionID string) {
	h.mu.Lock()
	h.held[sessionID] = true
	h.mu.Unlock()
}

func (h *handoffs) release(sessionID string) {
	h.mu.Lock()
	delete(h.held, sessionID)
	h.mu.Unlock()
}

// filter leaves out the sessions that are held.
func (h *handoffs) filter(sources []provider.Source) []provider.Source {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.held) == 0 {
		return sources
	}
	out := make([]provider.Source, 0, len(sources))
	for _, src := range sources {
		if !h.held[src.ID()] {
			out = append(out, src)
		}
	}
	return out
}

func (h *handoffs) hooks() sync.HandoffHooks {
	return sync.HandoffHooks{
		Prepare:  h.prepare,
		Begin:    h.begin,
		Data:     h.data,
		Verify:   h.verify,
		Commit:   h.commit,
		Abort:    h.abort,
		Welcomed: h.welcomed,
	}
}

// prepare stops the session's process, then reads the final size of its file and uploads a fresh snapshot.
func (h *handoffs) prepare(ctx context.Context, sessionID, _ string) (string, int64, error) {
	if err := h.bridge.stopSession(sessionID); err != nil {
		return "", 0, err
	}
	loc, err := h.locate(sessionID)
	if err != nil {
		return "", 0, err
	}
	size, err := provider.CompleteLength(loc.File)
	if err != nil {
		return "", 0, err
	}
	snap, err := h.snaps.capture(ctx, sessionID)
	if err != nil {
		return "", 0, err
	}
	// The core waits for the file to arrive up to `size`; sync does not wait for its next round.
	h.client.Kick()
	return snap.ID, size, nil
}

func (h *handoffs) begin(_ context.Context, m sync.HandoffInstall) error {
	if !provider.ValidSessionID(m.SessionID) {
		return errors.New("invalid-session-id")
	}
	tr, err := workspace.BeginTransfer(h.cfg.HopletHome, workspace.TransferHeader{
		HandoffID:      m.HandoffID,
		SessionID:      m.SessionID,
		TargetPath:     m.TargetPath,
		SnapshotBytes:  m.SnapshotBytes,
		SnapshotSHA256: m.SnapshotSHA256,
		SessionBytes:   m.SessionBytes,
		SessionSHA256:  m.SessionSHA256,
	})
	if err != nil {
		return err
	}
	h.mu.Lock()
	for id, old := range h.transfers {
		old.Close()
		delete(h.transfers, id)
	}
	h.transfers[m.HandoffID] = tr
	// Held from here on: the project entry the installation adds must not start a sync of the session.
	h.held[m.SessionID] = true
	h.mu.Unlock()
	return nil
}

func (h *handoffs) data(ctx context.Context, m sync.HandoffData) (*sync.HandoffWorktree, error) {
	h.mu.Lock()
	tr := h.transfers[m.HandoffID]
	h.mu.Unlock()
	if tr == nil {
		return nil, errors.New("no-transfer")
	}
	fail := func(err error) (*sync.HandoffWorktree, error) {
		h.mu.Lock()
		delete(h.transfers, m.HandoffID)
		delete(h.held, m.SessionID)
		h.mu.Unlock()
		tr.Close()
		return nil, err
	}
	if err := tr.Write(m.Part, m.Seq, m.Last, m.Encoding, m.Data); err != nil {
		return fail(err)
	}
	if !tr.Complete() {
		return nil, nil
	}
	h.mu.Lock()
	delete(h.transfers, m.HandoffID)
	h.mu.Unlock()
	installed, err := h.inst.Install(ctx, tr)
	if err != nil {
		h.release(m.SessionID)
		return nil, err
	}
	return &sync.HandoffWorktree{Clone: installed.Record.Clone, Worktree: installed.Record.Worktree, Branch: installed.Record.Branch}, nil
}

func (h *handoffs) verify(ctx context.Context, _, handoffID string) (sync.HandoffVerification, error) {
	v, err := h.inst.Verify(ctx, handoffID)
	if err != nil {
		return sync.HandoffVerification{}, err
	}
	return sync.HandoffVerification{SessionBytes: v.SessionBytes, SessionSHA256: v.SessionSHA256, TreeHash: v.TreeHash}, nil
}

// commit is called after the client has accepted or given up the ownership of the session.
func (h *handoffs) commit(m sync.HandoffCommit) {
	h.mu.Lock()
	delete(h.transfers, m.HandoffID)
	h.mu.Unlock()
	if err := h.inst.Finish(m.HandoffID); err != nil {
		log.Printf("[agent] handoff %s: forgetting the record: %v", shortID(m.HandoffID), err)
	}
	h.release(m.SessionID)
	if m.OwnerDeviceID != h.client.DeviceID() {
		// The session lives elsewhere now: its process here must not run on.
		h.bridge.mgr.Stop(m.SessionID)
	}
}

func (h *handoffs) abort(m sync.HandoffAbort) {
	h.mu.Lock()
	if tr := h.transfers[m.HandoffID]; tr != nil {
		tr.Close()
		delete(h.transfers, m.HandoffID)
	}
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := h.inst.Abort(ctx, m.HandoffID); err != nil {
		log.Printf("[agent] handoff %s: removing the installation: %v", shortID(m.HandoffID), err)
		return
	}
	h.release(m.SessionID)
}

// welcomed settles what an earlier run left open and stops processes of sessions that moved away: an
// installation whose handoff committed while this agent was away is kept, one that did not is removed, and
// a session another device owns now does not run here.
func (h *handoffs) welcomed(sessions []sync.SessionMeta) {
	owners := make(map[string]string, len(sessions))
	for _, s := range sessions {
		owners[s.SessionID] = s.OwnerDeviceID
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, r := range h.inst.Records() {
		if r.State != workspace.StateInstalled {
			continue
		}
		if owners[r.SessionID] == h.client.DeviceID() {
			if err := h.inst.Finish(r.HandoffID); err == nil {
				h.release(r.SessionID)
				h.client.Kick()
			}
			continue
		}
		if err := h.inst.Abort(ctx, r.HandoffID); err == nil {
			h.release(r.SessionID)
		}
	}
	for _, st := range h.bridge.mgr.States() {
		if owner, ok := owners[st.SessionID]; ok && owner != "" && owner != h.client.DeviceID() {
			h.bridge.mgr.Stop(st.SessionID)
		}
	}
}
