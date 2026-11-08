package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/yangflow/hoplet/internal/store"
)

func (f *writes) candidate(w Write) Entry {
	f.t.Helper()
	res := f.apply(w)
	if res.Outcome != OutcomeCandidate {
		f.t.Fatalf("%+v", res)
	}
	return res.Entry
}

func (f *writes) get(id string) Entry {
	f.t.Helper()
	e, err := f.svc.Get(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return e
}

func (f *writes) openCandidates() int {
	f.t.Helper()
	cs, err := f.svc.Candidates(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return len(cs)
}

func TestAcceptCreate(t *testing.T) {
	f := newWrites(t)
	c := f.candidate(create("note", f.observed("p", after)))
	narrow := Scope{ScopeRepo, "narrow"}
	got, err := f.svc.Accept(context.Background(), c.ID, "a1", &narrow)
	if err != nil || got.ID != c.ID || got.Status != StatusActive || got.Scope != narrow || len(got.Evidence) != 1 || got.Evidence[0].Kind != EvidenceObserved {
		t.Fatalf("%+v, %v", got, err)
	}
	if f.openCandidates() != 0 {
		t.Fatal("still open")
	}
	bad := Scope{ScopeGlobal, "v"}
	if _, err := f.svc.Accept(context.Background(), c.ID, "a2", &bad); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad scope: %v", err)
	}
}

func TestAcceptUpdate(t *testing.T) {
	f := newWrites(t)
	e := f.userCreate("one")
	c := f.candidate(update(e, "two", f.observed("p", after)))
	got, err := f.svc.Accept(context.Background(), c.ID, "a1", nil)
	if err != nil || got.ID != e.ID || got.Version != 2 || got.Body != "two" || got.Status != StatusActive || got.Evidence[0].Kind != EvidenceObserved {
		t.Fatalf("%+v, %v", got, err)
	}
	if p := f.get(c.ID); p.Status != StatusSuperseded || p.ReplacedBy != e.ID {
		t.Fatalf("%+v", p)
	}
}

func TestAcceptUpdateConflict(t *testing.T) {
	f := newWrites(t)
	e := f.userCreate("one")
	c := f.candidate(update(e, "two", f.observed("p", after)))
	f.apply(Write{Actor: ActorUser, Action: ActionUpdate, Target: e.ID, ExpectedVersion: 1, Body: "edited"})
	if _, err := f.svc.Accept(context.Background(), c.ID, "a1", nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("%v", err)
	}
	if f.openCandidates() != 1 || f.get(e.ID).Body != "edited" || f.get(c.ID).Status != StatusCandidate {
		t.Fatal("conflict changed something")
	}
}

func TestAcceptWithdraw(t *testing.T) {
	f := newWrites(t)
	e := f.userCreate("one")
	c := f.candidate(extraction(Write{Action: ActionWithdraw, Target: e.ID, ExpectedVersion: 1, Reason: "outdated", Evidence: []Evidence{f.said("p", after)}}, "p", 1, 100))
	narrow := Scope{ScopeRepo, "n"}
	if _, err := f.svc.Accept(context.Background(), c.ID, "bad", &narrow); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("scope: %v", err)
	}
	got, err := f.svc.Accept(context.Background(), c.ID, "a1", nil)
	if err != nil || got.ID != e.ID || got.Status != StatusWithdrawn || got.Body != "one" {
		t.Fatalf("%+v, %v", got, err)
	}
	if p := f.get(c.ID); p.Status != StatusSuperseded || p.ReplacedBy != e.ID {
		t.Fatalf("%+v", p)
	}
}

func TestRejectThenReplay(t *testing.T) {
	f := newWrites(t)
	c := f.candidate(create("a claim", f.observed("p", before)))
	got, err := f.svc.Reject(context.Background(), c.ID, "r1")
	if err != nil || got.Status != StatusWithdrawn {
		t.Fatalf("%+v, %v", got, err)
	}
	if res := f.apply(create("a claim", f.observed("p", after))); res.Code != CodeWithdrawnReplay {
		t.Fatalf("%+v", res)
	}
}

func TestDecidedCandidates(t *testing.T) {
	f := newWrites(t)
	c := f.candidate(create("note", f.observed("p", after)))
	first, err := f.svc.Accept(context.Background(), c.ID, "a1", nil)
	if err != nil {
		t.Fatal(err)
	}
	n := f.committed()
	again, err := f.svc.Accept(context.Background(), c.ID, "a1", nil)
	if err != nil || again.Version != first.Version || f.committed() != n {
		t.Fatalf("%+v, %v", again, err)
	}
	if _, err := f.svc.Accept(context.Background(), c.ID, "a2", nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("new accept: %v", err)
	}
	if _, err := f.svc.Reject(context.Background(), c.ID, "r1"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("reject: %v", err)
	}
	if _, err := f.svc.Accept(context.Background(), "mem_00000000000000000000000000000000", "a3", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := f.svc.Accept(context.Background(), c.ID, "", nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("no op: %v", err)
	}
}

func TestDecisionsResumeAfterCrash(t *testing.T) {
	for _, stop := range []string{"after-begin", "after-temp", "after-rename", "after-finish"} {
		for _, nth := range []int{1, 2} {
			t.Run(stop, func(t *testing.T) {
				f := newWrites(t)
				e := f.userCreate("one")
				c := f.candidate(update(e, "two", f.observed("p", after)))
				seen := 0
				f.svc.hook = func(point string) error {
					if point == stop {
						if seen++; seen == nth {
							return errCrash
						}
					}
					return nil
				}
				if _, err := f.svc.Accept(context.Background(), c.ID, "a1", nil); !errors.Is(err, errCrash) {
					t.Fatalf("%v", err)
				}
				f.reopen()
				got, err := f.svc.Accept(context.Background(), c.ID, "a1", nil)
				if err != nil || got.Version != 2 || got.Body != "two" {
					t.Fatalf("%+v, %v", got, err)
				}
				if p := f.get(c.ID); p.Status != StatusSuperseded || f.openCandidates() != 0 || f.get(e.ID).Version != 2 {
					t.Fatalf("%+v", p)
				}
			})
		}
	}
	t.Run("reject", func(t *testing.T) {
		f := newWrites(t)
		c := f.candidate(create("note", f.observed("p", after)))
		f.svc.hook = func(point string) error {
			if point == "after-finish" {
				return errCrash
			}
			return nil
		}
		if _, err := f.svc.Reject(context.Background(), c.ID, "r1"); !errors.Is(err, errCrash) {
			t.Fatalf("%v", err)
		}
		f.reopen()
		got, err := f.svc.Reject(context.Background(), c.ID, "r1")
		if err != nil || got.Status != StatusWithdrawn || got.Version != 2 || f.openCandidates() != 0 {
			t.Fatalf("%+v, %v", got, err)
		}
	})
}
