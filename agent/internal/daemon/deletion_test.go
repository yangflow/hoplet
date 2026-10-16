package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/projects"
	claudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

const deleteSession = "0b7e1c52-3f44-4a63-9d0e-2a6f5e8c1d11"

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// deletionHome is a clone with the worktree of a handoff, the session file in a Claude store for the
// worktree, and a projects.json that imports the session.
func deletionHome(t *testing.T) (cfg *config.Config, st *store, clone, worktree, file string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clone = filepath.Join(root, "repo")
	if err := os.Mkdir(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, clone, "init", "-q", "-b", "main")
	gitRun(t, clone, "config", "user.email", "t@example.com")
	gitRun(t, clone, "config", "user.name", "t")
	gitRun(t, clone, "commit", "-q", "--allow-empty", "-m", "init")
	worktree = filepath.Join(root, "repo-0b7e1c52")
	gitRun(t, clone, "worktree", "add", "-q", "-b", "hoplet/0b7e1c52", worktree)
	cfg = &config.Config{HopletHome: filepath.Join(root, "home"), ClaudeProjects: filepath.Join(root, "claude")}
	file = filepath.Join(cfg.ClaudeProjects, claudeprovider.DirName(worktree), deleteSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + worktree + `","sessionId":"` + deleteSession + `"}` + "\n"
	if err := os.WriteFile(file, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	pf := &projects.File{Items: []projects.Item{
		{Path: clone, Imported: []string{"other"}},
		{Path: worktree, Imported: []string{deleteSession}},
	}}
	if err := pf.Save(cfg.HopletHome); err != nil {
		t.Fatal(err)
	}
	return cfg, newStore(cfg, pf), clone, worktree, file
}

func statuses(steps []sync.DeletionStep) map[string]string {
	out := map[string]string{}
	for _, s := range steps {
		out[s.Step] = s.Status
	}
	return out
}

func TestSessionInspectAndDeleteEverything(t *testing.T) {
	cfg, st, clone, worktree, file := deletionHome(t)
	ctx := context.Background()
	got, err := sessionInspect(cfg)(ctx, deleteSession)
	if err != nil || got.VendorFile == nil || got.VendorFile.Path != file || got.Worktree == nil || got.Worktree.Path != worktree {
		t.Fatalf("inspect: %+v %v", got, err)
	}
	steps := sessionDelete(cfg, st)(ctx, deleteSession, true, true, false)
	for step, status := range map[string]string{
		sync.StepStopSyncing: sync.StatusDone, sync.StepWorktree: sync.StatusDone,
		sync.StepBranch: sync.StatusDone, sync.StepVendorFile: sync.StatusDone,
	} {
		if statuses(steps)[step] != status {
			t.Fatalf("steps %+v", steps)
		}
	}
	for _, gone := range []string{worktree, file} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s is left: %v", gone, err)
		}
	}
	if exec.Command("git", "-C", clone, "show-ref", "--verify", "--quiet", "refs/heads/hoplet/0b7e1c52").Run() == nil {
		t.Fatal("the branch is left")
	}
	reloaded, _ := projects.Load(cfg.HopletHome)
	for _, it := range reloaded.Items {
		for _, id := range it.Imported {
			if id == deleteSession {
				t.Fatal("still imported")
			}
		}
	}
	if len(reloaded.Items[0].Imported) != 1 || reloaded.Items[0].Imported[0] != "other" {
		t.Fatalf("another session was touched: %+v", reloaded.Items[0])
	}
	// Everything is gone, and asking again succeeds: a missing target is done.
	again := sessionDelete(cfg, st)(ctx, deleteSession, true, true, false)
	for _, s := range again {
		if s.Status != sync.StatusDone {
			t.Fatalf("a repeat must succeed: %+v", again)
		}
	}
}

func TestSessionDeleteOnlyWhatIsAsked(t *testing.T) {
	cfg, st, _, worktree, file := deletionHome(t)
	steps := sessionDelete(cfg, st)(context.Background(), deleteSession, false, false, false)
	if len(steps) != 1 || steps[0].Step != sync.StepStopSyncing || steps[0].Status != sync.StatusDone {
		t.Fatalf("steps %+v", steps)
	}
	for _, kept := range []string{worktree, file} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("%s must stay: %v", kept, err)
		}
	}
}

func TestSessionDeleteLeavesADirectoryThatIsNotOurs(t *testing.T) {
	cfg, st, clone, worktree, file := deletionHome(t)
	gitRun(t, clone, "worktree", "remove", "--force", worktree)
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(worktree, "mine.txt")
	if err := os.WriteFile(mine, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := sessionDelete(cfg, st)(context.Background(), deleteSession, true, true, false)
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("a directory that is not ours was touched: %v", err)
	}
	// The directory is no worktree of the clone on the branch: it is not found, so it is never removed, and
	// the steps that do not depend on it go on.
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the vendor file was asked for: %v", err)
	}
	if statuses(steps)[sync.StepVendorFile] != sync.StatusDone {
		t.Fatalf("steps %+v", steps)
	}
}

func TestSessionDeleteKeepsADirtyWorktreeUnlessTicked(t *testing.T) {
	cfg, st, clone, worktree, file := deletionHome(t)
	if err := os.WriteFile(filepath.Join(worktree, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := sessionDelete(cfg, st)(context.Background(), deleteSession, true, true, false)
	got := statuses(steps)
	if got[sync.StepWorktree] != sync.StatusFailed || got[sync.StepBranch] != sync.StatusSkipped || got[sync.StepVendorFile] != sync.StatusSkipped {
		t.Fatalf("steps %+v", steps)
	}
	for _, s := range steps {
		if s.Step == sync.StepWorktree && s.Reason != "dirty" {
			t.Fatalf("reason %q", s.Reason)
		}
	}
	for _, kept := range []string{worktree, file} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("%s must stay: %v", kept, err)
		}
	}
	if exec.Command("git", "-C", clone, "show-ref", "--verify", "--quiet", "refs/heads/hoplet/0b7e1c52").Run() != nil {
		t.Fatal("the branch must stay")
	}
	// Ticked knowing it is dirty: the retry removes it.
	again := sessionDelete(cfg, st)(context.Background(), deleteSession, true, true, true)
	for _, s := range again {
		if s.Status != sync.StatusDone {
			t.Fatalf("steps %+v", again)
		}
	}
	if _, err := os.Lstat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree is left: %v", err)
	}
}

func TestSessionForget(t *testing.T) {
	cfg, st, _, _, _ := deletionHome(t)
	if err := st.forgetSession(deleteSession); err != nil {
		t.Fatal(err)
	}
	if err := st.forgetSession(deleteSession); err != nil {
		t.Fatalf("forgetting twice: %v", err)
	}
	reloaded, _ := projects.Load(cfg.HopletHome)
	for _, it := range reloaded.Items {
		for _, id := range it.Imported {
			if id == deleteSession {
				t.Fatal("still imported")
			}
		}
	}
}
