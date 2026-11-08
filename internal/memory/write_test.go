package memory

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

var (
	repoScope = Scope{Kind: ScopeRepo, Value: "r"}
	before    = time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	after     = time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond)
)

type writes struct {
	*fixture
	seq int
}

func newWrites(t *testing.T) *writes {
	t.Helper()
	f := &writes{fixture: newFixture(t)}
	for id, mode := range map[proto.SessionID]proto.Mode{"p": proto.ModePersonal, "q": proto.ModePersonal, "c": proto.ModeCode} {
		sess := proto.Session{
			ID: id, Mode: mode, Harness: proto.HarnessRef{Kind: "claude"}, Directory: "/work",
			Owner: proto.OwnerMe, Origin: proto.OriginMe, CreatedAt: before,
		}
		if mode == proto.ModeCode {
			sess.Isolation = proto.InPlace
		}
		_, err := f.st.CreateSession(context.Background(), sess)
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *writes) op() string {
	f.seq++
	return "op" + strconv.Itoa(f.seq)
}

func (f *writes) event(session proto.SessionID, at time.Time, agent string, p proto.Payload) string {
	f.t.Helper()
	res, err := f.st.Append(context.Background(), store.AppendRequest{Session: session, Turn: 1, Time: at, Entries: []store.Entry{{Payload: p, Agent: agent}}})
	if err != nil {
		f.t.Fatal(err)
	}
	return "event:" + string(session) + "/" + strconv.FormatUint(res.LastSeq, 10)
}

func (f *writes) said(session proto.SessionID, at time.Time) Evidence {
	f.t.Helper()
	return Evidence{EvidenceUserStated, f.event(session, at, "", proto.Message{ID: f.op(), Role: proto.RoleUser, Text: "x"})}
}

func (f *writes) observed(session proto.SessionID, at time.Time) Evidence {
	f.t.Helper()
	return Evidence{EvidenceObserved, f.event(session, at, "", proto.Message{ID: f.op(), Role: proto.RoleAssistant, Text: "x"})}
}

func (f *writes) apply(w Write) Result {
	f.t.Helper()
	if w.OpID == "" {
		w.OpID = f.op()
	}
	res, err := f.svc.Apply(context.Background(), w)
	if err != nil {
		f.t.Fatalf("apply: %v", err)
	}
	return res
}

func create(body string, evidence ...Evidence) Write {
	return Write{Actor: ActorCompanion, Action: ActionCreate, Category: CategoryFact, Scope: repoScope, Body: body, Session: "p", Evidence: evidence}
}

func update(e Entry, body string, evidence ...Evidence) Write {
	return Write{Actor: ActorCompanion, Action: ActionUpdate, Target: e.ID, ExpectedVersion: e.Version, Body: body, Session: "p", Evidence: evidence}
}

func (f *writes) userCreate(body string) Entry {
	f.t.Helper()
	res := f.apply(Write{Actor: ActorUser, Action: ActionCreate, Category: CategoryFact, Scope: repoScope, Body: body})
	if res.Outcome != OutcomeApplied {
		f.t.Fatalf("%+v", res)
	}
	return res.Entry
}

func (f *writes) userWithdraw(e Entry) Entry {
	f.t.Helper()
	return f.apply(Write{Actor: ActorUser, Action: ActionWithdraw, Target: e.ID, ExpectedVersion: e.Version}).Entry
}

func extraction(w Write, session proto.SessionID, from, to uint64) Write {
	w.Actor, w.Session, w.Batch = ActorExtraction, "", &Batch{Session: session, From: from, To: to}
	return w
}

func (f *writes) committed() int {
	f.t.Helper()
	ops, err := f.st.AllCommittedMemoryOps(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return len(ops)
}

func TestApplyUser(t *testing.T) {
	f := newWrites(t)
	e := f.userCreate("the build uses make")
	if e.Status != StatusActive || len(e.Evidence) != 1 || e.Evidence[0].Kind != EvidenceUserStated || !strings.HasPrefix(e.Evidence[0].Ref, "op:") {
		t.Fatalf("%+v", e)
	}
	res := f.apply(Write{Actor: ActorUser, Action: ActionUpdate, Target: e.ID, ExpectedVersion: 1, Body: "the build uses go"})
	if res.Outcome != OutcomeApplied || res.Entry.Version != 2 || res.Entry.Scope != repoScope {
		t.Fatalf("%+v", res)
	}
	_, err := f.svc.Apply(context.Background(), Write{OpID: f.op(), Actor: ActorUser, Action: ActionUpdate, Target: e.ID, ExpectedVersion: 1, Body: "z"})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	w := f.apply(Write{Actor: ActorUser, Action: ActionWithdraw, Target: e.ID, ExpectedVersion: 2, Reason: "wrong"})
	if w.Entry.Status != StatusWithdrawn || w.Entry.Body != "the build uses go" {
		t.Fatalf("%+v", w)
	}
}

func TestApplyBadRequests(t *testing.T) {
	cases := []struct {
		name string
		w    Write
	}{
		{"no op", Write{Actor: ActorUser, Action: ActionCreate}},
		{"file actor", Write{OpID: "a", Actor: ActorFile, Action: ActionCreate, Category: CategoryFact, Scope: repoScope, Body: "x"}},
		{"no category", Write{OpID: "b", Actor: ActorUser, Action: ActionCreate, Scope: repoScope, Body: "x"}},
		{"global with value", Write{OpID: "c", Actor: ActorUser, Action: ActionCreate, Category: CategoryFact, Scope: Scope{ScopeGlobal, "v"}, Body: "x"}},
		{"user evidence", Write{OpID: "d", Actor: ActorUser, Action: ActionCreate, Category: CategoryFact, Scope: repoScope, Body: "x", Evidence: []Evidence{{EvidenceObserved, "op:z"}}}},
		{"blank body", Write{OpID: "e", Actor: ActorUser, Action: ActionCreate, Category: CategoryFact, Scope: repoScope, Body: "  "}},
		{"update without target", Write{OpID: "f", Actor: ActorUser, Action: ActionUpdate, ExpectedVersion: 1, Body: "x"}},
		{"withdraw without reason", Write{OpID: "g", Actor: ActorCompanion, Session: "p", Action: ActionWithdraw, Target: "t", ExpectedVersion: 1}},
		{"companion without session", Write{OpID: "h", Actor: ActorCompanion, Action: ActionCreate, Category: CategoryFact, Scope: repoScope, Body: "x"}},
		{"extraction without batch", Write{OpID: "i", Actor: ActorExtraction, Action: ActionCreate, Category: CategoryFact, Scope: repoScope, Body: "x"}},
		{"code session", Write{OpID: "j", Actor: ActorCompanion, Session: "c", Action: ActionCreate, Category: CategoryFact, Scope: repoScope, Body: "x"}},
		{"unknown action", Write{OpID: "k", Actor: ActorUser, Action: "merge", Body: "x"}},
	}
	f := newWrites(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.svc.Apply(context.Background(), c.w); !errors.Is(err, store.ErrInvalid) {
				t.Fatalf("got %v", err)
			}
			if c.w.OpID != "" {
				if _, err := f.st.MemoryOpByID(context.Background(), c.w.OpID); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("audited: %v", err)
				}
			}
		})
	}
}

func TestApplyEvidence(t *testing.T) {
	cases := []struct {
		name string
		make func(f *writes) Write
	}{
		{"none", func(_ *writes) Write { return create("x") }},
		{"bad format", func(_ *writes) Write { return create("x", Evidence{EvidenceObserved, "note:p/1"}) }},
		{"op reference", func(_ *writes) Write { return create("x", Evidence{EvidenceUserStated, "op:abc"}) }},
		{"missing event", func(_ *writes) Write { return create("x", Evidence{EvidenceObserved, "event:p/999"}) }},
		{"other session", func(f *writes) Write { return create("x", f.observed("q", before)) }},
		{"missing artifact", func(_ *writes) Write { return create("x", Evidence{EvidenceObserved, "artifact:nope/1"}) }},
		{"assistant as user", func(f *writes) Write {
			return create("x", Evidence{EvidenceUserStated, f.observed("p", before).Ref})
		}},
		{"child report as user", func(f *writes) Write {
			return create("x", Evidence{EvidenceUserStated, f.event("p", before, "", proto.Message{ID: "r", Role: proto.RoleUser, Text: "x", From: "q"})})
		}},
		{"subagent as user", func(f *writes) Write {
			return create("x", Evidence{EvidenceUserStated, f.event("p", before, "sub", proto.Message{ID: "s", Role: proto.RoleUser, Text: "x"})})
		}},
		{"non-message as user", func(f *writes) Write {
			return create("x", Evidence{EvidenceUserStated, f.event("p", before, "", proto.TurnStarted{})})
		}},
		{"outside batch", func(f *writes) Write {
			ev := f.said("p", before)
			return extraction(create("x", ev), "p", 100, 200)
		}},
		{"other session than batch", func(f *writes) Write { return extraction(create("x", f.said("q", before)), "p", 1, 200) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newWrites(t)
			res := f.apply(c.make(f))
			if res.Outcome != OutcomeRejected || res.Code != CodeBadEvidence {
				t.Fatalf("%+v", res)
			}
			if f.committed() != 0 {
				t.Fatal("rejected write was committed")
			}
		})
	}
}

func TestApplyArtifactEvidence(t *testing.T) {
	f := newWrites(t)
	a, err := f.st.PublishArtifact(context.Background(), proto.Artifact{ID: "a1", Session: "p", Name: "n", Kind: proto.ArtifactKind("html"), Version: 1, Entry: "index.html", By: proto.ArtifactByCLI})
	if err != nil {
		t.Fatal(err)
	}
	ref := "artifact:" + string(a.ID) + "/1"
	if res := f.apply(create("x", Evidence{EvidenceObserved, ref})); res.Outcome != OutcomeCandidate {
		t.Fatalf("own: %+v", res)
	}
	w := create("y", Evidence{EvidenceObserved, ref})
	w.Session = "q"
	if res := f.apply(w); res.Code != CodeBadEvidence {
		t.Fatalf("other: %+v", res)
	}
	if res := f.apply(create("z", Evidence{EvidenceUserStated, ref})); res.Code != CodeBadEvidence {
		t.Fatalf("user_stated: %+v", res)
	}
}

func TestApplyCredential(t *testing.T) {
	blocked := map[string]string{
		"sk":       "sk-abcdefghijklmnopqrstuvwx",
		"ghp":      "ghp_abcdefghijklmnopqrstuvwx",
		"gho":      "gho_abcdefghijklmnopqrstuvwx",
		"pat":      "github_pat_abcdefghijklmnopqrstuvwx",
		"aws":      "AKIAABCDEFGHIJKLMNOP",
		"google":   "AIzaabcdefghijklmnopqrstuvwxyz012345",
		"slack":    "xoxb-1234567890-abc",
		"pem":      "-----BEGIN RSA PRIVATE KEY-----",
		"password": "password: hunter22",
		"apikey":   "api_key=abcdefg",
		"密码":       "登录密码是 abc12345",
		"密钥":       "密钥：abcdef123",
	}
	f := newWrites(t)
	for name, body := range blocked {
		t.Run(name, func(t *testing.T) {
			id := f.op()
			res := f.apply(Write{OpID: id, Actor: ActorUser, Action: ActionCreate, Category: CategoryFact, Scope: repoScope, Body: "note " + body})
			if res.Outcome != OutcomeRejected || res.Code != CodeCredential {
				t.Fatalf("%+v", res)
			}
			op, err := f.st.MemoryOpByID(context.Background(), id)
			if err != nil || op.Content != "" || op.Hash != "" || strings.Contains(op.Reason, body) || op.State != store.MemoryOpRejected {
				t.Fatalf("audit %+v, %v", op, err)
			}
		})
	}
	for _, body := range []string{"登录密码要定期更换", "the password policy is strict", "task-abcdefghijklmnopqrstuvwxyz", "token 用量"} {
		if hasCredential(body) {
			t.Errorf("%q flagged", body)
		}
	}
	e := f.userCreate("target")
	res := f.apply(Write{Actor: ActorCompanion, Session: "p", Action: ActionWithdraw, Target: e.ID, ExpectedVersion: 1, Reason: "password=abcdef12", Evidence: []Evidence{f.said("p", after)}})
	if res.Code != CodeCredential {
		t.Fatalf("reason: %+v", res)
	}
}

func TestApplyGate(t *testing.T) {
	global := Scope{Kind: ScopeGlobal}
	other := Scope{ScopeRepo, "other"}
	cases := []struct {
		name     string
		kind     EvidenceKind
		category Category
		scope    Scope
		base     *Scope // updates an entry with this scope when set
		want     Outcome
	}{
		{"stated", EvidenceUserStated, CategoryFact, global, nil, OutcomeApplied},
		{"stated same scope", EvidenceUserStated, CategoryFact, repoScope, &repoScope, OutcomeApplied},
		{"stated narrowed", EvidenceUserStated, CategoryFact, repoScope, &global, OutcomeApplied},
		{"stated broadened", EvidenceUserStated, CategoryFact, global, &repoScope, OutcomeCandidate},
		{"stated moved", EvidenceUserStated, CategoryFact, other, &repoScope, OutcomeCandidate},
		{"verified experience", EvidenceVerified, CategoryExperience, repoScope, nil, OutcomeApplied},
		{"verified other category", EvidenceVerified, CategoryFact, repoScope, nil, OutcomeCandidate},
		{"verified global", EvidenceVerified, CategoryExperience, global, nil, OutcomeCandidate},
		{"verified broadened", EvidenceVerified, CategoryExperience, other, &repoScope, OutcomeCandidate},
		{"observed", EvidenceObserved, CategoryFact, repoScope, nil, OutcomeCandidate},
		{"inferred", EvidenceInferred, CategoryExperience, repoScope, nil, OutcomeCandidate},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newWrites(t)
			ev := Evidence{c.kind, f.observed("p", after).Ref}
			if c.kind == EvidenceUserStated {
				ev = f.said("p", after)
			}
			w := Write{Actor: ActorCompanion, Session: "p", Action: ActionCreate, Category: c.category, Scope: c.scope, Body: "new body", Evidence: []Evidence{ev}}
			if c.base != nil {
				base := f.apply(Write{Actor: ActorUser, Action: ActionCreate, Category: c.category, Scope: *c.base, Body: "base"}).Entry
				w.Action, w.Category, w.Target, w.ExpectedVersion = ActionUpdate, "", base.ID, base.Version
			}
			res := f.apply(w)
			if res.Outcome != c.want {
				t.Fatalf("%+v", res)
			}
			if c.want == OutcomeCandidate && (res.Entry.Status != StatusCandidate || (c.base != nil && res.Entry.ID == w.Target)) {
				t.Fatalf("%+v", res)
			}
		})
	}
	t.Run("extraction withdraw", func(t *testing.T) {
		f := newWrites(t)
		e := f.userCreate("target")
		ev := f.said("p", after)
		w := extraction(Write{Action: ActionWithdraw, Target: e.ID, ExpectedVersion: 1, Reason: "no longer true", Evidence: []Evidence{ev}}, "p", 1, 100)
		res := f.apply(w)
		if res.Outcome != OutcomeCandidate || res.Entry.Body != "no longer true" {
			t.Fatalf("%+v", res)
		}
		if got, err := f.svc.Get(context.Background(), e.ID); err != nil || got.Status != StatusActive {
			t.Fatalf("%+v, %v", got, err)
		}
	})
	t.Run("companion withdraw", func(t *testing.T) {
		f := newWrites(t)
		e := f.userCreate("target")
		res := f.apply(Write{Actor: ActorCompanion, Session: "p", Action: ActionWithdraw, Target: e.ID, ExpectedVersion: 1, Reason: "user said so", Evidence: []Evidence{f.said("p", after)}})
		if res.Outcome != OutcomeApplied || res.Entry.Status != StatusWithdrawn {
			t.Fatalf("%+v", res)
		}
	})
}

func TestApplyVersionConflict(t *testing.T) {
	f := newWrites(t)
	e := f.userCreate("one")
	f.apply(Write{Actor: ActorUser, Action: ActionUpdate, Target: e.ID, ExpectedVersion: 1, Body: "two"})
	w := update(e, "three", f.said("p", after))
	w.OpID = f.op()
	if _, err := f.svc.Apply(context.Background(), w); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("companion: %v", err)
	}
	res := f.apply(extraction(update(e, "three", f.said("p", after)), "p", 1, 100))
	if res.Outcome != OutcomeCandidate {
		t.Fatalf("extraction: %+v", res)
	}
	if got, err := f.svc.Get(context.Background(), e.ID); err != nil || got.Body != "two" {
		t.Fatalf("%+v, %v", got, err)
	}
	cs, err := f.svc.Candidates(context.Background())
	if err != nil || len(cs) != 1 || cs[0].ExpectedVersion != 2 || cs[0].Target != e.ID {
		t.Fatalf("%+v, %v", cs, err)
	}
	unknown := update(Entry{ID: "mem_00000000000000000000000000000000", Version: 1}, "x", f.said("p", after))
	unknown.OpID = f.op()
	if _, err := f.svc.Apply(context.Background(), unknown); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown target: %v", err)
	}
}

func TestApplyRetry(t *testing.T) {
	cases := []struct {
		name string
		make func(f *writes) Write
		want Outcome
	}{
		{"applied", func(f *writes) Write { return create("a", f.said("p", after)) }, OutcomeApplied},
		{"candidate", func(f *writes) Write { return create("b", f.observed("p", after)) }, OutcomeCandidate},
		{"rejected", func(_ *writes) Write { return create("c") }, OutcomeRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newWrites(t)
			w := c.make(f)
			w.OpID = "same"
			first := f.apply(w)
			n := f.committed()
			second := f.apply(w)
			if first.Outcome != c.want || second.Outcome != c.want || second.Entry.ID != first.Entry.ID || second.Code != first.Code || second.Reason != first.Reason {
				t.Fatalf("%+v then %+v", first, second)
			}
			if f.committed() != n {
				t.Fatal("retry wrote")
			}
			cs, err := f.svc.Candidates(context.Background())
			if err != nil || (c.want == OutcomeCandidate) != (len(cs) == 1) {
				t.Fatalf("%+v, %v", cs, err)
			}
		})
	}
}

func TestApplyReplay(t *testing.T) {
	const old, fresh = "old conclusion", "new conclusion"
	cases := []struct {
		name string
		make func(f *writes) Write
		code string
		want Outcome
	}{
		{"evidence older than the user's change to the target", func(f *writes) Write {
			return update(f.userCreate(fresh), "another", f.said("p", before))
		}, CodeStaleEvidence, OutcomeRejected},
		{"body the user replaced", func(f *writes) Write {
			e := f.userCreate(old)
			f.apply(Write{Actor: ActorUser, Action: ActionUpdate, Target: e.ID, ExpectedVersion: 1, Body: fresh})
			return create(" old   conclusion", f.said("p", before))
		}, CodeStaleEvidence, OutcomeRejected},
		{"replaced body with later observation", func(f *writes) Write {
			e := f.userCreate(old)
			f.apply(Write{Actor: ActorUser, Action: ActionUpdate, Target: e.ID, ExpectedVersion: 1, Body: fresh})
			return create(old, f.observed("p", after))
		}, "", OutcomeCandidate},
		{"withdrawn body", func(f *writes) Write {
			f.userWithdraw(f.userCreate(old))
			return create(old, f.said("p", before))
		}, CodeWithdrawnReplay, OutcomeRejected},
		{"withdrawn body with later observation", func(f *writes) Write {
			f.userWithdraw(f.userCreate(old))
			return create(old, f.observed("p", after))
		}, CodeWithdrawnReplay, OutcomeRejected},
		{"withdrawn target", func(f *writes) Write {
			return update(f.userWithdraw(f.userCreate(old)), old, f.observed("p", after))
		}, CodeWithdrawnReplay, OutcomeRejected},
		{"later statement restores by update", func(f *writes) Write {
			return update(f.userWithdraw(f.userCreate(old)), old, f.said("p", after))
		}, "", OutcomeApplied},
		{"later statement creates anew", func(f *writes) Write {
			f.userWithdraw(f.userCreate(old))
			return create(old, f.said("p", after))
		}, "", OutcomeApplied},
		{"withdraw on old evidence", func(f *writes) Write {
			e := f.userCreate(old)
			return Write{Actor: ActorCompanion, Session: "p", Action: ActionWithdraw, Target: e.ID, ExpectedVersion: e.Version, Reason: "r", Evidence: []Evidence{f.said("p", before)}}
		}, CodeStaleEvidence, OutcomeRejected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newWrites(t)
			res := f.apply(c.make(f))
			if res.Outcome != c.want || res.Code != c.code {
				t.Fatalf("%+v", res)
			}
			if c.want == OutcomeApplied && res.Entry.Status != StatusActive {
				t.Fatalf("%+v", res)
			}
		})
	}
	t.Run("hand edit counts as the user's", func(t *testing.T) {
		f := newWrites(t)
		e := f.userCreate(old)
		file := path(f.root, e.Category, e.ID)
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(strings.Replace(string(data), old, fresh, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		f.scan()
		e, err = f.svc.Get(context.Background(), e.ID)
		if err != nil {
			t.Fatal(err)
		}
		if res := f.apply(update(e, "another", f.said("p", before))); res.Code != CodeStaleEvidence {
			t.Fatalf("target: %+v", res)
		}
		if res := f.apply(create(old, f.said("p", before))); res.Code != CodeStaleEvidence {
			t.Fatalf("body: %+v", res)
		}
	})
}

func TestApplyFailedRename(t *testing.T) {
	f := newWrites(t)
	e := f.userCreate("one")
	target := path(f.root, e.Category, e.ID)
	f.svc.hook = func(point string) error {
		if point != "after-temp" {
			return nil
		}
		if err := os.Remove(target); err != nil {
			return err
		}
		return os.MkdirAll(target+"/blocker", 0o700)
	}
	res, err := f.svc.Apply(context.Background(), Write{OpID: f.op(), Actor: ActorUser, Action: ActionUpdate, Target: e.ID, ExpectedVersion: 1, Body: "two"})
	if err == nil || res.Outcome == OutcomeApplied {
		t.Fatalf("%+v, %v", res, err)
	}
}
