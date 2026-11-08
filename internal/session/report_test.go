package session

import (
	"context"
	"errors"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// TestSendFromNamesTheSource checks a report: the log records the text as
// it is, with its source, and the harness gets it after a line naming the
// source.
func TestSendFromNamesTheSource(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}, {Steps: []fake.Step{{EchoPrompt: true}}}}})
	m := e.manager()
	defer m.Close()
	ctx := context.Background()
	parent := e.create(m)
	src, err := m.Create(ctx, CreateRequest{Mode: proto.ModePersonal, Harness: "fake", Title: "Look up", Parent: parent.ID, Owner: proto.OwnerDelegated})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SendFrom(ctx, parent.ID, src.ID, proto.ReportError, "", "found it", "r1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(parent.ID, "turn end", turnEnded)
	var user, echo []proto.Message
	for _, p := range payloads(t, evs) {
		if msg, ok := p.(proto.Message); ok {
			if msg.Role == proto.RoleUser {
				user = append(user, msg)
			} else {
				echo = append(echo, msg)
			}
		}
	}
	if len(user) != 1 || user[0].Text != "found it" || user[0].From != src.ID || user[0].Report != proto.ReportError {
		t.Fatalf("logged %+v", user)
	}
	want := `[Report from Hoplet session ` + string(src.ID) + ` "Look up"]` + "\n\nfound it"
	if len(echo) != 1 || echo[0].Text != want {
		t.Fatalf("harness got %+v, want %q", echo, want)
	}
	// A retry of the recorded report keeps what was recorded.
	res, err := m.SendFrom(ctx, parent.ID, src.ID, proto.ReportError, "", "found it", "r1")
	if err != nil || res.Turn != 1 {
		t.Fatalf("retry %+v %v", res, err)
	}
	// The user's own messages carry no source line.
	if _, err := m.Send(ctx, parent.ID, "mine", "k2"); err != nil {
		t.Fatal(err)
	}
	evs = e.waitFor(parent.ID, "second turn end", func(evs []proto.Event) bool { return countTurnEnds(evs) == 2 })
	last := payloads(t, evs)
	var got []string
	for _, p := range last {
		if msg, ok := p.(proto.Message); ok && msg.Role != proto.RoleUser {
			got = append(got, msg.Text)
		}
	}
	if len(got) != 2 || got[1] != "mine" {
		t.Fatalf("harness got %q", got)
	}
}

func countTurnEnds(evs []proto.Event) int {
	n := 0
	for _, ev := range evs {
		if ev.Type == proto.TypeTurnEnded {
			n++
		}
	}
	return n
}

// TestDelegatedChildIsNoCompanion checks that a personal session with a
// parent is stored as the parent's child and gets no companion.
func TestDelegatedChildIsNoCompanion(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1"})
	m := e.manager()
	defer m.Close()
	ctx := context.Background()
	parent := e.create(m)
	for name, dir := range map[string]string{"session dir": "", "given dir": e.work} {
		c, err := m.Create(ctx, CreateRequest{Mode: proto.ModePersonal, Harness: "fake", Directory: dir, Parent: parent.ID, Owner: proto.OwnerDelegated})
		if err != nil {
			t.Fatal(err)
		}
		if c.Parent != parent.ID || c.Owner != proto.OwnerDelegated {
			t.Errorf("%s: child %+v", name, c)
		}
		if _, err := e.store.CompanionByMain(ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: companion: %v", name, err)
		}
		want := dir
		if want == "" {
			want = m.dir(c.ID)
		}
		if got, err := m.WorkDir(ctx, c.ID); err != nil || got != want {
			t.Errorf("%s: work dir %q %v, want %q", name, got, err, want)
		}
	}
	if _, err := m.Create(ctx, CreateRequest{Mode: proto.ModePersonal, Harness: "fake", Parent: "missing", Owner: proto.OwnerDelegated}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing parent: %v", err)
	}
}
