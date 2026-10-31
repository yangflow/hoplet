package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
		{SleepMS: 1500},
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "two"}},
		{Emit: &fake.Ev{Ev: "message", ID: "m1", Text: "one two"}},
	}}}})
	first := e.manager()
	sess := e.create(first)
	if err := first.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "first delta", func(evs []proto.Event) bool { return count(evs, proto.TypeMessageDelta) == 1 })
	if err := first.Close(); err != nil { // the daemon goes away; the shim and harness keep running
		t.Fatal(err)
	}

	second := e.manager()
	defer second.Close()
	if err := second.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	want := []proto.EventType{proto.TypeMessage, proto.TypeTurnStarted, proto.TypeMessageDelta, proto.TypeMessageDelta, proto.TypeMessage, proto.TypeTurnEnded}
	if len(evs) != len(want) {
		t.Fatalf("events: %s", describe(evs))
	}
	for i, ev := range evs {
		if ev.Type != want[i] {
			t.Fatalf("event %d: %s", i, describe(evs))
		}
	}
	if end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded); end.Reason != proto.TurnCompleted {
		t.Fatalf("end %+v", end)
	}
}

func TestRecoverWithoutShimAbortsOpenTurn(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{SleepMS: 60_000}}}}})
	first := e.manager()
	sess := e.create(first)
	if err := first.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
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
