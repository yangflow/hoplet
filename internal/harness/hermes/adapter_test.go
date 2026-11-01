package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-x.run.Events():
			if ev.Payload != nil {
				out = append(out, ev.Payload)
			}
			if ev.Offset == x.offset && ev.Raw == nil && ev.Payload == nil {
				return out
			}
		case <-timeout:
			x.t.Fatalf("no events for %s", line)
		}
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

// attach connects the run to session native.
func (x *tester) attach(native string, running bool) {
	x.t.Helper()
	n := len(x.io.written())
	x.push(`{"hoplet_bridge":"connected"}`)
	x.answer(n, fmt.Sprintf(`{"session_id":%q,"stored_session_id":%q,"running":%v,"revision":1,"execution_generation":1,"pending":[],"prompts":[]}`, native, native, running))
}

func eventLine(typ, payload string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"event","params":{"type":%q,"session_id":"s1","execution_generation":1,"admission_id":"a1","payload":%s}}`, typ, payload)
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
	payloads := x.answer(0, `{"session_id":"s1","stored_session_id":"s1","running":false,"revision":0,"execution_generation":0}`)
	_ = payloads
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
	n := len(x.io.written())
	if err := x.run.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
	stale := func(i int) []proto.Payload {
		return x.push(fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"error":{"code":4001,"message":"stale_generation","data":{"reason":"stale_generation","execution_generation":5}}}`, x.writes(i)[0].ID))
	}
	if p := stale(n); len(p) != 0 {
		t.Fatalf("first refusal %v", p)
	}
	if w := x.writes(n + 1); len(w) != 1 || w[0].Params["execution_generation"] != float64(5) {
		t.Fatalf("retry %+v", w)
	}
	if p := stale(n + 1); len(p) != 1 || !strings.Contains(p[0].(proto.Error).Message, "stale_generation") {
		t.Fatalf("second refusal %v", p)
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
	if err := x.run.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if w := x.writes(0); len(w) != 1 || w[0].Method != "prompt.submit" {
		t.Fatalf("after a daemon restart %+v", w)
	}
}
