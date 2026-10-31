package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// Kind is the fake harness's kind.
const Kind = "fake"

// Harness drives the fakeharness binary at Binary with the script at Script.
// Capabilities overrides what the fake reports and supports; nil means
// Steer, PartialStream, Resume, SetMode and SetModel.
type Harness struct {
	Binary       string
	Script       string
	Capabilities *proto.Capabilities
}

var _ harness.StdioHarness = (*Harness)(nil)

func (h *Harness) caps() proto.Capabilities {
	if h.Capabilities != nil {
		return *h.Capabilities
	}
	return proto.Capabilities{Steer: true, PartialStream: true, Resume: true, SetMode: true, SetModel: true}
}

// Kind reports "fake".
func (h *Harness) Kind() string { return Kind }

// Probe reports the fake harness's capabilities.
func (h *Harness) Probe(context.Context) (harness.Info, error) {
	return harness.Info{Version: "1", Capabilities: h.caps(), Supported: true}, nil
}

// Launch returns the command, environment and directory for the binary.
func (h *Harness) Launch(spec harness.Spec) (harness.Launch, error) {
	if h.Binary == "" || h.Script == "" {
		return harness.Launch{}, errors.New("fake: binary and script are required")
	}
	if spec.Resume != "" && !h.caps().Resume {
		return harness.Launch{}, harness.ErrUnsupported
	}
	env := append(append([]string(nil), spec.Env...), "HOPLET_FAKE_SCRIPT="+h.Script)
	if spec.Resume != "" {
		env = append(env, "HOPLET_FAKE_RESUME="+spec.Resume)
	}
	return harness.Launch{Command: []string{h.Binary}, Env: env, Dir: spec.WorkDir}, nil
}

// Attach starts translating io's lines into events.
func (h *Harness) Attach(_ context.Context, spec harness.Spec, io harness.Stdio) (harness.Run, error) {
	if spec.Resume != "" && !h.caps().Resume {
		return nil, harness.ErrUnsupported
	}
	r := &run{
		io:       io,
		caps:     h.caps(),
		events:   make(chan harness.Event, 64),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
		inTurn:   spec.State.InTurn,
		native:   spec.State.Native,
	}
	go r.loop()
	return r, nil
}

type run struct {
	io       harness.Stdio
	caps     proto.Capabilities
	events   chan harness.Event
	done     chan struct{} // closed by Close
	finished chan struct{} // closed when loop has returned
	once     sync.Once

	inTurn bool // loop only

	mu     sync.Mutex
	native string
	err    error
}

// emit delivers ev unless Close has been called.
func (r *run) emit(ev harness.Event) bool {
	select {
	case r.events <- ev:
		return true
	case <-r.done:
		return false
	}
}

func (r *run) loop() {
	defer close(r.finished)
	defer close(r.events)
	lines := r.io.Lines()
	for {
		var line harness.Line
		select {
		case l, ok := <-lines:
			if !ok {
				select {
				case <-r.done:
					return // the caller closed the run; that is not a detach
				default:
				}
				r.mu.Lock()
				r.err = harness.ErrDetached
				r.mu.Unlock()
				return
			}
			line = l
		case <-r.done:
			return
		}
		ev, final := r.handle(line)
		if !r.emit(ev) || final {
			return
		}
	}
}

// handle turns one line into exactly one event; final reports the harness
// has exited.
func (r *run) handle(line harness.Line) (ev harness.Event, final bool) {
	ev = harness.Event{Raw: line.Data, Offset: line.Offset}
	switch line.Kind {
	case harness.Stderr:
		ev.Stderr = true
	case harness.Stdin:
	case harness.StdinError:
		ev.Payload = proto.Error{Message: "harness stdin write failed: " + string(line.Data)}
		ev.StdinError = true
	case harness.Exit:
		ev.Raw = nil
		ev.Exit = &harness.ExitStatus{Code: line.Code, Signal: line.Signal}
		if r.inTurn {
			r.inTurn = false
			ev.Payload = proto.TurnEnded{Reason: proto.TurnAborted, Message: exitMessage(line)}
		}
		return ev, true
	case harness.Stdout:
		p, native := translate(line.Data)
		if native != "" {
			ev.Native = native
			r.mu.Lock()
			r.native = native
			r.mu.Unlock()
		}
		switch p.(type) {
		case proto.TurnStarted:
			r.inTurn = true
		case proto.TurnEnded:
			r.inTurn = false
		}
		ev.Payload = p
	}
	return ev, false
}

func exitMessage(l harness.Line) string {
	if l.Signal != "" {
		return "harness killed by SIG" + strings.TrimPrefix(l.Signal, "SIG")
	}
	return fmt.Sprintf("harness exited with code %d", l.Code)
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
		switch reason := proto.TurnEndReason(ev.Reason); reason {
		case proto.TurnCompleted, proto.TurnInterrupted, proto.TurnFailed, proto.TurnAborted:
			return proto.TurnEnded{Reason: reason, Message: ev.Text}, ""
		}
		return proto.TurnEnded{Reason: proto.TurnFailed, Message: "unknown turn end reason " + ev.Reason}, ""
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
	case "usage":
		return proto.Usage{InputTokens: ev.In, OutputTokens: ev.Out}, ""
	case "file_change":
		return proto.FileChange{Path: ev.Name, Op: proto.FileOp(ev.Kind)}, ""
	case "error":
		return proto.Error{Message: ev.Text}, ""
	}
	return proto.Error{Message: "unknown harness event " + ev.Ev}, ""
}

func (r *run) send(ctx context.Context, op Op) error {
	b, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("fake: encode op: %w", err)
	}
	return r.io.WriteLine(ctx, b)
}

func (r *run) Send(ctx context.Context, text string) error {
	return r.send(ctx, Op{Op: "prompt", Text: text})
}

func (r *run) Steer(ctx context.Context, text string) error {
	if !r.caps.Steer {
		return harness.ErrUnsupported
	}
	return r.send(ctx, Op{Op: "steer", Text: text})
}

func (r *run) Interrupt(ctx context.Context) error { return r.send(ctx, Op{Op: "interrupt"}) }

func (r *run) Respond(ctx context.Context, approvalID, optionID string) error {
	return r.send(ctx, Op{Op: "respond", ID: approvalID, Option: optionID})
}

func (r *run) Answer(ctx context.Context, questionID, answer string) error {
	return r.send(ctx, Op{Op: "answer", ID: questionID, Text: answer})
}

func (r *run) SetMode(ctx context.Context, mode proto.PermissionMode) error {
	if !r.caps.SetMode {
		return harness.ErrUnsupported
	}
	return r.send(ctx, Op{Op: "set_mode", Text: string(mode)})
}

func (r *run) SetModel(ctx context.Context, model string) error {
	if !r.caps.SetModel {
		return harness.ErrUnsupported
	}
	return r.send(ctx, Op{Op: "set_model", Text: model})
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

// Close stops translating, releases the transport and waits for the loop.
func (r *run) Close() error {
	var err error
	r.once.Do(func() {
		close(r.done)
		err = r.io.Close()
	})
	<-r.finished
	return err
}
