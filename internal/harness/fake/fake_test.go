package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
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

// payloadAs asserts p has type T.
func payloadAs[T proto.Payload](t *testing.T, p proto.Payload) T {
	t.Helper()
	v, ok := p.(T)
	if !ok {
		t.Fatalf("payload is %T (%#v), want %T", p, p, v)
	}
	return v
}

// start runs the fake harness with s and returns the attached run.
func start(t *testing.T, s Script) harness.Run {
	t.Helper()
	r, _ := startWith(t, s, nil, nil)
	return r
}

// startWith is start with custom capabilities and a hook to adjust the spec.
func startWith(t *testing.T, s Script, caps *proto.Capabilities, adjust func(*harness.Spec)) (harness.Run, *harness.ExecStdio) {
	t.Helper()
	dir := t.TempDir()
	path, err := WriteScript(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{Binary: binary, Script: path, Capabilities: caps}
	spec := harness.Spec{Session: "s1", WorkDir: dir, Env: os.Environ(), Mode: proto.ModeConfirm}
	if adjust != nil {
		adjust(&spec)
	}
	l, err := h.Launch(spec)
	if err != nil {
		t.Fatal(err)
	}
	io, err := harness.StartExec(context.Background(), l.Command, l.Env, l.Dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := h.Attach(context.Background(), spec, io)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, io
}

// waitEvent returns the first event satisfying match.
func waitEvent(t *testing.T, r harness.Run, match func(harness.Event) bool) harness.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				t.Fatal("events closed before the awaited event")
			}
			if match(ev) {
				return ev
			}
		case <-timeout:
			t.Fatal("timed out waiting for an event")
		}
	}
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
	req := payloadAs[proto.ApprovalRequested](t, next(t, r))
	if req.ID != "a1" || len(req.Options) != 2 || req.Options[0].Scope != proto.ScopeOnce || req.Kind != proto.ApprovalCommand {
		t.Fatalf("approval %#v", req)
	}
	if err := r.Respond(ctx, "a1", "once"); err != nil {
		t.Fatal(err)
	}
	if m := payloadAs[proto.Message](t, next(t, r)); m.Text != "approved:once" {
		t.Fatalf("after approval: %#v", m)
	}
	if q := payloadAs[proto.Question](t, next(t, r)); q.ID != "q1" {
		t.Fatalf("question %#v", q)
	}
	if err := r.Answer(ctx, "q1", "api"); err != nil {
		t.Fatal(err)
	}
	if m := payloadAs[proto.Message](t, next(t, r)); m.Text != "answered:api" {
		t.Fatalf("after answer: %#v", m)
	}
	if e := payloadAs[proto.TurnEnded](t, next(t, r)); e.Reason != proto.TurnCompleted {
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
	if m := payloadAs[proto.Message](t, next(t, r)); m.Text != "steer:faster" {
		t.Fatalf("steer: %#v", m)
	}
	begin := time.Now()
	if err := r.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	if e := payloadAs[proto.TurnEnded](t, next(t, r)); e.Reason != proto.TurnInterrupted {
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
	end := payloadAs[proto.TurnEnded](t, got[len(got)-1])
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
		case ev, ok := <-r.Events():
			if !ok {
				t.Fatal("events closed early")
			}
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
	if e := payloadAs[proto.Error](t, next(t, r)); e.Message != "no more scripted turns" {
		t.Fatalf("exhausted script: %#v", e)
	}
}

func TestProbeAndLaunch(t *testing.T) {
	h := &Harness{Binary: binary, Script: "/x.json"}
	info, err := h.Probe(context.Background())
	if err != nil || !info.Supported || !info.Capabilities.Steer || !info.Capabilities.Resume {
		t.Fatalf("probe %+v %v", info, err)
	}
	l, err := h.Launch(harness.Spec{Env: []string{"A=1"}, WorkDir: "/w", Resume: "n7"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.Env, []string{"A=1", "HOPLET_FAKE_SCRIPT=/x.json", "HOPLET_FAKE_RESUME=n7"}) || l.Command[0] != binary || l.Dir != "/w" {
		t.Fatalf("launch %+v", l)
	}
	if _, err := (&Harness{}).Launch(harness.Spec{}); err == nil {
		t.Fatal("launch without binary accepted")
	}
}

func TestRawMatchesNativeLine(t *testing.T) {
	r := start(t, Script{Session: "native-1", Turns: []Turn{{Steps: []Step{
		{Emit: &Ev{Ev: "delta", ID: "m1", Text: "x"}},
	}}}})
	if err := r.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	var raws []string
	var native string
	for len(raws) < 4 {
		ev := waitEvent(t, r, func(harness.Event) bool { return true })
		if ev.Stderr {
			continue
		}
		raws = append(raws, string(ev.Raw))
		if ev.Native != "" {
			native = ev.Native
		}
	}
	enc := func(e Ev) string {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	want := []string{
		enc(Ev{Ev: "session", ID: "native-1"}),
		enc(Ev{Ev: "turn_started"}),
		enc(Ev{Ev: "delta", ID: "m1", Text: "x"}),
		enc(Ev{Ev: "turn_ended", Reason: "completed"}),
	}
	if !slices.Equal(raws, want) {
		t.Fatalf("raw lines %q, want %q", raws, want)
	}
	if native != "native-1" {
		t.Fatalf("Native %q", native)
	}
}

func TestAttachInTurnThenKilledAborts(t *testing.T) {
	r, io := startWith(t, Script{Turns: []Turn{{}}}, nil, func(s *harness.Spec) {
		s.State = harness.State{InTurn: true, Native: "n0"}
	})
	if r.NativeSession() != "n0" {
		t.Fatalf("native from state %q", r.NativeSession())
	}
	if err := io.Signal("TERM"); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, r, func(ev harness.Event) bool { return ev.Exit != nil })
	end := payloadAs[proto.TurnEnded](t, ev.Payload)
	if end.Reason != proto.TurnAborted || end.Message != "harness killed by SIGTERM" {
		t.Fatalf("end %#v", end)
	}
	if ev.Exit.Signal != "TERM" || len(ev.Raw) != 0 {
		t.Fatalf("exit %+v raw %q", ev.Exit, ev.Raw)
	}
}

func TestExitStatusOnEvents(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{{Exit: intp(3)}}}}})
	if err := r.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, r, func(ev harness.Event) bool { return ev.Exit != nil })
	if ev.Exit.Code != 3 || ev.Exit.Signal != "" {
		t.Fatalf("exit %+v", ev.Exit)
	}
	r2 := start(t, Script{Turns: []Turn{{Steps: []Step{{Signal: "KILL"}}}}})
	if err := r2.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, r2, func(ev harness.Event) bool { return ev.Exit != nil })
	if ev.Exit.Signal != "KILL" {
		t.Fatalf("exit %+v", ev.Exit)
	}
	if end := payloadAs[proto.TurnEnded](t, ev.Payload); end.Message != "harness killed by SIGKILL" {
		t.Fatalf("end %#v", end)
	}
}

func TestUnsupportedCapabilities(t *testing.T) {
	ctx := context.Background()
	r, _ := startWith(t, Script{Turns: []Turn{{}}}, &proto.Capabilities{}, nil)
	if err := r.Steer(ctx, "x"); !errors.Is(err, harness.ErrUnsupported) {
		t.Fatalf("Steer: %v", err)
	}
	if err := r.SetMode(ctx, proto.ModeReadOnly); !errors.Is(err, harness.ErrUnsupported) {
		t.Fatalf("SetMode: %v", err)
	}
	if err := r.SetModel(ctx, "m"); !errors.Is(err, harness.ErrUnsupported) {
		t.Fatalf("SetModel: %v", err)
	}
	h := &Harness{Binary: binary, Script: "/x.json", Capabilities: &proto.Capabilities{}}
	spec := harness.Spec{Resume: "n1"}
	if _, err := h.Launch(spec); !errors.Is(err, harness.ErrUnsupported) {
		t.Fatalf("Launch resume: %v", err)
	}
	if _, err := h.Attach(ctx, spec, nil); !errors.Is(err, harness.ErrUnsupported) {
		t.Fatalf("Attach resume: %v", err)
	}
	info, err := h.Probe(ctx)
	if err != nil || info.Capabilities.Steer || !info.Supported {
		t.Fatalf("probe %+v %v", info, err)
	}
}

func TestResume(t *testing.T) {
	r, _ := startWith(t, Script{Session: "orig", ResumeTurn: 1, Turns: []Turn{
		{Steps: []Step{{Emit: &Ev{Ev: "message", ID: "m", Text: "zero"}}}},
		{Steps: []Step{{EchoPrompt: true}}},
	}}, nil, func(s *harness.Spec) { s.Resume = "native-9" })
	ev := waitEvent(t, r, func(ev harness.Event) bool { return ev.Native != "" })
	if ev.Native != "native-9" || r.NativeSession() != "native-9" {
		t.Fatalf("native %q / %q", ev.Native, r.NativeSession())
	}
	if err := r.Send(context.Background(), "again"); err != nil {
		t.Fatal(err)
	}
	got := until(t, r)
	if m := payloadAs[proto.Message](t, got[1]); m.Text != "again" {
		t.Fatalf("resumed at the wrong turn: %#v", got)
	}
}

func TestSetModeAndModel(t *testing.T) {
	r := start(t, Script{})
	ctx := context.Background()
	if err := r.SetMode(ctx, proto.ModeAutoEdit); err != nil {
		t.Fatal(err)
	}
	if m := payloadAs[proto.Message](t, next(t, r)); m.Text != "mode:auto_edit" {
		t.Fatalf("mode echo %#v", m)
	}
	if err := r.SetModel(ctx, "big"); err != nil {
		t.Fatal(err)
	}
	if m := payloadAs[proto.Message](t, next(t, r)); m.Text != "model:big" {
		t.Fatalf("model echo %#v", m)
	}
}

func TestUsageAndFileChange(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{
		{Emit: &Ev{Ev: "usage", In: 5, Out: 7}},
		{Emit: &Ev{Ev: "file_change", Name: "a.go", Kind: "modified"}},
	}}}})
	if err := r.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	got := until(t, r)
	if u := payloadAs[proto.Usage](t, got[1]); u.InputTokens != 5 || u.OutputTokens != 7 {
		t.Fatalf("usage %#v", u)
	}
	if f := payloadAs[proto.FileChange](t, got[2]); f.Path != "a.go" || f.Op != proto.FileModified {
		t.Fatalf("file change %#v", f)
	}
}

func TestUnknownTurnEndReasonFails(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{{Raw: `{"ev":"turn_ended","reason":"weird"}`}}}}})
	if err := r.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	next(t, r) // turn started
	end := payloadAs[proto.TurnEnded](t, next(t, r))
	if end.Reason != proto.TurnFailed || end.Message != "unknown turn end reason weird" {
		t.Fatalf("end %#v", end)
	}
}

func TestIgnoreInterruptKeepsTurnRunning(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{
		{IgnoreIntr: true},
		{SleepMS: 500},
		{Emit: &Ev{Ev: "message", ID: "m", Text: "done"}},
	}}}})
	if err := r.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	next(t, r) // turn started
	if err := r.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := until(t, r)
	if m := payloadAs[proto.Message](t, got[0]); m.Text != "done" {
		t.Fatalf("after interrupt: %#v", got)
	}
	if end := payloadAs[proto.TurnEnded](t, got[1]); end.Reason != proto.TurnCompleted {
		t.Fatalf("end %#v", end)
	}
}

func TestInterruptOutsideTurnIsSilent(t *testing.T) {
	r := start(t, Script{})
	ctx := context.Background()
	if err := r.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.SetModel(ctx, "m"); err != nil { // acts as a barrier
		t.Fatal(err)
	}
	if m := payloadAs[proto.Message](t, next(t, r)); m.Text != "model:m" {
		t.Fatalf("expected only the barrier echo, got %#v", m)
	}
}

func TestCloseKillsHungHarness(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{{Hang: true}}}}})
	if err := r.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	next(t, r) // turn started
	begin := time.Now()
	r.Close()
	if time.Since(begin) > 3*time.Second {
		t.Fatal("Close was not prompt")
	}
	select {
	case _, ok := <-r.Events():
		for ok {
			_, ok = <-r.Events()
		}
	case <-time.After(time.Second):
		t.Fatal("Events not closed after Close")
	}
}

func TestCloseWithoutReaderDoesNotHang(t *testing.T) {
	steps := make([]Step, 300)
	for i := range steps {
		steps[i] = Step{Emit: &Ev{Ev: "delta", ID: "m", Text: "x"}}
	}
	r := start(t, Script{Turns: []Turn{{Steps: steps}}})
	if err := r.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // the events buffer fills and the loop blocks
	done := make(chan struct{})
	go func() { r.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung with nobody reading Events")
	}
}

// stubStdio feeds canned lines to the adapter.
type stubStdio struct{ lines chan harness.Line }

func (s *stubStdio) WriteLine(context.Context, []byte) error { return nil }
func (s *stubStdio) Lines() <-chan harness.Line              { return s.lines }
func (s *stubStdio) Signal(string) error                     { return nil }
func (s *stubStdio) Close() error                            { return nil }

func TestEveryLineYieldsAnEventWithOffset(t *testing.T) {
	stub := &stubStdio{lines: make(chan harness.Line, 8)}
	h := &Harness{Binary: "b", Script: "s"}
	r, err := h.Attach(context.Background(), harness.Spec{}, stub)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	stub.lines <- harness.Line{Kind: harness.Stdin, Data: []byte("in"), Offset: 10}
	stub.lines <- harness.Line{Kind: harness.StdinError, Data: []byte("broken pipe"), Offset: 20}
	stub.lines <- harness.Line{Kind: harness.Stderr, Data: []byte("err"), Offset: 30}
	stub.lines <- harness.Line{Kind: harness.Stdout, Data: []byte(`{"ev":"turn_started"}`), Offset: 40}
	stub.lines <- harness.Line{Kind: harness.Exit, Offset: 50, Code: 9}
	var evs []harness.Event
	for ev := range r.Events() {
		evs = append(evs, ev)
	}
	if len(evs) != 5 {
		t.Fatalf("got %d events: %+v", len(evs), evs)
	}
	for i, off := range []int64{10, 20, 30, 40, 50} {
		if evs[i].Offset != off {
			t.Fatalf("event %d offset %d, want %d", i, evs[i].Offset, off)
		}
	}
	if evs[0].Payload != nil || string(evs[0].Raw) != "in" {
		t.Fatalf("stdin event %+v", evs[0])
	}
	if e := payloadAs[proto.Error](t, evs[1].Payload); !strings.Contains(e.Message, "broken pipe") || len(evs[1].Raw) == 0 {
		t.Fatalf("stdin error event %+v", evs[1])
	}
	if !evs[2].Stderr || evs[2].Payload != nil {
		t.Fatalf("stderr event %+v", evs[2])
	}
	end := payloadAs[proto.TurnEnded](t, evs[4].Payload)
	if end.Reason != proto.TurnAborted || evs[4].Exit == nil || evs[4].Exit.Code != 9 || evs[4].Raw != nil {
		t.Fatalf("exit event %+v", evs[4])
	}
	if r.Err() != nil {
		t.Fatalf("err %v", r.Err())
	}
}

func TestLinesClosedWithoutExitIsDetached(t *testing.T) {
	stub := &stubStdio{lines: make(chan harness.Line)}
	r, err := (&Harness{}).Attach(context.Background(), harness.Spec{}, stub)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	close(stub.lines)
	waitClosed(t, r)
	if !errors.Is(r.Err(), harness.ErrDetached) {
		t.Fatalf("err %v, want ErrDetached", r.Err())
	}
}

func waitClosed(t *testing.T, r harness.Run) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-r.Events():
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("Events never closed")
		}
	}
}
