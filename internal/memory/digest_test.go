package memory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/store"
)

func digestEntry(n int, c Category, k ScopeKind, at time.Time, body string) Entry {
	sc := Scope{Kind: k, Value: "v"}
	if k == ScopeGlobal {
		sc.Value = ""
	}
	return Entry{ID: fmt.Sprintf("mem_%032x", n), Version: 1, Category: c, Status: StatusActive, Scope: sc, UpdatedAt: at, Body: body}
}

func ids(items []DigestItem) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.ID[len(i.ID)-1:])
	}
	return out
}

func TestComposeDigestOrder(t *testing.T) {
	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		entries []Entry
		want    []string
	}{
		{"categories", []Entry{
			digestEntry(1, CategoryExperience, ScopeGlobal, now, "e"),
			digestEntry(2, CategoryFact, ScopeGlobal, now, "f"),
			digestEntry(3, CategoryPreference, ScopeGlobal, now, "p"),
			digestEntry(4, CategoryConvention, ScopeGlobal, now, "c"),
		}, []string{"4", "3", "2", "1"}},
		{"scopes", []Entry{
			digestEntry(1, CategoryFact, ScopeGlobal, now, "g"),
			digestEntry(2, CategoryFact, ScopeHost, now, "h"),
			digestEntry(3, CategoryFact, ScopeRepo, now, "r"),
			digestEntry(4, CategoryFact, ScopeCompanion, now, "c"),
		}, []string{"3", "4", "2", "1"}},
		{"newest first", []Entry{
			digestEntry(1, CategoryFact, ScopeGlobal, now, "old"),
			digestEntry(2, CategoryFact, ScopeGlobal, now.Add(time.Hour), "new"),
		}, []string{"2", "1"}},
		{"category before scope", []Entry{
			digestEntry(1, CategoryFact, ScopeRepo, now, "f"),
			digestEntry(2, CategoryConvention, ScopeGlobal, now, "c"),
		}, []string{"2", "1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ids(composeDigest(c.entries).Entries); !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// sized is an entry whose digest line is exactly n bytes.
func sized(id int, n int) Entry {
	e := digestEntry(id, CategoryFact, ScopeGlobal, time.Unix(0, 0), "")
	overhead := len(fmt.Sprintf("- [%s v%d %s] \n", e.ID, e.Version, e.Scope.Kind))
	e.Body = strings.Repeat("x", n-overhead)
	return e
}

func TestComposeDigestBudget(t *testing.T) {
	cases := []struct {
		name          string
		entries       []Entry
		bodies, index []string
	}{
		{"exactly full", []Entry{sized(1, digestBudget)}, []string{"1"}, []string{}},
		{"one byte over", []Entry{sized(1, digestBudget+1)}, []string{}, []string{"1"}},
		{"later short entry still fits", []Entry{sized(1, digestBudget-100), sized(2, 200), sized(3, 100)}, []string{"1", "3"}, []string{"2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := composeDigest(c.entries)
			if got := ids(d.Entries); !slices.Equal(got, c.bodies) {
				t.Errorf("bodies %v, want %v", got, c.bodies)
			}
			if got := ids(d.Indexed); !slices.Equal(got, c.index) {
				t.Errorf("index %v, want %v", got, c.index)
			}
		})
	}
}

func TestComposeDigestIndexLimit(t *testing.T) {
	entries := []Entry{sized(0, digestBudget)}
	for i := 1; i <= 200; i++ {
		entries = append(entries, digestEntry(i, CategoryFact, ScopeGlobal, time.Unix(0, 0), "a title long enough to fill the index"))
	}
	d := composeDigest(entries)
	omitted := len(entries) - len(d.Entries) - len(d.Indexed)
	if omitted == 0 || !strings.Contains(d.Text, fmt.Sprintf("%d more entries", omitted)) {
		t.Fatalf("omitted %d: %q", omitted, d.Text[len(d.Text)-80:])
	}
}

func TestDigestEmpty(t *testing.T) {
	if d := composeDigest(nil); d.Text != "" {
		t.Fatalf("%q", d.Text)
	}
}

func TestDigestOnlyActiveInScope(t *testing.T) {
	f := newWrites(t)
	ctx := context.Background()
	global := Scope{Kind: ScopeGlobal}
	repo := Scope{Kind: ScopeRepo, Value: "r"}
	create := func(body string, sc Scope) Entry {
		res := f.apply(Write{Actor: ActorUser, Action: ActionCreate, Category: CategoryFact, Scope: sc, Body: body})
		if res.Outcome != OutcomeApplied {
			t.Fatalf("%+v", res)
		}
		return res.Entry
	}
	keep := create("kept global", global)
	inRepo := create("kept repo", repo)
	other := create("other repo", Scope{Kind: ScopeRepo, Value: "s"})
	gone := create("withdrawn", global)
	f.apply(Write{Actor: ActorUser, Action: ActionWithdraw, Target: gone.ID, ExpectedVersion: 1, Reason: "no"})
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	cand, err := f.svc.Commit(ctx, Op{ID: "cand", Actor: ActorCompanion, Reason: "r", Entry: Entry{
		ID: id, Category: CategoryFact, Status: StatusCandidate, Scope: global, Body: "candidate",
	}})
	if err != nil {
		t.Fatal(err)
	}

	scopes := []Scope{global, repo}
	d, err := f.svc.Digest(ctx, scopes)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Entries) != 2 || !strings.Contains(d.Text, "kept global") || !strings.Contains(d.Text, "kept repo") {
		t.Fatalf("%+v", d)
	}
	for _, no := range []string{"other repo", "withdrawn", "candidate"} {
		if strings.Contains(d.Text, no) {
			t.Errorf("digest has %q", no)
		}
	}
	if _, err := f.svc.GetInScope(ctx, scopes, keep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.GetInScope(ctx, scopes, inRepo.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{other.ID, gone.ID, cand.ID} {
		if _, err := f.svc.GetInScope(ctx, scopes, id); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: %v", id, err)
		}
	}
	limited, err := f.svc.InScope(ctx, scopes, store.MemoryQuery{Limit: 1})
	if err != nil || len(limited) != 1 {
		t.Fatalf("%v, %v", limited, err)
	}
}
