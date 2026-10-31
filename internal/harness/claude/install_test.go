package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ageUpdate moves the recorded follow-up back in time.
func ageUpdate(t *testing.T, home string, by time.Duration) {
	t.Helper()
	u, ok := lastUpdate(BinaryPath(home))
	if !ok {
		t.Fatal("no update.json")
	}
	u.At = u.At.Add(-by)
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(updateFile(BinaryPath(home)), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// checker is a fake self-check that records the binaries it checked.
type checker struct {
	err     error
	checked []string
	content [][]byte // what each checked file held
}

func (c *checker) check(_ context.Context, bin string) error {
	c.checked = append(c.checked, bin)
	b, _ := os.ReadFile(bin)
	c.content = append(c.content, b)
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
	u, ok := Follow(ctx, home, newerDir, c.check)
	if !ok || u.Error != "" || u.From != "2.1.312" || u.Version != "2.1.400" || len(c.checked) != 1 {
		t.Fatalf("follow %+v %v, checked %v", u, ok, c.checked)
	}
	// The check ran on a private copy inside bin/ with the source's content.
	if got := c.checked[0]; filepath.Dir(got) != filepath.Dir(BinaryPath(home)) {
		t.Fatalf("checked %s, outside bin/", got)
	}
	if want, err := os.ReadFile(newer); err != nil || !bytes.Equal(c.content[0], want) {
		t.Fatalf("the checked copy differs from the source: %v", err)
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
	if !strings.Contains(info.Notice, "attempt 1 of 3") {
		t.Fatalf("notice %q lacks the attempt", info.Notice)
	}
}

func TestFollowGivesUpAfterThreeAttempts(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	user, _ := fakeClaude(t, "2.1.312")
	c := &checker{}
	if _, err := Install(ctx, user, home, c.check); err != nil {
		t.Fatal(err)
	}
	_, brokenDir := fakeClaude(t, "2.1.500")
	c.err = errors.New("flaky")
	for i := 1; i <= 3; i++ {
		u, ok := Follow(ctx, home, brokenDir, c.check)
		if !ok || u.Attempts != i || len(c.checked) != i {
			t.Fatalf("attempt %d: %+v %v, checked %d", i, u, ok, len(c.checked))
		}
		ageUpdate(t, home, 24*time.Hour) // a day passes
	}
	if _, ok := Follow(ctx, home, brokenDir, c.check); ok || len(c.checked) != 3 {
		t.Fatalf("a given-up version was checked again: %d", len(c.checked))
	}
	info, err := (&Harness{Binary: BinaryPath(home)}).Probe(ctx)
	if err != nil || !info.Attention || !strings.Contains(info.Notice, "attempt 3 of 3") || !strings.Contains(info.Notice, "will not try this version again") {
		t.Fatalf("notice %+v %v", info, err)
	}
}

func TestFollowRecordsNothingWhenCancelled(t *testing.T) {
	home := t.TempDir()
	user, _ := fakeClaude(t, "2.1.312")
	c := &checker{}
	if _, err := Install(context.Background(), user, home, c.check); err != nil {
		t.Fatal(err)
	}
	_, newerDir := fakeClaude(t, "2.1.400")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.err = context.Canceled
	if u, ok := Follow(ctx, home, newerDir, c.check); ok || u != (Update{}) {
		t.Fatalf("a cancelled follow recorded %+v %v", u, ok)
	}
	if _, ok := lastUpdate(BinaryPath(home)); ok {
		t.Fatal("a cancelled follow wrote update.json")
	}
	c.err = nil
	n := len(c.checked)
	if u, ok := Follow(context.Background(), home, newerDir, c.check); !ok || u.Error != "" || len(c.checked) != n+1 {
		t.Fatalf("the next follow %+v %v, checked %d", u, ok, len(c.checked))
	}
}

func TestInstallPrunesCrashedTempCopies(t *testing.T) {
	home := t.TempDir()
	user, _ := fakeClaude(t, "2.1.312")
	dir := filepath.Dir(BinaryPath(home))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, ".claude-crashed")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), user, home, (&checker{}).check); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover temp copy kept: %v", err)
	}
}

func TestFollowTriesAFailedVersionOncePerDay(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	user, _ := fakeClaude(t, "2.1.312")
	c := &checker{}
	if _, err := Install(ctx, user, home, c.check); err != nil {
		t.Fatal(err)
	}
	_, brokenDir := fakeClaude(t, "2.1.500")
	c.err = errors.New("flaky")
	if _, ok := Follow(ctx, home, brokenDir, c.check); !ok || len(c.checked) != 1 {
		t.Fatalf("first follow, checked %d", len(c.checked))
	}
	// A restarted daemon follows again at once: no second check.
	if _, ok := Follow(ctx, home, brokenDir, c.check); ok || len(c.checked) != 1 {
		t.Fatalf("a failure just recorded was retried: checked %d", len(c.checked))
	}
	ageUpdate(t, home, 19*time.Hour)
	if _, ok := Follow(ctx, home, brokenDir, c.check); ok || len(c.checked) != 1 {
		t.Fatalf("retried after 19h: checked %d", len(c.checked))
	}
	ageUpdate(t, home, 2*time.Hour)
	if u, ok := Follow(ctx, home, brokenDir, c.check); !ok || u.Attempts != 2 || len(c.checked) != 2 {
		t.Fatalf("after 21h: %+v %v, checked %d", u, ok, len(c.checked))
	}
}

func TestInstallWithoutACheckerRefusesAnUnverifiedVersion(t *testing.T) {
	bin, _ := fakeClaude(t, "2.1.400")
	home := t.TempDir()
	_, err := Install(context.Background(), bin, home, nil)
	if err == nil || !strings.Contains(err.Error(), "no self-check was given") {
		t.Fatalf("install without a checker: %v", err)
	}
	if _, err := os.Lstat(BinaryPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an unverified version was linked: %v", err)
	}
	// A verified version needs no checker.
	good, _ := fakeClaude(t, "2.1.312")
	if _, err := Install(context.Background(), good, home, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSelfCheckFailureIsMarked(t *testing.T) {
	bin, _ := fakeClaude(t, "2.1.400")
	c := &checker{err: errors.New("boom")}
	_, err := Install(context.Background(), bin, t.TempDir(), c.check)
	if !errors.Is(err, ErrSelfCheck) || !errors.Is(err, c.err) {
		t.Fatalf("error %v is not ErrSelfCheck wrapping the cause", err)
	}
}

func TestNextUpdateCountsOnlySelfCheckFailures(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	check := (&selfCheckError{version: "2.1.500", err: errors.New("no approval")})
	other := errors.New("claude: disk full")
	prior := Update{Version: "2.1.500", Error: "x", Attempts: 2}
	tests := []struct {
		name         string
		last         Update
		failedBefore bool
		err          error
		wantAttempts int
		wantErr      bool
	}{
		{"success", prior, true, nil, 0, false},
		{"first check failure", Update{}, false, check, 1, true},
		{"repeated check failure", prior, true, check, 3, true},
		{"other failure is not counted", Update{}, false, other, 0, true},
		{"other failure resets the count", prior, true, other, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := nextUpdate(tt.last, tt.failedBefore, "2.1.400", "2.1.500", tt.err, now)
			if u.Attempts != tt.wantAttempts || (u.Error != "") != tt.wantErr || u.From != "2.1.400" || !u.At.Equal(now) {
				t.Fatalf("%+v", u)
			}
		})
	}
}

func TestNoticeForAnUncountedFailure(t *testing.T) {
	u := Update{At: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Version: "2.1.500", Error: "claude: disk full"}
	got, attention := u.notice("2.1.400")
	if !attention || !strings.Contains(got, "could not move to claude 2.1.500 on 2026-11-01: claude: disk full; it will try again") || strings.Contains(got, "attempt") {
		t.Fatalf("notice %q %v", got, attention)
	}
}
