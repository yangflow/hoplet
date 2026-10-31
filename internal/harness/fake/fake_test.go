package fake

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakeharness")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary, err = BuildBinary(context.Background(), dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func intp(n int) *int { return &n }

// start runs the fake harness with s and returns the attached run.
func start(t *testing.T, s Script) harness.Run {
	t.Helper()
	dir := t.TempDir()
	path, err := WriteScript(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{Binary: binary, Script: path}
	spec := harness.Spec{Session: "s1", WorkDir: dir, Env: os.Environ(), Mode: proto.ModeConfirm}
	l, err := h.Launch(spec)
	if err != nil {
		t.Fatal(err)
	}
	io, err := harness.StartExec(context.Background(), l.Command, l.Env, dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := h.Attach(context.Background(), spec, io)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// next returns the next event that has a payload.
func next(t *testing.T, r harness.Run) proto.Payload {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				t.Fatal("events closed")
			}
			if ev.Payload != nil {
				return ev.Payload
			}
		case <-timeout:
			t.Fatal("timed out waiting for an event")
		}
	}
}

// until collects payloads up to and including the first TurnEnded.
func until(t *testing.T, r harness.Run) []proto.Payload {
	t.Helper()
	var got []proto.Payload
	for {
		p := next(t, r)
		got = append(got, p)
		if _, ok := p.(proto.TurnEnded); ok {
			return got
		}
	}
}

func TestStreamingTurn(t *testing.T) {
	r := start(t, Script{Session: "native-1", Turns: []Turn{{Steps: []Step{
		{Emit: &Ev{Ev: "delta", ID: "m1", Text: "hel"}},
		{Emit: &Ev{Ev: "delta", ID: "m1", Text: "lo"}},
		{Emit: &Ev{Ev: "tool", ID: "t1", Kind: "exec", Name: "Bash", Summary: "ls", Status: "done"}},
		{Emit: &Ev{Ev: "message", ID: "m1", Text: "hello"}},
	}}}})
	if err := r.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	got := until(t, r)
	want := []proto.Payload{
		proto.TurnStarted{},
		proto.MessageDelta{MessageID: "m1", Text: "hel"},
		proto.MessageDelta{MessageID: "m1", Text: "lo"},
		proto.ToolCall{ID: "t1", Kind: proto.ToolExec, Name: "Bash", Summary: "ls", Status: proto.ToolDone},
		proto.Message{ID: "m1", Role: proto.RoleAssistant, Text: "hello"},
		proto.TurnEnded{Reason: proto.TurnCompleted},
	}
	if !slices.EqualFunc(got, want, func(a, b proto.Payload) bool { return fmt.Sprint(a) == fmt.Sprint(b) }) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
	if r.NativeSession() != "native-1" {
		t.Fatalf("native session %q", r.NativeSession())
	}
}

func TestApprovalAndQuestion(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{
		{Emit: &Ev{Ev: "approval", ID: "a1", Kind: "command", Title: "Run go test", Options: []Option{{ID: "once", Label: "Allow once", Scope: "once"}, {ID: "deny", Label: "Deny", Scope: "deny"}}}},
		{AwaitApproval: "a1"},
		{Emit: &Ev{Ev: "question", ID: "q1", Text: "Which repo?"}},
		{AwaitAnswer: "q1"},
	}}}})
	ctx := context.Background()
	if err := r.Send(ctx, "go"); err != nil {
		t.Fatal(err)
	}
	next(t, r) // turn started
	req, ok := next(t, r).(proto.ApprovalRequested)
	if !ok || req.ID != "a1" || len(req.Options) != 2 || req.Options[0].Scope != proto.ScopeOnce || req.Kind != proto.ApprovalCommand {
		t.Fatalf("approval %#v", req)
	}
	if err := r.Respond(ctx, "a1", "once"); err != nil {
		t.Fatal(err)
	}
	if m := next(t, r).(proto.Message); m.Text != "approved:once" {
		t.Fatalf("after approval: %#v", m)
	}
	if q := next(t, r).(proto.Question); q.ID != "q1" {
		t.Fatalf("question %#v", q)
	}
	if err := r.Answer(ctx, "q1", "api"); err != nil {
		t.Fatal(err)
	}
	if m := next(t, r).(proto.Message); m.Text != "answered:api" {
		t.Fatalf("after answer: %#v", m)
	}
	if e := next(t, r).(proto.TurnEnded); e.Reason != proto.TurnCompleted {
		t.Fatalf("end %#v", e)
	}
}

func TestSteerAndInterruptDuringSlowOutput(t *testing.T) {
	r := start(t, Script{Turns: []Turn{
		{Steps: []Step{{SleepMS: 60_000}, {Emit: &Ev{Ev: "message", ID: "never", Text: "never"}}}},
		{Steps: []Step{{EchoPrompt: true}}},
	}})
	ctx := context.Background()
	if err := r.Send(ctx, "slow"); err != nil {
		t.Fatal(err)
	}
	next(t, r) // turn started
	if err := r.Steer(ctx, "faster"); err != nil {
		t.Fatal(err)
	}
	if m := next(t, r).(proto.Message); m.Text != "steer:faster" {
		t.Fatalf("steer: %#v", m)
	}
	begin := time.Now()
	if err := r.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	if e := next(t, r).(proto.TurnEnded); e.Reason != proto.TurnInterrupted {
		t.Fatalf("interrupt: %#v", e)
	}
	if time.Since(begin) > 2*time.Second {
		t.Fatal("interrupt was not prompt")
	}
	if err := r.Send(ctx, "again"); err != nil {
		t.Fatal(err)
	}
	got := until(t, r)
	if m, ok := got[1].(proto.Message); !ok || m.Text != "again" {
		t.Fatalf("next turn: %#v", got)
	}
}

func TestCrashMidTurnAbortsTurn(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{
		{Emit: &Ev{Ev: "delta", ID: "m1", Text: "partial"}},
		{Exit: intp(3)},
	}}}})
	if err := r.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	got := until(t, r)
	end := got[len(got)-1].(proto.TurnEnded)
	if end.Reason != proto.TurnAborted || end.Message != "harness exited with code 3" {
		t.Fatalf("end %#v", end)
	}
	for range r.Events() {
	}
	if r.Err() != nil {
		t.Fatalf("err %v", r.Err())
	}
}

func TestRawLinesAndErrors(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{
		{Raw: "not json"},
		{Stderr: "warning on stderr"},
		{Emit: &Ev{Ev: "mystery"}},
	}}}})
	if err := r.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	// stderr is a separate pipe, so its line may arrive after turn_ended.
	var sawStderr, ended bool
	var errs []string
	timeout := time.After(5 * time.Second)
	for !sawStderr || !ended {
		select {
		case ev := <-r.Events():
			switch p := ev.Payload.(type) {
			case nil:
				if string(ev.Raw) == "warning on stderr" {
					sawStderr = true
				}
			case proto.Error:
				errs = append(errs, p.Message)
				if len(ev.Raw) == 0 {
					t.Fatal("error event without raw line")
				}
			case proto.TurnEnded:
				ended = true
			}
		case <-timeout:
			t.Fatal("timed out")
		}
	}
	if !slices.Equal(errs, []string{"unparseable harness line", "unknown harness event mystery"}) {
		t.Fatalf("stderr %v errors %q", sawStderr, errs)
	}
	if err := r.Send(context.Background(), "extra"); err != nil {
		t.Fatal(err)
	}
	if e := next(t, r).(proto.Error); e.Message != "no more scripted turns" {
		t.Fatalf("exhausted script: %#v", e)
	}
}

func TestProbeAndLaunch(t *testing.T) {
	h := &Harness{Binary: binary, Script: "/x.json"}
	info, err := h.Probe(context.Background())
	if err != nil || info.Kind != Kind || !info.Capabilities.Steer {
		t.Fatalf("probe %+v %v", info, err)
	}
	l, err := h.Launch(harness.Spec{Env: []string{"A=1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.Env, []string{"A=1", "HOPLET_FAKE_SCRIPT=/x.json"}) || l.Command[0] != binary {
		t.Fatalf("launch %+v", l)
	}
	if _, err := (&Harness{}).Launch(harness.Spec{}); err == nil {
		t.Fatal("launch without binary accepted")
	}
}
