package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// Kind is the fake harness's kind.
const Kind = "fake"

// Harness drives the fakeharness binary at Binary with the script at Script.
type Harness struct {
	Binary string
	Script string
}

var _ harness.StdioHarness = (*Harness)(nil)

// Kind reports "fake".
func (h *Harness) Kind() string { return Kind }

// Probe reports the fake harness's capabilities.
func (h *Harness) Probe(context.Context) (harness.Info, error) {
	return harness.Info{
		Kind:         Kind,
		Version:      "1",
		Capabilities: proto.Capabilities{Steer: true, PartialStream: true},
	}, nil
}

// Launch returns the command and environment for the binary.
func (h *Harness) Launch(spec harness.Spec) (harness.Launch, error) {
	if h.Binary == "" || h.Script == "" {
		return harness.Launch{}, errors.New("fake: binary and script are required")
	}
	env := append(append([]string(nil), spec.Env...), "HOPLET_FAKE_SCRIPT="+h.Script)
	return harness.Launch{Command: []string{h.Binary}, Env: env}, nil
}

// Attach starts translating io's lines into events.
func (h *Harness) Attach(_ context.Context, _ harness.Spec, io harness.Stdio) (harness.Run, error) {
	r := &run{io: io, events: make(chan harness.Event, 64)}
	go r.loop()
	return r, nil
}

type run struct {
	io     harness.Stdio
	events chan harness.Event

	mu     sync.Mutex
	native string
	err    error
}

func (r *run) loop() {
	defer close(r.events)
	inTurn := false
	for line := range r.io.Lines() {
		switch line.Kind {
		case harness.Stderr:
			r.events <- harness.Event{Raw: line.Data}
		case harness.Exit:
			if inTurn {
				r.events <- harness.Event{Payload: proto.TurnEnded{
					Reason:  proto.TurnAborted,
					Message: fmt.Sprintf("harness exited with code %d", line.Code),
				}}
			}
			return
		case harness.Stdout:
			p, native := translate(line.Data)
			if native != "" {
				r.mu.Lock()
				r.native = native
				r.mu.Unlock()
			}
			switch p.(type) {
			case proto.TurnStarted:
				inTurn = true
			case proto.TurnEnded:
				inTurn = false
			}
			r.events <- harness.Event{Payload: p, Raw: line.Data}
		}
	}
	r.mu.Lock()
	r.err = errors.New("fake: stdio closed before the harness exited")
	r.mu.Unlock()
}

// translate maps one stdout line to a payload; the second result is the
// native session ID when the line announces it.
func translate(line []byte) (proto.Payload, string) {
	var ev Ev
	if err := json.Unmarshal(line, &ev); err != nil {
		return proto.Error{Message: "unparseable harness line"}, ""
	}
	switch ev.Ev {
	case "session":
		return nil, ev.ID
	case "turn_started":
		return proto.TurnStarted{}, ""
	case "turn_ended":
		return proto.TurnEnded{Reason: proto.TurnEndReason(ev.Reason)}, ""
	case "delta":
		return proto.MessageDelta{MessageID: ev.ID, Text: ev.Text}, ""
	case "message":
		return proto.Message{ID: ev.ID, Role: proto.RoleAssistant, Text: ev.Text}, ""
	case "thinking":
		return proto.Thinking{Text: ev.Text}, ""
	case "tool":
		return proto.ToolCall{ID: ev.ID, Kind: proto.ToolKind(ev.Kind), Name: ev.Name, Summary: ev.Summary, Status: proto.ToolStatus(ev.Status)}, ""
	case "approval":
		opts := make([]proto.ApprovalOption, len(ev.Options))
		for i, o := range ev.Options {
			opts[i] = proto.ApprovalOption{ID: o.ID, Label: o.Label, Scope: proto.ApprovalScope(o.Scope)}
		}
		return proto.ApprovalRequested{ID: ev.ID, Kind: proto.ApprovalKind(ev.Kind), Title: ev.Title, Detail: ev.Text, Options: opts}, ""
	case "question":
		return proto.Question{ID: ev.ID, Text: ev.Text}, ""
	case "error":
		return proto.Error{Message: ev.Text}, ""
	}
	return proto.Error{Message: "unknown harness event " + ev.Ev}, ""
}

func (r *run) send(op Op) error {
	b, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("fake: encode op: %w", err)
	}
	return r.io.WriteLine(b)
}

func (r *run) Send(_ context.Context, text string) error {
	return r.send(Op{Op: "prompt", Text: text})
}

func (r *run) Steer(_ context.Context, text string) error {
	return r.send(Op{Op: "steer", Text: text})
}

func (r *run) Interrupt(context.Context) error { return r.send(Op{Op: "interrupt"}) }

func (r *run) Respond(_ context.Context, approvalID, optionID string) error {
	return r.send(Op{Op: "respond", ID: approvalID, Option: optionID})
}

func (r *run) Answer(_ context.Context, questionID, answer string) error {
	return r.send(Op{Op: "answer", ID: questionID, Text: answer})
}

func (r *run) Events() <-chan harness.Event { return r.events }

func (r *run) NativeSession() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.native
}

func (r *run) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *run) Close() error { return r.io.Close() }
