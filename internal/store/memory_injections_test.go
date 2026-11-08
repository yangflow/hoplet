package store

import (
	"context"
	"testing"
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
