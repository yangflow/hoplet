package main

import (
	"bytes"
	"flag"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func TestParseAcceptsFlagsAroundArguments(t *testing.T) {
	for name, tc := range map[string]struct {
		args     []string
		min, max int
		pos      []string
		json     bool
		code     int
		ok       bool
	}{
		"flags first":    {[]string{"--json", "id", "hello"}, 2, -1, []string{"id", "hello"}, true, 0, true},
		"flags after":    {[]string{"id", "hello", "--json"}, 2, -1, []string{"id", "hello"}, true, 0, true},
		"dash dash":      {[]string{"id", "--", "--json", "-x"}, 2, -1, []string{"id", "--json", "-x"}, false, 0, true},
		"too few":        {[]string{"id"}, 2, -1, nil, false, 2, false},
		"too many":       {[]string{"a", "b"}, 1, 1, nil, false, 2, false},
		"unknown flag":   {[]string{"--nope"}, 0, 0, nil, false, 2, false},
		"help":           {[]string{"--help"}, 0, 0, nil, false, 0, false},
		"no positionals": {nil, 0, 0, nil, false, 0, true},
	} {
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		asJSON := fs.Bool("json", false, "")
		code, ok := parse(fs, tc.args, tc.min, tc.max)
		if code != tc.code || ok != tc.ok {
			t.Errorf("%s: (%d, %v), want (%d, %v)", name, code, ok, tc.code, tc.ok)
			continue
		}
		if ok && (!slices.Equal(fs.Args(), tc.pos) || *asJSON != tc.json) {
			t.Errorf("%s: args %q json %v", name, fs.Args(), *asJSON)
		}
	}
}

func event(t *testing.T, turn uint32, p proto.Payload) proto.Event {
	t.Helper()
	ev, err := proto.NewEvent("s", 1, turn, time.Now(), p)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestRender(t *testing.T) {
	for want, p := range map[string]proto.Payload{
		"── turn 2 ──":             proto.TurnStarted{},
		"── turn 2 interrupted ──": proto.TurnEnded{Reason: proto.TurnInterrupted},
		"> fix it\n  please":       proto.Message{ID: "m", Role: proto.RoleUser, Text: "fix it\nplease"},
		"done":                     proto.Message{ID: "m", Role: proto.RoleAssistant, Text: "done"},
		"(thinking) first …":       proto.Thinking{Text: "first\nsecond"},
		"· bash go test [running]": proto.ToolCall{ID: "t", Kind: proto.ToolExec, Name: "bash", Summary: "go test", Status: proto.ToolRunning},
		"~ modified main.go":       proto.FileChange{Path: "main.go", Op: proto.FileModified},
		"! approval a1: Run go test\n  options: once, no": proto.ApprovalRequested{ID: "a1", Kind: proto.ApprovalCommand, Title: "Run go test",
			Options: []proto.ApprovalOption{{ID: "once", Label: "Allow once"}, {ID: "no", Label: "Deny"}}},
		"✓ approval a1 allowed (once) by cli@mac": proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "once", DecidedBy: "cli@mac"},
		"? question q1: Which?\n  options: a | b": proto.Question{ID: "q1", Text: "Which?", Options: []string{"a", "b"}},
		"error: boom": proto.Error{Message: "boom"},
		"":            proto.MessageDelta{MessageID: "m", Text: "par"},
	} {
		if got := render(event(t, 2, p)); got != want {
			t.Errorf("render %T: %q, want %q", p, got, want)
		}
	}
}

func TestReached(t *testing.T) {
	idle := proto.Session{Status: proto.StatusIdle}
	now := time.Now()
	for name, tc := range map[string]struct {
		st                  proto.SessionState
		p                   progress
		idle, blocked, done bool
	}{
		"idle":            {proto.SessionState{Session: idle}, progress{1, 1}, true, false, true},
		"message pending": {proto.SessionState{Session: idle}, progress{2, 1}, false, false, false},
		"queued":          {proto.SessionState{Session: idle, Queue: []proto.QueuedMessage{{Key: "k"}}}, progress{1, 1}, false, false, false},
		"failed head":     {proto.SessionState{Session: idle, Queue: []proto.QueuedMessage{{Key: "k", Error: "x"}}}, progress{1, 1}, false, true, true},
		"running":         {proto.SessionState{Session: proto.Session{Status: proto.StatusRunning}}, progress{1, 0}, false, false, false},
		"waiting":         {proto.SessionState{Session: proto.Session{Status: proto.StatusWaiting}}, progress{1, 0}, false, true, true},
		"errored":         {proto.SessionState{Session: proto.Session{Status: proto.StatusErrored}}, progress{1, 1}, false, false, true},
		"archived":        {proto.SessionState{Session: proto.Session{Status: proto.StatusRunning, ArchivedAt: &now}}, progress{1, 0}, false, false, true},
	} {
		got := [3]bool{reached(waitIdle, tc.st, tc.p), reached(waitBlocked, tc.st, tc.p), reached(waitDone, tc.st, tc.p)}
		if got != [3]bool{tc.idle, tc.blocked, tc.done} {
			t.Errorf("%s: idle/blocked/done %v", name, got)
		}
	}
}

func TestProgressTracksUserMessages(t *testing.T) {
	var p progress
	p.add([]proto.Event{
		event(t, 1, proto.Message{ID: "u", Role: proto.RoleUser, Text: "a"}),
		event(t, 1, proto.Message{ID: "x", Role: proto.RoleAssistant, Text: "b"}),
		event(t, 1, proto.TurnEnded{Reason: proto.TurnCompleted}),
		event(t, 2, proto.Message{ID: "u2", Role: proto.RoleUser, Text: "c"}),
	})
	if p != (progress{messageTurn: 2, endedTurn: 1}) {
		t.Fatalf("progress %+v", p)
	}
}

func TestDescribeEnv(t *testing.T) {
	got := describeEnv(proto.EnvInfo{Shell: "/bin/zsh", Vars: 40, Withheld: []string{"GITHUB_TOKEN"}, Proxy: "http://127.0.0.1:7890", ProxySource: "system"})
	if got != "/bin/zsh: 40 variables; withheld: GITHUB_TOKEN; proxy http://127.0.0.1:7890 (system)" {
		t.Fatalf("%q", got)
	}
	if got := describeEnv(proto.EnvInfo{Shell: "/bin/zsh", Vars: 3}); !strings.HasSuffix(got, "; no proxy") {
		t.Fatalf("%q", got)
	}
}

func TestSessionUsage(t *testing.T) {
	for args, code := range map[string]int{"": 2, "help": 0, "frob": 2, "send onlyid": 2, "new": 2, "wait id --for later": 2} {
		var out, errb bytes.Buffer
		argv := append([]string{"session"}, strings.Fields(args)...)
		if got := run(argv, &out, &errb); got != code {
			t.Errorf("session %s: exit %d, want %d (stderr %q)", args, got, code, errb.String())
		}
	}
}
