package daemon

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// specHarness is a fake harness keeping the specs it launched with.
type specHarness struct {
	*fake.Harness

	mu    sync.Mutex
	specs []harness.Spec
}

func (h *specHarness) Launch(spec harness.Spec) (harness.Launch, error) {
	h.mu.Lock()
	h.specs = append(h.specs, spec)
	h.mu.Unlock()
	return h.Harness.Launch(spec)
}

func (h *specHarness) profile() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.specs[len(h.specs)-1].Profile
}

// TestProfileAcrossHarnesses checks memory end to end: what a companion
// saves on one harness starts a new session on another, which can read the
// entry back, and the store knows what that session was offered.
func TestProfileAcrossHarnesses(t *testing.T) {
	echo := fake.Turn{Steps: []fake.Step{{EchoPrompt: true}}}
	h, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{echo}})
	other := &specHarness{Harness: &fake.Harness{Binary: fakeExe, Script: script}}
	cfg := testConfig(h, script)
	cfg.Harnesses["other"] = other
	d := start(t, cfg)
	c := dial(t, h, "device:a")

	first, err := c.Create(timeout(t), proto.CreateParams{Mode: proto.ModePersonal, Harness: "fake", Permission: proto.ModeAutoEdit})
	if err != nil {
		t.Fatal(err)
	}
	said, err := c.Send(timeout(t), proto.SendParams{Session: first.ID, Text: "I like green tea", Key: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	watch(t, c, first.ID, 0).until(turnEnded(1))

	args := fmt.Sprintf(`{"action":"create","category":"preference","scope":"global","body":"Likes green tea","evidence":[{"kind":"user_stated","ref":"event:%s/%d"}],"reason":"the user said so"}`, first.ID, said.Seq)
	r := callTool(t, toolsOf(t, d, h, first.ID), "profile_write", args)
	var saved struct {
		Outcome, ID string
		Version     int
	}
	if err := json.Unmarshal([]byte(r.Text), &saved); r.IsError || err != nil || saved.Outcome != "applied" {
		t.Fatalf("profile_write %+v %v", r, err)
	}

	// The entry can move to what its source session applies to, also after
	// the user's edit has replaced its evidence.
	edited, err := c.ProfileUpdate(timeout(t), proto.ProfileUpdateParams{ID: saved.ID, ExpectedVersion: saved.Version, Body: "Likes green tea", OpID: "edit"})
	if err != nil || edited.Outcome != "applied" {
		t.Fatalf("update %+v %v", edited, err)
	}
	saved.Version = edited.Entry.Version
	detail, err := c.ProfileGet(timeout(t), saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(detail.Scopes, func(s proto.ProfileScope) bool { return s.Kind == "companion" && s.Value != "" }) {
		t.Fatalf("scopes %+v", detail.Scopes)
	}

	second, err := c.Create(timeout(t), proto.CreateParams{Mode: proto.ModePersonal, Harness: "other", Permission: proto.ModeAutoEdit})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(timeout(t), proto.SendParams{Session: second.ID, Text: "hello", Key: "k2"}); err != nil {
		t.Fatal(err)
	}
	watch(t, c, second.ID, 0).until(turnEnded(1))
	if got := other.profile(); !strings.Contains(got, "Likes green tea") {
		t.Fatalf("second launch's digest %q", got)
	}
	in, err := d.store.MemoryInjections(timeout(t), second.ID)
	if err != nil || len(in) != 1 || in[0].Status != store.MemoryInjectionDelivered ||
		len(in[0].Entries) != 1 || in[0].Entries[0].ID != saved.ID || in[0].Entries[0].Version != saved.Version {
		t.Fatalf("injections %+v %v", in, err)
	}
	r = callTool(t, toolsOf(t, d, h, second.ID), "profile_read", fmt.Sprintf(`{"id":%q}`, saved.ID))
	if r.IsError || !strings.Contains(r.Text, "Likes green tea") {
		t.Fatalf("profile_read %+v", r)
	}
}
