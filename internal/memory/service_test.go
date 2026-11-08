package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/store"
)

var errCrash = errors.New("simulated crash")

type fixture struct {
	t    *testing.T
	root string
	st   *store.Store
	svc  *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &fixture{t: t, root: filepath.Join(t.TempDir(), "profile"), st: st}
	f.reopen()
	return f
}

func (f *fixture) reopen() {
	f.t.Helper()
	svc, err := Open(context.Background(), Config{Root: f.root, Store: f.st})
	if err != nil {
		f.t.Fatal(err)
	}
	f.svc = svc
}

func newEntry(body string) Entry {
	e := testEntry()
	e.Version, e.UpdatedAt = 0, zeroTime
	e.Body = body
	return e
}

func (f *fixture) commit(opID, body string, expected int) Entry {
	f.t.Helper()
	e, err := f.svc.Commit(context.Background(), Op{ID: opID, Entry: newEntry(body), ExpectedVersion: expected, Actor: ActorUser, Reason: "test"})
	if err != nil {
		f.t.Fatal(err)
	}
	return e
}

func (f *fixture) file() string {
	return path(f.root, CategoryPreference, testID)
}

func (f *fixture) body() string {
	f.t.Helper()
	b, err := os.ReadFile(f.file())
	if err != nil {
		f.t.Fatal(err)
	}
	e, err := Decode(b)
	if err != nil {
		f.t.Fatal(err)
	}
	return e.Body
}

func (f *fixture) opState(id string) string {
	f.t.Helper()
	op, err := f.st.MemoryOpByID(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return op.State
}

func (f *fixture) temps() []string {
	f.t.Helper()
	got, err := filepath.Glob(filepath.Join(filepath.Dir(f.file()), tempPrefix+"*"))
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func TestCommitCreatesAndUpdates(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if e := f.commit("o1", "第一版", 0); e.Version != 1 || e.UpdatedAt.IsZero() {
		t.Fatalf("%+v", e)
	}
	e := f.commit("o2", "第二版", 1)
	if e.Version != 2 || f.body() != "第二版" {
		t.Fatalf("%+v, %s", e, f.body())
	}
	info, err := os.Stat(f.file())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", info, err)
	}
	got, err := f.svc.Get(ctx, testID)
	if err != nil || got.Version != 2 || got.Body != "第二版" {
		t.Fatalf("%+v, %v", got, err)
	}
	row, err := f.st.MemoryIndexEntry(ctx, testID)
	data, rerr := os.ReadFile(f.file())
	if err != nil || rerr != nil || row.Version != 2 || row.Hash != hashOf(data) {
		t.Fatalf("%+v, %v, %v", row, err, rerr)
	}
	found, err := f.svc.Search(ctx, store.MemoryQuery{Text: "二版"})
	if err != nil || len(found) != 1 {
		t.Fatalf("%+v, %v", found, err)
	}
	hist, err := f.svc.History(ctx, testID)
	if err != nil || len(hist) != 2 || hist[0].Version != 2 || !strings.Contains(hist[1].Content, "第一版") || hist[1].Actor != ActorUser {
		t.Fatalf("%+v, %v", hist, err)
	}
}

func TestCommitRejects(t *testing.T) {
	ctx := context.Background()
	moved := newEntry("x")
	moved.Category = CategoryFact
	bad := newEntry("x")
	bad.Scope = Scope{ScopeGlobal, "v"}
	tests := []struct {
		name string
		op   Op
		want error
	}{
		{"stale version", Op{ID: "n", Entry: newEntry("x"), ExpectedVersion: 0, Actor: ActorUser}, store.ErrConflict},
		{"ahead version", Op{ID: "n", Entry: newEntry("x"), ExpectedVersion: 5, Actor: ActorUser}, store.ErrConflict},
		{"category move", Op{ID: "n", Entry: moved, ExpectedVersion: 1, Actor: ActorUser}, store.ErrInvalid},
		{"invalid entry", Op{ID: "n", Entry: bad, ExpectedVersion: 1, Actor: ActorUser}, store.ErrInvalid},
		{"no op ID", Op{Entry: newEntry("x"), ExpectedVersion: 1, Actor: ActorUser}, store.ErrInvalid},
		{"bad actor", Op{ID: "n", Entry: newEntry("x"), ExpectedVersion: 1, Actor: "x"}, store.ErrInvalid},
		{"same ID other content", Op{ID: "o1", Entry: newEntry("other"), ExpectedVersion: 0, Actor: ActorUser}, store.ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.commit("o1", "原文", 0)
			if _, err := f.svc.Commit(ctx, tt.op); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if f.body() != "原文" {
				t.Fatal(f.body())
			}
		})
	}
}

func TestCommitRefusesUnscannedEdit(t *testing.T) {
	f := newFixture(t)
	f.commit("o1", "原文", 0)
	data, err := os.ReadFile(f.file())
	if err != nil {
		t.Fatal(err)
	}
	edited := append(data, []byte("手改\n")...)
	if err := os.WriteFile(f.file(), edited, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Commit(context.Background(), Op{ID: "o2", Entry: newEntry("x"), ExpectedVersion: 1, Actor: ActorUser}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("got %v", err)
	}
	if got, _ := os.ReadFile(f.file()); string(got) != string(edited) {
		t.Fatal("hand edit was overwritten")
	}
}

func TestCommitRetryWritesNothing(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	first := f.commit("o1", "原文", 0)
	again, err := f.svc.Commit(ctx, Op{ID: "o1", Entry: newEntry("原文"), ExpectedVersion: 0, Actor: ActorUser, Reason: "test"})
	if err != nil || again.Version != 1 || !again.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("%+v, %v", again, err)
	}
	if hist, err := f.svc.History(ctx, testID); err != nil || len(hist) != 1 {
		t.Fatalf("%+v, %v", hist, err)
	}
}

func TestCrashRecovery(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		point       string
		wantBody    string
		wantVersion int
		wantState   string
	}{
		{"after-begin", "第一版", 1, store.MemoryOpFailed},
		{"after-temp", "第一版", 1, store.MemoryOpFailed},
		{"after-rename", "第二版", 2, store.MemoryOpCommitted},
		{"after-finish", "第二版", 2, store.MemoryOpCommitted},
	}
	for _, tt := range tests {
		t.Run(tt.point, func(t *testing.T) {
			f := newFixture(t)
			f.commit("o1", "第一版", 0)
			f.svc.hook = func(p string) error {
				if p == tt.point {
					return errCrash
				}
				return nil
			}
			op := Op{ID: "o2", Entry: newEntry("第二版"), ExpectedVersion: 1, Actor: ActorUser, Reason: "test"}
			if _, err := f.svc.Commit(ctx, op); !errors.Is(err, errCrash) {
				t.Fatalf("got %v", err)
			}
			f.reopen()
			if got := f.body(); got != tt.wantBody {
				t.Fatalf("body %q", got)
			}
			if got := f.opState("o2"); got != tt.wantState {
				t.Fatalf("state %q", got)
			}
			if got := f.temps(); len(got) != 0 {
				t.Fatalf("temp files left: %v", got)
			}
			row, err := f.st.MemoryIndexEntry(ctx, testID)
			if err != nil || row.Version != tt.wantVersion {
				t.Fatalf("%+v, %v", row, err)
			}
			got, err := f.svc.Commit(ctx, op)
			if tt.wantState == store.MemoryOpCommitted {
				if err != nil || got.Version != 2 {
					t.Fatalf("%+v, %v", got, err)
				}
			} else if !errors.Is(err, store.ErrConflict) {
				t.Fatalf("retry of a failed op: %v", err)
			}
			hist, err := f.svc.History(ctx, testID)
			if err != nil || len(hist) != tt.wantVersion {
				t.Fatalf("%+v, %v", hist, err)
			}
		})
	}
}

func TestRenameFailureIsNotCommitted(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if err := os.MkdirAll(f.file(), 0o700); err != nil { // a directory blocks the rename
		t.Fatal(err)
	}
	_, err := f.svc.Commit(ctx, Op{ID: "o1", Entry: newEntry("x"), Actor: ActorUser})
	if err == nil {
		t.Fatal("commit succeeded")
	}
	if got := f.opState("o1"); got != store.MemoryOpFailed {
		t.Fatal(got)
	}
	if _, err := f.svc.Get(ctx, testID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if got := f.temps(); len(got) != 0 {
		t.Fatalf("temp files left: %v", got)
	}
}

func TestConcurrentCommitsOneWins(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.commit("o0", "起点", 0)
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.svc.Commit(ctx, Op{ID: fmt.Sprintf("c%d", i), Entry: newEntry(fmt.Sprintf("b%d", i)), ExpectedVersion: 1, Actor: ActorUser})
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, store.ErrConflict):
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d commits won", wins)
	}
}
