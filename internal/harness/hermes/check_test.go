package hermes

import (
	"context"
	"errors"
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
	if _, checked, _ := Verify(ctx, dir, env, c.check, false); checked || c.runs != 1 {
		t.Fatalf("checked again the same day: %d runs", c.runs)
	}
	c.err = nil
	o, checked, err = Verify(ctx, dir, env, c.check, true) // forced, as `hoplet harness verify` does
	if err != nil || !checked || o.Error != "" || !verified.Has(dir, "0.23.0") {
		t.Fatalf("a passing version: %+v %v %v", o, checked, err)
	}
	if _, checked, _ := Verify(ctx, dir, env, c.check, true); checked || c.runs != 2 {
		t.Fatalf("a version that passed was checked again: %d runs", c.runs)
	}
}

func TestVerifyRecordsNothingWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	_, newDir := fakeHermes(t, "0.23.0")
	ctx, cancel := context.WithCancel(context.Background())
	check := func(context.Context, string, []string) error { cancel(); return context.Canceled }
	if _, checked, err := Verify(ctx, dir, []string{"PATH=" + newDir}, check, true); checked || err == nil {
		t.Fatalf("cancelled: %v %v", checked, err)
	}
	if _, ok := verified.Last(dir); ok {
		t.Fatal("a cancelled check was recorded")
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
