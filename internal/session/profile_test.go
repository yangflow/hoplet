package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

type fakeMemory struct {
	digest memory.Digest
	err    error

	mu     sync.Mutex
	scopes [][]memory.Scope
}

func (f *fakeMemory) Digest(_ context.Context, scopes []memory.Scope) (memory.Digest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopes = append(f.scopes, scopes)
	return f.digest, f.err
}

// digestHarness is a harness with an entry for a Code session's digest.
type digestHarness struct{ *personaHarness }

func (digestHarness) TakesCodeDigest() bool { return true }

var aDigest = memory.Digest{
	Text:    "remembered",
	Entries: []memory.DigestItem{{ID: "a", Version: 2}},
	Indexed: []memory.DigestItem{{ID: "b", Version: 1}},
}

func (e *env) memoryManager(digests bool, mem Memory) (*Manager, *personaHarness) {
	e.t.Helper()
	p := &personaHarness{Harness: &fake.Harness{Binary: fakeExe, Script: e.script}}
	var h harness.Harness = p
	if digests {
		h = digestHarness{p}
	}
	m, err := New(Config{
		Store: e.store, Root: filepath.Join(e.root, "s"), Companions: filepath.Join(e.root, "c"), Spawner: ExecSpawner{Exe: hopletExe},
		Harnesses: map[string]harness.Harness{"fake": h},
		Env:       []string{"PATH=" + os.Getenv("PATH")},
		Memory:    mem, Host: "mac",
		RepoIdentity: func(context.Context, string) (string, bool) { return "github.com/acme/app", true },
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return m, p
}

func (e *env) firstTurn(m *Manager, id proto.SessionID) {
	e.t.Helper()
	if _, err := m.Send(context.Background(), id, "one", "k1"); err != nil {
		e.t.Fatal(err)
	}
	e.waitFor(id, "first turn end", turnEnded)
}

func (e *env) injections(id proto.SessionID) []store.MemoryInjection {
	e.t.Helper()
	got, err := e.store.MemoryInjections(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

var oneTurn = fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}}}

func TestCodeSessionDigest(t *testing.T) {
	for name, tc := range map[string]struct {
		digests bool
		profile string
		status  string
		entries int
	}{
		"harness with an entry": {digests: true, profile: "remembered", status: store.MemoryInjectionDelivered, entries: 2},
		"harness without one":   {status: store.MemoryInjectionUnsupported},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, oneTurn)
			m, h := e.memoryManager(tc.digests, &fakeMemory{digest: aDigest})
			defer m.Close()
			sess := e.create(m)
			e.firstTurn(m, sess.ID)
			if got := h.launched()[0].Profile; got != tc.profile {
				t.Errorf("profile %q", got)
			}
			got := e.injections(sess.ID)
			if len(got) != 1 || got[0].Status != tc.status || len(got[0].Entries) != tc.entries {
				t.Fatalf("injections %+v", got)
			}
		})
	}
}

func TestMemorySwitchedOff(t *testing.T) {
	off := func(e *env, change func(*proto.ProfileSettings)) {
		e.t.Helper()
		set, err := e.store.MemorySettings(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		change(&set)
		if err := e.store.SetMemorySettings(context.Background(), set); err != nil {
			e.t.Fatal(err)
		}
	}
	t.Run("code", func(t *testing.T) {
		e := newEnv(t, oneTurn)
		m, h := e.memoryManager(true, &fakeMemory{digest: aDigest})
		defer m.Close()
		off(e, func(s *proto.ProfileSettings) { s.UseCode = false })
		sess := e.create(m)
		e.firstTurn(m, sess.ID)
		if got := h.launched()[0].Profile; got != "" {
			t.Errorf("profile %q", got)
		}
		if got := e.injections(sess.ID); len(got) != 0 {
			t.Errorf("injections %+v", got)
		}
	})
	t.Run("personal", func(t *testing.T) {
		e := newEnv(t, oneTurn)
		m, h := e.memoryManager(false, &fakeMemory{digest: aDigest})
		defer m.Close()
		off(e, func(s *proto.ProfileSettings) { s.UsePersonal = false })
		sess, _ := e.createPersonal(m)
		e.firstTurn(m, sess.ID)
		if got := h.launched()[0].Profile; got != "" {
			t.Errorf("profile %q", got)
		}
		if got := e.injections(sess.ID); len(got) != 0 {
			t.Errorf("injections %+v", got)
		}
	})
}

func TestCompanionDigestScopesAndRecord(t *testing.T) {
	e := newEnv(t, oneTurn)
	mem := &fakeMemory{digest: aDigest}
	m, h := e.memoryManager(false, mem)
	defer m.Close()
	sess, c := e.createPersonal(m)
	e.firstTurn(m, sess.ID)
	if got := h.launched()[0].Profile; got != "remembered" {
		t.Errorf("profile %q", got)
	}
	want := []memory.Scope{{Kind: memory.ScopeGlobal}, {Kind: memory.ScopeHost, Value: "mac"}, {Kind: memory.ScopeCompanion, Value: string(c.ID)}}
	if len(mem.scopes) != 1 || !slices.Equal(mem.scopes[0], want) {
		t.Errorf("scopes %+v", mem.scopes)
	}
	got := e.injections(sess.ID)
	if len(got) != 1 || got[0].Status != store.MemoryInjectionDelivered || len(got[0].Entries) != 2 || !got[0].Entries[1].Indexed {
		t.Fatalf("injections %+v", got)
	}
}

func TestCodeScopesHaveTheRepoAndNoCompanion(t *testing.T) {
	e := newEnv(t, oneTurn)
	m, _ := e.memoryManager(true, &fakeMemory{})
	defer m.Close()
	scopes, err := m.ProfileScopes(context.Background(), e.create(m))
	want := []memory.Scope{{Kind: memory.ScopeGlobal}, {Kind: memory.ScopeHost, Value: "mac"}, {Kind: memory.ScopeRepo, Value: "github.com/acme/app"}}
	if err != nil || !slices.Equal(scopes, want) {
		t.Fatalf("%+v %v", scopes, err)
	}
}

func TestChildSessionScopesAndDigest(t *testing.T) {
	e := newEnv(t, oneTurn)
	m, h := e.memoryManager(false, &fakeMemory{digest: aDigest})
	defer m.Close()
	ctx := context.Background()
	parent, c := e.createPersonal(m)

	// A delegated Personal child reads memory through the tools only, yet
	// it reads what its companion can.
	child, err := m.Create(ctx, CreateRequest{Mode: proto.ModePersonal, Harness: "fake", Title: "Look up", Parent: parent.ID, Owner: proto.OwnerDelegated})
	if err != nil {
		t.Fatal(err)
	}
	e.firstTurn(m, child.ID)
	if got := h.launched()[0].Profile; got != "" {
		t.Errorf("child profile %q", got)
	}
	if got := e.injections(child.ID); len(got) != 0 {
		t.Errorf("child injections %+v", got)
	}
	scopes, err := m.ProfileScopes(ctx, child)
	if err != nil || !slices.Contains(scopes, memory.Scope{Kind: memory.ScopeCompanion, Value: string(c.ID)}) {
		t.Errorf("child scopes %+v %v", scopes, err)
	}
}

func TestHandoffCodeSessionGetsTheDigest(t *testing.T) {
	e := newEnv(t, oneTurn)
	m, h := e.memoryManager(true, &fakeMemory{digest: aDigest})
	defer m.Close()
	ctx := context.Background()
	parent, _ := e.createPersonal(m)
	child, err := m.Create(ctx, CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work, Parent: parent.ID, Owner: proto.OwnerDelegated})
	if err != nil {
		t.Fatal(err)
	}
	e.firstTurn(m, child.ID)
	if got := h.launched()[0].Profile; got != "remembered" {
		t.Errorf("profile %q", got)
	}
}

func TestResumeKeepsTheDigestButRecordsNothing(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", ResumeTurn: 1, Turns: []fake.Turn{
		{Steps: []fake.Step{{EchoPrompt: true}}}, {Steps: []fake.Step{{EchoPrompt: true}}},
	}})
	m, h := e.memoryManager(false, &fakeMemory{digest: aDigest})
	defer m.Close()
	ctx := context.Background()
	sess, _ := e.createPersonal(m)
	e.firstTurn(m, sess.ID)
	if err := m.Stop(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	waitShimGone(t, m, sess.ID)
	if _, err := m.Send(ctx, sess.ID, "two", "k2"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "second turn end", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnEnded) == 2 })
	specs := h.launched()
	if len(specs) != 2 || specs[1].Resume == "" || specs[1].Profile != "remembered" {
		t.Fatalf("specs %+v", specs)
	}
	if got := e.injections(sess.ID); len(got) != 1 {
		t.Errorf("injections %+v", got)
	}
}

func TestDigestFailureDoesNotStopTheSession(t *testing.T) {
	e := newEnv(t, oneTurn)
	m, h := e.memoryManager(true, &fakeMemory{err: errors.New("disk gone")})
	defer m.Close()
	sess := e.create(m)
	e.firstTurn(m, sess.ID)
	if got := h.launched()[0].Profile; got != "" {
		t.Errorf("profile %q", got)
	}
	got := e.injections(sess.ID)
	if len(got) != 1 || got[0].Status != store.MemoryInjectionFailed || got[0].Detail == "" {
		t.Fatalf("injections %+v", got)
	}
}

func TestEmptyDigestRecordsNothing(t *testing.T) {
	e := newEnv(t, oneTurn)
	m, _ := e.memoryManager(true, &fakeMemory{})
	defer m.Close()
	sess := e.create(m)
	e.firstTurn(m, sess.ID)
	if got := e.injections(sess.ID); len(got) != 0 {
		t.Errorf("injections %+v", got)
	}
}
