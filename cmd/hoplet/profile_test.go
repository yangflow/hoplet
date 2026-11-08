package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// seedProfile writes an entry and two candidates into the running daemon's
// home, the way a companion would.
func seedProfile(t *testing.T, h string) (entry string, candidates [2]string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	svc, err := memory.Open(ctx, memory.Config{Root: home.Profile(h), Store: st})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Apply(ctx, memory.Write{OpID: "seed", Actor: memory.ActorUser, Action: memory.ActionCreate, Category: memory.CategoryFact, Scope: memory.Scope{Kind: memory.ScopeRepo, Value: "r"}, Body: "the first fact"})
	if err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{"proposal one", "proposal two"} {
		id, err := memory.NewID()
		if err != nil {
			t.Fatal(err)
		}
		_, err = svc.Commit(ctx, memory.Op{ID: "seed-" + id, Actor: memory.ActorCompanion, Reason: "noticed", Entry: memory.Entry{
			ID: id, Category: memory.CategoryFact, Status: memory.StatusCandidate, Scope: memory.Scope{Kind: memory.ScopeRepo, Value: "r"}, Body: body,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AddMemoryCandidate(ctx, store.MemoryCandidate{EntryID: id, Action: "create", Actor: "companion", Reason: "noticed"}); err != nil {
			t.Fatal(err)
		}
		candidates[i] = id
	}
	return res.Entry.ID, candidates
}

func TestProfileCommands(t *testing.T) {
	h, _ := testHome(t)
	startDaemonProcess(t, h)
	entry, cands := seedProfile(t, h)

	if out := mustCLI(t, "profile", "list"); !strings.Contains(out, entry) || !strings.Contains(out, "repo:r") || strings.Contains(out, cands[0]) {
		t.Fatalf("list: %q", out)
	}
	if out := mustCLI(t, "profile", "list", "--all", "--category", "fact"); !strings.Contains(out, cands[0]) {
		t.Fatalf("list --all: %q", out)
	}
	if out := mustCLI(t, "profile", "list", "--status", "candidate", "--scope", "repo:r", "--text", "two"); !strings.Contains(out, cands[1]) || strings.Contains(out, cands[0]) {
		t.Fatalf("filtered: %q", out)
	}
	var listed []proto.ProfileEntry
	if err := json.Unmarshal([]byte(mustCLI(t, "profile", "list", "--json")), &listed); err != nil || len(listed) != 1 || listed[0].ID != entry {
		t.Fatalf("list --json: %+v %v", listed, err)
	}
	if out := mustCLI(t, "profile", "show", entry); !strings.Contains(out, "the first fact") || !strings.Contains(out, "v1") || !strings.Contains(out, "user") {
		t.Fatalf("show: %q", out)
	}

	var out, errb bytes.Buffer
	a := testApp(&out, &errb)
	a.stdin = strings.NewReader("the second fact\n")
	if code := a.run([]string{"profile", "edit", entry, "--body", "-"}); code != 0 {
		t.Fatalf("edit: exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "Saved "+entry+", version 2") {
		t.Fatalf("edit: %q", out.String())
	}

	out.Reset()
	errb.Reset()
	a.stdin = strings.NewReader("password: hunter22")
	if code := a.run([]string{"profile", "edit", entry, "--body", "-"}); code == 0 || strings.Contains(out.String(), "Saved") || !strings.Contains(errb.String(), "credential") {
		t.Fatalf("refused edit: exit %d, %q, %q", code, out.String(), errb.String())
	}

	var detail proto.ProfileDetail
	if err := json.Unmarshal([]byte(mustCLI(t, "profile", "show", entry, "--json")), &detail); err != nil || detail.Entry.Body != "the second fact" || len(detail.History) != 2 {
		t.Fatalf("show --json: %+v %v", detail, err)
	}

	if got := mustCLI(t, "profile", "candidates"); !strings.Contains(got, cands[0]) || !strings.Contains(got, "companion") {
		t.Fatalf("candidates: %q", got)
	}
	var waiting []proto.ProfileCandidate
	if err := json.Unmarshal([]byte(mustCLI(t, "profile", "candidates", "--json")), &waiting); err != nil || len(waiting) != 2 {
		t.Fatalf("candidates --json: %+v %v", waiting, err)
	}
	if got := mustCLI(t, "profile", "accept", cands[0], "--scope", "repo:narrow"); !strings.Contains(got, "active") {
		t.Fatalf("accept: %q", got)
	}
	var rejected proto.ProfileEntry
	if err := json.Unmarshal([]byte(mustCLI(t, "profile", "reject", cands[1], "--json")), &rejected); err != nil || rejected.Status != "withdrawn" {
		t.Fatalf("reject --json: %+v %v", rejected, err)
	}
	if got := mustCLI(t, "profile", "withdraw", entry, "--reason", "no longer true"); !strings.HasPrefix(got, "Withdrew "+entry+", version 3") {
		t.Fatalf("withdraw: %q", got)
	}
}

func TestProfileUsageErrors(t *testing.T) {
	const id = "mem_00000000000000000000000000000000"
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no command", []string{"profile"}, "usage"},
		{"unknown", []string{"profile", "nope"}, "unknown command"},
		{"list arg", []string{"profile", "list", "x"}, ""},
		{"all and status", []string{"profile", "list", "--all", "--status", "active"}, "exclude"},
		{"bad scope", []string{"profile", "list", "--scope", "planet:x"}, "bad scope"},
		{"global with value", []string{"profile", "accept", id, "--scope", "global:x"}, "bad scope"},
		{"repo without value", []string{"profile", "edit", id, "--body", "-", "--scope", "repo"}, "bad scope"},
		{"show no id", []string{"profile", "show"}, ""},
		{"edit no body", []string{"profile", "edit", id}, "--body"},
		{"other host", []string{"--host", "x", "profile", "list"}, "--host"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			a := testApp(&out, &errb)
			if code := a.run(c.args); code != 2 || !strings.Contains(errb.String(), c.want) {
				t.Fatalf("exit %d: %q", code, errb.String())
			}
		})
	}
}
