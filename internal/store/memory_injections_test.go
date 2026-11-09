package store

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestMemoryInjections(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.AddMemoryInjection(ctx, MemoryInjection{Session: "a", Status: MemoryInjectionFailed, Detail: "boom"}); err != nil {
		t.Fatal(err)
	}
	entries := []MemoryInjectionEntry{{ID: "x", Version: 2}, {ID: "y", Version: 1, Indexed: true}}
	if err := st.AddMemoryInjection(ctx, MemoryInjection{Session: "a", Status: MemoryInjectionDelivered, Entries: entries}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMemoryInjection(ctx, MemoryInjection{Session: "b", Status: MemoryInjectionUnsupported}); err != nil {
		t.Fatal(err)
	}
	got, err := st.MemoryInjections(ctx, "a")
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v, %v", got, err)
	}
	if got[0].Status != MemoryInjectionFailed || got[0].Detail != "boom" || len(got[0].Entries) != 0 {
		t.Errorf("%+v", got[0])
	}
	if got[1].Status != MemoryInjectionDelivered || len(got[1].Entries) != 2 || !got[1].Entries[1].Indexed || got[1].CreatedAt.IsZero() {
		t.Errorf("%+v", got[1])
	}
}

func TestMemoryUses(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	in := func(id proto.SessionID, status string, entries ...MemoryInjectionEntry) {
		t.Helper()
		if err := st.AddMemoryInjection(ctx, MemoryInjection{Session: id, Status: status, Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	in("a", MemoryInjectionDelivered, MemoryInjectionEntry{ID: "whole", Version: 1}, MemoryInjectionEntry{ID: "listed", Version: 1, Indexed: true})
	in("b", MemoryInjectionDelivered, MemoryInjectionEntry{ID: "whole", Version: 2})
	in("c", MemoryInjectionFailed, MemoryInjectionEntry{ID: "lost", Version: 1})
	// Read after being injected: still one session.
	if err := st.AddMemoryUse(ctx, "a", "whole", 2, MemoryUseRead); err != nil {
		t.Fatal(err)
	}
	// An indexed entry counts once it is read.
	if err := st.AddMemoryUse(ctx, "a", "other", 1, MemoryUseRead); err != nil {
		t.Fatal(err)
	}
	counts, err := st.MemoryUseCounts(ctx)
	if want := (map[string]int{"whole": 2, "other": 1}); err != nil || !maps.Equal(counts, want) {
		t.Fatalf("%v, %v", counts, err)
	}
	used, err := st.MemoryUsesOf(ctx, "a")
	if err != nil || !slices.Equal(used, []string{"whole", "other"}) {
		t.Fatalf("%q, %v", used, err)
	}
}
