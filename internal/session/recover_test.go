package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

// TestRecoverMidTurn checks the property the shim exists for: the daemon
// goes away mid-turn, a new one attaches from the committed offset, and the
// turn completes in the store with every event exactly once.
func TestRecoverMidTurn(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "one "}},
		{Emit: &fake.Ev{Ev: "approval", ID: "a1", Kind: "command", Title: "go on", Options: []fake.Option{{ID: "once", Label: "Once", Scope: "once"}}}},
		{AwaitApproval: "a1"},
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "two"}},
		{Emit: &fake.Ev{Ev: "message", ID: "m1", Text: "one two"}},
	}}}})
	first := e.manager()
	sess := e.create(first)
	ctx := context.Background()
	if _, err := first.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "approval", func(evs []proto.Event) bool { return count(evs, proto.TypeApprovalRequest) == 1 })
	if err := first.Close(); err != nil { // the daemon goes away; the shim and harness keep running
		t.Fatal(err)
	}

	second := e.manager()
	defer second.Close()
	if err := second.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Respond(ctx, sess.ID, "a1", "once", "device:1", "r1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	want := []proto.EventType{
		proto.TypeMessage, proto.TypeTurnStarted, proto.TypeMessageDelta, proto.TypeApprovalRequest,
		proto.TypeApprovalResolved, proto.TypeMessage, proto.TypeMessageDelta, proto.TypeMessage, proto.TypeTurnEnded,
	}
	if len(evs) != len(want) {
		t.Fatalf("events: %s", describe(evs))
	}
	for i, ev := range evs {
		if ev.Type != want[i] || ev.Seq != uint64(i+1) {
			t.Fatalf("event %d: %s", i, describe(evs))
		}
	}
	ps := payloads(t, evs)
	texts := []string{ps[2].(proto.MessageDelta).Text, ps[5].(proto.Message).Text, ps[6].(proto.MessageDelta).Text, ps[7].(proto.Message).Text}
	if wantTexts := []string{"one ", "approved:once", "two", "one two"}; !slices.Equal(texts, wantTexts) {
		t.Fatalf("texts %q, want %q", texts, wantTexts)
	}
	if end := ps[len(ps)-1].(proto.TurnEnded); end.Reason != proto.TurnCompleted {
		t.Fatalf("end %+v", end)
	}
	e.waitOffsetAtSpoolEnd(second, sess.ID)
}

func TestRecoverWithoutShimAbortsOpenTurn(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{SleepMS: 60_000}}}}})
	first := e.manager()
	sess := e.create(first)
	if _, err := first.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn start", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnStarted) == 1 })
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	killShim(t, first.dir(sess.ID))
	waitShimGone(t, first, sess.ID)

	second := e.manager()
	defer second.Close()
	if err := second.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded)
	if end.Reason != proto.TurnAborted || !strings.Contains(end.Message, "not running") {
		t.Fatalf("end %+v", end)
	}
}

// killShim kills the shim that owns dir and its harness's process group,
// as if the machine had lost both.
func killShim(t *testing.T, dir string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(b), "\n")
	var start struct {
		Pid int `json:"pid"`
	}
	if err := json.Unmarshal([]byte(first), &start); err != nil || start.Pid == 0 {
		t.Fatalf("no start record in %q: %v", first, err)
	}
	out, err := exec.Command("pgrep", "-f", "shim --dir "+dir).Output()
	if err != nil {
		t.Fatalf("find shim: %v", err)
	}
	for _, f := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
	}
	_ = syscall.Kill(-start.Pid, syscall.SIGKILL) // the harness may already be gone
}
