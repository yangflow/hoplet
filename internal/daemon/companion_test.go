package daemon

import (
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/session"
	"github.com/yangflow/hoplet/proto"
)

// keeper is the fake harness keeping the persona its native session
// started with, as Hermes does.
type keeper struct{ *fake.Harness }

func (keeper) KeepsPersona() bool { return true }

func TestCompanionMethods(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	cfg.Harnesses["keeper"] = keeper{&fake.Harness{Binary: fakeExe, Script: script}}
	d := start(t, cfg)
	c := dial(t, h, "cli")
	host := c.Hello().HostID
	if host == "" || host != string(d.srv.identity.ID()) {
		t.Fatalf("host ID %q", host)
	}

	made, err := c.CreateCompanion(timeout(t), proto.CompanionCreateParams{Harness: "fake", Title: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	comp, sess := made.Companion, made.Session
	if comp.ID == "" || comp.Name != "Ada" || comp.Host != host || comp.Main != sess.ID || comp.Dir == "" ||
		sess.Mode != proto.ModePersonal || sess.Title != "Ada" || sess.Directory != "" {
		t.Fatalf("created %+v", made)
	}
	got, err := c.Companion(timeout(t), string(comp.ID[:6]))
	if err != nil || got.ID != comp.ID || got.Host != host || got.Dir != comp.Dir {
		t.Fatalf("get %+v %v", got, err)
	}
	if _, err := c.Companion(timeout(t), "nope"); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("get unknown: %v", err)
	}
	// A personal session made with sessions.create is a companion too.
	if _, err := c.Create(timeout(t), proto.CreateParams{Mode: proto.ModePersonal, Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	cs, err := c.Companions(timeout(t), false)
	if err != nil || len(cs) != 2 || cs[0].ID != comp.ID || cs[1].Host != host {
		t.Fatalf("list %+v %v", cs, err)
	}

	if p, err := c.Persona(timeout(t), string(comp.ID)); err != nil || p.Text != "" || p.ID != comp.ID {
		t.Fatalf("persona %+v %v", p, err)
	}
	r, err := c.SetPersona(timeout(t), string(comp.ID), "Start with: OK, partner.")
	if err != nil || r.Applies != proto.AppliesNextLaunch {
		t.Fatalf("set persona %+v %v", r, err)
	}
	if p, _ := c.Persona(timeout(t), string(comp.ID)); p.Text != "Start with: OK, partner." {
		t.Fatalf("persona read back %+v", p)
	}
	if _, err := c.SetPersona(timeout(t), string(comp.ID), strings.Repeat("a", session.MaxPersona+1)); apiCode(err) != proto.CodeInvalid {
		t.Fatalf("too long: %v", err)
	}

	// A harness keeping its native session's persona: once the main
	// session has run, only a new main session reads a change.
	kept, err := c.CreateCompanion(timeout(t), proto.CompanionCreateParams{Harness: "keeper"})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := c.SetPersona(timeout(t), string(kept.Companion.ID), "a"); err != nil || r.Applies != proto.AppliesNextLaunch {
		t.Fatalf("before the first turn %+v %v", r, err)
	}
	w := watch(t, c, kept.Session.ID, 0)
	if _, err := c.Send(timeout(t), proto.SendParams{Session: kept.Session.ID, Text: "hi", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeTurnEnded) == 1 })
	if r, err := c.SetPersona(timeout(t), string(kept.Companion.ID), "b"); err != nil || r.Applies != proto.AppliesNewMainSession {
		t.Fatalf("after the first turn %+v %v", r, err)
	}

	// Archiving the main session hides the companion from the default list.
	if err := c.Call(timeout(t), proto.MethodSessionsArchive, proto.SessionRef{Session: sess.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if cs, _ := c.Companions(timeout(t), false); len(cs) != 2 {
		t.Fatalf("list after archiving %+v", cs)
	}
	if cs, _ := c.Companions(timeout(t), true); len(cs) != 3 || cs[0].ArchivedAt == nil {
		t.Fatalf("list all %+v", cs)
	}
}

func TestRemoteCompanions(t *testing.T) {
	b := startB(t, echoScript, limits{})
	r := dialRemote(t, b.pairWith(t, newIdentity(t), "phone"))
	made, err := r.CreateCompanion(timeout(t), proto.CompanionCreateParams{Harness: "fake", Title: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetPersona(timeout(t), string(made.Companion.ID), "x"); err != nil {
		t.Fatal(err)
	}
	if p, err := r.Persona(timeout(t), string(made.Companion.ID)); err != nil || p.Text != "x" {
		t.Fatalf("persona %+v %v", p, err)
	}
	if cs, err := r.Companions(timeout(t), false); err != nil || len(cs) != 1 {
		t.Fatalf("list %+v %v", cs, err)
	}
	if got, err := r.Companion(timeout(t), string(made.Companion.ID)); err != nil || got.Host != r.Hello().HostID {
		t.Fatalf("get %+v %v", got, err)
	}
}
