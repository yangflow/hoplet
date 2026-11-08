package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/store"
)

func TestStartCreatesProfile(t *testing.T) {
	dir, script := newHome(t, fake.Script{})
	d := start(t, testConfig(dir, script))
	if info, err := os.Stat(filepath.Join(home.Profile(dir), "entries")); err != nil || !info.IsDir() {
		t.Fatalf("%v, %v", info, err)
	}
	if p, err := d.srv.memory.Problems(context.Background()); err != nil || len(p) != 0 {
		t.Fatalf("%+v, %v", p, err)
	}
}

func TestStartRecoversPendingMemoryOp(t *testing.T) {
	ctx := context.Background()
	dir, script := newHome(t, fake.Script{})
	st, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginMemoryOp(ctx, store.MemoryOp{ID: "o1", EntryID: "e", NewVersion: 1, Actor: "user", Content: "never written", Hash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	d := start(t, testConfig(dir, script))
	op, err := d.store.MemoryOpByID(ctx, "o1")
	if err != nil || op.State != store.MemoryOpFailed {
		t.Fatalf("%+v, %v", op, err)
	}
}
