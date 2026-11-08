package codex

import (
	"context"
	"errors"
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

func TestServerRequestsAreRefused(t *testing.T) {
	p := started(t)
	p.out(`{"id":7,"method":"item/commandExecution/requestApproval","params":{"threadId":"t1"}}`)
	if got := p.lastWrite(); got != `{"id":7,"result":{"decision":"decline"}}` {
		t.Fatalf("wrote %s", got)
	}
	p.out(`{"id":8,"method":"item/tool/requestUserInput","params":{"threadId":"t1"}}`)
	if got := p.lastWrite(); !strings.Contains(got, `"id":8,"error"`) {
		t.Fatalf("wrote %s", got)
	}
	if len(p.golden) != 2 || !strings.Contains(p.golden[0], "proto.Error") {
		t.Fatalf("got %q", p.golden)
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
	for _, s := range []harness.Spec{{Plan: true}, {Fast: true}, {Ultracode: true}} {
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
