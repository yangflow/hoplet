package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	gosync "sync"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/locate"
	"github.com/yangflow/hoplet/agent/internal/provider"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/runtime/claude"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// modelEnv selects the model of Claude Code processes (`--model`); unset leaves Claude Code's default.
// It exists for manual checks that must not spend a large model's quota.
const modelEnv = "HOPLET_CLAUDE_MODEL"

// maxQueuedOutput bounds the output events waiting to be sent; beyond it output is dropped, which is
// harmless: it is transient and the session file is the record.
const maxQueuedOutput = 4096

// kickEvery bounds how often runtime activity wakes the scan loop so sync follows the session file.
const kickEvery = 250 * time.Millisecond

// bridge connects the runtime Manager to the core: events go out as runtime.* messages in order, and
// the core's commands come in through sync.RuntimeHooks.
type bridge struct {
	cfg    *config.Config
	locate runtime.Locator
	mgr    *runtime.Manager
	send   func(any) error
	kick   func()

	mu       gosync.Mutex
	cond     *gosync.Cond
	queue    []runtime.Event
	outputs  int
	closed   bool
	lastKick time.Time
	done     chan struct{}
}

// turnDone is told when a session's turn ended, from the Manager's goroutine; it must not block.
func newBridge(cfg *config.Config, send func(any) error, kick func(), turnDone func(sessionID string)) *bridge {
	b := &bridge{cfg: cfg, locate: locator(cfg), send: send, kick: kick, done: make(chan struct{})}
	b.cond = gosync.NewCond(&b.mu)
	b.mgr = runtime.NewManager(runtime.Config{
		Driver: claude.New(claude.Options{Bin: cfg.ClaudeBin, Model: os.Getenv(modelEnv)}),
		Locate: locator(cfg),
		Emit:   b.emit,

		TurnDone: turnDone,

		IdleTimeout: time.Duration(cfg.RuntimeIdleMinutes) * time.Minute,
		MaxIdle:     cfg.RuntimeMaxIdleProcesses,
	})
	go b.run()
	return b
}

// locator finds a session's working directory and file. The provider answers with the directory whose
// store holds the file, which for a handed-off session is its worktree here, not the machine it came from.
func locator(cfg *config.Config) runtime.Locator {
	return func(sessionID string) (runtime.Location, error) {
		sess, err := locate.Locate(sessionID, finders(cfg)...)
		if err != nil {
			return runtime.Location{}, err
		}
		return runtime.Location{Dir: sess.Cwd, File: sess.JSONLPath}, nil
	}
}

// emit queues an event; it never blocks the Manager.
func (b *bridge) emit(ev runtime.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if _, isOutput := ev.(runtime.OutputEvent); isOutput {
		if b.outputs >= maxQueuedOutput {
			return
		}
		b.outputs++
	}
	b.queue = append(b.queue, ev)
	b.cond.Signal()
}

func (b *bridge) run() {
	defer close(b.done)
	for {
		b.mu.Lock()
		for len(b.queue) == 0 && !b.closed {
			b.cond.Wait()
		}
		if len(b.queue) == 0 {
			b.mu.Unlock()
			return
		}
		ev := b.queue[0]
		b.queue = b.queue[1:]
		if _, isOutput := ev.(runtime.OutputEvent); isOutput {
			b.outputs--
		}
		kick := time.Since(b.lastKick) >= kickEvery
		if kick {
			b.lastKick = time.Now()
		}
		b.mu.Unlock()

		if msg := wireMessage(ev); msg != nil {
			// Not connected: the state of every process is reported again on reconnect.
			if err := b.send(msg); err != nil && !errors.Is(err, sync.ErrNotConnected) {
				log.Printf("[agent] runtime event not sent: %v", err)
			}
		}
		if kick || isState(ev) {
			b.kick()
		}
	}
}

func isState(ev runtime.Event) bool {
	_, ok := ev.(runtime.StateEvent)
	return ok
}

func capabilities(caps []runtime.Capability, models []string) sync.RuntimeCapabilities {
	out := sync.RuntimeCapabilities{Models: []string{}}
	out.Models = append(out.Models, models...)
	for _, c := range caps {
		switch c {
		case runtime.CapPermissions:
			out.Permissions = true
		case runtime.CapInterrupt:
			out.Interrupt = true
		case runtime.CapThinking:
			out.Thinking = true
		case runtime.CapFast:
			out.Fast = true
		}
	}
	return out
}

func wireMessage(ev runtime.Event) any {
	switch ev := ev.(type) {
	case runtime.StateEvent:
		return sync.RuntimeState{
			V: sync.ProtocolVersion, Type: sync.TypeRuntimeState, SessionID: ev.SessionID,
			State: string(ev.State), Error: ev.Error, Capabilities: capabilities(ev.Capabilities, ev.Models),
		}
	case runtime.OutputEvent:
		o := ev.Output
		return sync.RuntimeOutput{
			V: sync.ProtocolVersion, Type: sync.TypeRuntimeOutput, SessionID: ev.SessionID,
			Kind: string(o.Kind), Text: o.Text, Tokens: o.Tokens, Tool: o.Tool, ToolUseID: o.ToolUseID,
			Input: o.Input, IsError: o.IsError, Truncated: o.Truncated,
		}
	case runtime.PermissionEvent:
		r := ev.Request
		input := r.Input
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		return sync.RuntimePermission{
			V: sync.ProtocolVersion, Type: sync.TypeRuntimePermission, SessionID: ev.SessionID,
			RequestID: r.RequestID, Tool: r.Tool, ToolUseID: r.ToolUseID, Input: input,
		}
	}
	return nil
}

// hooks are the handlers of the core's remote control messages.
func (b *bridge) hooks() sync.RuntimeHooks {
	return sync.RuntimeHooks{
		Prompt: func(sessionID, text string) error { return b.mgr.Prompt(sessionID, text) },
		Answer: func(sessionID, requestID string, allow bool) error {
			return b.mgr.Permission(sessionID, requestID, allow)
		},
		Interrupt: func(sessionID string) error { return b.mgr.Interrupt(sessionID) },
		Stop:      func(sessionID string) { b.mgr.Stop(sessionID) },
		Connected: b.report,
		Started:   func(string) { b.report() },
	}
}

// providers are announced in device.hello: the runtime driver's capabilities, so the core can validate the
// options of a new session before any session exists.
func (b *bridge) providers() []sync.ProviderInfo {
	d := b.mgr.Driver()
	info := sync.ProviderInfo{Provider: d.Name(), Capabilities: capabilities(d.Capabilities(), d.Models())}
	if vd, ok := d.(runtime.Versioned); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if version, tested, err := vd.Version(ctx); err == nil {
			info.Version, info.Tested = version, &tested
		}
	}
	return []sync.ProviderInfo{info}
}

// report tells the core the state of every process this agent runs and every permission request that
// waits for an answer; the core rebuilds its runtime state from it.
func (b *bridge) report() {
	for _, st := range b.mgr.States() {
		b.emit(st)
	}
	for _, p := range b.mgr.Pending() {
		b.emit(p)
	}
}

// observe tells the Manager the sizes of the synced session files, which is how it notices writes it did
// not make.
func (b *bridge) observe(ctx context.Context, sources []provider.Source) {
	for _, src := range sources {
		if size, err := src.Size(ctx); err == nil && size >= 0 {
			b.mgr.Observe(src.ID(), size)
		}
	}
}

// stopSession ends a session's process before its file is exported for handoff.
func (b *bridge) stopSession(sessionID string) error {
	if !b.mgr.StopAndWait(sessionID, 15*time.Second) {
		return errors.New("the session's process did not stop")
	}
	return nil
}

// close stops every process and flushes the events already queued.
func (b *bridge) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b.mgr.Shutdown(ctx)
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
	select {
	case <-b.done:
	case <-time.After(2 * time.Second):
	}
}
