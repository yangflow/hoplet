package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

func writeRecord(t *testing.T, home string, r workspace.Record) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "handoff"), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(home, "handoff", r.HandoffID+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

type namedSource struct{ id string }

func (s namedSource) ID() string                          { return s.id }
func (s namedSource) Dir() string                         { return "" }
func (s namedSource) Cwd() string                         { return "" }
func (s namedSource) InProject(string) bool               { return false }
func (s namedSource) Size(context.Context) (int64, error) { return 0, nil }
func (s namedSource) Read(context.Context, int64, int64) ([]byte, error) {
	return nil, nil
}

func newTestHandoffs(t *testing.T) (*handoffs, *config.Config) {
	t.Helper()
	cfg := &config.Config{HopletHome: t.TempDir(), ClaudeProjects: t.TempDir(), DeviceName: "x"}
	loaded, err := projects.Load(cfg.HopletHome)
	if err != nil {
		t.Fatal(err)
	}
	b, _, _, _ := newTestBridge(t)
	h := newHandoffs(cfg, newStore(cfg, loaded), b, nil, locator(cfg))
	h.client = sync.NewClient(cfg, "me-device", "test", sync.Hooks{})
	return h, cfg
}

func TestRecoveredInstallationsAreHeldBackFromSync(t *testing.T) {
	h, cfg := newTestHandoffs(t)
	gone := filepath.Join(t.TempDir(), "gone")
	writeRecord(t, cfg.HopletHome, workspace.Record{HandoffID: "h-fin", SessionID: "finished", State: workspace.StateInstalled, Clone: gone, Worktree: gone, Branch: "hoplet/x"})
	writeRecord(t, cfg.HopletHome, workspace.Record{HandoffID: "h-half", SessionID: "half", State: workspace.StateInstalling, Clone: gone, Worktree: gone, Branch: "hoplet/y"})

	h.recover(context.Background())
	// The half-made one is removed; the finished one waits for the core and stays out of sync.
	if recs := h.inst.Records(); len(recs) != 1 || recs[0].SessionID != "finished" {
		t.Fatalf("records = %+v", recs)
	}
	sources := []provider.Source{namedSource{"finished"}, namedSource{"half"}, namedSource{"other"}}
	got := h.filter(sources)
	if len(got) != 2 || got[0].ID() != "half" || got[1].ID() != "other" {
		t.Fatalf("filter = %v", got)
	}
}

func TestWelcomeSettlesAnInstallationByWhoOwnsTheSession(t *testing.T) {
	for name, tc := range map[string]struct {
		owner string
	}{
		"the core committed the handoff": {owner: "me-device"},
		"the core did not":               {owner: "machine-a"},
		"the core knows no such session": {owner: ""},
	} {
		t.Run(name, func(t *testing.T) {
			h, cfg := newTestHandoffs(t)
			gone := filepath.Join(t.TempDir(), "gone")
			writeRecord(t, cfg.HopletHome, workspace.Record{HandoffID: "h1", SessionID: "s1", State: workspace.StateInstalled, Clone: gone, Worktree: gone, Branch: "hoplet/x"})
			h.recover(context.Background())
			if len(h.filter([]provider.Source{namedSource{"s1"}})) != 0 {
				t.Fatal("not held after recovery")
			}
			var sessions []sync.SessionMeta
			if tc.owner != "" {
				sessions = append(sessions, sync.SessionMeta{SessionID: "s1", OwnerDeviceID: tc.owner})
			}
			h.welcomed(sessions)
			if recs := h.inst.Records(); len(recs) != 0 {
				t.Fatalf("records = %+v", recs)
			}
			if len(h.filter([]provider.Source{namedSource{"s1"}})) != 1 {
				t.Fatal("still held after the core's word")
			}
		})
	}
}

func TestBeginHoldsTheSessionAndRefusesWhatCannotBeStaged(t *testing.T) {
	h, _ := newTestHandoffs(t)
	good := sync.HandoffInstall{
		SessionID: "s1", HandoffID: "h1",
		SnapshotBytes: 10, SnapshotSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		SessionBytes: 10, SessionSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
	}
	bad := good
	bad.SnapshotBytes = workspace.MaxSnapshotCompressed + 1
	if err := h.begin(context.Background(), bad); err == nil {
		t.Fatal("an oversized snapshot was accepted")
	}
	if len(h.filter([]provider.Source{namedSource{"s1"}})) != 1 {
		t.Fatal("a refused transfer held the session")
	}
	invalid := good
	invalid.SessionID = "../x"
	if err := h.begin(context.Background(), invalid); err == nil {
		t.Fatal("an invalid session ID was accepted")
	}
	if err := h.begin(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	if len(h.filter([]provider.Source{namedSource{"s1"}})) != 0 {
		t.Fatal("the session is not held during the transfer")
	}
	// A chunk that breaks the transfer ends it and lets the session go.
	if _, err := h.data(context.Background(), sync.HandoffData{SessionID: "s1", HandoffID: "h1", Part: "session", Seq: 4, Encoding: "base64", Data: "AA=="}); err == nil {
		t.Fatal("out-of-order chunk accepted")
	}
	if len(h.filter([]provider.Source{namedSource{"s1"}})) != 1 {
		t.Fatal("a failed transfer kept the session held")
	}
	if _, err := os.Stat(filepath.Join(h.cfg.HopletHome, "handoff", "transfer-h1")); !os.IsNotExist(err) {
		t.Fatal("staging stayed")
	}
}
