package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	gosync "sync"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/configs"
	"github.com/yangflow/hoplet/agent/internal/locate"
	"github.com/yangflow/hoplet/agent/internal/provider"
	"github.com/yangflow/hoplet/agent/internal/rules"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/runtime/claude"
	"github.com/yangflow/hoplet/agent/internal/runtime/codex"
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
	att    *attachments // images uploaded for coming prompts
	rules  *rules.Store // the permission rules in force (ADR 0029)
	locate runtime.Locator
	send   func(any) error
	kick   func()

	owners              *configs.Owners // which configuration started each session
	turnDone, turnStart func(sessionID string)

	cmu      gosync.RWMutex
	entries  []*entry           // one per configuration, built-ins first
	retired  []*runtime.Manager // managers replaced by a changed configuration; their sessions run on
	file     *configs.File      // the last providers.json that loaded
	stamp    configs.Stamp
	reloadMu gosync.Mutex // serialises reloads and updates

	mu       gosync.Mutex
	cond     *gosync.Cond
	queue    []runtime.Event
	held     []runtime.RuleDecisionEvent // rule decisions the core has not received
	outputs  int
	closed   bool
	lastKick time.Time
	done     chan struct{}
}

// ownGrace overrides the runtime's grace after a turn result when set; tests shorten it.
var ownGrace time.Duration

// turnDone and turnStart are told when a session's turn ended or began, from the Manager's goroutine; they
// must not block. turnStart may be nil.
func newBridge(cfg *config.Config, send func(any) error, kick func(), turnDone, turnStart func(sessionID string)) *bridge {
	b := &bridge{
		cfg: cfg, att: newAttachments(filepath.Join(cfg.HopletHome, "attachments")),
		locate: locator(cfg), send: send, kick: kick, done: make(chan struct{}),
		turnDone: turnDone, turnStart: turnStart,
	}
	home, _ := os.UserHomeDir()
	b.rules = rules.OpenStore(rules.NewEnv(home, cfg.HopletHome), cfg.HopletHome, cfg.PermissionDeny)
	b.cond = gosync.NewCond(&b.mu)
	var err error
	if b.owners, err = configs.LoadOwners(cfg.HopletHome); err != nil {
		log.Printf("[agent] %v; sessions started from other configurations are treated as the vendor's own", err)
		b.owners = configs.NewOwners(cfg.HopletHome)
	}
	b.stamp = configs.StampOf(cfg.HopletHome)
	if b.file, err = configs.Load(cfg.HopletHome); err != nil {
		log.Printf("[agent] %v; only the built-in configurations are available", err)
		b.file = &configs.File{}
	}
	b.apply(b.file.List())
	go b.run()
	return b
}

// entry is one configuration with the driver and manager that run it.
type entry struct {
	conf configs.Configuration
	spec string // what the driver and manager were built from
	drv  runtime.Driver
	mgr  *runtime.Manager

	mu      gosync.Mutex
	checked bool // the version and the models were asked for
	version string
	tested  *bool
}

// specOf identifies what a configuration's processes are built from: a change of it replaces the manager. The
// name and the enabled flag do not change it.
func specOf(c configs.Configuration) string {
	raw, _ := json.Marshal(struct {
		ID, Extends, Command string
		Env                  map[string]string
		Defaults             map[string]any
	}{c.ID, c.Extends, c.Command, c.Env, c.Defaults})
	return string(raw)
}

// build makes the driver and manager of c: its command (or the agent's claude_bin or codex_bin) and its
// environment over the agent's, without the agent's own HOPLET_* variables.
func (b *bridge) build(c configs.Configuration, spec string) *entry {
	env := c.Environ(os.Environ())
	var d runtime.Driver
	switch c.Extends {
	case configs.Codex:
		bin := b.cfg.CodexBin
		if c.Command != "" {
			bin = c.Command
		}
		d = codex.New(codex.Options{Bin: bin, ImageDir: filepath.Join(b.cfg.HopletHome, "codex-images"), Env: env, Defaults: c.Defaults})
	default:
		bin := b.cfg.ClaudeBin
		if c.Command != "" {
			bin = c.Command
		}
		d = claude.New(claude.Options{Bin: bin, Model: os.Getenv(modelEnv), Env: env, Defaults: c.Defaults, HopletHome: b.cfg.HopletHome, Rules: b.ruleSource})
	}
	m := runtime.NewManager(runtime.Config{
		Driver: d,
		Locate: configLocator(b.cfg, c),
		Emit:   b.emit,

		TurnDone:  b.turnDone,
		TurnStart: b.turnStart,

		IdleTimeout: time.Duration(b.cfg.RuntimeIdleMinutes) * time.Minute,
		MaxIdle:     b.cfg.RuntimeMaxIdleProcesses,
		OwnGrace:    ownGrace,
	})
	return &entry{conf: c, spec: spec, drv: d, mgr: m}
}

// apply makes list the configurations: an unchanged one keeps its manager, a changed or removed one hands its
// manager to the retired, which keeps the sessions it runs going (a change applies to processes started
// afterwards).
// ruleSource builds the permission engine of one process from the rules in force now: the floor, the
// operator's permission_deny, and the pushed global, project and session rules that apply to it. The driver
// asks again for every tool call, so a push holds in running processes.
func (b *bridge) ruleSource(spec runtime.StartSpec) (*rules.Engine, error) {
	return b.rules.Engine(spec.SessionID, spec.Dir)
}

func (b *bridge) apply(list []configs.Configuration) {
	setSources(b.cfg, list)
	b.cmu.Lock()
	defer b.cmu.Unlock()
	old := map[string]*entry{}
	for _, e := range b.entries {
		old[e.conf.ID] = e
	}
	next := make([]*entry, 0, len(list))
	for _, c := range list {
		spec := specOf(c)
		if e, ok := old[c.ID]; ok && e.spec == spec {
			e.mu.Lock()
			e.conf = c
			e.mu.Unlock()
			next = append(next, e)
			delete(old, c.ID)
			continue
		}
		next = append(next, b.build(c, spec))
	}
	for _, e := range old {
		b.retired = append(b.retired, e.mgr)
	}
	b.entries = next
}

// snapshot is the current entries.
func (b *bridge) snapshot() []*entry {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	return append([]*entry(nil), b.entries...)
}

func (e *entry) config() configs.Configuration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conf
}

// entry is the entry of the configuration id, or nil.
func (b *bridge) entry(id string) *entry {
	for _, e := range b.snapshot() {
		if e.config().ID == id {
			return e
		}
	}
	return nil
}

// managers lists every runtime manager: those of the current configurations, built-ins first, then the
// retired ones that still run sessions.
func (b *bridge) managers() []*runtime.Manager {
	b.cmu.RLock()
	defer b.cmu.RUnlock()
	out := make([]*runtime.Manager, 0, len(b.entries)+len(b.retired))
	for _, e := range b.entries {
		out = append(out, e.mgr)
	}
	return append(out, b.retired...)
}

// manager is the manager of the configuration id (a built-in's id is its vendor's name), or nil.
func (b *bridge) manager(id string) *runtime.Manager {
	if e := b.entry(id); e != nil {
		return e.mgr
	}
	return nil
}

// errUnavailable refuses a start or a resume under a configuration that was removed or disabled.
var errUnavailable = errors.New("configuration-unavailable")

// holder is the manager that already holds the session, or nil.
func (b *bridge) holder(sessionID string) *runtime.Manager {
	for _, m := range b.managers() {
		if m.Knows(sessionID) {
			return m
		}
	}
	return nil
}

// ownerOf is the configuration a session belongs to: the one that started it, else the one whose own session
// store holds it, else the built-in of the vendor whose store does; empty when none does.
func (b *bridge) ownerOf(sessionID string) string {
	if id, ok := b.owners.Get(sessionID); ok {
		return id
	}
	return ownerByFind(b.cfg, sessionID)
}

// sessionConfiguration is the configuration to announce for a session: the one that started it, else the one
// whose own session store the last scan found it in; empty for the vendor's own.
func (b *bridge) sessionConfiguration(sessionID string) string {
	if id, ok := b.owners.Get(sessionID); ok {
		return id
	}
	id, _ := extraOwner(sessionID)
	return id
}

// installStore is the session store a handed-off session installs into: the configuration's own when its
// environment names a session directory, else the vendor's.
func (b *bridge) installStore(vendor, configuration string) provider.Store {
	if e := b.entry(configuration); configuration != "" && e != nil {
		if st, ok := storeOf(b.cfg, e.config()).(provider.Store); ok {
			return st
		}
	}
	return vendorStore(b.cfg, vendor)
}

// forgetConfiguration drops the record of the configuration that started a session whose entry is removed or
// whose ownership left the machine.
func (b *bridge) forgetConfiguration(sessionID string) {
	if err := b.owners.Forget(sessionID); err != nil {
		log.Printf("[agent] session configuration not forgotten: %v", err)
	}
}

// resolve is the manager that runs the session. A process that runs stays with the manager that started it.
// Otherwise it is the manager of the session's configuration (a change of a configuration applies to processes
// started afterwards), which errUnavailable refuses when the configuration was removed or is disabled. A
// session no store has goes to Claude Code's manager, whose error then says the session is not here.
func (b *bridge) resolve(sessionID string) (*runtime.Manager, error) {
	if m := b.holder(sessionID); m != nil && m.PID(sessionID) != 0 {
		return m, nil
	}
	id := b.ownerOf(sessionID)
	if id == "" {
		id = configs.Claude
	}
	e := b.entry(id)
	if e == nil || !e.config().Enabled {
		return nil, errUnavailable
	}
	return e.mgr, nil
}

// managerOf is the manager that runs the session, for what needs only a manager: the holder, else the
// configuration's, else Claude Code's.
func (b *bridge) managerOf(sessionID string) *runtime.Manager {
	if m, err := b.resolve(sessionID); err == nil {
		return m
	}
	if m := b.manager(b.ownerOf(sessionID)); m != nil {
		return m
	}
	return b.manager(configs.Claude)
}

// locator finds a session's working directory and file in any store.
func locator(cfg *config.Config) runtime.Locator {
	return func(sessionID string) (runtime.Location, error) {
		sess, err := locate.Locate(sessionID, finders(cfg)...)
		if err != nil {
			return runtime.Location{}, err
		}
		return runtime.Location{Dir: sess.Cwd, File: sess.JSONLPath}, nil
	}
}

// configLocator finds a session's working directory and file in the store of conf: its own when its
// environment names a session directory, else its vendor's. The provider answers with the directory whose
// store holds the file, which for a handed-off session is its worktree here, not the machine it came from.
func configLocator(cfg *config.Config, conf configs.Configuration) runtime.Locator {
	return func(sessionID string) (runtime.Location, error) {
		p := storeOf(cfg, conf)
		if p == nil {
			return runtime.Location{}, locate.ErrNotFound
		}
		sess, err := locate.Locate(sessionID, p)
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
			if err := b.send(msg); errors.Is(err, sync.ErrNotConnected) {
				// A rule decision is an audit record: it waits for the reconnect, which reports it again.
				if d, ok := ev.(runtime.RuleDecisionEvent); ok {
					b.holdDecision(d)
				}
			} else if err != nil {
				log.Printf("[agent] runtime event not sent: %v", err)
			}
		}
		if kick || isState(ev) {
			b.kick()
		}
	}
}

// maxHeldDecisions bounds the rule decisions kept in memory while the core is unreachable; past it the oldest go.
// They are lost when the agent exits.
const maxHeldDecisions = 500

func (b *bridge) holdDecision(d runtime.RuleDecisionEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.held) >= maxHeldDecisions {
		b.held = b.held[1:]
		log.Printf("[agent] too many rule decisions wait for the core; the oldest was dropped")
	}
	b.held = append(b.held, d)
}

func isState(ev runtime.Event) bool {
	_, ok := ev.(runtime.StateEvent)
	return ok
}

func capabilities(caps []runtime.Capability, config []runtime.ConfigOption) sync.RuntimeCapabilities {
	out := sync.RuntimeCapabilities{Config: wireConfig(config)}
	for _, c := range caps {
		switch c {
		case runtime.CapPermissions:
			out.Permissions = true
		case runtime.CapInterrupt:
			out.Interrupt = true
		case runtime.CapImages:
			out.Images = true
		case runtime.CapSteer:
			out.Steer = true
		}
	}
	return out
}

// wireConfig is the driver's settings as the wire says them; the list is never null.
func wireConfig(config []runtime.ConfigOption) []sync.ConfigOption {
	out := make([]sync.ConfigOption, 0, len(config))
	for _, o := range config {
		w := sync.ConfigOption{
			Type: string(o.Type), ID: o.ID, Name: o.Name, Description: o.Description, Category: o.Category,
			CurrentValue: o.Current,
		}
		for _, v := range o.Options {
			w.Options = append(w.Options, sync.ConfigValue{Value: v.Value, Name: v.Name, Description: v.Description, Risk: v.Risk, Icon: v.Icon})
		}
		out = append(out, w)
	}
	return out
}

func wireMessage(ev runtime.Event) any {
	switch ev := ev.(type) {
	case runtime.StateEvent:
		return sync.RuntimeState{
			V: sync.ProtocolVersion, Type: sync.TypeRuntimeState, SessionID: ev.SessionID,
			State: string(ev.State), Error: ev.Error, Capabilities: capabilities(ev.Capabilities, ev.Config),
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
		out := sync.RuntimePermission{
			V: sync.ProtocolVersion, Type: sync.TypeRuntimePermission, SessionID: ev.SessionID,
			RequestID: r.RequestID, Tool: r.Tool, ToolUseID: r.ToolUseID, Input: input,
		}
		if pe := r.PlanExit; pe != nil {
			out.PlanExit = &sync.PlanExit{Plan: pe.Plan, PlanFile: pe.File, Option: pe.Option, Modes: pe.Modes, Default: pe.Default, FreshContext: pe.FreshContext}
		}
		return out
	case runtime.RuleDecisionEvent:
		return sync.RuntimePermissionAuto{
			V: sync.ProtocolVersion, Type: sync.TypeRuntimePermissionAuto, SessionID: ev.SessionID,
			ToolUseID: ev.ToolUseID, Decision: ev.Decision, RuleID: ev.RuleID, Scope: ev.Scope, Revision: ev.Revision,
		}
	case runtime.CommandsEvent:
		list := make([]sync.Command, 0, len(ev.Commands))
		for _, c := range ev.Commands {
			list = append(list, sync.Command{
				Name: c.Name, Description: c.Description, ArgumentHint: c.ArgumentHint,
				Kind: c.Kind, Source: c.Source, Plugin: c.Plugin,
			})
		}
		return sync.RuntimeCommands{
			V: sync.ProtocolVersion, Type: sync.TypeRuntimeCommands, SessionID: ev.SessionID, Cwd: ev.Dir, Commands: list,
		}
	}
	return nil
}

// hooks are the handlers of the core's remote control messages.
func (b *bridge) hooks() sync.RuntimeHooks {
	return sync.RuntimeHooks{
		Prompt: func(sessionID, text string, ids []string) error {
			p, err := b.att.prompt(text, ids)
			if err != nil {
				return err
			}
			m, err := b.resolve(sessionID)
			if err != nil {
				return err
			}
			return m.PromptWith(sessionID, p)
		},
		Steer: func(sessionID, text string, ids []string) error {
			p, err := b.att.prompt(text, ids)
			if err != nil {
				return err
			}
			m, err := b.resolve(sessionID)
			if err != nil {
				return err
			}
			return m.Steer(sessionID, p)
		},
		Config: func(sessionID, configID string, value any) error {
			m, err := b.resolve(sessionID)
			if err != nil {
				return err
			}
			return m.SetConfig(sessionID, configID, value)
		},
		Answer: func(sessionID, requestID string, allow bool, message, mode string) error {
			return b.managerOf(sessionID).Decide(sessionID, requestID, runtime.Verdict{Allow: allow, Message: message, Mode: mode})
		},
		Interrupt: func(sessionID string) error { return b.managerOf(sessionID).Interrupt(sessionID) },
		Stop:      func(sessionID string) { b.managerOf(sessionID).Stop(sessionID) },
		Connected: b.report,
		Started:   func(string) { b.report() },
	}
}

// providers is the list the core gets in device.hello and in device.providers: one entry per configuration,
// disabled ones included, with the capabilities of its runtime driver (so the core can validate the options of
// a new session before any session exists), its version, and its configuration without any value.
func (b *bridge) providers() []sync.ProviderInfo {
	environ := os.Environ()
	var out []sync.ProviderInfo
	for _, e := range b.snapshot() {
		conf := e.config()
		e.check(conf.Enabled)
		e.mu.Lock()
		version, tested := e.version, e.tested
		e.mu.Unlock()
		out = append(out, sync.ProviderInfo{
			Provider:      conf.Extends,
			Configuration: wireConfiguration(conf, environ),
			Capabilities:  capabilities(e.drv.Capabilities(), e.drv.Config()),
			Version:       version,
			Tested:        tested,
		})
	}
	return out
}

// check asks an enabled configuration's driver once for what needs its process: the version, and for Codex
// the models the new-session form offers.
func (e *entry) check(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.checked || !enabled {
		return
	}
	e.checked = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if cd, ok := e.drv.(*codex.Driver); ok {
		_ = cd.LoadModels(ctx)
	}
	if vd, ok := e.drv.(runtime.Versioned); ok {
		if version, tested, err := vd.Version(ctx); err == nil {
			e.version, e.tested = version, &tested
		}
	}
}

// wireConfiguration is a configuration as the core sees it: the names of its variables and whether each has a
// value, never a value.
func wireConfiguration(c configs.Configuration, environ []string) sync.ProviderConfiguration {
	env := make([]sync.ProviderEnvVar, 0, len(c.Env))
	for _, n := range c.EnvNames() {
		env = append(env, sync.ProviderEnvVar{Name: n, Set: c.EnvSet(n, environ)})
	}
	defaults := make(map[string]any, len(c.Defaults))
	for k, v := range c.Defaults {
		defaults[k] = v
	}
	return sync.ProviderConfiguration{
		ID: c.ID, Name: c.Name, Extends: c.Extends, Enabled: c.Enabled, Command: c.Command,
		Env: env, Defaults: defaults,
	}
}

// reload applies providers.json when it changed since the last look and says whether it did. A file that does
// not load leaves the configurations as they are.
func (b *bridge) reload() bool {
	b.reloadMu.Lock()
	defer b.reloadMu.Unlock()
	return b.reloadLocked()
}

func (b *bridge) reloadLocked() bool {
	st := configs.StampOf(b.cfg.HopletHome)
	if st == b.stamp {
		return false
	}
	b.stamp = st
	f, err := configs.Load(b.cfg.HopletHome)
	if err != nil {
		log.Printf("[agent] %v; keeping the configurations in use", err)
		return false
	}
	b.file = f
	b.apply(f.List())
	return true
}

// update handles device.providers.update: it changes the name, the enabled flag or the defaults of one
// configuration, refusing a default the option does not declare, and returns the list afterwards.
func (b *bridge) update(req sync.DeviceProvidersUpdate) ([]sync.ProviderInfo, error) {
	b.reloadMu.Lock()
	defer b.reloadMu.Unlock()
	b.reloadLocked()
	e := b.entry(req.ID)
	if e == nil {
		return nil, configs.ErrUnknown
	}
	opts := e.drv.Config()
	for id, v := range req.Defaults {
		if !runtime.DeclaresValue(opts, id, v) {
			return nil, fmt.Errorf("%w: %s is not an option of %s or does not accept that value", configs.ErrInvalid, id, req.ID)
		}
	}
	f, err := configs.Load(b.cfg.HopletHome)
	if err != nil {
		return nil, err
	}
	if err := f.Update(req.ID, configs.Patch{Name: req.Name, Enabled: req.Enabled, Defaults: req.Defaults}); err != nil {
		return nil, err
	}
	if err := f.Save(b.cfg.HopletHome); err != nil {
		return nil, err
	}
	b.reloadLocked()
	return b.providers(), nil
}

// report tells the core the state of every process this agent runs and every permission request that
// waits for an answer; the core rebuilds its runtime state from it.
func (b *bridge) report() {
	b.mu.Lock()
	held := b.held
	b.held = nil
	b.mu.Unlock()
	for _, d := range held {
		b.emit(d)
	}
	for _, m := range b.managers() {
		for _, st := range m.States() {
			b.emit(st)
		}
		for _, p := range m.Pending() {
			b.emit(p)
		}
		for _, c := range m.Commands() {
			b.emit(c)
		}
	}
}

// observe tells the Manager the sizes of the synced session files, which is how it notices writes it did
// not make.
func (b *bridge) observe(ctx context.Context, sources []provider.Source) {
	for _, src := range sources {
		if size, err := src.Size(ctx); err == nil && size >= 0 {
			if m := b.observer(src); m != nil {
				m.Observe(src.ID(), size)
			}
		}
	}
}

// observer is the manager to tell the size of a synced file to: the one that holds the session, otherwise the
// one of its configuration, as recorded, as the last scan found it, or else the vendor's own.
func (b *bridge) observer(src provider.Source) *runtime.Manager {
	id := src.ID()
	if m := b.holder(id); m != nil {
		return m
	}
	owner, ok := b.owners.Get(id)
	if !ok {
		owner, ok = extraOwner(id)
	}
	if !ok {
		owner = src.Provider()
	}
	return b.manager(owner)
}

// stopSession ends a session's process before its file is exported for handoff.
func (b *bridge) stopSession(sessionID string) error {
	if !b.managerOf(sessionID).StopAndWait(sessionID, 15*time.Second) {
		return errors.New("the session's process did not stop")
	}
	return nil
}

// close stops every process and flushes the events already queued.
func (b *bridge) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, m := range b.managers() {
		m.Shutdown(ctx)
	}
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
	select {
	case <-b.done:
	case <-time.After(2 * time.Second):
	}
}
