package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func TestBaselineRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "a")
	newSession(t, s, "b")
	if _, ok, err := s.Baseline(ctx, "a"); err != nil || ok {
		t.Fatalf("baseline before saving: %v %v", ok, err)
	}
	if _, _, err := s.Baseline(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("baseline of an unknown session: %v", err)
	}
	first := Baseline{TakenAt: t0, Repos: []BaselineRepo{
		{Path: "web", Tree: "t2", Skipped: []proto.SkippedFile{{Path: "big.bin", Size: 99, Reason: "too large"}}},
		{Path: ".", Tree: "t1"},
		{Path: "api", Error: "boom"},
	}}
	if err := s.SaveBaseline(ctx, "a", first); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Baseline(ctx, "a")
	if err != nil || !ok {
		t.Fatalf("baseline: %v %v", ok, err)
	}
	want := Baseline{TakenAt: t0, Repos: []BaselineRepo{
		{Path: ".", Tree: "t1"},
		{Path: "api", Error: "boom"},
		{Path: "web", Tree: "t2", Skipped: []proto.SkippedFile{{Path: "big.bin", Size: 99, Reason: "too large"}}},
	}}
	if got.Error != "" || !got.TakenAt.Equal(want.TakenAt) || !reflect.DeepEqual(got.Repos, want.Repos) {
		t.Fatalf("baseline %+v, want %+v", got, want)
	}

	// A session has one baseline: saving again fails and changes nothing.
	second := Baseline{TakenAt: t0.Add(time.Minute), Error: "timed out", Repos: []BaselineRepo{{Path: "x", Tree: "t9"}}}
	if err := s.SaveBaseline(ctx, "a", second); err == nil {
		t.Fatal("a second baseline was saved")
	}
	again, _, err := s.Baseline(ctx, "a")
	if err != nil || again.Error != "" || !reflect.DeepEqual(again.Repos, want.Repos) {
		t.Fatalf("baseline after a refused save %+v %v", again, err)
	}
	if _, ok, _ := s.Baseline(ctx, "b"); ok {
		t.Fatal("another session's baseline changed")
	}
	if err := s.SaveBaseline(ctx, "nope", first); !errors.Is(err, ErrNotFound) {
		t.Fatalf("save for an unknown session: %v", err)
	}
}

func TestBaselineWithoutRepos(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "a")
	if err := s.SaveBaseline(ctx, "a", Baseline{TakenAt: t0, Error: "no git"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Baseline(ctx, "a")
	if err != nil || !ok || got.Error != "no git" || len(got.Repos) != 0 {
		t.Fatalf("baseline %+v %v %v", got, ok, err)
	}
}
