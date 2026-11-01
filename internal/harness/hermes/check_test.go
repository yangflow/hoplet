package hermes

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness/verified"
)

// checker is a fake self-check that counts its runs.
type checker struct {
	err  error
	runs int
}

func (c *checker) check(context.Context, string, []string) error {
	c.runs++
	return c.err
}

func TestVerify(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	_, recordedDir := fakeHermes(t, "0.22.5")
	c := &checker{}
	if _, checked, err := Verify(ctx, dir, []string{"PATH=" + recordedDir}, c.check, true); err != nil || checked || c.runs != 0 {
		t.Fatalf("a recorded version: %v %v, %d runs", checked, err, c.runs)
	}

	_, newDir := fakeHermes(t, "0.23.0")
	env := []string{"PATH=" + newDir}
	c.err = errors.New("no approval came")
	o, checked, err := Verify(ctx, dir, env, c.check, false)
	if err != nil || !checked || o.Attempts != 1 || !strings.Contains(o.Error, "no approval came") || verified.Has(dir, "0.23.0") {
		t.Fatalf("a failing version: %+v %v %v", o, checked, err)
	}
	if _, checked, err := Verify(ctx, dir, env, c.check, false); err != nil || checked || c.runs != 1 {
		t.Fatalf("checked again the same day: %v %v, %d runs", checked, err, c.runs)
	}
	o, checked, err = Verify(ctx, dir, env, c.check, true) // forced past the daily gate
	if err != nil || !checked || o.Attempts != 2 || c.runs != 2 {
		t.Fatalf("a second failure: %+v %v %v, %d runs", o, checked, err, c.runs)
	}
	if last, ok := verified.Last(dir); !ok || last.Attempts != 2 {
		t.Fatalf("the second failure was recorded as %+v %v", last, ok)
	}
	c.err = nil
	o, checked, err = Verify(ctx, dir, env, c.check, true) // forced, as `hoplet harness verify` does
	if err != nil || !checked || o.Error != "" || !verified.Has(dir, "0.23.0") {
		t.Fatalf("a passing version: %+v %v %v", o, checked, err)
	}
	if _, checked, err := Verify(ctx, dir, env, c.check, true); err != nil || checked || c.runs != 3 {
		t.Fatalf("a version that passed was checked again: %v %v, %d runs", checked, err, c.runs)
	}
}

func TestVerifyGivesUpAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	_, newDir := fakeHermes(t, "0.23.0")
	env := []string{"PATH=" + newDir}
	long := time.Now().Add(-10 * verified.RetryAfter) // long past the daily gate
	if err := verified.Save(dir, verified.Outcome{At: long, Version: "0.23.0", Error: "x", Attempts: verified.MaxAttempts}); err != nil {
		t.Fatal(err)
	}
	c := &checker{err: errors.New("still failing")}
	if _, checked, err := Verify(ctx, dir, env, c.check, false); err != nil || checked || c.runs != 0 {
		t.Fatalf("after %d failures: %v %v, %d runs", verified.MaxAttempts, checked, err, c.runs)
	}
	o, checked, err := Verify(ctx, dir, env, c.check, true)
	if err != nil || !checked || c.runs != 1 || o.Attempts != verified.MaxAttempts+1 {
		t.Fatalf("forced after giving up: %+v %v %v, %d runs", o, checked, err, c.runs)
	}
}

func TestVerifyRecordsNothingForASetupFailure(t *testing.T) {
	dir := t.TempDir()
	_, newDir := fakeHermes(t, "0.23.0")
	c := &checker{err: errors.Join(errCheckSetup, errors.New("no space left"))}
	_, checked, err := Verify(context.Background(), dir, []string{"PATH=" + newDir}, c.check, true)
	if checked || !errors.Is(err, errCheckSetup) {
		t.Fatalf("a setup failure: %v %v", checked, err)
	}
	if _, ok := verified.Last(dir); ok || verified.Has(dir, "0.23.0") {
		t.Fatal("a setup failure was recorded")
	}
}

func TestVerifyRecordsNothingWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	_, newDir := fakeHermes(t, "0.23.0")
	ctx, cancel := context.WithCancel(context.Background())
	check := func(context.Context, string, []string) error { cancel(); return context.Canceled }
	if _, checked, err := Verify(ctx, dir, []string{"PATH=" + newDir}, check, true); checked || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v %v", checked, err)
	}
	if _, ok := verified.Last(dir); ok || verified.Has(dir, "0.23.0") {
		t.Fatal("a cancelled check was recorded")
	}
	if _, err := os.Stat(filepath.Join(dir, "update.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an outcome file exists: %v", err)
	}
}

func TestProbeSeesAVersionVerifiedHere(t *testing.T) {
	dir := t.TempDir()
	_, newDir := fakeHermes(t, "0.23.0")
	h := &Harness{Dir: dir}
	h.UseEnv(func() []string { return []string{"PATH=" + newDir} })
	info, err := h.Probe(context.Background())
	if err != nil || info.Supported || !strings.Contains(info.Notice, "hoplet harness verify hermes") {
		t.Fatalf("before the check %+v %v", info, err)
	}
	if err := verified.Add(dir, "0.23.0"); err != nil {
		t.Fatal(err)
	}
	if err := verified.Save(dir, verified.Outcome{At: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Version: "0.23.0"}); err != nil {
		t.Fatal(err)
	}
	info, err = h.Probe(context.Background()) // within the probe's minute: state is read anyway
	if err != nil || !info.Supported || info.Attention || !strings.Contains(info.Notice, "verified on this machine") {
		t.Fatalf("after the check %+v %v", info, err)
	}
}

func TestNotice(t *testing.T) {
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		o         verified.Outcome
		want      string
		attention bool
	}{
		{verified.Outcome{At: day, Version: "1"}, "verified on this machine on 2026-11-01", false},
		{verified.Outcome{At: day, Version: "1", Error: "x"}, "could not check Hermes 1", true},
		{verified.Outcome{At: day, Version: "1", Error: "x", Attempts: 1}, "attempt 1 of 3", true},
		{verified.Outcome{At: day, Version: "1", Error: "x", Attempts: 3}, "hoplet harness verify hermes", true},
		{verified.Outcome{At: day, Version: "2"}, "", false}, // about another version
	} {
		got, attention := notice(tt.o, "1")
		if !strings.Contains(got, tt.want) || (tt.want == "" && got != "") || attention != tt.attention {
			t.Errorf("%+v: %q %v", tt.o, got, attention)
		}
	}
}
