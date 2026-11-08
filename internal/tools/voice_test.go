package tools

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func voiceSet() (*Set, *changes) {
	got := &changes{}
	return New(fakeSource{got: got, sessions: []proto.Session{
		{ID: "abcd1111", Title: "one", Mode: proto.ModeCode, Directory: "/work", Model: "m", ModelProvider: "p", Harness: proto.HarnessRef{Kind: "codex"}},
		{ID: "abcd2222", Title: "two", Mode: proto.ModePersonal},
	}}), got
}

var voiceCaller = proto.Session{ID: "call", Mode: proto.ModeVoice}

func TestVoiceCallerHasOnlyItsTools(t *testing.T) {
	set, _ := voiceSet()
	var names []string
	for _, info := range set.List(voiceCaller) {
		names = append(names, info.Name)
	}
	slices.Sort(names)
	if want := []string{"handoff", "session_read", "session_send", "sessions_list"}; !slices.Equal(names, want) {
		t.Fatalf("tools: %v", names)
	}
	for _, name := range []string{"session_get", "session_create", "report", "artifact_publish"} {
		res, err := set.Call(context.Background(), voiceCaller, name, nil)
		if err != nil || !res.IsError {
			t.Fatalf("%s: %+v %v", name, res, err)
		}
	}
}

func TestVoiceToolsAreOnlyForCalls(t *testing.T) {
	set, _ := voiceSet()
	for _, mode := range []proto.Mode{proto.ModeCode, proto.ModePersonal} {
		caller := proto.Session{ID: "x", Mode: mode}
		if slices.ContainsFunc(set.List(caller), func(i proto.ToolInfo) bool { return i.Name == "session_read" }) {
			t.Fatalf("%s sees a voice tool", mode)
		}
	}
}

func voiceCall(t *testing.T, set *Set, name, args string) proto.ToolsCallResult {
	t.Helper()
	res, err := set.Call(context.Background(), voiceCaller, name, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestVoiceSend(t *testing.T) {
	set, got := voiceSet()
	if res := voiceCall(t, set, "session_send", `{"session":"abcd","text":"hi"}`); !res.IsError {
		t.Fatalf("an unclear target was accepted: %s", res.Text)
	}
	if len(got.sends) != 0 {
		t.Fatalf("sent to an unclear target: %+v", got.sends)
	}
	if res := voiceCall(t, set, "session_send", `{"session":"abcd1","text":"hi"}`); res.IsError {
		t.Fatal(res.Text)
	}
	if len(got.sends) != 1 || got.sends[0].Session != "abcd1111" || got.sends[0].Via != proto.ViaVoice || got.sends[0].Text != "hi" {
		t.Fatalf("sends: %+v", got.sends)
	}
}

func TestVoiceRead(t *testing.T) {
	set, _ := voiceSet()
	res := voiceCall(t, set, "session_read", `{"session":"abcd1"}`)
	if res.IsError || !strings.Contains(res.Text, "the latest reply") {
		t.Fatalf("read: %+v", res)
	}
}

func TestVoiceHandoff(t *testing.T) {
	set, got := voiceSet()
	if res := voiceCall(t, set, "handoff", `{"reference":"abcd2","task":"do"}`); !res.IsError {
		t.Fatalf("a reference with no directory was accepted: %s", res.Text)
	}
	if res := voiceCall(t, set, "handoff", `{"reference":"abcd","task":"do"}`); !res.IsError {
		t.Fatalf("an unclear reference was accepted: %s", res.Text)
	}
	if len(got.creates) != 0 {
		t.Fatalf("created: %+v", got.creates)
	}
	if res := voiceCall(t, set, "handoff", `{"reference":"abcd1","task":"do","title":"t"}`); res.IsError {
		t.Fatal(res.Text)
	}
	c := got.creates[0]
	if c.Mode != proto.ModeCode || c.Directory != "/work" || c.Harness != "codex" || c.Model != "m" || c.ModelProvider != "p" ||
		c.Parent != "" || c.Owner != proto.OwnerMe || c.Permission != proto.ModeConfirm || c.Prompt != "do" || c.Via != proto.ViaVoice {
		t.Fatalf("create: %+v", c)
	}
}
