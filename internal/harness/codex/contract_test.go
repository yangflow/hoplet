package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fakeStdio feeds lines to a run and records what it writes.
type fakeStdio struct {
	lines  chan harness.Line
	mu     sync.Mutex
	writes [][]byte
}

func newFakeStdio() *fakeStdio { return &fakeStdio{lines: make(chan harness.Line)} }

func (f *fakeStdio) WriteLine(_ context.Context, line []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte(nil), line...))
	return nil
}

func (f *fakeStdio) written() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.writes...)
}

func (f *fakeStdio) Lines() <-chan harness.Line { return f.lines }
func (f *fakeStdio) Signal(string) error        { return nil }
func (f *fakeStdio) Close() error               { return nil }

// replayer drives runs through a recording.
type replayer struct {
	t                  *testing.T
	io                 *fakeStdio
	run                *run
	offset             int64
	seen               int    // writes already compared
	approval, question string // the last asked
	golden             []string
}

func (p *replayer) attach(spec harness.Spec) {
	p.t.Helper()
	r, err := newRun(p.io, spec)
	if err != nil {
		p.t.Fatal(err)
	}
	r.prefix = "hoplet-x"
	p.run = r
	go r.loop()
	if err := r.start(context.Background()); err != nil {
		p.t.Fatal(err)
	}
}

// next waits for one event.
func (p *replayer) next() (harness.Event, bool) {
	p.t.Helper()
	select {
	case ev, ok := <-p.run.Events():
		return ev, ok
	case <-time.After(10 * time.Second):
		p.t.Fatalf("no event after line at offset %d", p.offset)
		return harness.Event{}, false
	}
}

// push feeds one line and waits until the run has handled it: the position
// line after it is handled only once everything before it is.
func (p *replayer) push(l harness.Line) {
	p.t.Helper()
	p.offset += 2
	l.Offset = p.offset
	p.io.lines <- l
	if l.Kind == harness.Exit {
		for {
			ev, ok := p.next()
			if !ok {
				return
			}
			p.record(ev)
		}
	}
	p.io.lines <- harness.Line{Kind: harness.Position, Offset: p.offset + 1}
	for {
		ev, ok := p.next()
		if !ok {
			p.t.Fatal("events closed early")
		}
		if ev.Offset == p.offset+1 {
			return
		}
		p.record(ev)
	}
}

func (p *replayer) record(ev harness.Event) {
	if ev.Payload == nil && ev.Native == "" {
		return
	}
	switch q := ev.Payload.(type) {
	case proto.ApprovalRequested:
		p.approval = q.ID
	case proto.Question:
		p.question = q.ID
	}
	line := ""
	if ev.Native != "" {
		line = "native " + ev.Native + " "
	}
	if ev.Payload != nil {
		b, err := json.Marshal(ev.Payload)
		if err != nil {
			p.t.Fatal(err)
		}
		line += fmt.Sprintf("%T %s", ev.Payload, b)
	}
	if len(ev.Output) > 0 {
		line += fmt.Sprintf(" output=%q", ev.Output)
	}
	p.golden = append(p.golden, strings.TrimSpace(line))
}

// setting issues the Hoplet command a recorded settings update stands for.
func (p *replayer) setting(approval, reviewer, model, effort string, collab *collaborationMode) error {
	ctx := context.Background()
	switch {
	case collab != nil:
		return p.run.SetPlan(ctx, collab.Mode == collabPlan)
	case model != "":
		return p.run.SetModel(ctx, model, "")
	case effort != "":
		return p.run.SetEffort(ctx, proto.Effort(effort))
	}
	for _, mode := range caps().Modes {
		if m, err := nativeMode(mode); err == nil && m.approval == approval && m.reviewer == reviewer {
			return p.run.SetMode(ctx, mode)
		}
	}
	return fmt.Errorf("no mode has approval %q and reviewer %q", approval, reviewer)
}

// input issues the Hoplet command a recorded input line stands for, then
// checks the run wrote exactly that line.
func (p *replayer) input(in string) {
	p.t.Helper()
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Input             []textInput        `json:"input"`
			ApprovalPolicy    string             `json:"approvalPolicy"`
			ApprovalsReviewer string             `json:"approvalsReviewer"`
			SandboxPolicy     *sandboxPolicy     `json:"sandboxPolicy"`
			Model             string             `json:"model"`
			Effort            string             `json:"effort"`
			CollaborationMode *collaborationMode `json:"collaborationMode"`
		} `json:"params"`
		Result struct {
			Decision json.RawMessage            `json:"decision"`
			Answers  map[string]userInputAnswer `json:"answers"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		p.t.Fatal(err)
	}
	ctx := context.Background()
	var err error
	switch m.Method {
	case "turn/start":
		err = p.run.Send(ctx, m.Params.Input[0].Text)
	case "turn/interrupt":
		err = p.run.Interrupt(ctx)
	case "turn/steer":
		err = p.run.Steer(ctx, m.Params.Input[0].Text)
	case methodSettings:
		if p.seen >= len(p.io.written()) { // not written by the thread's opening
			err = p.setting(m.Params.ApprovalPolicy, m.Params.ApprovalsReviewer, m.Params.Model, m.Params.Effort, m.Params.CollaborationMode)
		}
	case "":
		switch {
		case len(m.Result.Decision) > 0:
			var option string
			if json.Unmarshal(m.Result.Decision, &option) != nil {
				option = string(m.Result.Decision)
			}
			err = p.run.Respond(ctx, p.approval, option)
		case m.Result.Answers != nil:
			for _, a := range m.Result.Answers {
				err = p.run.Answer(ctx, p.question, a.Answers[0])
			}
		}
	}
	if err != nil {
		p.t.Fatalf("command for %s: %v", in, err)
	}
	w := p.io.written()
	if p.seen >= len(w) {
		p.t.Fatalf("the run did not write\n  %s", in)
	}
	var got, want any
	if json.Unmarshal(w[p.seen], &got) != nil || json.Unmarshal([]byte(in), &want) != nil || !reflect.DeepEqual(got, want) {
		p.t.Fatalf("for recorded input\n  %s\nthe run wrote\n  %s", in, w[p.seen])
	}
	p.seen++
	p.golden = append(p.golden, "write "+in)
}

func TestContract(t *testing.T) {
	cases := []struct {
		name   string
		mode   proto.PermissionMode
		plan   bool
		resume string
		soul   string
		effort proto.Effort
	}{
		{name: "basic"}, {name: "interrupt"}, {name: "reattach"},
		{name: "resume", resume: "01a1bb41-4ef7-7fb2-bf42-6d83400a667e"},
		{name: "approve", mode: proto.ModeConfirm},
		{name: "deny", mode: proto.ModeConfirm},
		{name: "filechange", mode: proto.ModeConfirm},
		{name: "steer"},
		{name: "plan", plan: true},
		{name: "settings"},
		{name: "persona", mode: proto.ModeConfirm, soul: "The codeword is heron. When asked for the codeword, answer with it.", effort: proto.EffortLow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mode := c.mode
			if mode == "" {
				mode = proto.ModeFullAuto
			}
			spec := harness.Spec{WorkDir: "/work", Mode: mode, Plan: c.plan, Resume: c.resume, Persona: c.soul, Effort: c.effort}
			path := filepath.Join("testdata", PinnedVersion, c.name+".jsonl")
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			p := &replayer{t: t, io: newFakeStdio()}
			p.attach(spec)
			sc := bufio.NewScanner(f)
			sc.Buffer(nil, 4<<20)
			for sc.Scan() {
				var e Entry
				if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
					t.Fatal(err)
				}
				switch e.Dir {
				case "in":
					p.input(e.Line)
				case "out":
					p.push(harness.Line{Kind: harness.Stdout, Data: []byte(e.Line)})
				case "exit":
					p.push(harness.Line{Kind: harness.Exit, Code: -1})
				case "reattach":
					native := p.run.NativeSession()
					if err := p.run.Close(); err != nil {
						t.Fatal(err)
					}
					p.attach(harness.Spec{WorkDir: "/work", Mode: proto.ModeFullAuto, State: harness.State{InTurn: true, Native: native}})
				}
			}
			if w := p.io.written(); p.seen != len(w) {
				t.Fatalf("the run wrote lines the recording does not have: %q", w[p.seen:])
			}
			got := strings.Join(p.golden, "\n") + "\n"
			golden := strings.TrimSuffix(path, ".jsonl") + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("events differ from %s:\n%s", golden, got)
			}
		})
	}
}
