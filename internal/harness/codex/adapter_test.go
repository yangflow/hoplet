package codex

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// started attaches a run and takes it through the handshake.
func started(t *testing.T) *replayer {
	t.Helper()
	p := &replayer{t: t, io: newFakeStdio()}
	p.attach(harness.Spec{WorkDir: "/work", Mode: proto.ModeFullAuto})
	t.Cleanup(func() { _ = p.run.Close() })
	p.out(`{"id":"hoplet-x-1","result":{}}`)
	p.out(`{"id":"hoplet-x-2","result":{"thread":{"id":"t1"},"model":"m"}}`)
	p.golden = nil
	return p
}

func (p *replayer) out(line string) {
	p.t.Helper()
	p.push(harness.Line{Kind: harness.Stdout, Data: []byte(line)})
}

func (p *replayer) lastWrite() string {
	w := p.io.written()
	return string(w[len(w)-1])
}

func TestNativeModes(t *testing.T) {
	tests := []struct {
		mode proto.PermissionMode
		want native
	}{
		{proto.ModeConfirm, native{"untrusted", "workspace-write", "user"}},
		{proto.ModeAutoEdit, native{"on-request", "workspace-write", "user"}},
		{proto.ModeAutoReview, native{"on-request", "workspace-write", "auto_review"}},
		{proto.ModeFullAuto, native{"never", "danger-full-access", "user"}},
	}
	for _, tt := range tests {
		if got, err := nativeMode(tt.mode); err != nil || got != tt.want {
			t.Errorf("%s: got %v, %v", tt.mode, got, err)
		}
	}
	if _, err := nativeMode("nope"); err == nil {
		t.Error("an unknown mode was accepted")
	}
}

func TestUnknownItemsKeepOnlyTheRawLine(t *testing.T) {
	p := started(t)
	line := `{"method":"item/completed","params":{"threadId":"t1","item":{"type":"newThing","id":"x"}}}`
	p.io.lines <- harness.Line{Kind: harness.Stdout, Data: []byte(line), Offset: 100}
	ev, _ := p.next()
	if ev.Payload != nil || string(ev.Raw) != line {
		t.Fatalf("got %+v", ev)
	}
	p.out(`{"method":"brand/new","params":{}}`)
	p.out(`not json`)
	if len(p.golden) != 0 {
		t.Fatalf("got %q", p.golden)
	}
}

func TestOtherThreadsAreNotShown(t *testing.T) {
	p := started(t)
	p.out(`{"method":"item/completed","params":{"threadId":"sub","item":{"type":"agentMessage","id":"a","text":"hi"}}}`)
	if len(p.golden) != 0 {
		t.Fatalf("got %q", p.golden)
	}
}

func TestExitMidTurnAborts(t *testing.T) {
	p := started(t)
	p.out(`{"method":"turn/started","params":{"threadId":"t1","turn":{"id":"u1","status":"inProgress"}}}`)
	p.push(harness.Line{Kind: harness.Exit, Code: 3})
	if got := p.golden[len(p.golden)-1]; !strings.Contains(got, `"reason":"aborted"`) || !strings.Contains(got, "code 3") {
		t.Fatalf("got %q", p.golden)
	}
	if err := p.run.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestRefusedTurnStartEndsTheTurn(t *testing.T) {
	p := started(t)
	if err := p.run.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	p.out(`{"id":"hoplet-x-3","error":{"code":-32600,"message":"no quota"}}`)
	got := strings.Join(p.golden, "\n")
	if !strings.Contains(got, "proto.TurnStarted") || !strings.Contains(got, `"reason":"failed","message":"no quota"`) {
		t.Fatalf("got %s", got)
	}
}

func TestUnshowableRequestsAreRefused(t *testing.T) {
	for name, line := range map[string]string{
		"unknown":  `{"id":7,"method":"mcpServer/elicitation/request","params":{"threadId":"t1"}}`,
		"secret":   `{"id":7,"method":"item/tool/requestUserInput","params":{"questions":[{"id":"k","question":"key?","isSecret":true}]}}`,
		"no item":  `{"id":7,"method":"item/commandExecution/requestApproval","params":{"threadId":"t1"}}`,
		"no input": `{"id":7,"method":"item/tool/requestUserInput","params":{"questions":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := started(t)
			p.out(line)
			if got := p.lastWrite(); !strings.Contains(got, `"id":7,"error"`) {
				t.Fatalf("wrote %s", got)
			}
			if got := strings.Join(p.golden, "\n"); !strings.Contains(got, refusedNotice) {
				t.Fatalf("got %s", got)
			}
		})
	}
}

// approvalEvent is the last approval the replayer saw.
func approvalEvent(t *testing.T, p *replayer) proto.ApprovalRequested {
	t.Helper()
	var a proto.ApprovalRequested
	for _, l := range p.golden {
		if b, ok := strings.CutPrefix(l, "proto.ApprovalRequested "); ok {
			if err := json.Unmarshal([]byte(b), &a); err != nil {
				t.Fatal(err)
			}
		}
	}
	return a
}

func TestApprovalOptionsPassThrough(t *testing.T) {
	amend := `{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["date"]}}`
	allow := `{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"action":"allow","host":"x.dev"}}}`
	deny := `{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"action":"deny","host":"x.dev"}}}`
	p := started(t)
	p.out(`{"id":3,"method":"item/commandExecution/requestApproval","params":{"itemId":"c1","command":"date","reason":"why","availableDecisions":["accept","acceptForSession",` + amend + `,` + allow + `,` + deny + `,"decline","cancel","fromTheFuture"]}}`)
	a := approvalEvent(t, p)
	want := []proto.ApprovalOption{
		{ID: "accept", Label: "Allow once", Scope: proto.ScopeOnce},
		{ID: "acceptForSession", Label: "Allow for this session", Scope: proto.ScopeSession},
		{ID: amend, Label: "Always allow this command", Scope: proto.ScopeAlways},
		{ID: allow, Label: "Always allow this host", Scope: proto.ScopeAlways},
		{ID: deny, Label: "Always deny this host", Scope: proto.ScopeDeny},
		{ID: "decline", Label: "Deny", Scope: proto.ScopeDeny},
		{ID: "cancel", Label: "Deny and stop", Scope: proto.ScopeDeny},
	}
	if !reflect.DeepEqual(a.Options, want) || a.ToolCallID != "c1" || a.Detail != "why" || a.Kind != proto.ApprovalCommand {
		t.Fatalf("got %+v", a)
	}
	if err := p.run.Respond(context.Background(), a.ID, amend); err != nil {
		t.Fatal(err)
	}
	if got := p.lastWrite(); got != `{"id":3,"result":{"decision":`+amend+`}}` {
		t.Fatalf("wrote %s", got)
	}
}

func TestFirstAnswerWins(t *testing.T) {
	ctx := context.Background()
	p := started(t)
	p.out(`{"id":3,"method":"item/fileChange/requestApproval","params":{"itemId":"f1"}}`)
	a := approvalEvent(t, p)
	if a.Kind != proto.ApprovalFileChange || len(a.Options) != 4 {
		t.Fatalf("got %+v", a)
	}
	if err := p.run.Respond(ctx, a.ID, "not-an-option"); err == nil {
		t.Fatal("an unknown option was accepted")
	}
	if err := p.run.Respond(ctx, a.ID, "accept"); err != nil {
		t.Fatal(err)
	}
	wrote := p.lastWrite()
	if err := p.run.Respond(ctx, a.ID, "decline"); err == nil || p.lastWrite() != wrote {
		t.Fatalf("a late answer: %v, wrote %s", err, p.lastWrite())
	}
	// Codex withdrew this one: the answer has nowhere to go.
	p.out(`{"id":4,"method":"item/fileChange/requestApproval","params":{"itemId":"f2"}}`)
	a = approvalEvent(t, p)
	p.out(`{"method":"serverRequest/resolved","params":{"requestId":4}}`)
	if err := p.run.Respond(ctx, a.ID, "accept"); err == nil {
		t.Fatal("an answer to a withdrawn approval was accepted")
	}
	// After a daemon restart the ID alone still reaches Codex.
	p = started(t)
	if err := p.run.Respond(ctx, `9:c1`, "accept"); err != nil || p.lastWrite() != `{"id":9,"result":{"decision":"accept"}}` {
		t.Fatalf("%v, wrote %s", err, p.lastWrite())
	}
	if err := p.run.Respond(ctx, "nonsense", "accept"); err == nil {
		t.Fatal("a malformed approval ID was accepted")
	}
}

func TestPermissionApproval(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct{ option, want string }{
		{grantTurn, `{"id":5,"result":{"permissions":{"network":{"enabled":true}},"scope":"turn"}}`},
		{grantSession, `{"id":5,"result":{"permissions":{"network":{"enabled":true}},"scope":"session"}}`},
		{grantNone, `{"id":5,"result":{"permissions":{},"scope":"turn"}}`},
	} {
		p := started(t)
		p.out(`{"id":5,"method":"item/permissions/requestApproval","params":{"itemId":"p1","permissions":{"network":{"enabled":true}}}}`)
		a := approvalEvent(t, p)
		if err := p.run.Respond(ctx, a.ID, c.option); err != nil {
			t.Fatal(err)
		}
		if got := p.lastWrite(); got != c.want {
			t.Fatalf("wrote %s", got)
		}
		if err := p.run.Respond(ctx, a.ID, c.option); err == nil {
			t.Fatal("a late answer was accepted")
		}
	}
	p := started(t) // the requested profile does not survive a restart
	if err := p.run.Respond(ctx, "5:p1", grantTurn); err == nil {
		t.Fatal("a permission approval from before a restart was accepted")
	}
}

func TestQuestionsAnsweredTogether(t *testing.T) {
	ctx := context.Background()
	p := started(t)
	p.out(`{"id":6,"method":"item/tool/requestUserInput","params":{"itemId":"i","questions":[{"id":"a","question":"A?","options":[{"label":"x"},{"label":"y"}]},{"id":"b","question":"B?"}]}}`)
	got := strings.Join(p.golden, "\n")
	if !strings.Contains(got, `{"id":"6:a","text":"A?","options":["x","y"]}`) || !strings.Contains(got, `{"id":"6:b","text":"B?"`) {
		t.Fatalf("got %s", got)
	}
	before := len(p.io.written())
	if err := p.run.Answer(ctx, "6:a", "x"); err != nil {
		t.Fatal(err)
	}
	if err := p.run.Answer(ctx, "6:a", "y"); err == nil {
		t.Fatal("a late answer was accepted")
	}
	if len(p.io.written()) != before {
		t.Fatal("replied before every question had its answer")
	}
	if err := p.run.Answer(ctx, "6:b", "free text"); err != nil {
		t.Fatal(err)
	}
	if got := p.lastWrite(); got != `{"id":6,"result":{"answers":{"a":{"answers":["x"]},"b":{"answers":["free text"]}}}}` {
		t.Fatalf("wrote %s", got)
	}
	p.out(`{"id":7,"method":"item/tool/requestUserInput","params":{"questions":[{"id":"a","question":"A?"}]}}`)
	p.out(`{"method":"serverRequest/resolved","params":{"requestId":7}}`)
	if err := p.run.Answer(ctx, "7:a", "x"); err == nil {
		t.Fatal("an answer to a withdrawn question was accepted")
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	wrote := func(t *testing.T, p *replayer) map[string]any {
		t.Helper()
		var m struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal([]byte(p.lastWrite()), &m); err != nil || m.Method != methodSettings {
			t.Fatalf("%v: wrote %s", err, p.lastWrite())
		}
		delete(m.Params, "threadId")
		return m.Params
	}
	p := started(t) // started in full_auto
	for _, c := range []struct {
		name string
		set  func() error
		want string
	}{
		{"same sandbox class is kept out", func() error { return p.run.SetMode(ctx, proto.ModeFullAuto) }, `{"approvalPolicy":"never","approvalsReviewer":"user"}`},
		{"leaving full auto", func() error { return p.run.SetMode(ctx, proto.ModeConfirm) }, `{"approvalPolicy":"untrusted","approvalsReviewer":"user","sandboxPolicy":{"type":"workspaceWrite"}}`},
		{"between modes with one sandbox", func() error { return p.run.SetMode(ctx, proto.ModeAutoReview) }, `{"approvalPolicy":"on-request","approvalsReviewer":"auto_review"}`},
		{"auto edit", func() error { return p.run.SetMode(ctx, proto.ModeAutoEdit) }, `{"approvalPolicy":"on-request","approvalsReviewer":"user"}`},
		{"back to full auto", func() error { return p.run.SetMode(ctx, proto.ModeFullAuto) }, `{"approvalPolicy":"never","approvalsReviewer":"user","sandboxPolicy":{"type":"dangerFullAccess"}}`},
		{"effort", func() error { return p.run.SetEffort(ctx, proto.EffortHigh) }, `{"effort":"high"}`},
		{"model", func() error { return p.run.SetModel(ctx, "m2", "") }, `{"model":"m2"}`},
		{"plan on carries the model and effort", func() error { return p.run.SetPlan(ctx, true) }, `{"collaborationMode":{"mode":"plan","settings":{"model":"m2","reasoning_effort":"high","developer_instructions":null}}}`},
		{"a model change in plan reaches it", func() error { return p.run.SetModel(ctx, "m3", "") }, `{"model":"m3","collaborationMode":{"mode":"plan","settings":{"model":"m3","reasoning_effort":"high","developer_instructions":null}}}`},
		{"plan off", func() error { return p.run.SetPlan(ctx, false) }, `{"collaborationMode":{"mode":"default","settings":{"model":"m3","reasoning_effort":"high","developer_instructions":null}}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.set(); err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(c.want), &want); err != nil {
				t.Fatal(err)
			}
			if got := wrote(t, p); !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v", got)
			}
		})
	}
	for name, err := range map[string]error{
		"fast":      p.run.SetFast(ctx, true),
		"ultracode": p.run.SetUltracode(ctx, true),
		"effort":    p.run.SetEffort(ctx, "nope"),
	} {
		if !errors.Is(err, harness.ErrUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := p.run.SetMode(ctx, "nope"); err == nil {
		t.Error("an unknown mode was accepted")
	}
}

func TestRefusedSettingIsShown(t *testing.T) {
	p := started(t)
	if err := p.run.SetEffort(context.Background(), proto.EffortLow); err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(p.lastWrite()), &m); err != nil {
		t.Fatal(err)
	}
	p.out(`{"id":"` + m.ID + `","error":{"code":-32600,"message":"bad effort"}}`)
	if got := strings.Join(p.golden, "\n"); !strings.Contains(got, "bad effort") {
		t.Fatalf("got %s", got)
	}
}

func TestSteerNeedsATurn(t *testing.T) {
	p := started(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.run.Steer(ctx, "more"); err == nil {
		t.Fatal("steered with no turn under way")
	}
}

func TestUsageCountsOnlyWhatIsNew(t *testing.T) {
	p := started(t)
	u := func(total, in, out int) string {
		return `{"method":"thread/tokenUsage/updated","params":{"threadId":"t1","tokenUsage":{"total":{"totalTokens":` + strconv.Itoa(total) + `,"inputTokens":` + strconv.Itoa(in) + `,"outputTokens":` + strconv.Itoa(out) + `},"last":{"totalTokens":5,"inputTokens":4,"outputTokens":1},"modelContextWindow":100}}}`
	}
	p.out(u(10, 8, 2))
	p.out(u(10, 8, 2))
	p.out(u(16, 12, 4))
	want := []string{
		`proto.ContextUsage {"used":5,"window":100}`,
		`proto.Usage {"input_tokens":4,"output_tokens":1}`,
		`proto.ContextUsage {"used":5,"window":100}`,
		`proto.ContextUsage {"used":5,"window":100}`,
		`proto.Usage {"input_tokens":4,"output_tokens":2}`,
	}
	if strings.Join(p.golden, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %q", p.golden)
	}
}

func TestCommandsFailWhenTheThreadCannotOpen(t *testing.T) {
	p := &replayer{t: t, io: newFakeStdio()}
	p.attach(harness.Spec{WorkDir: "/work"})
	t.Cleanup(func() { _ = p.run.Close() })
	p.out(`{"id":"hoplet-x-1","error":{"code":1,"message":"nope"}}`)
	if err := p.run.Send(context.Background(), "hi"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("got %v", err)
	}
}

func TestDetachIsNotAnExit(t *testing.T) {
	io := newFakeStdio()
	r, err := newRun(io, harness.Spec{WorkDir: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	go r.loop()
	close(io.lines)
	select {
	case <-r.finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not end")
	}
	if !errors.Is(r.Err(), harness.ErrDetached) {
		t.Fatalf("got %v", r.Err())
	}
}

func TestUnsupportedSpecs(t *testing.T) {
	h := &Harness{Binary: "/bin/codex"}
	for _, s := range []harness.Spec{{Fast: true}, {Ultracode: true}} {
		if _, err := h.Launch(s); !errors.Is(err, harness.ErrUnsupported) {
			t.Errorf("%+v: %v", s, err)
		}
	}
	l, err := h.Launch(harness.Spec{WorkDir: "/w", Env: []string{"A=1"}})
	if err != nil || strings.Join(l.Command, " ") != "/bin/codex app-server" || l.Dir != "/w" {
		t.Fatalf("got %+v, %v", l, err)
	}
}

func TestParseVersion(t *testing.T) {
	if v, err := parseVersion([]byte("codex-cli 0.167.0\n")); err != nil || v != "0.167.0" {
		t.Fatalf("got %q, %v", v, err)
	}
	if _, err := parseVersion([]byte("something else entirely")); err == nil {
		t.Fatal("accepted a foreign binary")
	}
}
