package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/scrub"
	"github.com/yangflow/hoplet/proto"
)

// Step is one step of a recording script.
//
//	{"do":"send","text":"..."}        send a user message
//	{"do":"wait","for":"turn_ended"}  wait for turn_started, tool_running,
//	                                  approval, question or turn_ended; "timeout" is in seconds,
//	                                  default 120
//	{"do":"interrupt"}                interrupt the turn
//	{"do":"steer","text":"..."}       add to the turn
//	{"do":"respond","text":"accept"}  answer the last approval waited for
//	{"do":"answer","text":"..."}      answer the last question waited for
//	{"do":"mode","text":"auto_edit"}  set the permission mode
//	{"do":"plan","text":"on"}         turn plan mode on, or off
//	{"do":"model","text":"..."}       set the model
//	{"do":"effort","text":"low"}      set the effort
//	{"do":"reattach"}                 connect anew to the running server, as
//	                                  after a daemon restart
type Step struct {
	Do      string `json:"do"`
	Text    string `json:"text,omitempty"`
	For     string `json:"for,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// Entry is one recorded line: "in" (written to Codex), "out" (its stdout),
// "exit" (its exit code) or "reattach" (where the recording connected anew).
type Entry struct {
	Dir  string `json:"dir"`
	Line string `json:"line"`
}

// RecordConfig configures a recording.
type RecordConfig struct {
	Binary string
	Dir    string // working directory
	Env    []string
	Spec   harness.Spec // WorkDir and Env are filled in
	Steps  []Step
}

// procStdio runs codex directly, writing every line to the recording.
type procStdio struct {
	stdin io.WriteCloser
	lines chan harness.Line

	mu  sync.Mutex
	enc *json.Encoder
	err error
}

func (p *procStdio) record(dir, line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.enc.Encode(Entry{Dir: dir, Line: line}); err != nil && p.err == nil {
		p.err = err
	}
}

// WriteLine writes one line to codex and records it.
func (p *procStdio) WriteLine(_ context.Context, line []byte) error {
	p.record("in", string(line))
	_, err := p.stdin.Write(append(append([]byte(nil), line...), '\n'))
	return err
}

// Lines delivers codex's stdout.
func (p *procStdio) Lines() <-chan harness.Line { return p.lines }

// Signal is not used by recordings.
func (p *procStdio) Signal(string) error { return nil }

// Close keeps the process running, so a recording can connect anew.
func (p *procStdio) Close() error { return nil }

// startProcess runs l and connects it to a procStdio that writes every line
// to out. stop closes stdin, kills the process and records its exit.
func startProcess(ctx context.Context, l harness.Launch, out io.Writer) (_ *procStdio, stop func() error, _ error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := launchCommand(ctx, l)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("codex: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("codex: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("codex: %w", err)
	}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	p := &procStdio{stdin: stdin, lines: make(chan harness.Line), enc: enc}
	go func() {
		defer close(p.lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(nil, harness.MaxLine)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			p.record("out", string(line))
			select {
			case p.lines <- harness.Line{Kind: harness.Stdout, Data: line}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return p, func() error {
		closeErr := stdin.Close()
		cancel()
		waitErr := cmd.Wait()
		code := 0
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			code, waitErr = ee.ExitCode(), nil
		}
		if ctx.Err() != nil { // killed by the cancel above: the normal way out
			waitErr = nil
		}
		p.record("exit", strconv.Itoa(code))
		return errors.Join(closeErr, waitErr, p.err)
	}, nil
}

// Record runs Codex through this adapter, plays the steps and writes every
// line in both directions to out. The recording holds what Codex printed,
// personal paths included: Sanitize it before committing it as a fixture.
func Record(ctx context.Context, cfg RecordConfig, out io.Writer) error {
	spec := cfg.Spec
	spec.WorkDir, spec.Env = cfg.Dir, cfg.Env
	if len(spec.MCP) > 0 && spec.Dir == "" {
		dir, err := os.MkdirTemp("", "hoplet-record-")
		if err != nil {
			return fmt.Errorf("codex: %w", err)
		}
		defer os.RemoveAll(dir)
		spec.Dir = dir
	}
	l, err := (&Harness{Binary: cfg.Binary}).Launch(spec)
	if err != nil {
		return err
	}
	for name, b := range l.Files {
		if err := os.WriteFile(filepath.Join(spec.Dir, name), b, 0o600); err != nil {
			return fmt.Errorf("codex: %w", err)
		}
	}
	p, stop, err := startProcess(ctx, l, out)
	if err != nil {
		return err
	}
	playErr := play(ctx, p, spec, cfg.Steps)
	return errors.Join(playErr, stop())
}

// wanted reports whether ev is what a wait step waits for.
func wanted(ev harness.Event, what string) bool {
	switch p := ev.Payload.(type) {
	case proto.TurnStarted:
		return what == "turn_started"
	case proto.TurnEnded:
		return what == "turn_ended"
	case proto.ToolCall:
		return what == "tool_running" && p.Status == proto.ToolRunning
	case proto.ApprovalRequested:
		return what == "approval"
	case proto.Question:
		return what == "question"
	}
	return false
}

func play(ctx context.Context, p *procStdio, spec harness.Spec, steps []Step) (err error) {
	r, err := newRun(p, spec)
	if err != nil {
		return err
	}
	go r.loop()
	defer func() { err = errors.Join(err, r.Close()) }()
	if err := r.start(ctx); err != nil {
		return err
	}
	inTurn := false
	var last harness.Event // what the last wait stopped at
	for _, s := range steps {
		switch s.Do {
		case "send":
			err = r.Send(ctx, s.Text)
			inTurn = true
		case "interrupt":
			err = r.Interrupt(ctx)
		case "steer":
			err = r.Steer(ctx, s.Text)
		case "respond":
			a, _ := last.Payload.(proto.ApprovalRequested)
			err = r.Respond(ctx, a.ID, s.Text)
		case "answer":
			q, _ := last.Payload.(proto.Question)
			err = r.Answer(ctx, q.ID, s.Text)
		case "mode":
			err = r.SetMode(ctx, proto.PermissionMode(s.Text))
		case "plan":
			err = r.SetPlan(ctx, s.Text == "on")
		case "model":
			err = r.SetModel(ctx, s.Text, "")
		case "effort":
			err = r.SetEffort(ctx, proto.Effort(s.Text))
		case "reattach":
			native := r.NativeSession()
			if err := r.Close(); err != nil {
				return err
			}
			p.record("reattach", "")
			next := spec
			next.Resume, next.State = "", harness.State{InTurn: inTurn, Native: native}
			if r, err = newRun(p, next); err != nil {
				return err
			}
			go r.loop()
			err = r.start(ctx)
		case "wait":
			last, err = waitFor(ctx, r, s)
			if s.For == "turn_ended" {
				inTurn = false
			}
		default:
			err = fmt.Errorf("codex: unknown step %q", s.Do)
		}
		if err != nil {
			return fmt.Errorf("codex: step %s: %w", s.Do, err)
		}
	}
	return nil
}

func waitFor(ctx context.Context, r *run, s Step) (harness.Event, error) {
	limit := s.Timeout
	if limit == 0 {
		limit = 120
	}
	timeout := time.After(time.Duration(limit) * time.Second)
	for {
		select {
		case ev, ok := <-r.events:
			if !ok {
				return harness.Event{}, errors.New("codex: the run ended")
			}
			if wanted(ev, s.For) {
				return ev, nil
			}
		case <-timeout:
			return harness.Event{}, fmt.Errorf("codex: nothing matched %q within %ds", s.For, limit)
		case <-ctx.Done():
			return harness.Event{}, ctx.Err()
		}
	}
}

// requestIDs are this adapter's own request IDs, which differ per run.
var requestIDs = regexp.MustCompile(`hoplet-[0-9a-f]{8}-`)

// droppedMethods only describe the user's account and setup.
var droppedMethods = map[string]bool{
	"account/updated": true, "account/rateLimits/updated": true, "remoteControl/status/changed": true,
	"mcpServer/startupStatus/updated": true, "turn/moderationMetadata": true, "thread/status/changed": true,
	"thread/started": true, "thread/settings/updated": true,
}

// Sanitize turns a recording into a contract-test fixture: it drops events
// about the user's account and setup, reduces the responses to what the
// adapter reads, and replaces dir and home with /work and /Users/me.
func Sanitize(in io.Reader, out io.Writer, dir, home string) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(nil, harness.MaxLine+1<<20)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	paths := scrub.New(dir, home)
	name := scrub.Word(filepath.Base(home), "me")
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return fmt.Errorf("codex: recording: %w", err)
		}
		if e.Dir == "in" || e.Dir == "out" {
			line, keep, err := reduce(e.Line)
			if err != nil {
				return err
			}
			if !keep {
				continue
			}
			e.Line = requestIDs.ReplaceAllString(name(paths(line)), "hoplet-x-")
		}
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("codex: fixture: %w", err)
		}
	}
	return sc.Err()
}

// reduce trims one line; keep is false for a line to drop.
func reduce(line string) (string, bool, error) {
	var v map[string]any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		return "", false, fmt.Errorf("codex: recorded line: %w", err)
	}
	method, _ := v["method"].(string)
	if droppedMethods[method] {
		return "", false, nil
	}
	delete(v, "emittedAtMs")
	if res, ok := v["result"].(map[string]any); ok {
		switch t, isThread := res["thread"].(map[string]any); {
		case isThread:
			v["result"] = map[string]any{"thread": map[string]any{"id": t["id"]}, "model": res["model"], "reasoningEffort": res["reasoningEffort"]}
		case res["userAgent"] != nil: // initialize: where Codex lives here
			v["result"] = map[string]any{}
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false, fmt.Errorf("codex: recorded line: %w", err)
	}
	return string(b), true, nil
}
