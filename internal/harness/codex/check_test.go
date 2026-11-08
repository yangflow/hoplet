package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness/verified"
)

var errNoApproval = errors.New("no approval came")

func noneRecorded(string) bool { return false }

func TestVerifyVersion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	runs := 0
	var fail error
	check := func(context.Context, string) error { runs++; return fail }

	if _, checked, err := verifyVersion(ctx, dir, "1.0", "/bin/x", func(string) bool { return true }, check, nil); err != nil || checked || runs != 0 {
		t.Fatalf("a recorded version: checked %v, runs %d, %v", checked, runs, err)
	}
	fail = errNoApproval
	o, checked, err := verifyVersion(ctx, dir, "1.0", "/bin/x", noneRecorded, check, nil)
	if err != nil || !checked || o.Error == "" || o.Attempts != 1 || verified.Has(dir, "1.0") {
		t.Fatalf("failing: %+v, checked %v, %v", o, checked, err)
	}
	fail = nil
	if o, _, err = verifyVersion(ctx, dir, "1.0", "/bin/x", noneRecorded, check, nil); err != nil || o.Error != "" || !verified.Has(dir, "1.0") {
		t.Fatalf("passing: %+v, %v", o, err)
	}
	runs = 0
	if _, checked, _ := verifyVersion(ctx, dir, "1.0", "/bin/x", noneRecorded, check, nil); checked || runs != 0 {
		t.Fatalf("a version that passed was checked again")
	}
}

func TestVerifyDoesNotBlameTheVersion(t *testing.T) {
	dir := t.TempDir()
	setup := func(context.Context, string) error { return errors.Join(errCheckSetup, errors.New("no temp dir")) }
	if _, _, err := verifyVersion(context.Background(), dir, "1.0", "/b", noneRecorded, setup, nil); !errors.Is(err, errCheckSetup) {
		t.Fatalf("got %v", err)
	}
	if _, ok := verified.Last(dir); ok {
		t.Fatal("a setup failure was recorded against the version")
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := func(context.Context, string) error { cancel(); return errNoApproval }
	if _, _, err := verifyVersion(ctx, dir, "1.0", "/b", noneRecorded, stopped, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if _, ok := verified.Last(dir); ok {
		t.Fatal("a cancelled check was recorded against the version")
	}
}

func installOld(t *testing.T, home string) {
	t.Helper()
	dir := stateDir(BinaryPath(home))
	if err := os.MkdirAll(filepath.Join(dir, "0.1.0", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0.1.0", "bin", "codex"), []byte(script("0.1.0")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("0.1.0", filepath.Join(dir, currentLink)); err != nil {
		t.Fatal(err)
	}
}

func TestInstallFailingTheCheckStaysOnTheOldCopy(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	installOld(t, home)
	src := release(t, tarball(t, packageFiles(PinnedVersion)), "")
	src.recorded = noneRecorded
	var staged string
	src.Check = func(_ context.Context, bin string) error { staged = bin; return errNoApproval }
	if _, err := Install(ctx, home, src); err == nil || !strings.Contains(err.Error(), "no approval came") {
		t.Fatalf("got %v", err)
	}
	if v, err := version(ctx, BinaryPath(home)); err != nil || v != "0.1.0" {
		t.Fatalf("after a failed check Hoplet runs %q, %v", v, err)
	}
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the failed copy was left behind: %v", err)
	}
	info, err := (&Harness{Binary: BinaryPath(home)}).Probe(ctx)
	if err != nil || !info.Attention || !strings.Contains(info.Notice, PinnedVersion+" failed the self-check") || !strings.Contains(info.Notice, "stays on 0.1.0") {
		t.Fatalf("doctor says %+v, %v", info, err)
	}

	src.Check = func(context.Context, string) error { return nil }
	if _, err := Install(ctx, home, src); err != nil {
		t.Fatal(err)
	}
	if v, err := version(ctx, BinaryPath(home)); err != nil || v != PinnedVersion {
		t.Fatalf("after a passing check Hoplet runs %q, %v", v, err)
	}
	info, err = (&Harness{Binary: BinaryPath(home)}).Probe(ctx)
	if err != nil || !info.Supported || info.Attention || info.Notice != "" {
		t.Fatalf("a version that passed here: %+v, %v", info, err)
	}
}

func TestInstallWithoutACheckRefusesAnUnrecordedVersion(t *testing.T) {
	src := release(t, tarball(t, packageFiles(PinnedVersion)), "")
	src.recorded = noneRecorded
	if _, err := Install(context.Background(), t.TempDir(), src); !errors.Is(err, errCheckSetup) {
		t.Fatalf("got %v", err)
	}
}

func TestProbeSupport(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	installOld(t, home)
	h := &Harness{Binary: BinaryPath(home)}
	if info, err := h.Probe(ctx); err != nil || info.Supported {
		t.Fatalf("an unrecorded version: %+v, %v", info, err)
	}
	if err := verified.Add(stateDir(h.Binary), "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if info, err := h.Probe(ctx); err != nil || !info.Supported {
		t.Fatalf("a version that passed: %+v, %v", info, err)
	}
}

func TestNoticeOnlyForAVersionNotInUse(t *testing.T) {
	at := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	if n, a := notice(verified.Outcome{At: at, Version: "2", Error: "x"}, "2"); n != "" || a {
		t.Fatalf("got %q", n)
	}
	if n, a := notice(verified.Outcome{At: at, Version: "2"}, "1"); n != "" || a {
		t.Fatalf("got %q", n)
	}
	if n, a := notice(verified.Outcome{At: at, Version: "2", Error: "x"}, "1"); !a || !strings.Contains(n, "2026-11-08") {
		t.Fatalf("got %q", n)
	}
}
