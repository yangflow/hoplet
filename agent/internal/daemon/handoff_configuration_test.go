package daemon

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/configs"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

const confSession = "22222222-2222-4222-8222-222222222222"

// handoffTarget is a target device with a clone of the owner's repository and a snapshot of the owner's change.
type handoffTarget struct {
	cfg      *config.Config
	b        *bridge
	h        *handoffs
	clone    string
	archive  []byte
	stream   []byte
	sessions string
}

func newHandoffTarget(t *testing.T) *handoffTarget {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "a", "repo")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, source, "init", "-q", "-b", "main")
	gitIn(t, source, "config", "user.email", "t@t.test")
	gitIn(t, source, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(source, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, source, "add", ".")
	gitIn(t, source, "commit", "-q", "-m", "seed")
	snap, err := workspace.CreateSnapshot(context.Background(), source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(snap.Path)
	if err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(root, "b", "repo")
	if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "clone", "-q", source, clone)
	cfg := &config.Config{
		HopletHome: t.TempDir(), ClaudeProjects: t.TempDir(), CodexHome: t.TempDir(), DeviceName: "target",
		ClaudeBin: "/nonexistent/claude", CodexBin: "/nonexistent/codex",
		RuntimeIdleMinutes: 30, RuntimeMaxIdleProcesses: 3,
	}
	loaded, err := projects.Load(cfg.HopletHome)
	if err != nil {
		t.Fatal(err)
	}
	b := newBridge(cfg, (&sink{}).send, func() {}, func(string) {}, nil)
	t.Cleanup(b.close)
	h := newHandoffs(cfg, newStore(cfg, loaded), b, nil, locator(cfg))
	h.client = sync.NewClient(cfg, "target-device", "test", sync.Hooks{})
	first := `{"type":"user","cwd":"/elsewhere","sessionId":"` + confSession + `","message":{"role":"user","content":"hi"}}`
	return &handoffTarget{
		cfg: cfg, b: b, h: h, clone: clone, archive: archive,
		stream: []byte(first + "\n" + strings.Repeat(`{"n":1}`+"\n", 100)),
	}
}

func (g *handoffTarget) install(id, configuration string) error {
	return g.h.begin(context.Background(), sync.HandoffInstall{
		SessionID: confSession, HandoffID: id, Provider: "claude", Configuration: configuration, TargetPath: g.clone,
		SnapshotBytes: int64(len(g.archive)), SnapshotSHA256: hexSum(g.archive),
		SessionBytes: int64(len(g.stream)), SessionSHA256: hexSum(g.stream),
	})
}

func (g *handoffTarget) transfer(t *testing.T, id string) {
	t.Helper()
	feedPart(t, g.h, id, confSession, workspace.PartSnapshot, g.archive)
	if feedPart(t, g.h, id, confSession, workspace.PartSession, g.stream) == nil {
		t.Fatal("the last chunk did not install")
	}
}

// addOwnDirectory adds a configuration over Claude Code whose environment names a session directory of its own.
func (g *handoffTarget) addOwnDirectory(t *testing.T, id string) string {
	t.Helper()
	dir := t.TempDir()
	f, err := configs.Load(g.cfg.HopletHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add(configs.Configuration{ID: id, Extends: configs.Claude, Env: map[string]string{"CLAUDE_CONFIG_DIR": dir}}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(g.cfg.HopletHome); err != nil {
		t.Fatal(err)
	}
	if !g.b.reload() {
		t.Fatal("reload did not see the change")
	}
	return dir
}

func TestHandoffInstallRefusesAnUnavailableConfiguration(t *testing.T) {
	g := newHandoffTarget(t)
	addConfiguration(t, g.b, "off", false)
	for name, req := range map[string]sync.HandoffInstall{
		"disabled":   {Provider: "claude", Configuration: "off"},
		"unknown":    {Provider: "claude", Configuration: "nope"},
		"mismatched": {Provider: "codex", Configuration: "claude"},
	} {
		req.SessionID, req.HandoffID, req.TargetPath = confSession, "h-"+name, g.clone
		if err := g.h.begin(context.Background(), req); !errors.Is(err, errUnavailable) {
			t.Errorf("%s: err = %v", name, err)
		}
		if g.h.moving(confSession) {
			t.Errorf("%s: the session is held after a refusal", name)
		}
	}
}

func TestHandoffInstallsInTheConfigurationsOwnStoreAndRecordsItOnCommit(t *testing.T) {
	g := newHandoffTarget(t)
	dir := g.addOwnDirectory(t, "alt")
	if err := g.install("h-alt", "alt"); err != nil {
		t.Fatal(err)
	}
	g.transfer(t, "h-alt")
	rec, ok := g.h.inst.Find("h-alt")
	if !ok || rec.Configuration != "alt" {
		t.Fatalf("record = %+v, %v", rec, ok)
	}
	if want := filepath.Join(dir, "projects"); !strings.HasPrefix(rec.SessionFile, want) {
		t.Fatalf("session file %s is not under %s", rec.SessionFile, want)
	}
	if got, err := os.ReadFile(rec.SessionFile); err != nil || !bytes.Equal(got, g.stream) {
		t.Fatalf("installed bytes differ: %v", err)
	}
	if entries, _ := os.ReadDir(g.cfg.ClaudeProjects); len(entries) != 0 {
		t.Fatalf("the default store holds %d entries", len(entries))
	}
	if _, ok := g.b.owners.Get(confSession); ok {
		t.Fatal("the owner is recorded before the commit")
	}
	g.h.commit(sync.HandoffCommit{SessionID: confSession, HandoffID: "h-alt", OwnerDeviceID: "target-device"})
	if owner, ok := g.b.owners.Get(confSession); !ok || owner != "alt" {
		t.Fatalf("owner = %q %v", owner, ok)
	}
}

func TestHandoffAbortRecordsNoOwnerAndTheBuiltInHasNoEntry(t *testing.T) {
	g := newHandoffTarget(t)
	g.addOwnDirectory(t, "alt")
	if err := g.install("h-1", "alt"); err != nil {
		t.Fatal(err)
	}
	g.transfer(t, "h-1")
	rec, _ := g.h.inst.Find("h-1")
	g.h.abort(sync.HandoffAbort{SessionID: confSession, HandoffID: "h-1"})
	if _, ok := g.b.owners.Get(confSession); ok {
		t.Fatal("an abort recorded an owner")
	}
	if _, err := os.Stat(rec.SessionFile); !os.IsNotExist(err) {
		t.Fatalf("the abort left %s: %v", rec.SessionFile, err)
	}

	// A session recorded under a configuration loses the entry when the built-in one commits.
	if err := g.b.owners.Set(confSession, "alt"); err != nil {
		t.Fatal(err)
	}
	if err := g.install("h-2", ""); err != nil {
		t.Fatal(err)
	}
	g.transfer(t, "h-2")
	rec, _ = g.h.inst.Find("h-2")
	if !strings.HasPrefix(rec.SessionFile, g.cfg.ClaudeProjects) {
		t.Fatalf("session file %s is not under the default store", rec.SessionFile)
	}
	g.h.commit(sync.HandoffCommit{SessionID: confSession, HandoffID: "h-2", OwnerDeviceID: "target-device"})
	if owner, ok := g.b.owners.Get(confSession); ok {
		t.Fatalf("owner = %q after a built-in handoff", owner)
	}
}
