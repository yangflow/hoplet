package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// fakeHermes writes a hermes executable into a new directory that prints
// version and counts its runs.
func fakeHermes(t *testing.T, version string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "hermes")
	script := "#!/bin/sh\necho run >> " + filepath.Join(dir, "runs") + "\necho 'Hermes Agent v" + version + " (2026.9.24) · stable'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func runs(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "runs"))
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "run\n")
}

func TestFind(t *testing.T) {
	bin, dir := fakeHermes(t, "0.22.5")
	if got, err := Find([]string{"PATH=/nonexistent:" + dir}); err != nil || got != bin {
		t.Fatalf("on PATH: %q %v", got, err)
	}
	home := t.TempDir()
	local := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(local, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(local, "hermes")); err != nil {
		t.Fatal(err)
	}
	if got, err := Find([]string{"PATH=/usr/bin", "HOME=" + home}); err != nil || got != filepath.Join(local, "hermes") {
		t.Fatalf("in ~/.local/bin: %q %v", got, err)
	}
	if _, err := Find([]string{"PATH=/nonexistent"}); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("missing: %v", err)
	}
}

func TestProbeIsReusedForAMinute(t *testing.T) {
	_, dir := fakeHermes(t, "0.22.5")
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	h := &Harness{nowFunc: func() time.Time { return now }}
	h.UseEnv(func() []string { return []string{"PATH=" + dir} })
	ctx := context.Background()
	for range 3 {
		info, err := h.Probe(ctx)
		if err != nil || info.Version != "0.22.5" || !info.Supported || !info.Capabilities.Steer || info.Capabilities.PlanMode {
			t.Fatalf("probe %+v %v", info, err)
		}
	}
	if n := runs(t, dir); n != 1 {
		t.Fatalf("hermes ran %d times", n)
	}
	now = now.Add(probeTTL + time.Second)
	if _, err := h.Probe(ctx); err != nil || runs(t, dir) != 2 {
		t.Fatalf("after a minute: %v, %d runs", err, runs(t, dir))
	}
	_, other := fakeHermes(t, "0.30.0")
	h.UseEnv(func() []string { return []string{"PATH=" + other} })
	if info, err := h.Probe(ctx); err != nil || info.Supported {
		t.Fatalf("an unrecorded version is supported: %+v %v", info, err)
	}
}

func TestLaunch(t *testing.T) {
	bin, dir := fakeHermes(t, "0.22.5")
	h := &Harness{Exe: "/opt/hoplet"}
	env := []string{"PATH=" + dir, "HOME=/home/me"}
	l, err := h.Launch(harness.Spec{Mode: proto.ModeConfirm, Env: env, WorkDir: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(l.Command, " ") != "/opt/hoplet hermes-bridge --hermes "+bin || l.Dir != "/work" || len(l.Env) != 2 {
		t.Fatalf("launch %+v", l)
	}
	if _, err := h.Launch(harness.Spec{Mode: proto.ModeAutoReview, Env: env}); err == nil {
		t.Fatal("a mode Hermes lacks launched")
	}
	if _, err := h.Attach(context.Background(), harness.Spec{Mode: proto.ModeConfirm, Plan: true}, newFakeStdio()); !errors.Is(err, harness.ErrUnsupported) {
		t.Fatalf("plan mode: %v", err)
	}
}

// tester drives one run line by line.
type tester struct {
	t      *testing.T
	io     *fakeStdio
	run    *run
	offset int64
	// agents are the subagents the last push's payloads belong to, one
	// per payload, empty for the session's own.
	agents []string
}

func newTester(t *testing.T, spec harness.Spec) *tester {
	t.Helper()
	if spec.Mode == "" {
		spec.Mode = proto.ModeConfirm
	}
	if spec.Session == "" {
		spec.Session = "hs1"
	}
	x := &tester{t: t, io: newFakeStdio()}
	x.run = newRun(x.io, spec)
	x.run.receiptDelay = time.Millisecond
	t.Cleanup(func() { _ = x.run.Close() })
	return x
}

// push feeds a stdout line and returns the payloads it produced.
func (x *tester) push(line string) []proto.Payload {
	x.t.Helper()
	x.offset += 2
	x.io.lines <- harness.Line{Kind: harness.Stdout, Data: []byte(line), Offset: x.offset - 1}
	x.io.lines <- harness.Line{Kind: harness.Position, Offset: x.offset}
	var out []proto.Payload
	x.agents = nil
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-x.run.Events():
			if ev.Payload != nil {
				out = append(out, ev.Payload)
				x.agents = append(x.agents, ev.Agent)
			}
			if ev.Offset == x.offset && ev.Raw == nil && ev.Payload == nil {
				x.checkBusy(line)
				return out
			}
		case <-timeout:
			x.t.Fatalf("no events for %s", line)
		}
	}
}

// checkBusy fails unless the run is busy exactly while turns are queued.
// The loop is waiting for the next line, so its state can be read.
func (x *tester) checkBusy(line string) {
	x.t.Helper()
	if busy, queued := x.run.isBusy(), len(x.run.queue); busy != (queued > 0) {
		x.t.Fatalf("after %s: busy %v with %d turns queued", line, busy, queued)
	}
}

// writes returns the requests the run wrote, from the n-th on.
func (x *tester) writes(from int) []rpc {
	var out []rpc
	for _, w := range x.io.written()[from:] {
		out = append(out, parseRPC(x.t, w))
	}
	return out
}

// answer pushes a result for the i-th write.
func (x *tester) answer(i int, result string) []proto.Payload {
	x.t.Helper()
	return x.push(fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":%s}`, x.writes(i)[0].ID, result))
}

// answerLast pushes a result for the most recent write.
func (x *tester) answerLast(result string) []proto.Payload {
	x.t.Helper()
	return x.answer(len(x.io.written())-1, result)
}

// attach connects the run to session native.
func (x *tester) attach(native string, running bool) {
	x.t.Helper()
	n := len(x.io.written())
	x.push(`{"hoplet_bridge":"connected"}`)
	x.answer(n, fmt.Sprintf(`{"session_id":%q,"stored_session_id":%q,"running":%v,"revision":1,"execution_generation":1,"pending":[],"prompts":[]}`, native, native, running))
}

func eventLine(typ, payload string) string { return eventFor("a1", typ, payload) }

func eventFor(admission, typ, payload string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"event","params":{"type":%q,"session_id":"s1","execution_generation":1,"admission_id":%q,"payload":%s}}`, typ, admission, payload)
}

// refuse pushes a refusal for the i-th write.
func (x *tester) refuse(i int, reason, data string) []proto.Payload {
	x.t.Helper()
	return x.push(fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"error":{"code":4001,"message":%q,"data":{"reason":%q%s}}}`, x.writes(i)[0].ID, reason, reason, data))
}

// waitWrites waits until the run has written n lines (a timer wrote the
// last one).
func (x *tester) waitWrites(n int) {
	x.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(x.io.written()) < n {
		if time.Now().After(deadline) {
			x.t.Fatalf("the run wrote %d lines, want %d", len(x.io.written()), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRequestsWaitForTheSnapshot(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	if err := x.run.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if n := len(x.io.written()); n != 0 {
		t.Fatalf("wrote before attaching: %d", n)
	}
	x.push(`{"hoplet_bridge":"connected"}`)
	if w := x.writes(0); len(w) != 1 || w[0].Method != "session.resume" {
		t.Fatalf("on connecting %+v", w)
	}
	x.answer(0, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":1,"execution_generation":1}`)
	if w := x.writes(1); len(w) != 1 || w[0].Method != "prompt.submit" || w[0].Params["text"] != "hi" || w[0].Params["session_id"] != "s1" {
		t.Fatalf("after attaching %+v", w)
	}
}

func TestCreateUsesTheHopletSessionAndThenItsMode(t *testing.T) {
	x := newTester(t, harness.Spec{Session: "hop1", Mode: proto.ModeFullAuto, WorkDir: "/work", Effort: proto.EffortLow, Fast: true})
	x.push(`{"hoplet_bridge":"connected"}`)
	w := x.writes(0)
	if len(w) != 1 || w[0].Method != "session.create" || w[0].Params["request_id"] != "hop1" || w[0].Params["yolo"] != true ||
		w[0].Params["reasoning"] != "low" || w[0].Params["service_tier"] != "fast" || w[0].Params["title"] != nil {
		t.Fatalf("create %+v", w)
	}
	if payloads := x.answer(0, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":0,"execution_generation":0}`); len(payloads) != 0 {
		t.Fatalf("creating produced %v", payloads)
	}
	if w := x.writes(1); len(w) != 1 || w[0].Method != "session.mutate" || w[0].Params["operation"] != "edit_policy" {
		t.Fatalf("after creating %+v", w)
	}
	if x.run.NativeSession() != "s1" {
		t.Fatalf("native %q", x.run.NativeSession())
	}
}

func TestStaleGenerationIsRetriedOnce(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", true)
	x.push(eventLine("message.start", `{}`))
	x.push(eventLine("approval.request", `{"kind":"approval","prompt_id":"p1","execution_generation":1,"command":"x","choices":["once","deny"]}`))
	n := len(x.io.written())
	if err := x.run.Respond(context.Background(), "p1", "once"); err != nil {
		t.Fatal(err)
	}
	stale := func(i int) []proto.Payload { return x.refuse(i, "stale_generation", `,"execution_generation":5`) }
	if p := stale(n); len(p) != 0 {
		t.Fatalf("first refusal %v", p)
	}
	if w := x.writes(n + 1); len(w) != 1 || w[0].Params["execution_generation"] != float64(5) {
		t.Fatalf("retry %+v", w)
	}
	p := stale(n + 1)
	if len(p) != 1 || !strings.Contains(p[0].(proto.Error).Message, "did not take the approval answer: stale_generation") {
		t.Fatalf("second refusal %v", p)
	}
	// The answer was not taken: Hermes settling the approval itself is news.
	if p := x.push(eventLine("approval.settled", `{"prompt_id":"p1"}`)); len(p) != 1 || p[0].(proto.ApprovalResolved).Outcome != proto.OutcomeExpired {
		t.Fatalf("settled after a refused answer %v", p)
	}
}

func TestInterruptAndSteerAreNotRetriedWhenTheTurnIsOver(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", true)
	n := len(x.io.written())
	if err := x.run.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := x.refuse(n, "stale_generation", `,"execution_generation":5`); len(p) != 0 || len(x.io.written()) != n+1 {
		t.Fatalf("interrupt: %v, %d writes", p, len(x.io.written())-n)
	}
	if err := x.run.Steer(context.Background(), "more"); err != nil {
		t.Fatal(err)
	}
	p := x.refuse(n+1, "stale_generation", `,"execution_generation":5`)
	if len(p) != 1 || p[0].(proto.Error).Message != "the turn ended before the steer reached Hermes" || len(x.io.written()) != n+2 {
		t.Fatalf("steer: %v, %d writes", p, len(x.io.written())-n-1)
	}
}

func TestApprovals(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", true)
	x.push(eventLine("message.start", `{}`))
	x.push(eventLine("tool.start", `{"tool_id":"t1","name":"terminal","context":"rm -r x","args":{"command":"rm -r x"}}`))
	p := x.push(eventLine("approval.request", `{"kind":"approval","prompt_id":"p1","execution_generation":1,"command":"rm -r x","description":"recursive delete","choices":["once","session","always","deny"]}`))
	a, ok := p[0].(proto.ApprovalRequested)
	if !ok || a.Kind != proto.ApprovalCommand || a.ToolCallID != "t1" || len(a.Options) != 4 || a.Options[2].Scope != proto.ScopeAlways {
		t.Fatalf("request %+v", p)
	}
	// Settled elsewhere (Hermes's own UI, or its timeout): expired here.
	x.push(eventLine("approval.request", `{"kind":"approval","prompt_id":"p2","execution_generation":1,"command":"x","choices":["once","deny"]}`))
	if p := x.push(eventLine("approval.settled", `{"prompt_id":"p2"}`)); len(p) != 1 || p[0].(proto.ApprovalResolved).Outcome != proto.OutcomeExpired {
		t.Fatalf("settled elsewhere %v", p)
	}
	// Answered here: Hoplet records it, the settlement adds nothing.
	n := len(x.io.written())
	if err := x.run.Respond(context.Background(), "p1", "session"); err != nil {
		t.Fatal(err)
	}
	if w := x.writes(n); w[0].Method != "approval.respond" || w[0].Params["choice"] != "session" || w[0].Params["prompt_id"] != "p1" {
		t.Fatalf("respond %+v", w)
	}
	if p := x.push(eventLine("approval.settled", `{"prompt_id":"p1"}`)); len(p) != 0 {
		t.Fatalf("settled here %v", p)
	}
	edit := x.push(eventLine("approval.request", `{"kind":"approval","prompt_id":"p3","execution_generation":1,"command":"Edit /work/a","choices":["once","deny"],"edit":{"tool_name":"write_file","path":"/work/a"}}`))
	if a := edit[0].(proto.ApprovalRequested); a.Kind != proto.ApprovalFileChange || a.Title != "Edit /work/a" {
		t.Fatalf("edit approval %+v", a)
	}
}

func TestTurnEndsWithItsReceipt(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1", WorkDir: t.TempDir()})
	x.attach("s1", false)
	x.push(eventLine("message.start", `{}`))
	x.push(eventLine("message.delta", `{"text":"he"}`))
	x.push(eventLine("message.delta", `{"text":"llo"}`))
	n := len(x.io.written())
	p := x.push(eventLine("message.complete", `{"text":"hello","admission_id":"a1","status":"complete"}`))
	if len(p) != 1 || p[0].(proto.Message).Text != "hello" || p[0].(proto.Message).ID != "a1/0" {
		t.Fatalf("complete %v", p)
	}
	if w := x.writes(n); w[0].Method != "prompt.receipt" || w[0].Params["admission_id"] != "a1" {
		t.Fatalf("receipt request %+v", w)
	}
	p = x.answer(n, `{"admission_id":"a1","status":"terminal","outcome":"completed","result":{"final_response":"hello","completed":true},"usage":{"input_tokens":10,"output_tokens":2}}`)
	if len(p) != 2 || p[0] != (proto.Usage{InputTokens: 10, OutputTokens: 2}) || p[1] != (proto.TurnEnded{Reason: proto.TurnCompleted}) {
		t.Fatalf("receipt %v", p)
	}
}

func TestATurnThatEndedWhileAwayIsRecovered(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", true)
	x.push(eventLine("message.start", `{}`))
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	n := len(x.io.written())
	x.push(`{"hoplet_bridge":"connected"}`)
	x.answer(n, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":2,"execution_generation":1}`)
	if w := x.writes(n + 1); len(w) != 1 || w[0].Method != "prompt.receipt" {
		t.Fatalf("after reconnecting %+v", w)
	}
	p := x.answer(n+1, `{"admission_id":"a1","status":"terminal","outcome":"cancelled","result":{"final_response":"partial","interrupted":true},"usage":{"input_tokens":1,"output_tokens":1}}`)
	if len(p) != 3 || p[0].(proto.Message).Text != "partial" || p[2] != (proto.TurnEnded{Reason: proto.TurnInterrupted}) {
		t.Fatalf("recovered %v", p)
	}
}

func TestATurnNothingNamesIsAborted(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1", State: harness.State{InTurn: true, Native: "s1"}})
	x.push(`{"hoplet_bridge":"connected"}`)
	p := x.answer(0, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":2,"execution_generation":1}`)
	if len(p) != 1 || p[0].(proto.TurnEnded).Reason != proto.TurnAborted {
		t.Fatalf("unnamed turn %v", p)
	}
}

func TestSettingsWaitForTheTurnAndGoOneAtATime(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	x.push(eventLine("message.start", `{}`))
	ctx := context.Background()
	n := len(x.io.written())
	if err := x.run.SetMode(ctx, proto.ModeFullAuto); err != nil {
		t.Fatal(err)
	}
	if err := x.run.SetEffort(ctx, proto.EffortLow); err != nil {
		t.Fatal(err)
	}
	if w := x.writes(n); len(w) != 1 || w[0].Method != "config.set" || w[0].Params["value"] != "1" {
		t.Fatalf("mid-turn %+v", w)
	}
	x.push(eventLine("message.complete", `{"text":"","admission_id":"a1","status":"complete"}`))
	r := len(x.io.written()) - 1
	x.answer(r, `{"admission_id":"a1","status":"terminal","outcome":"completed","result":{}}`)
	w := x.writes(r + 1)
	if len(w) != 1 || w[0].Params["operation"] != "edit_policy" {
		t.Fatalf("after the turn %+v", w)
	}
	x.answer(r+1, `{"revision":7,"execution_generation":3}`)
	w = x.writes(r + 2)
	if len(w) != 1 || w[0].Params["operation"] != "reasoning" || w[0].Params["expected_revision"] != float64(7) {
		t.Fatalf("second setting %+v", w)
	}
	if err := x.run.SetMode(ctx, proto.ModeAutoReview); err == nil {
		t.Fatal("auto_review set")
	}
	if err := x.run.SetPlan(ctx, true); !errors.Is(err, harness.ErrUnsupported) {
		t.Fatalf("plan: %v", err)
	}
	if err := x.run.Answer(ctx, "q", "a"); !errors.Is(err, harness.ErrUnsupported) {
		t.Fatalf("answer: %v", err)
	}
}

func TestEditsReportTheirFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	x := newTester(t, harness.Spec{Resume: "s1", WorkDir: dir})
	x.attach("s1", false)
	x.push(eventLine("message.start", `{}`))
	for name, op := range map[string]proto.FileOp{"old.txt": proto.FileModified, "new.txt": proto.FileCreated} {
		args, _ := json.Marshal(map[string]string{"path": name})
		x.push(eventLine("tool.start", fmt.Sprintf(`{"tool_id":%q,"name":"write_file","context":%q,"args":%s}`, name, name, args)))
		p := x.push(eventLine("tool.complete", fmt.Sprintf(`{"tool_id":%q,"name":"write_file","context":%q,"args":%s,"is_error":false}`, name, name, args)))
		if len(p) != 2 || p[1] != (proto.FileChange{Path: filepath.Join(dir, name), Op: op}) {
			t.Fatalf("%s: %v", name, p)
		}
	}
}

func TestBridgeExitMidTurnAborts(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	x.push(eventLine("message.start", `{}`))
	x.io.lines <- harness.Line{Kind: harness.Exit, Offset: 99}
	var got []proto.Payload
	for ev := range x.run.Events() {
		if ev.Payload != nil {
			got = append(got, ev.Payload)
		}
	}
	if len(got) != 1 || got[0].(proto.TurnEnded).Reason != proto.TurnAborted {
		t.Fatalf("exit %v", got)
	}
}

func TestUnreadableLine(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	if p := x.push(`not json`); len(p) != 1 {
		t.Fatalf("unreadable %v", p)
	}
}

func TestMessagesWaitForSettings(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	ctx := context.Background()
	n := len(x.io.written())
	if err := x.run.SetEffort(ctx, proto.EffortHigh); err != nil {
		t.Fatal(err)
	}
	if err := x.run.Send(ctx, "go"); err != nil {
		t.Fatal(err)
	}
	if w := x.writes(n); len(w) != 1 || w[0].Method != "session.mutate" {
		t.Fatalf("before the setting is applied %+v", w)
	}
	x.answer(n, `{"revision":2,"execution_generation":1}`)
	if w := x.writes(n + 1); len(w) != 1 || w[0].Method != "prompt.submit" || w[0].Params["text"] != "go" {
		t.Fatalf("after it %+v", w)
	}
}

func TestTheFirstMessageWaitsForTheCreatedSessionsMode(t *testing.T) {
	x := newTester(t, harness.Spec{Mode: proto.ModeAutoEdit})
	if err := x.run.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	x.push(`{"hoplet_bridge":"connected"}`)
	x.answer(0, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":0,"execution_generation":0}`)
	if w := x.writes(1); len(w) != 1 || w[0].Params["operation"] != "edit_policy" {
		t.Fatalf("after creating %+v", w)
	}
	x.answer(1, `{"revision":1,"execution_generation":1}`)
	if w := x.writes(2); len(w) != 1 || w[0].Method != "prompt.submit" {
		t.Fatalf("after the mode %+v", w)
	}
}

func TestAttachingToARunningBridgeNeedsNoNewConnection(t *testing.T) {
	x := newTester(t, harness.Spec{State: harness.State{Native: "s1"}})
	x.push(`{"hoplet_bridge":"keepalive"}`) // the resume is written before any line is read
	if err := x.run.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if w := x.writes(0); len(w) != 2 || w[0].Method != "session.resume" || w[1].Method != "prompt.submit" {
		t.Fatalf("after a daemon restart %+v", w)
	}
}

func complete(admission, text string) string {
	return eventFor(admission, "message.complete", fmt.Sprintf(`{"text":%q,"admission_id":%q,"status":"complete"}`, text, admission))
}

func receipt(admission, status, usage string) string {
	return fmt.Sprintf(`{"admission_id":%q,"status":%q,"outcome":"completed","result":{"final_response":"x","completed":true}%s}`, admission, status, usage)
}

func TestAQueuedNextTurnRacingTheReceiptEndsBothTurns(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	x.push(eventFor("a1", "message.start", `{}`))
	n := len(x.io.written())
	if p := x.push(complete("a1", "one")); len(p) != 1 || p[0].(proto.Message).ID != "a1/0" {
		t.Fatalf("turn 1 complete %v", p)
	}
	// Hermes starts the queued message before the receipt is answered.
	p := x.push(eventFor("a2", "message.start", `{}`))
	if len(p) != 2 || p[0] != (proto.TurnEnded{Reason: proto.TurnCompleted}) || p[1] != (proto.TurnStarted{}) {
		t.Fatalf("turn 2 start %v", p)
	}
	p = x.answer(n, receipt("a1", "terminal", `,"usage":{"input_tokens":10,"output_tokens":2}`))
	if len(p) != 1 || p[0] != (proto.Usage{InputTokens: 10, OutputTokens: 2}) {
		t.Fatalf("late receipt %v", p)
	}
	if p := x.push(complete("a2", "two")); len(p) != 1 || p[0].(proto.Message).Text != "two" || p[0].(proto.Message).ID != "a2/0" {
		t.Fatalf("turn 2 complete %v", p)
	}
	w := x.writes(n + 1)
	if len(w) != 1 || w[0].Method != "prompt.receipt" || w[0].Params["admission_id"] != "a2" {
		t.Fatalf("second receipt request %+v", w)
	}
	p = x.answer(n+1, receipt("a2", "terminal", `,"usage":{"input_tokens":1,"output_tokens":1}`))
	if len(p) != 2 || p[0] != (proto.Usage{InputTokens: 1, OutputTokens: 1}) || p[1] != (proto.TurnEnded{Reason: proto.TurnCompleted}) {
		t.Fatalf("turn 2 receipt %v", p)
	}
	if len(x.io.written()) != n+2 {
		t.Fatalf("extra writes: %d", len(x.io.written())-n)
	}
}

func TestACompleteOfAnotherTurnIsIgnored(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	x.push(eventFor("a2", "message.start", `{}`))
	n := len(x.io.written())
	if p := x.push(complete("a1", "old")); len(p) != 0 || len(x.io.written()) != n {
		t.Fatalf("stray complete %v", p)
	}
}

// endedTurn starts a turn and completes it, leaving its receipt request as
// the last write; it returns that write's index.
func endedTurn(x *tester) int {
	x.push(eventLine("message.start", `{}`))
	n := len(x.io.written())
	x.push(complete("a1", ""))
	return n
}

func TestReceiptsThatDoNotSayHowTheTurnEndedStillEndIt(t *testing.T) {
	ended := proto.TurnEnded{Reason: proto.TurnCompleted}
	t.Run("unknown", func(t *testing.T) {
		x := newTester(t, harness.Spec{Resume: "s1"})
		x.attach("s1", false)
		n := endedTurn(x)
		if p := x.answer(n, `{"admission_id":"a1","status":"unknown"}`); len(p) != 1 || p[0] != ended {
			t.Fatalf("unknown %v", p)
		}
	})
	t.Run("refused", func(t *testing.T) {
		x := newTester(t, harness.Spec{Resume: "s1"})
		x.attach("s1", false)
		n := endedTurn(x)
		if p := x.refuse(n, "not_found", ""); len(p) != 1 || p[0] != ended {
			t.Fatalf("refused %v", p)
		}
	})
	t.Run("started then terminal", func(t *testing.T) {
		x := newTester(t, harness.Spec{Resume: "s1"})
		x.attach("s1", false)
		n := endedTurn(x)
		if p := x.answer(n, receipt("a1", "started", "")); len(p) != 0 {
			t.Fatalf("started %v", p)
		}
		x.waitWrites(n + 2)
		if w := x.writes(n + 1); w[0].Method != "prompt.receipt" || w[0].Params["admission_id"] != "a1" {
			t.Fatalf("retry %+v", w)
		}
		p := x.answer(n+1, receipt("a1", "terminal", `,"usage":{"input_tokens":3,"output_tokens":4}`))
		if len(p) != 2 || p[0] != (proto.Usage{InputTokens: 3, OutputTokens: 4}) || p[1] != ended {
			t.Fatalf("terminal %v", p)
		}
	})
	t.Run("retries run out", func(t *testing.T) {
		x := newTester(t, harness.Spec{Resume: "s1"})
		x.attach("s1", false)
		n := endedTurn(x)
		for i := range receiptRetries {
			if p := x.answer(n+i, receipt("a1", "queued", "")); len(p) != 0 {
				t.Fatalf("retry %d: %v", i, p)
			}
			x.waitWrites(n + i + 2)
		}
		if p := x.answer(n+receiptRetries, receipt("a1", "started", "")); len(p) != 1 || p[0] != ended {
			t.Fatalf("exhausted %v", p)
		}
	})
	t.Run("recovery", func(t *testing.T) {
		recover := func(t *testing.T) (*tester, int) {
			x := newTester(t, harness.Spec{Resume: "s1", State: harness.State{InTurn: true, Native: "s1"}})
			x.push(`{"hoplet_bridge":"connected"}`)
			x.answer(0, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":2,"execution_generation":1,"pending":[{"admission_id":"a1","status":"started"}]}`)
			if w := x.writes(1); len(w) != 1 || w[0].Method != "prompt.receipt" {
				t.Fatalf("recovery asks %+v", w)
			}
			return x, 1
		}
		x, n := recover(t)
		want := proto.TurnEnded{Reason: proto.TurnAborted, Message: "Hermes has no record of the turn"}
		if p := x.answer(n, `{"admission_id":"a1","status":"unknown"}`); len(p) != 1 || p[0] != want {
			t.Fatalf("unknown %v", p)
		}
		x, n = recover(t)
		for i := range receiptRetries {
			x.answer(n+i, receipt("a1", "started", ""))
			x.waitWrites(n + i + 2)
		}
		want.Message = "Hermes did not report how the turn ended"
		if p := x.answer(n+receiptRetries, receipt("a1", "started", "")); len(p) != 1 || p[0] != want {
			t.Fatalf("exhausted %v", p)
		}
	})
}

func TestASettingInFlightWhenTheConnectionDropsIsSentAgain(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	ctx := context.Background()
	n := len(x.io.written())
	if err := x.run.SetEffort(ctx, proto.EffortLow); err != nil {
		t.Fatal(err)
	}
	if err := x.run.Send(ctx, "go"); err != nil {
		t.Fatal(err)
	}
	oldID := x.writes(n)[0].ID
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	x.push(`{"hoplet_bridge":"connected"}`)
	if w := x.writes(n + 1); len(w) != 1 || w[0].Method != "session.resume" {
		t.Fatalf("on connecting %+v", w)
	}
	x.answer(n+1, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":4,"execution_generation":1}`)
	w := x.writes(n + 2)
	if len(w) != 1 || w[0].Method != "session.mutate" || w[0].Params["operation"] != "reasoning" || w[0].Params["expected_revision"] != float64(4) {
		t.Fatalf("setting after attaching %+v", w)
	}
	// The answer of the lost connection is dropped.
	if p := x.push(fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":{"revision":99,"execution_generation":9}}`, oldID)); len(p) != 0 || len(x.io.written()) != n+3 {
		t.Fatalf("stale answer %v", p)
	}
	x.answer(n+2, `{"revision":5,"execution_generation":1}`)
	if w := x.writes(n + 3); len(w) != 1 || w[0].Method != "prompt.submit" || w[0].Params["text"] != "go" {
		t.Fatalf("message after the setting %+v", w)
	}
}

func TestAMessageInFlightWhenTheConnectionDropsIsSentAgainOnce(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	n := len(x.io.written())
	if err := x.run.Send(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	first := x.writes(n)[0]
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	// Held by the bridge, refused by the new connection before it attached:
	// nothing to report.
	if p := x.refuse(n, "permission_denied", ""); len(p) != 0 {
		t.Fatalf("refusal of a lost request %v", p)
	}
	x.push(`{"hoplet_bridge":"connected"}`)
	x.answer(n+1, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":1,"execution_generation":1}`)
	w := x.writes(n + 2)
	if len(w) != 1 || w[0].Method != "prompt.submit" || w[0].Params["text"] != "hello" || w[0].Params["submission_id"] != first.Params["submission_id"] {
		t.Fatalf("resent %+v (first %+v)", w, first)
	}
	// A refusal on the current connection is reported.
	p := x.refuse(n+2, "permission_denied", "")
	if len(p) != 1 || !strings.Contains(p[0].(proto.Error).Message, "permission_denied") {
		t.Fatalf("current refusal %v", p)
	}
}

func TestInterruptsAndReceiptsInFlightAreDroppedOnDisconnect(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", true)
	n := len(x.io.written())
	if err := x.run.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	x.push(`{"hoplet_bridge":"connected"}`)
	x.answer(n+1, `{"session_id":"s1","stored_session_id":"s1","running":true,"revision":1,"execution_generation":1}`)
	if len(x.io.written()) != n+2 {
		t.Fatalf("something was sent again: %d writes", len(x.io.written())-n)
	}
}

func TestApprovalsSettledWhileAwayAreResolved(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", true)
	x.push(eventLine("message.start", `{}`))
	x.push(eventLine("approval.request", `{"kind":"approval","prompt_id":"p1","execution_generation":1,"command":"x","choices":["once","deny"]}`))
	x.push(eventLine("approval.request", `{"kind":"approval","prompt_id":"p2","execution_generation":1,"command":"y","choices":["once","deny"]}`))
	if err := x.run.Respond(context.Background(), "p2", "once"); err != nil {
		t.Fatal(err)
	}
	n := len(x.io.written())
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	x.push(`{"hoplet_bridge":"connected"}`)
	p := x.answer(n, `{"session_id":"s1","stored_session_id":"s1","running":true,"revision":1,"execution_generation":1,"prompts":[]}`)
	if len(p) != 1 || p[0] != (proto.ApprovalResolved{ID: "p1", Outcome: proto.OutcomeExpired, DecidedBy: "hermes"}) {
		t.Fatalf("after reconnecting %v", p)
	}
	// The answer Hoplet gave to p2 was lost with the connection, and p2 is
	// gone: it is not sent again.
	if len(x.io.written()) != n+1 {
		t.Fatalf("sent %d more", len(x.io.written())-n-1)
	}
}

func TestAnAnswerWaitsForItsApprovalAfterReconnecting(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", true)
	x.push(eventLine("approval.request", `{"kind":"approval","prompt_id":"p1","execution_generation":1,"command":"x","choices":["once","deny"]}`))
	n := len(x.io.written())
	if err := x.run.Respond(context.Background(), "p1", "once"); err != nil {
		t.Fatal(err)
	}
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	x.push(`{"hoplet_bridge":"connected"}`)
	x.answer(n+1, `{"session_id":"s1","stored_session_id":"s1","running":true,"revision":1,"execution_generation":3,"prompts":[{"kind":"approval","prompt_id":"p1","execution_generation":3}]}`)
	w := x.writes(n + 2)
	if len(w) != 1 || w[0].Method != "approval.respond" || w[0].Params["execution_generation"] != float64(3) {
		t.Fatalf("answer after attaching %+v", w)
	}
}

func TestSettledForAnApprovalTheRunDoesNotKnowIsResolved(t *testing.T) {
	x := newTester(t, harness.Spec{State: harness.State{Native: "s1"}})
	p := x.push(eventLine("approval.settled", `{"prompt_id":"p9"}`))
	if len(p) != 1 || p[0] != (proto.ApprovalResolved{ID: "p9", Outcome: proto.OutcomeExpired, DecidedBy: "hermes"}) {
		t.Fatalf("unknown approval settled %v", p)
	}
}

func TestANewerPickWinsOverAGuardedModelsConfirmation(t *testing.T) {
	const guarded = `{"status":"confirmation_required","confirm":"tok","revision":2,"execution_generation":1}`
	t.Run("confirmed", func(t *testing.T) {
		x := newTester(t, harness.Spec{Resume: "s1"})
		x.attach("s1", false)
		n := len(x.io.written())
		if err := x.run.SetModel(context.Background(), "A", ""); err != nil {
			t.Fatal(err)
		}
		x.answer(n, guarded)
		w := x.writes(n + 1)
		payload, _ := w[0].Params["payload"].(map[string]any)
		if len(w) != 1 || payload["model"] != "A" || payload["confirm"] != "tok" {
			t.Fatalf("confirmation %+v", w)
		}
	})
	t.Run("superseded", func(t *testing.T) {
		x := newTester(t, harness.Spec{Resume: "s1"})
		x.attach("s1", false)
		n := len(x.io.written())
		ctx := context.Background()
		if err := x.run.SetModel(ctx, "A", ""); err != nil {
			t.Fatal(err)
		}
		if err := x.run.SetModel(ctx, "B", ""); err != nil {
			t.Fatal(err)
		}
		x.answer(n, guarded)
		w := x.writes(n + 1)
		payload, _ := w[0].Params["payload"].(map[string]any)
		if len(w) != 1 || payload["model"] != "B" || payload["confirm"] != nil {
			t.Fatalf("newer pick %+v", w)
		}
	})
}

// gatedStdio holds the write of one message until the gate opens.
type gatedStdio struct {
	*fakeStdio
	hold string
	hit  chan struct{}
	gate chan struct{}
	once sync.Once
}

func (g *gatedStdio) WriteLine(ctx context.Context, line []byte) error {
	if bytes.Contains(line, []byte(g.hold)) {
		g.once.Do(func() { close(g.hit) })
		<-g.gate
	}
	return g.fakeStdio.WriteLine(ctx, line)
}

// TestMessagesKeepTheirOrderWhileHeldOnesAreReleased holds the write of a
// released message and sends another from a second goroutine. Before the
// ordering lock the second one was written at once, ahead of the first; the
// test waits (yielding, no sleeping) until it would have been written, and
// only then opens the gate.
func TestMessagesKeepTheirOrderWhileHeldOnesAreReleased(t *testing.T) {
	g := &gatedStdio{fakeStdio: newFakeStdio(), hold: `"text":"m1"`, hit: make(chan struct{}), gate: make(chan struct{})}
	x := &tester{t: t, io: g.fakeStdio}
	x.run = newRun(g, harness.Spec{Session: "hs1", Mode: proto.ModeConfirm, Resume: "s1"})
	t.Cleanup(func() { _ = x.run.Close() })
	x.attach("s1", false)
	ctx := context.Background()
	n := len(x.io.written())
	if err := x.run.SetEffort(ctx, proto.EffortLow); err != nil {
		t.Fatal(err)
	}
	if err := x.run.Send(ctx, "m1"); err != nil { // waits for the setting
		t.Fatal(err)
	}
	// The setting is applied: the loop releases m1 and stops in its write.
	x.offset += 2
	g.lines <- harness.Line{Kind: harness.Stdout, Data: fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%q,"result":{"revision":2,"execution_generation":1}}`, x.writes(n)[0].ID), Offset: x.offset - 1}
	<-g.hit
	done := make(chan error, 1)
	go func() { done <- x.run.Send(ctx, "m2") }()
	wroteM2 := func() bool {
		for _, w := range x.io.written() {
			if bytes.Contains(w, []byte(`"text":"m2"`)) {
				return true
			}
		}
		return false
	}
	for range 100000 {
		if wroteM2() {
			break
		}
		runtime.Gosched()
	}
	close(g.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	g.lines <- harness.Line{Kind: harness.Position, Offset: x.offset}
	for ev := range x.run.Events() {
		if ev.Offset == x.offset && ev.Raw == nil && ev.Payload == nil {
			break
		}
	}
	w := x.writes(n + 1)
	if len(w) != 2 || w[0].Params["text"] != "m1" || w[1].Params["text"] != "m2" {
		t.Fatalf("order %+v", w)
	}
}

func TestResentMessagesKeepTheirPlace(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	ctx := context.Background()
	n := len(x.io.written())
	if err := x.run.Send(ctx, "m1"); err != nil { // in flight
		t.Fatal(err)
	}
	if err := x.run.SetEffort(ctx, proto.EffortLow); err != nil { // in flight
		t.Fatal(err)
	}
	if err := x.run.Send(ctx, "m2"); err != nil { // held for the setting
		t.Fatal(err)
	}
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	x.push(`{"hoplet_bridge":"connected"}`)
	x.answer(n+2, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":4,"execution_generation":1}`)
	if w := x.writes(n + 3); len(w) != 1 || w[0].Method != "session.mutate" {
		t.Fatalf("after attaching %+v", w)
	}
	x.answer(n+3, `{"revision":5,"execution_generation":1}`)
	w := x.writes(n + 4)
	if len(w) != 2 || w[0].Params["text"] != "m1" || w[1].Params["text"] != "m2" {
		t.Fatalf("order %+v", w)
	}
}

func TestRecoveryAsksForTheRunningTurnsReceipt(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", true)
	x.push(eventFor("a1", "message.start", `{}`))
	n := len(x.io.written())
	if err := x.run.Send(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	x.answer(n, `{"admission_id":"a2","status":"queued"}`)
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	x.push(`{"hoplet_bridge":"connected"}`)
	x.answer(n+1, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":2,"execution_generation":1}`)
	if w := x.writes(n + 2); len(w) != 1 || w[0].Method != "prompt.receipt" || w[0].Params["admission_id"] != "a1" {
		t.Fatalf("receipt request %+v", w)
	}
}

const runningA2 = `{"session_id":"s1","stored_session_id":"s1","running":true,"revision":2,"execution_generation":1,"pending":[{"admission_id":"a2","status":"started"}]}`

func TestATurnThatStartedWhileAwayIsStarted(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	n := len(x.io.written())
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	x.push(`{"hoplet_bridge":"connected"}`)
	if p := x.answer(n, runningA2); len(p) != 1 || p[0] != (proto.TurnStarted{}) {
		t.Fatalf("snapshot %v", p)
	}
	if err := x.run.SetEffort(context.Background(), proto.EffortLow); err != nil {
		t.Fatal(err)
	}
	if len(x.io.written()) != n+1 {
		t.Fatal("a setting went out while the turn runs")
	}
	if p := x.push(complete("a2", "hi")); len(p) != 1 || p[0].(proto.Message).ID != "a2/0" {
		t.Fatalf("complete %v", p)
	}
	p := x.answer(n+1, receipt("a2", "terminal", ""))
	if len(p) != 1 || p[0] != (proto.TurnEnded{Reason: proto.TurnCompleted}) {
		t.Fatalf("receipt %v", p)
	}
	if w := x.writes(n + 2); len(w) != 1 || w[0].Method != "session.mutate" {
		t.Fatalf("the setting after the turn %+v", w)
	}
}

func TestATurnThatStartedWhileAwayWaitsForTheOneBefore(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	x.push(eventFor("a1", "message.start", `{}`))
	n := endedTurn(x) // a1 completed, its receipt asked for
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	x.push(`{"hoplet_bridge":"connected"}`)
	if p := x.answer(n+1, runningA2); len(p) != 0 {
		t.Fatalf("snapshot %v", p)
	}
	if w := x.writes(n + 2); len(w) != 1 || w[0].Method != "prompt.receipt" || w[0].Params["admission_id"] != "a1" {
		t.Fatalf("receipt request %+v", w)
	}
	// a2 runs, but a1's end comes first.
	if p := x.push(eventFor("a2", "message.delta", `{"text":"he"}`)); len(p) != 0 {
		t.Fatalf("delta %v", p)
	}
	if p := x.push(complete("a2", "hello")); len(p) != 0 {
		t.Fatalf("complete %v", p)
	}
	if err := x.run.SetEffort(context.Background(), proto.EffortLow); err != nil {
		t.Fatal(err)
	}
	if len(x.io.written()) != n+3 {
		t.Fatal("a setting went out while a turn runs")
	}
	p := x.answer(n+2, receipt("a1", "terminal", `,"usage":{"input_tokens":1,"output_tokens":1}`))
	if len(p) != 5 || p[0] != (proto.Usage{InputTokens: 1, OutputTokens: 1}) || p[1] != (proto.TurnEnded{Reason: proto.TurnCompleted}) ||
		p[2] != (proto.TurnStarted{}) || p[3] != (proto.MessageDelta{MessageID: "a2/0", Text: "he"}) || p[4].(proto.Message).Text != "hello" {
		t.Fatalf("resolved %v", p)
	}
	if w := x.writes(n + 3); len(w) != 1 || w[0].Method != "prompt.receipt" || w[0].Params["admission_id"] != "a2" {
		t.Fatalf("a2's receipt request %+v", w)
	}
	p = x.answer(n+3, receipt("a2", "terminal", ""))
	if len(p) != 1 || p[0] != (proto.TurnEnded{Reason: proto.TurnCompleted}) {
		t.Fatalf("a2 end %v", p)
	}
	if w := x.writes(n + 4); len(w) != 1 || w[0].Method != "session.mutate" {
		t.Fatalf("the setting after the turns %+v", w)
	}
}

func TestAReattachedRunAdoptsTheTurnFromItsFirstEvent(t *testing.T) {
	x := newTester(t, harness.Spec{State: harness.State{Native: "s1", InTurn: true}})
	if p := x.push(eventFor("a7", "message.delta", `{"text":"x"}`)); len(p) != 1 || p[0].(proto.MessageDelta).MessageID != "a7/0" {
		t.Fatalf("delta %v", p)
	}
	n := len(x.io.written())
	x.push(complete("a7", "x"))
	if w := x.writes(n); len(w) != 1 || w[0].Params["admission_id"] != "a7" {
		t.Fatalf("receipt request %+v", w)
	}
}

func TestStateAboutLostConnectionsIsBounded(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	ctx := context.Background()
	for i := range 4 {
		n := len(x.io.written())
		if err := x.run.Send(ctx, fmt.Sprintf("m%d", i)); err != nil {
			t.Fatal(err)
		}
		x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
		x.push(`{"hoplet_bridge":"connected"}`)
		x.answer(n+1, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":1,"execution_generation":1}`)
		x.answer(n+2, `{"admission_id":"a","status":"queued"}`)
	}
	x.run.mu.Lock()
	stale, requests := len(x.run.stale), len(x.run.requests)
	x.run.mu.Unlock()
	if stale > 1 || requests != 0 {
		t.Fatalf("%d stale, %d requests kept", stale, requests)
	}
}

func TestClosingStopsTheReceiptTimers(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.run.receiptDelay = time.Hour
	x.attach("s1", false)
	n := endedTurn(x)
	x.answer(n, receipt("a1", "started", ""))
	count := func() int {
		x.run.mu.Lock()
		defer x.run.mu.Unlock()
		return len(x.run.timers)
	}
	if count() != 1 {
		t.Fatalf("%d timers", count())
	}
	if err := x.run.Close(); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatalf("%d timers after closing", count())
	}
}

func TestSanitizeKeepsOnlyWhatTheAdapterReads(t *testing.T) {
	spool := func(line string) string {
		b, _ := json.Marshal(map[string]any{"k": "stdout", "d": line})
		return string(b) + "\n"
	}
	in := spool(`{"jsonrpc":"2.0","id":"1","result":{"admission_id":"a1","status":"terminal","outcome":"completed","messages":[{"content":"secret"}],"tool_outputs":["secret"],"result":{"final_response":"hi","tool_outputs":["secret"]},"usage":{"input_tokens":1,"output_tokens":2}}}`) +
		spool(`{"jsonrpc":"2.0","method":"event","params":{"type":"message.delta","payload":{"text":"annie in /data/hermes-x/state.db, annies, ann_x"}}}`) +
		spool(`{"jsonrpc":"2.0","method":"event","params":{"type":"subagent.start","payload":{"subagent_id":"sa-0","toolsets":["secret-mcp"]}}}`)
	var out bytes.Buffer
	if err := Sanitize(strings.NewReader(in), &out, "/work/p", "/home/annie", []string{"HERMES_HOME=/data/hermes-x"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines %q", lines)
	}
	if strings.Contains(lines[2], "secret") || !strings.Contains(lines[2], "sa-0") {
		t.Fatalf("subagent %s", lines[2])
	}
	if strings.Contains(lines[0], "secret") || strings.Contains(lines[0], "messages") ||
		!strings.Contains(lines[0], `\"usage\":{\"input_tokens\":1,\"output_tokens\":2}`) || !strings.Contains(lines[0], `final_response`) {
		t.Fatalf("receipt %s", lines[0])
	}
	if !strings.Contains(lines[1], `me in /Users/me/.hermes/state.db, annies, ann_x`) {
		t.Fatalf("scrubbed %s", lines[1])
	}
}

// reconnect drops the connection and connects again, answering the resume
// with a snapshot in which turn running (0 for none) is running.
func (x *tester) reconnect(running int) []proto.Payload {
	x.t.Helper()
	n := len(x.io.written())
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	x.push(`{"hoplet_bridge":"connected"}`)
	snap := `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":2,"execution_generation":1}`
	if running > 0 {
		snap = fmt.Sprintf(`{"session_id":"s1","stored_session_id":"s1","running":true,"revision":2,"execution_generation":1,"pending":[{"admission_id":"a%d","status":"started"}]}`, running)
	}
	return x.answer(n, snap)
}

// last returns the most recent write.
func (x *tester) last() rpc {
	w := x.io.written()
	return parseRPC(x.t, w[len(w)-1])
}

func TestASecondDropWhileATurnWaitsEndsBothTurns(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	n := endedTurn(x) // a1 completed, receipt asked for
	_ = n
	if p := x.reconnect(2); len(p) != 0 {
		t.Fatalf("first snapshot %v", p)
	}
	if p := x.push(eventFor("a2", "message.delta", `{"text":"he"}`)); len(p) != 0 {
		t.Fatalf("delta %v", p)
	}
	// a2 finishes during the second outage.
	if p := x.reconnect(0); len(p) != 0 {
		t.Fatalf("second snapshot %v", p)
	}
	if w := x.last(); w.Method != "prompt.receipt" || w.Params["admission_id"] != "a1" {
		t.Fatalf("asked %+v", w)
	}
	p := x.answerLast(receipt("a1", "terminal", ""))
	if len(p) != 3 || p[0] != (proto.TurnEnded{Reason: proto.TurnCompleted}) || p[1] != (proto.TurnStarted{}) || p[2] != (proto.MessageDelta{MessageID: "a2/0", Text: "he"}) {
		t.Fatalf("a1 receipt %v", p)
	}
	if w := x.last(); w.Method != "prompt.receipt" || w.Params["admission_id"] != "a2" {
		t.Fatalf("a2's receipt asked %+v", w)
	}
	p = x.answerLast(receipt("a2", "terminal", ""))
	if len(p) != 2 || p[0].(proto.Message).ID != "a2/0" || p[1] != (proto.TurnEnded{Reason: proto.TurnCompleted}) {
		t.Fatalf("a2 receipt %v", p)
	}
}

func TestAThirdTurnFoundOnASecondDropIsNotLost(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	endedTurn(x)
	x.reconnect(2)
	x.push(eventFor("a2", "message.delta", `{"text":"two"}`))
	x.reconnect(3)
	p := x.answerLast(receipt("a1", "terminal", ""))
	if len(p) != 3 || p[1] != (proto.TurnStarted{}) {
		t.Fatalf("a1 receipt %v", p) // a1 end, a2 start, a2's delta
	}
	n := len(x.io.written())
	p = x.answerLast(receipt("a2", "terminal", ""))
	if len(p) != 3 || p[0].(proto.Message).Text != "x" || p[1] != (proto.TurnEnded{Reason: proto.TurnCompleted}) || p[2] != (proto.TurnStarted{}) {
		t.Fatalf("a2 receipt %v", p) // a2's answer, a2 end, a3 start
	}
	if len(x.io.written()) != n {
		t.Fatalf("a3 is running, not over: %+v", x.last())
	}
}

func TestATurnStartingLiveDoesNotOvertakeTheOnesBeforeIt(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	endedTurn(x)
	x.reconnect(2)
	x.push(complete("a2", "two"))
	// a1's message.complete told how it ended: it ends here, a2 follows.
	p := x.push(eventFor("a3", "message.start", `{}`))
	if len(p) != 3 || p[0] != (proto.TurnEnded{Reason: proto.TurnCompleted}) || p[1] != (proto.TurnStarted{}) || p[2].(proto.Message).Text != "two" {
		t.Fatalf("a3 started %v", p)
	}
	p = x.answerLast(receipt("a2", "terminal", ""))
	if len(p) != 2 || p[0] != (proto.TurnEnded{Reason: proto.TurnCompleted}) || p[1] != (proto.TurnStarted{}) {
		t.Fatalf("a2 receipt %v", p)
	}
}

func TestATurnTheSnapshotStartedIsStartedOnce(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	if p := x.reconnect(2); len(p) != 1 || p[0] != (proto.TurnStarted{}) {
		t.Fatalf("snapshot %v", p)
	}
	if p := x.push(eventFor("a2", "message.start", `{}`)); len(p) != 0 {
		t.Fatalf("second start %v", p)
	}
}

func TestAnOrphanReceiptDoesNotEndAnotherTurn(t *testing.T) {
	x := newTester(t, harness.Spec{State: harness.State{Native: "s1", InTurn: true}})
	if p := x.push(eventFor("a7", "message.delta", `{"text":"x"}`)); len(p) != 1 {
		t.Fatalf("delta %v", p)
	}
	// The open turn is known now: a receipt of another turn ends nothing,
	// and its usage may have been counted by the old daemon.
	if p := x.push(orphan("a0", `,"usage":{"input_tokens":2,"output_tokens":1}`)); len(p) != 0 {
		t.Fatalf("orphan %v", p)
	}
	if p := x.push(eventFor("a7", "message.delta", `{"text":"x"}`)); len(p) != 1 {
		t.Fatalf("the turn went on: %v", p)
	}
}

// orphan is a receipt answer to a request the old daemon sent.
func orphan(admission, usage string) string {
	return `{"jsonrpc":"2.0","id":"old","result":` + receipt(admission, "terminal", usage) + `}`
}

// restarted is a run attached to a running bridge after a daemon restart,
// with a turn open in the store; it checks the one resume it sends.
func restarted(t *testing.T) *tester {
	t.Helper()
	x := newTester(t, harness.Spec{State: harness.State{Native: "s1", InTurn: true}})
	x.push(`{"hoplet_bridge":"keepalive"}`) // the resume is written before any line is read
	if w := x.writes(0); len(w) != 1 || w[0].Method != "session.resume" || w[0].Params["session_id"] != "s1" || w[0].Params["omit_messages"] != true {
		t.Fatalf("on reattaching %+v", w)
	}
	return x
}

// resumes counts the session.resume requests written.
func (x *tester) resumes() int {
	n := 0
	for _, w := range x.writes(0) {
		if w.Method == "session.resume" {
			n++
		}
	}
	return n
}

var (
	restartAborted = proto.TurnEnded{Reason: proto.TurnAborted, Message: "the turn ended while the Hoplet daemon restarted"}
	completed      = proto.TurnEnded{Reason: proto.TurnCompleted}
	used           = proto.Usage{InputTokens: 5, OutputTokens: 3}
)

const (
	idle     = `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":2,"execution_generation":1}`
	withUse  = `,"usage":{"input_tokens":5,"output_tokens":3}`
	runningA = `{"session_id":"s1","stored_session_id":"s1","running":true,"revision":2,"execution_generation":1,"pending":[{"admission_id":%q,"status":"started"}]%s}`
)

func running(admission string) string { return fmt.Sprintf(runningA, admission, "") }

// TestAnOrphanReceiptEndsTheOpenTurnAfterARestart is the turn that ended
// while the daemon was down, after the old daemon had read its
// message.complete (and so emitted its answer): only the receipt answer is
// replayed. The snapshot settles the turn from it, without its answer.
func TestAnOrphanReceiptEndsTheOpenTurnAfterARestart(t *testing.T) {
	x := restarted(t)
	if p := x.push(orphan("a1", withUse)); len(p) != 0 {
		t.Fatalf("orphan %v", p)
	}
	if p := x.answer(0, idle); len(p) != 2 || p[0] != used || p[1] != completed {
		t.Fatalf("snapshot %v", p)
	}
	n := len(x.io.written())
	if err := x.run.SetEffort(context.Background(), proto.EffortLow); err != nil {
		t.Fatal(err)
	}
	if w := x.writes(n); len(w) != 1 || w[0].Method != "session.mutate" {
		t.Fatalf("setting after the turn %+v", w)
	}
	if x.resumes() != 1 {
		t.Fatalf("%d resumes", x.resumes())
	}
}

// TestATurnStartedWhileTheDaemonRestartedWaitsForTheSnapshot is the next
// turn starting before the receipt the old daemon asked for is answered:
// the new turn waits, and the snapshot settles the open one from its
// receipt.
func TestATurnStartedWhileTheDaemonRestartedWaitsForTheSnapshot(t *testing.T) {
	x := restarted(t)
	for _, l := range []string{eventFor("a2", "message.start", `{}`), orphan("a1", withUse), eventFor("a2", "message.delta", `{"text":"he"}`)} {
		if p := x.push(l); len(p) != 0 {
			t.Fatalf("before the snapshot %v", p)
		}
	}
	p := x.answer(0, runningA2)
	if len(p) != 4 || p[0] != used || p[1] != completed || p[2] != (proto.TurnStarted{}) || p[3] != (proto.MessageDelta{MessageID: "a2/0", Text: "he"}) {
		t.Fatalf("snapshot %v", p)
	}
	n := len(x.io.written())
	if p := x.push(complete("a2", "two")); len(p) != 1 || p[0].(proto.Message).ID != "a2/0" {
		t.Fatalf("a2 complete %v", p)
	}
	if w := x.writes(n); len(w) != 1 || w[0].Method != "prompt.receipt" || w[0].Params["admission_id"] != "a2" {
		t.Fatalf("a2's receipt request %+v", w)
	}
	if p := x.answer(n, receipt("a2", "terminal", "")); len(p) != 1 || p[0] != completed {
		t.Fatalf("a2 end %v", p)
	}
}

// TestAReplayedStartEndsTheOpenTurnFromItsHeldReceipt is the receipts
// answered before the next start: the latest ends the open turn, the
// earlier one is dropped, then the new turn starts.
func TestAReplayedStartEndsTheOpenTurnFromItsHeldReceipt(t *testing.T) {
	x := restarted(t)
	for _, l := range []string{orphan("a0", `,"usage":{"input_tokens":2,"output_tokens":2}`), orphan("a1", withUse), eventFor("a2", "message.start", `{}`)} {
		if p := x.push(l); len(p) != 0 {
			t.Fatalf("before the snapshot %v", p)
		}
	}
	if p := x.answer(0, runningA2); len(p) != 3 || p[0] != used || p[1] != completed || p[2] != (proto.TurnStarted{}) {
		t.Fatalf("snapshot %v", p)
	}
}

// TestAStartWithNoReceiptHeldAbortsTheOpenTurn is the old daemon having
// read the open turn's message.complete but not asked for its receipt.
func TestAStartWithNoReceiptHeldAbortsTheOpenTurn(t *testing.T) {
	x := restarted(t)
	if p := x.push(eventFor("a2", "message.start", `{}`)); len(p) != 0 {
		t.Fatalf("a2 start %v", p)
	}
	if p := x.answer(0, runningA2); len(p) != 2 || p[0] != restartAborted || p[1] != (proto.TurnStarted{}) {
		t.Fatalf("snapshot %v", p)
	}
}

// TestALateReceiptDoesNotEndTheOpenTurnAfterARestart is the old daemon
// having ended a0 at a7's start, a0's receipt still due, a7 open in the
// store: a0's receipt is dropped, and a7 ends from its own.
func TestALateReceiptDoesNotEndTheOpenTurnAfterARestart(t *testing.T) {
	x := restarted(t)
	if p := x.push(orphan("a0", withUse)); len(p) != 0 {
		t.Fatalf("orphan %v", p)
	}
	if p := x.push(eventFor("a7", "message.delta", `{"text":"he"}`)); len(p) != 1 || p[0] != (proto.MessageDelta{MessageID: "a7/0", Text: "he"}) {
		t.Fatalf("a7 delta %v", p)
	}
	if p := x.answer(0, running("a7")); len(p) != 0 {
		t.Fatalf("snapshot %v", p)
	}
	n := len(x.io.written())
	if p := x.push(complete("a7", "hello")); len(p) != 1 || p[0] != (proto.Message{ID: "a7/0", Role: proto.RoleAssistant, Text: "hello"}) {
		t.Fatalf("a7 complete %v", p)
	}
	if w := x.writes(n); len(w) != 1 || w[0].Params["admission_id"] != "a7" {
		t.Fatalf("receipt request %+v", w)
	}
	if p := x.answer(n, receipt("a7", "terminal", `,"usage":{"input_tokens":1,"output_tokens":1}`)); len(p) != 2 || p[1] != completed {
		t.Fatalf("a7 end %v", p)
	}
}

// TestAQueuedTurnIsNotTakenForTheOpenOneAfterARestart is the input of a
// late receipt (above) read as the old daemon holding a2 behind a1: a2's
// content is the open turn's.
func TestAQueuedTurnIsNotTakenForTheOpenOneAfterARestart(t *testing.T) {
	x := restarted(t)
	if p := x.push(orphan("a1", withUse)); len(p) != 0 {
		t.Fatalf("a1's receipt %v", p)
	}
	// Not a real replay: had the old daemon read a2's start, a2 would be the store's open turn.
	if p := x.push(eventFor("a2", "message.delta", `{"text":"he"}`)); len(p) != 1 {
		t.Fatalf("a2 delta %v", p)
	}
	if p := x.answer(0, runningA2); len(p) != 0 {
		t.Fatalf("snapshot %v", p)
	}
	n := len(x.io.written())
	if p := x.push(complete("a2", "hello")); len(p) != 1 || p[0] != (proto.Message{ID: "a2/0", Role: proto.RoleAssistant, Text: "hello"}) {
		t.Fatalf("a2 complete %v", p)
	}
	if p := x.answer(n, receipt("a2", "terminal", "")); len(p) != 1 || p[0] != completed {
		t.Fatalf("a2 end %v", p)
	}
}

// TestHeldReceiptsAfterARestart covers the other ways the snapshot settles
// the receipts held for the open turn.
func TestHeldReceiptsAfterARestart(t *testing.T) {
	t.Run("running, not adopted", func(t *testing.T) {
		x := restarted(t)
		x.push(orphan("a0", withUse))
		if p := x.answer(0, running("a7")); len(p) != 0 {
			t.Fatalf("snapshot %v", p)
		}
		if w := x.last(); w.Method != "prompt.receipt" || w.Params["admission_id"] != "a7" {
			t.Fatalf("asked %+v", w)
		}
		if p := x.answerLast(receipt("a7", "terminal", withUse)); len(p) != 2 || p[0] != used || p[1] != completed {
			t.Fatalf("a7 receipt %v", p)
		}
	})
	t.Run("idle, several", func(t *testing.T) {
		x := restarted(t)
		x.push(orphan("a0", withUse))
		x.push(orphan("a1", `,"usage":{"input_tokens":1,"output_tokens":1}`))
		if p := x.answer(0, idle); len(p) != 2 || p[0] != (proto.Usage{InputTokens: 1, OutputTokens: 1}) || p[1] != completed {
			t.Fatalf("snapshot %v", p)
		}
	})
	t.Run("adopted by an event", func(t *testing.T) {
		x := restarted(t)
		x.push(orphan("a0", withUse))
		x.push(orphan("a7", ""))
		p := x.push(eventFor("a7", "message.delta", `{"text":"x"}`))
		if len(p) != 2 || p[0] != (proto.MessageDelta{MessageID: "a7/0", Text: "x"}) || p[1] != completed {
			t.Fatalf("a7 delta %v", p)
		}
		if p := x.answer(0, idle); len(p) != 0 {
			t.Fatalf("snapshot %v", p)
		}
	})
	t.Run("refused", func(t *testing.T) {
		x := restarted(t)
		x.push(orphan("a1", withUse))
		p := x.refuse(0, "internal", "")
		if len(p) != 3 || !strings.Contains(p[0].(proto.Error).Message, "refused to attach the session") || p[1] != used || p[2] != completed {
			t.Fatalf("refused %v", p)
		}
	})
	t.Run("refused, none held", func(t *testing.T) {
		x := restarted(t)
		if p := x.refuse(0, "internal", ""); len(p) != 2 || p[1] != restartAborted {
			t.Fatalf("refused %v", p)
		}
	})
	t.Run("refused and resent", func(t *testing.T) {
		x := restarted(t)
		if p := x.refuse(0, "stale_generation", `,"execution_generation":4`); len(p) != 0 {
			t.Fatalf("refused %v", p)
		}
		if w := x.writes(1); len(w) != 1 || w[0].Method != "session.resume" {
			t.Fatalf("resent %+v", w)
		}
		if p := x.answer(1, running("a1")); len(p) != 0 {
			t.Fatalf("snapshot %v", p) // the open turn goes on: no end, no second start
		}
	})
}

func TestTheResumeAfterARestartResolvesTheOpenTurn(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		x := restarted(t)
		if p := x.answer(0, running("a3")); len(p) != 0 {
			t.Fatalf("snapshot %v", p)
		}
		probe := len(x.io.written()) - 1
		if w := x.writes(probe); len(w) != 1 || w[0].Method != "prompt.receipt" || w[0].Params["admission_id"] != "a3" {
			t.Fatalf("receipt request %+v", w)
		}
		if p := x.push(complete("a3", "three")); len(p) != 1 || p[0].(proto.Message).ID != "a3/0" || len(x.io.written()) != probe+1 {
			t.Fatalf("complete %v", p)
		}
		if p := x.answer(probe, receipt("a3", "terminal", "")); len(p) != 1 || p[0] != completed {
			t.Fatalf("end %v", p)
		}
		if x.resumes() != 1 {
			t.Fatalf("%d resumes", x.resumes())
		}
	})
	t.Run("idle", func(t *testing.T) {
		x := restarted(t)
		if p := x.answer(0, idle); len(p) != 1 || p[0] != restartAborted {
			t.Fatalf("snapshot %v", p)
		}
		n := len(x.io.written())
		if err := x.run.SetEffort(context.Background(), proto.EffortLow); err != nil {
			t.Fatal(err)
		}
		if w := x.writes(n); len(w) != 1 || w[0].Method != "session.mutate" {
			t.Fatalf("setting after the turn %+v", w)
		}
	})
}

// TestARunningOpenTurnIsAskedForItsReceipt is the old daemon having read
// the open turn's message.complete while Hermes was still settling it: the
// snapshot finds it running and nothing else would end it.
func TestARunningOpenTurnIsAskedForItsReceipt(t *testing.T) {
	t.Run("terminal", func(t *testing.T) {
		x := restarted(t)
		x.answer(0, running("a1"))
		if p := x.answerLast(receipt("a1", "terminal", withUse)); len(p) != 2 || p[0] != used || p[1] != completed {
			t.Fatalf("receipt %v", p)
		}
		n := len(x.io.written())
		if err := x.run.SetEffort(context.Background(), proto.EffortLow); err != nil {
			t.Fatal(err)
		}
		if w := x.writes(n); len(w) != 1 || w[0].Method != "session.mutate" {
			t.Fatalf("setting after the turn %+v", w)
		}
	})
	t.Run("still running", func(t *testing.T) {
		x := restarted(t)
		x.answer(0, running("a1"))
		n := len(x.io.written()) - 1
		for i := range receiptRetries {
			x.answer(n+i, receipt("a1", "started", ""))
			x.waitWrites(n + i + 2)
		}
		// Never aborted: the turn runs, and its own complete ends it.
		if p := x.answer(n+receiptRetries, receipt("a1", "started", "")); len(p) != 0 {
			t.Fatalf("exhausted %v", p)
		}
		m := len(x.io.written())
		if p := x.push(complete("a1", "done")); len(p) != 1 || p[0].(proto.Message).ID != "a1/0" {
			t.Fatalf("complete %v", p)
		}
		if w := x.writes(m); len(w) != 1 || w[0].Params["admission_id"] != "a1" {
			t.Fatalf("receipt request %+v", w)
		}
		if p := x.answer(m, receipt("a1", "terminal", "")); len(p) != 1 || p[0] != completed {
			t.Fatalf("end %v", p)
		}
	})
}

// TestASnapshotListsApprovalsTheRunDoesNotKnow is an approval whose
// request line an earlier daemon read: it can still be answered.
func TestASnapshotListsApprovalsTheRunDoesNotKnow(t *testing.T) {
	x := restarted(t)
	snap := fmt.Sprintf(runningA, "a1", `,"prompts":[{"kind":"approval","prompt_id":"p1","execution_generation":1,"command":"rm x","choices":["once","deny"]}]`)
	p := x.answer(0, snap)
	if len(p) != 1 {
		t.Fatalf("snapshot %v", p)
	}
	if a := p[0].(proto.ApprovalRequested); a.ID != "p1" || a.Title != "rm x" || len(a.Options) != 2 || a.ToolCallID != "" {
		t.Fatalf("approval %+v", a)
	}
	// Known now: a later snapshot does not list it again.
	n := len(x.io.written())
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	x.push(`{"hoplet_bridge":"connected"}`)
	if p := x.answer(n, snap); len(p) != 0 {
		t.Fatalf("second snapshot %v", p)
	}
}

// TestATurnTheStoreMissedIsFoundOnReattaching is the old daemon dying
// between a line's TurnEnded and TurnStarted: the store has no open turn,
// and the snapshot finds the running one.
func TestATurnTheStoreMissedIsFoundOnReattaching(t *testing.T) {
	x := newTester(t, harness.Spec{State: harness.State{Native: "s1"}})
	x.push(`{"hoplet_bridge":"keepalive"}`)
	if w := x.writes(0); len(w) != 1 || w[0].Method != "session.resume" {
		t.Fatalf("on reattaching %+v", w)
	}
	// A receipt the old daemon asked for and counted is not counted again.
	if p := x.push(orphan("a1", withUse)); len(p) != 0 {
		t.Fatalf("orphan %v", p)
	}
	if p := x.answer(0, running("a2")); len(p) != 1 || p[0] != (proto.TurnStarted{}) {
		t.Fatalf("snapshot %v", p)
	}
	n := len(x.io.written())
	if p := x.push(complete("a2", "two")); len(p) != 1 || p[0].(proto.Message).ID != "a2/0" {
		t.Fatalf("complete %v", p)
	}
	if p := x.answer(n, receipt("a2", "terminal", "")); len(p) != 1 || p[0] != completed {
		t.Fatalf("end %v", p)
	}
}

// TestASnapshotTheOldDaemonAskedForLeavesTheOpenTurn checks that only this
// run's own snapshot settles the store's open turn.
func TestASnapshotTheOldDaemonAskedForLeavesTheOpenTurn(t *testing.T) {
	x := restarted(t)
	if p := x.push(`{"jsonrpc":"2.0","id":"old","result":` + idle + `}`); len(p) != 0 {
		t.Fatalf("old idle snapshot %v", p)
	}
	if p := x.push(`{"jsonrpc":"2.0","id":"old","result":` + running("a3") + `}`); len(p) != 0 {
		t.Fatalf("old running snapshot %v", p)
	}
	if p := x.push(eventFor("a7", "message.delta", `{"text":"x"}`)); len(p) != 1 || p[0] != (proto.MessageDelta{MessageID: "a7/0", Text: "x"}) {
		t.Fatalf("a7 delta %v", p)
	}
	t.Run("own", func(t *testing.T) {
		y := restarted(t)
		y.push(`{"jsonrpc":"2.0","id":"old","result":` + idle + `}`)
		if p := y.answer(0, idle); len(p) != 1 || p[0] != restartAborted {
			t.Fatalf("own snapshot %v", p)
		}
	})
}

func TestAnApprovalOfAQueuedTurnWaitsForIt(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	x.push(eventFor("a1", "message.start", `{}`))
	x.push(eventFor("a1", "tool.start", `{"tool_id":"t1","name":"terminal","context":"ls"}`))
	x.push(complete("a1", ""))
	x.reconnect(2) // a2 started while away: it waits for a1's receipt
	for _, l := range []string{
		eventFor("a2", "tool.start", `{"tool_id":"t2","name":"terminal","context":"rm x"}`),
		eventFor("a2", "approval.request", `{"kind":"approval","prompt_id":"p2","execution_generation":1,"command":"rm x","choices":["once","deny"]}`),
		eventFor("a2", "error", `{"message":"slow"}`),
	} {
		if p := x.push(l); len(p) != 0 {
			t.Fatalf("a2's event shown in a1: %v", p)
		}
	}
	if w := x.last(); w.Method != "prompt.receipt" || w.Params["admission_id"] != "a1" {
		t.Fatalf("asked %+v", w)
	}
	p := x.answerLast(receipt("a1", "terminal", ""))
	if len(p) != 5 || p[0] != (proto.TurnEnded{Reason: proto.TurnCompleted}) || p[1] != (proto.TurnStarted{}) ||
		p[2].(proto.ToolCall).ID != "t2" || p[4] != (proto.Error{Message: "slow"}) {
		t.Fatalf("a1 receipt %v", p)
	}
	if a := p[3].(proto.ApprovalRequested); a.ID != "p2" || a.ToolCallID != "t2" {
		t.Fatalf("approval %+v", a)
	}
}

// TestTurnsKeepTheirOrderAcrossTwoDrops runs three turns over two drops,
// each snapshot finding any of them running (0: none), and checks that
// every turn the run learns of starts and ends once, in turn, and that the
// setting that waited goes out only when the last one is over.
func TestTurnsKeepTheirOrderAcrossTwoDrops(t *testing.T) {
	for first := 1; first <= 4; first++ {
		for second := first; second <= 4; second++ {
			t.Run(fmt.Sprintf("%d_%d", first, second), func(t *testing.T) {
				x := newTester(t, harness.Spec{Resume: "s1"})
				x.attach("s1", false)
				x.push(eventFor("a1", "message.start", `{}`))
				if err := x.run.SetEffort(context.Background(), proto.EffortLow); err != nil {
					t.Fatal(err)
				}
				var got []proto.Payload
				open := 1 // a1
				note := func(p []proto.Payload) {
					for _, q := range p {
						switch q.(type) {
						case proto.TurnStarted:
							open++
						case proto.TurnEnded:
							open--
						}
						got = append(got, q)
					}
					if open < 0 || open > 1 {
						t.Fatalf("%d turns open after %v", open, got)
					}
					for _, w := range x.io.written() {
						if parseRPC(t, w).Method == "session.mutate" && open > 0 {
							t.Fatalf("a setting was sent with a turn open: %v", got)
						}
					}
				}
				running := func(k int) int {
					if k == 4 {
						return 0
					}
					return k
				}
				note(x.reconnect(running(first)))
				answered := len(x.io.written()) // the second resume; what the first asked is lost
				note(x.reconnect(running(second)))
				// Answer what the run asks, finishing the running turn.
				finished := false
				for {
					w := x.io.written()
					if answered+1 < len(w) {
						answered++
						if r := parseRPC(t, w[answered]); r.Method == "prompt.receipt" {
							note(x.push(fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":%s}`, r.ID, receipt(r.Params["admission_id"].(string), "terminal", ""))))
						}
						continue
					}
					if !finished && second < 4 && open == 1 {
						// The turn Hermes still runs ends now.
						finished = true
						note(x.push(complete(fmt.Sprintf("a%d", second), "")))
						continue
					}
					break
				}
				if open != 0 {
					t.Fatalf("%d turns still open: %v", open, got)
				}
				starts, ends := 0, 0
				for _, q := range got {
					switch q.(type) {
					case proto.TurnStarted:
						starts++
					case proto.TurnEnded:
						ends++
					}
				}
				// a1 plus a new turn per snapshot that names one.
				want := 1
				if first > 1 && first < 4 {
					want++
				}
				if second > first && second < 4 {
					want++
				}
				if starts+1 != want || ends != want { // a1's start came before the first note
					t.Fatalf("%d starts, %d ends, want %d turns: %v", starts, ends, want, got)
				}
				sent := false
				for _, w := range x.io.written() {
					sent = sent || parseRPC(t, w).Method == "session.mutate"
				}
				if !sent {
					t.Fatal("the setting never went out")
				}
			})
		}
	}
}

// TestARefusedAttachFailsTheCommands is Hermes refusing to create or
// resume the session: nothing is sent while the refusal stands, and a new
// connection attaches again.
func TestARefusedAttachFailsTheCommands(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		spec         harness.Spec
	}{
		{"create", "session.create", harness.Spec{}},
		{"resume", "session.resume", harness.Spec{Resume: "s1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newTester(t, tc.spec)
			ctx := context.Background()
			if err := x.run.Send(ctx, "early"); err != nil { // waits for the attach
				t.Fatal(err)
			}
			x.push(`{"hoplet_bridge":"connected"}`)
			if w := x.writes(0); len(w) != 1 || w[0].Method != tc.method {
				t.Fatalf("on connecting %+v", w)
			}
			p := x.refuse(0, "session_not_found", "")
			if len(p) != 2 || p[0] != (proto.Error{Message: "Hermes refused to attach the session: session_not_found"}) ||
				p[1] != (proto.Error{Message: "1 request was not sent: Hermes refused to attach the session"}) {
				t.Fatalf("refusal %v", p)
			}
			for name, call := range map[string]func() error{
				"Send":      func() error { return x.run.Send(ctx, "hi") },
				"Respond":   func() error { return x.run.Respond(ctx, "p1", "once") },
				"Steer":     func() error { return x.run.Steer(ctx, "more") },
				"Interrupt": func() error { return x.run.Interrupt(ctx) },
				"SetMode":   func() error { return x.run.SetMode(ctx, proto.ModeFullAuto) },
				"SetFast":   func() error { return x.run.SetFast(ctx, true) },
				"SetEffort": func() error { return x.run.SetEffort(ctx, proto.EffortLow) },
				"SetModel":  func() error { return x.run.SetModel(ctx, "m", "") },
			} {
				if err := call(); err == nil || !strings.Contains(err.Error(), "refused to attach the session: session_not_found") {
					t.Fatalf("%s: %v", name, err)
				}
			}
			x.run.mu.Lock()
			waiting := len(x.run.waiting)
			x.run.mu.Unlock()
			if len(x.io.written()) != 1 || waiting != 0 {
				t.Fatalf("%d writes, %d waiting", len(x.io.written()), waiting)
			}
			x.push(`{"hoplet_bridge":"connected"}`)
			if w := x.writes(1); len(w) != 1 || w[0].Method != tc.method {
				t.Fatalf("on reconnecting %+v", w)
			}
			x.answer(1, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":1,"execution_generation":1}`)
			n := len(x.io.written())
			if err := x.run.Send(ctx, "hi"); err != nil {
				t.Fatal(err)
			}
			if w := x.writes(n); len(w) != 1 || w[0].Method != "prompt.submit" || w[0].Params["text"] != "hi" {
				t.Fatalf("after attaching %+v", w)
			}
		})
	}
}

func TestAnErrorAnswerToAnEarlierDaemonsRequestIsDropped(t *testing.T) {
	x := newTester(t, harness.Spec{State: harness.State{Native: "s1"}})
	if p := x.push(`{"jsonrpc":"2.0","id":"old","error":{"code":4001,"message":"revision_conflict","data":{"reason":"revision_conflict"}}}`); len(p) != 0 {
		t.Fatalf("orphan error %v", p)
	}
}

// TestAnAnswerDroppedWithARefusedAttachLetsTheApprovalExpire is an answer
// given while the connection was down, dropped when the attach is refused:
// the approval is not taken as answered, so its settlement elsewhere shows.
func TestAnAnswerDroppedWithARefusedAttachLetsTheApprovalExpire(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", true)
	x.push(eventLine("approval.request", `{"kind":"approval","prompt_id":"p1","execution_generation":1,"command":"x","choices":["once","deny"]}`))
	x.push(`{"hoplet_bridge":"disconnected","error":"gone"}`)
	if err := x.run.Respond(context.Background(), "p1", "once"); err != nil { // waits for the attach
		t.Fatal(err)
	}
	x.push(`{"hoplet_bridge":"connected"}`)
	p := x.refuse(len(x.io.written())-1, "session_not_found", "")
	if len(p) != 2 || p[1] != (proto.Error{Message: "1 request was not sent: Hermes refused to attach the session"}) {
		t.Fatalf("refusal %v", p)
	}
	x.push(`{"hoplet_bridge":"connected"}`)
	p = x.answerLast(`{"session_id":"s1","stored_session_id":"s1","running":true,"revision":1,"execution_generation":1,"prompts":[]}`)
	if len(p) != 1 || p[0] != (proto.ApprovalResolved{ID: "p1", Outcome: proto.OutcomeExpired, DecidedBy: "hermes"}) {
		t.Fatalf("after reconnecting %v", p)
	}
}

// The default effort goes to Hermes as null, which restores its own.
func TestDefaultEffortIsNull(t *testing.T) {
	x := newTester(t, harness.Spec{Resume: "s1"})
	x.attach("s1", false)
	n := len(x.io.written())
	if err := x.run.SetEffort(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	w := x.writes(n)
	payload, ok := w[0].Params["payload"].(map[string]any)
	if len(w) != 1 || w[0].Params["operation"] != "reasoning" || !ok || payload["effort"] != nil {
		t.Fatalf("default effort %+v", w)
	}
	if _, has := payload["effort"]; !has {
		t.Fatalf("no effort in %v", payload)
	}
}
