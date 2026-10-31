package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checker is a fake self-check that records the binaries it checked.
type checker struct {
	err     error
	checked []string
}

func (c *checker) check(_ context.Context, bin string) error {
	c.checked = append(c.checked, bin)
	return c.err
}

func link(t *testing.T, home string) string {
	t.Helper()
	target, err := os.Readlink(BinaryPath(home))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestInstallVerifiedVersionSkipsTheSelfCheck(t *testing.T) {
	bin, _ := fakeClaude(t, "2.1.312")
	home := t.TempDir()
	c := &checker{}
	got, err := Install(context.Background(), bin, home, c.check)
	if err != nil || got != (Installed{Version: "2.1.312"}) || len(c.checked) != 0 {
		t.Fatalf("install %+v %v, checked %v", got, err, c.checked)
	}
	if target := link(t, home); target != "claude-2.1.312" {
		t.Fatalf("link %q", target)
	}
	fi, err := os.Stat(BinaryPath(home))
	if err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("installed binary %v %v", fi, err)
	}
	if info, err := (&Harness{Binary: BinaryPath(home)}).Probe(context.Background()); err != nil || info.Version != "2.1.312" || !info.Supported {
		t.Fatalf("probe of the installed copy %+v %v", info, err)
	}
	// Installing again replaces the link in place.
	if _, err := Install(context.Background(), bin, home, c.check); err != nil {
		t.Fatal(err)
	}
}

func TestInstallChecksAnUnknownVersionOnce(t *testing.T) {
	bin, _ := fakeClaude(t, "2.1.400")
	home := t.TempDir()
	c := &checker{}
	got, err := Install(context.Background(), bin, home, c.check)
	if err != nil || got != (Installed{Version: "2.1.400", Checked: true}) || len(c.checked) != 1 {
		t.Fatalf("install %+v %v, checked %v", got, err, c.checked)
	}
	if info, err := (&Harness{Binary: BinaryPath(home)}).Probe(context.Background()); err != nil || !info.Supported {
		t.Fatalf("a version that passed here is unsupported: %+v %v", info, err)
	}
	// The pass is remembered.
	got, err = Install(context.Background(), bin, home, c.check)
	if err != nil || got.Checked || len(c.checked) != 1 {
		t.Fatalf("second install %+v %v, checked %v", got, err, c.checked)
	}
}

func TestInstallRefusesAVersionThatFailsTheSelfCheck(t *testing.T) {
	good, _ := fakeClaude(t, "2.1.312")
	home := t.TempDir()
	if _, err := Install(context.Background(), good, home, (&checker{}).check); err != nil {
		t.Fatal(err)
	}
	bad, _ := fakeClaude(t, "2.1.400")
	c := &checker{err: errors.New("no approval came")}
	if _, err := Install(context.Background(), bad, home, c.check); err == nil || !strings.Contains(err.Error(), "failed the self-check") || !strings.Contains(err.Error(), "no approval came") {
		t.Fatalf("failed check installed: %v", err)
	}
	if target := link(t, home); target != "claude-2.1.312" {
		t.Fatalf("a refused install changed the link to %q", target)
	}
	if _, err := os.Stat(BinaryPath(home) + "-2.1.400"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused version was copied: %v", err)
	}
}

func TestFollow(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	user, dir := fakeClaude(t, "2.1.312")
	c := &checker{}

	if _, ok := Follow(ctx, home, dir, c.check); ok {
		t.Fatal("followed without a copy of Hoplet's")
	}
	if _, err := Install(ctx, user, home, c.check); err != nil {
		t.Fatal(err)
	}
	if _, ok := Follow(ctx, home, "/nonexistent:"+dir, c.check); ok {
		t.Fatal("followed the same version")
	}

	// The user's claude updates itself; the follow-up checks and moves.
	newer, newerDir := fakeClaude(t, "2.1.400")
	newer, err := filepath.EvalSymlinks(newer) // what Install checks
	if err != nil {
		t.Fatal(err)
	}
	u, ok := Follow(ctx, home, newerDir, c.check)
	if !ok || u.Error != "" || u.From != "2.1.312" || u.Version != "2.1.400" || len(c.checked) != 1 || c.checked[0] != newer {
		t.Fatalf("follow %+v %v, checked %v", u, ok, c.checked)
	}
	if target := link(t, home); target != "claude-2.1.400" {
		t.Fatalf("link %q", target)
	}
	if _, err := os.Stat(BinaryPath(home) + "-2.1.312"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the old copy is still there: %v", err)
	}
	info, err := (&Harness{Binary: BinaryPath(home)}).Probe(ctx)
	if err != nil || !info.Supported || info.Attention || !strings.Contains(info.Notice, "followed from 2.1.312") {
		t.Fatalf("probe after follow %+v %v", info, err)
	}

	// A version that fails stays out, once.
	_, brokenDir := fakeClaude(t, "2.1.500")
	c.err = errors.New("interrupt ignored")
	u, ok = Follow(ctx, home, brokenDir, c.check)
	if !ok || !strings.Contains(u.Error, "interrupt ignored") || len(c.checked) != 2 {
		t.Fatalf("failed follow %+v %v, checked %v", u, ok, c.checked)
	}
	if target := link(t, home); target != "claude-2.1.400" {
		t.Fatalf("a failed follow moved the link to %q", target)
	}
	info, err = (&Harness{Binary: BinaryPath(home)}).Probe(ctx)
	if err != nil || !info.Attention || !strings.Contains(info.Notice, "2.1.500") || !strings.Contains(info.Notice, "stays on 2.1.400") {
		t.Fatalf("probe after a failed follow %+v %v", info, err)
	}
	if _, ok := Follow(ctx, home, brokenDir, c.check); ok || len(c.checked) != 2 {
		t.Fatalf("a failed version was checked again: %v", c.checked)
	}
}
