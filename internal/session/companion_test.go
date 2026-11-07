package session

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// personaHarness is the fake harness writing its persona into a work file,
// the way the Hermes adapter does; it keeps the specs it launched with.
type personaHarness struct {
	*fake.Harness
	keeps bool

	mu    sync.Mutex
	specs []harness.Spec
}

func (h *personaHarness) Launch(spec harness.Spec) (harness.Launch, error) {
	l, err := h.Harness.Launch(spec)
	if spec.Companion {
		l.WorkFiles = map[string][]byte{"CONTEXT.md": []byte(spec.Persona)}
	}
	h.mu.Lock()
	h.specs = append(h.specs, spec)
	h.mu.Unlock()
	return l, err
}

func (h *personaHarness) KeepsPersona() bool { return h.keeps }

func (h *personaHarness) launched() []harness.Spec {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.specs)
}

func (e *env) personaManager(keeps bool) (*Manager, *personaHarness) {
	e.t.Helper()
	h := &personaHarness{Harness: &fake.Harness{Binary: fakeExe, Script: e.script}, keeps: keeps}
	return e.managerWith(map[string]harness.Harness{"fake": h}), h
}

func (e *env) createPersonal(m *Manager) (proto.Session, proto.Companion) {
	e.t.Helper()
	sess, c, err := m.CreateCompanion(context.Background(), CreateRequest{Harness: "fake", Title: "Ada"})
	if err != nil {
		e.t.Fatal(err)
	}
	return sess, c
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestNewPersonalSessionIsACompanion(t *testing.T) {
	e := newEnv(t, fake.Script{})
	m, _ := e.personaManager(false)
	defer m.Close()
	ctx := context.Background()

	sess, err := m.Create(ctx, CreateRequest{Mode: proto.ModePersonal, Harness: "fake", Title: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	cs, err := m.Companions(ctx, false)
	if err != nil || len(cs) != 1 {
		t.Fatalf("companions %+v %v", cs, err)
	}
	c := cs[0]
	if c.Main != sess.ID || c.Name != "Ada" || c.Harness.Kind != "fake" || c.Dir != filepath.Join(e.root, "c", string(c.ID)) {
		t.Fatalf("companion %+v", c)
	}
	if got := mode(t, c.Dir); got != 0o700 {
		t.Errorf("companion dir mode %v", got)
	}
	if got := mode(t, filepath.Join(c.Dir, "persona.md")); got != 0o600 {
		t.Errorf("persona mode %v", got)
	}
	if p, err := m.Persona(ctx, string(c.ID[:4])); err != nil || p.Text != "" || p.ID != c.ID {
		t.Errorf("persona %+v %v", p, err)
	}
	if dir, err := m.WorkDir(ctx, sess.ID); err != nil || dir != c.Dir {
		t.Errorf("work dir %q %v", dir, err)
	}

	// A code session is none.
	e.create(m)
	if cs, _ := m.Companions(ctx, true); len(cs) != 1 {
		t.Fatalf("companions after a code session %+v", cs)
	}
	if _, _, err := m.CreateCompanion(ctx, CreateRequest{Harness: "fake", Directory: e.work}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("companion with a directory: %v", err)
	}
	// An archived main session hides its companion unless all are asked for.
	if err := m.Archive(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if cs, _ := m.Companions(ctx, false); len(cs) != 0 {
		t.Errorf("archived companion listed %+v", cs)
	}
	if cs, _ := m.Companions(ctx, true); len(cs) != 1 || cs[0].ArchivedAt == nil {
		t.Errorf("all companions %+v", cs)
	}
}

func TestCompanionPersonaReachesTheHarness(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", ResumeTurn: 1, Turns: []fake.Turn{
		{Steps: []fake.Step{{EchoPrompt: true}}}, {Steps: []fake.Step{{EchoPrompt: true}}},
	}})
	m, h := e.personaManager(false)
	defer m.Close()
	ctx := context.Background()
	sess, c := e.createPersonal(m)

	if _, err := m.Send(ctx, sess.ID, "one", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "first turn end", turnEnded)
	specs := h.launched()
	if len(specs) != 1 || specs[0].WorkDir != c.Dir || !specs[0].Companion || specs[0].Persona != "" {
		t.Fatalf("first launch %+v", specs)
	}

	applies, err := m.SetPersona(ctx, string(c.ID), "Start with: OK, partner.")
	if err != nil || applies != proto.AppliesNextLaunch {
		t.Fatalf("set persona %q %v", applies, err)
	}
	if got := mode(t, filepath.Join(c.Dir, "persona.md")); got != 0o600 {
		t.Errorf("persona mode %v", got)
	}
	if err := m.Stop(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	waitShimGone(t, m, sess.ID)
	if _, err := m.Send(ctx, sess.ID, "two", "k2"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "second turn end", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnEnded) == 2 })
	specs = h.launched()
	if len(specs) != 2 || specs[1].Persona != "Start with: OK, partner." {
		t.Fatalf("second launch %+v", specs)
	}
	f := filepath.Join(c.Dir, "CONTEXT.md")
	if b, err := os.ReadFile(f); err != nil || string(b) != "Start with: OK, partner." || mode(t, f) != 0o600 {
		t.Fatalf("work file %q %v", b, err)
	}
	if left, _ := filepath.Glob(filepath.Join(c.Dir, ".*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestPersonaKeptByTheHarness(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}}})
	m, _ := e.personaManager(true)
	defer m.Close()
	ctx := context.Background()
	sess, c := e.createPersonal(m)
	// No native session yet: the first start reads it.
	if applies, err := m.SetPersona(ctx, string(c.ID), "a"); err != nil || applies != proto.AppliesNextLaunch {
		t.Fatalf("before a native session: %q %v", applies, err)
	}
	if _, err := m.Send(ctx, sess.ID, "one", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn end", turnEnded)
	if applies, err := m.SetPersona(ctx, string(c.ID), "b"); err != nil || applies != proto.AppliesNewMainSession {
		t.Fatalf("with a native session: %q %v", applies, err)
	}
}

func TestBadPersona(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}}})
	m, h := e.personaManager(false)
	defer m.Close()
	ctx := context.Background()
	sess, c := e.createPersonal(m)
	for name, text := range map[string]string{
		"too long":  strings.Repeat("x", MaxPersona+1),
		"not UTF-8": "\xff\xfe",
	} {
		if _, err := m.SetPersona(ctx, string(c.ID), text); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: set: %v", name, err)
		}
	}
	if _, err := m.SetPersona(ctx, "beef", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown companion: %v", err)
	}
	// Edited by hand: the launch fails, saying why.
	for name, c2 := range map[string]struct {
		text string
		why  string
	}{
		"too long":  {strings.Repeat("x", MaxPersona+1), "over the limit"},
		"not UTF-8": {"\xff\xfe", "not UTF-8"},
	} {
		if err := os.WriteFile(filepath.Join(c.Dir, "persona.md"), []byte(c2.text), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := m.Send(ctx, sess.ID, "one", "k-"+name)
		if !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), c2.why) {
			t.Errorf("%s: send: %v", name, err)
		}
		if _, err := m.Persona(ctx, string(c.ID)); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: read: %v", name, err)
		}
	}
	if got := h.launched(); len(got) != 0 {
		t.Fatalf("launched with a bad persona: %+v", got)
	}
	// No persona file reads as empty.
	if err := os.Remove(filepath.Join(c.Dir, "persona.md")); err != nil {
		t.Fatal(err)
	}
	if p, err := m.Persona(ctx, string(c.ID)); err != nil || p.Text != "" {
		t.Fatalf("missing persona %+v %v", p, err)
	}
}

// TestMigratedCompanionStaysInItsSessionDir: a main session that worked in
// its session directory before companions existed keeps working there,
// and its companion directory appears at its first start.
func TestMigratedCompanionStaysInItsSessionDir(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}}})
	m, h := e.personaManager(false)
	defer m.Close()
	ctx := context.Background()
	sess, c := e.createPersonal(m)
	db, err := sql.Open("sqlite", filepath.Join(e.root, "db", store.DBName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `UPDATE companions SET in_session_dir = 1 WHERE id = ?`, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(c.Dir); err != nil {
		t.Fatal(err)
	}
	if dir, err := m.WorkDir(ctx, sess.ID); err != nil || dir != m.dir(sess.ID) {
		t.Fatalf("work dir %q %v", dir, err)
	}
	if _, err := m.Send(ctx, sess.ID, "one", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn end", turnEnded)
	if specs := h.launched(); len(specs) != 1 || specs[0].WorkDir != m.dir(sess.ID) || !specs[0].Companion {
		t.Fatalf("launch %+v", specs)
	}
	if got := mode(t, c.Dir); got != 0o700 {
		t.Errorf("companion dir mode %v", got)
	}
	if _, err := os.Stat(filepath.Join(m.dir(sess.ID), "CONTEXT.md")); err != nil {
		t.Errorf("work file: %v", err)
	}
}
