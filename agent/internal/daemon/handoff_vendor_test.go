package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// feedPart sends data to the target's data hook in chunks, the last one marked; it returns the worktree the
// completing chunk installed.
func feedPart(t *testing.T, h *handoffs, id, session, part string, data []byte) *sync.HandoffWorktree {
	t.Helper()
	const chunk = 64 << 10
	var installed *sync.HandoffWorktree
	for seq, off := 0, 0; off < len(data); seq++ {
		end := min(off+chunk, len(data))
		wt, err := h.data(context.Background(), sync.HandoffData{
			SessionID: session, HandoffID: id, Part: part, Seq: seq, Last: end == len(data),
			Encoding: "base64", Data: base64.StdEncoding.EncodeToString(data[off:end]),
		})
		if err != nil {
			t.Fatalf("%s chunk %d: %v", part, seq, err)
		}
		if wt != nil {
			installed = wt
		}
		off = end
	}
	return installed
}

// A handoff.install names the session's vendor, and the target installs the session file in that vendor's
// store, verifies it there, rolls it back from there and hands the session to that vendor's runtime manager.
func TestHandoffInstallsInTheStoreOfTheSessionsVendor(t *testing.T) {
	cases := []struct {
		provider string
		session  string
		first    string
		store    func(*config.Config) string // the directory the vendor's store lives in on the target
		other    func(*config.Config) string // the other vendor's
	}{
		{
			provider: "claude", session: "11111111-1111-4111-8111-111111111111",
			first: `{"type":"user","cwd":"/elsewhere","sessionId":"11111111-1111-4111-8111-111111111111","message":{"role":"user","content":"hi"}}`,
			store: func(c *config.Config) string { return c.ClaudeProjects },
			other: func(c *config.Config) string { return filepath.Join(c.CodexHome, "sessions") },
		},
		{
			provider: "codex", session: "019a0000-1111-7222-8333-444444444444",
			first: `{"timestamp":"2026-03-04T05:06:07.000Z","type":"session_meta","payload":{"id":"019a0000-1111-7222-8333-444444444444","timestamp":"2026-03-04T05:06:07.000Z","cwd":"/elsewhere"}}`,
			store: func(c *config.Config) string { return filepath.Join(c.CodexHome, "sessions") },
			other: func(c *config.Config) string { return c.ClaudeProjects },
		},
	}
	for _, c := range cases {
		t.Run(c.provider, func(t *testing.T) {
			root := t.TempDir()
			// The owner's repository, with a change the snapshot carries.
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
			if err := os.WriteFile(filepath.Join(source, "b.txt"), []byte("untracked\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			snap, err := workspace.CreateSnapshot(context.Background(), source, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			archive, err := os.ReadFile(snap.Path)
			if err != nil {
				t.Fatal(err)
			}
			// The target's own clone of the repository, and its stores.
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
			st := newStore(cfg, loaded)
			b := newBridge(cfg, (&sink{}).send, func() {}, func(string) {}, nil)
			t.Cleanup(b.close)
			h := newHandoffs(cfg, st, b, nil, locator(cfg))
			h.client = sync.NewClient(cfg, "target-device", "test", sync.Hooks{})

			// The session stream: the first line the vendor's store reads, then a body.
			stream := []byte(c.first + "\n" + strings.Repeat(`{"n":1}`+"\n", 3000))
			id := "h-" + c.provider
			err = h.begin(context.Background(), sync.HandoffInstall{
				SessionID: c.session, HandoffID: id, Provider: c.provider, TargetPath: clone,
				SnapshotBytes: int64(len(archive)), SnapshotSHA256: hexSum(archive),
				SessionBytes: int64(len(stream)), SessionSHA256: hexSum(stream),
			})
			if err != nil {
				t.Fatal(err)
			}
			feedPart(t, h, id, c.session, workspace.PartSnapshot, archive)
			if wt := feedPart(t, h, id, c.session, workspace.PartSession, stream); wt == nil {
				t.Fatal("the last chunk did not install")
			}

			rec, ok := h.inst.Find(id)
			if !ok || rec.Provider != c.provider || rec.State != workspace.StateInstalled {
				t.Fatalf("record = %+v, %v", rec, ok)
			}
			// The file is in this vendor's store, byte for byte, and the other vendor's store is untouched.
			if !strings.HasPrefix(rec.SessionFile, c.store(cfg)) {
				t.Fatalf("session file %s is not under %s", rec.SessionFile, c.store(cfg))
			}
			if got, err := os.ReadFile(rec.SessionFile); err != nil || !bytes.Equal(got, stream) {
				t.Fatalf("installed bytes differ: %v", err)
			}
			if entries, _ := os.ReadDir(c.other(cfg)); len(entries) != 0 {
				t.Fatalf("%s holds %d entries", c.other(cfg), len(entries))
			}
			// Verify reads the same copy back.
			v, err := h.verify(context.Background(), c.session, id)
			if err != nil || v.SessionBytes != int64(len(stream)) || v.SessionSHA256 != hexSum(stream) {
				t.Fatalf("verification = %+v, %v", v, err)
			}
			// The vendor's store finds the session and its runtime manager owns it.
			loc, err := providerLocator(cfg, c.provider)(c.session)
			if err != nil || loc.File != rec.SessionFile {
				t.Fatalf("location = %+v, %v", loc, err)
			}
			if m := b.manager(c.provider); m == nil || b.managerOf(c.session) != m {
				t.Fatalf("the session's manager is not the %s manager", c.provider)
			}
			// A rollback removes the file from the same store.
			if err := h.inst.Abort(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(rec.SessionFile); !os.IsNotExist(err) {
				t.Fatalf("the abort left %s: %v", rec.SessionFile, err)
			}
		})
	}
}
