package projects

import (
	"testing"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// A session handed off from another machine is kept with its worktree here, while its lines still record the
// working directory of the machine it came from.
func TestSessionKeptWithAWatchedPathSyncsAndListsThere(t *testing.T) {
	worktree := "/work/repo-1234abcd"
	file := &File{Items: []Item{{Path: worktree, Imported: []string{"moved"}}}}
	moved := fakeSrc{id: "moved", cwd: "/home/other/repo", kept: worktree}
	elsewhere := fakeSrc{id: "moved", cwd: "/home/other/repo", kept: "/some/other/dir"}
	notImported := fakeSrc{id: "other", cwd: "/home/other/repo", kept: worktree}

	got := file.Sync([]provider.Source{moved, elsewhere, notImported})
	if len(got) != 1 || got[0] != provider.Source(moved) {
		t.Fatalf("synced %v", got)
	}
	pending := file.Sessions(worktree, []provider.Source{moved, elsewhere, notImported})
	if len(pending) != 2 || pending[0].SessionID != "moved" || !pending[0].Imported || pending[1].Imported {
		t.Fatalf("sessions = %+v", pending)
	}
}
