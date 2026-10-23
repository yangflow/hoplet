package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

var errExited = errors.New("codex app-server exited")

// rpcError is the error object of a JSON-RPC response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("codex: %s (code %d)", e.Message, e.Code) }

// rpcMsg is any message of the app server: a response (ID, no Method), a server request (ID and Method) or a
// notification (Method, no ID).
type rpcMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// serverReq is an approval request of the app server the driver passed on and has not seen resolved.
type serverReq struct {
	id       json.RawMessage
	turnID   string
	answered bool
}

// process is one `codex app-server`. Its reader goroutine owns everything that arrives; Prompt, Steer and
// Answer are called with the manager's lock held, so they only write and never wait for a response.
type process struct {
	d      *Driver
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tailBuffer

	events   chan runtime.ProcessEvent
	evMu     sync.Mutex
	evClosed bool
	quit     chan struct{}
	quitOnce sync.Once
	stdinOne sync.Once

	wmu sync.Mutex // serializes writes to stdin

	mu          sync.Mutex
	nextID      int64
	pending     map[int64]func(json.RawMessage, error)
	done        bool // the output ended
	threadID    string
	threadModel string
	cfg         map[string]string
	reported    map[string]string // the last model and effort Codex reported
	turnID      string            // the running turn, once Codex announced it
	turnPending bool              // a turn was requested or runs
	turnPlan    bool              // the turn runs in plan collaboration
	planText    string            // the plan item of the running turn
	lastError   string
	requests    map[string]*serverReq
	plans       map[string]string // plan-exit requests waiting for the user, by request ID
	images      []string

	// The rule hook (ADR 0032): the process answers it as a hookTarget registered under its nonce.
	gone       chan struct{} // closed when the output ended
	dir        string        // the session's directory, as the daemon knows it
	rules      RuleSource    // nil when the process runs without the hook
	unregister func()
	mcp        *runtime.MCPSpec    // the Hoplet MCP server this process was given (ADR 0034), nil without
	onExit     func()              // ends the MCP registration with the process
	hookAsks   map[string]*hookAsk // approvals the hook holds open, by request ID
	hookSeq    int
	hookOK     map[string]time.Time // items the hook let through, by item ID, until Codex asks about them
	hookCalls  int                  // calls the hook asked about
	hookWarned bool
}

func newProcess(d *Driver, cmd *exec.Cmd, stdin io.WriteCloser, stderr *tailBuffer) *process {
	return &process{
		d: d, cmd: cmd, stdin: stdin, stderr: stderr,
		events:   make(chan runtime.ProcessEvent, 256),
		quit:     make(chan struct{}),
		pending:  map[int64]func(json.RawMessage, error){},
		cfg:      map[string]string{optMode: modeWorkspaceWrite, optModel: valueDefault, optEffort: valueDefault, optCollaboration: valueDefault},
		reported: map[string]string{},
		requests: map[string]*serverReq{},
		plans:    map[string]string{},
		gone:     make(chan struct{}),
		hookAsks: map[string]*hookAsk{},
		hookOK:   map[string]time.Time{},
	}
}

func (p *process) Events() <-chan runtime.ProcessEvent { return p.events }

// PID is the app server's process ID.
func (p *process) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// emit delivers an event unless the stream ended; once the process was killed a full stream drops it.
func (p *process) emit(ev runtime.ProcessEvent) {
	p.evMu.Lock()
	defer p.evMu.Unlock()
	if p.evClosed {
		return
	}
	select {
	case p.events <- ev:
		return
	default:
	}
	select {
	case p.events <- ev:
	case <-p.quit:
	}
}

func (p *process) closeEvents() {
	p.evMu.Lock()
	defer p.evMu.Unlock()
	if !p.evClosed {
		p.evClosed = true
		close(p.events)
	}
}

func (p *process) wrapErr(err error) error {
	if s := lastLine(p.stderr.String()); s != "" {
		return fmt.Errorf("%w: %s", err, s)
	}
	return err
}

// configure takes the session's settings; a value the driver does not declare keeps the default.
func (p *process) configure(cfg map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, v := range cfg {
		s, ok := v.(string)
		if ok && validValue(id, s) {
			p.cfg[id] = s
		}
	}
}

func validValue(id, v string) bool {
	switch id {
	case optMode:
		_, ok := modeByValue(v)
		return ok
	case optCollaboration:
		return v == valueDefault || v == collabPlan
	case optModel, optEffort:
		return v != ""
	}
	return false
}

// ---- JSON-RPC ----

func (p *process) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, err = p.stdin.Write(b)
	return err
}

// callAsync sends a request; cb runs on the reader goroutine with the result or the error, or on a new
// goroutine when the request could not be written.
func (p *process) callAsync(method string, params any, cb func(json.RawMessage, error)) {
	if cb == nil {
		cb = func(json.RawMessage, error) {}
	}
	p.mu.Lock()
	if p.done {
		p.mu.Unlock()
		go cb(nil, errExited)
		return
	}
	p.nextID++
	id := p.nextID
	p.pending[id] = cb
	p.mu.Unlock()
	if err := p.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		p.mu.Lock()
		_, ok := p.pending[id]
		delete(p.pending, id)
		p.mu.Unlock()
		if ok {
			go cb(nil, err)
		}
	}
}

func (p *process) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	type result struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	p.callAsync(method, params, func(raw json.RawMessage, err error) { ch <- result{raw, err} })
	select {
	case r := <-ch:
		return r.raw, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *process) notify(method string, params any) error {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		m["params"] = params
	}
	return p.write(m)
}

// read dispatches what the app server writes until its output ends, then reports the exit.
func (p *process) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var m rpcMsg
			if json.Unmarshal(line, &m) == nil {
				p.dispatch(m)
			}
		}
		if err != nil {
			break
		}
	}
	waitErr := p.cmd.Wait()
	close(p.gone)
	if p.unregister != nil {
		p.unregister()
	}
	if p.onExit != nil {
		p.onExit()
	}
	p.mu.Lock()
	p.done = true
	pending := p.pending
	p.pending = map[int64]func(json.RawMessage, error){}
	p.mu.Unlock()
	for _, cb := range pending {
		cb(nil, errExited)
	}
	if waitErr != nil {
		waitErr = p.wrapErr(waitErr)
	}
	for _, f := range p.images {
		_ = os.Remove(f)
	}
	p.emit(runtime.Exited{Err: waitErr})
	p.closeEvents()
}

func (p *process) dispatch(m rpcMsg) {
	switch {
	case m.Method != "" && len(m.ID) > 0:
		p.handleRequest(m.ID, m.Method, m.Params)
	case m.Method != "":
		p.handleNote(m.Method, m.Params)
	case len(m.ID) > 0:
		var id int64
		if json.Unmarshal(m.ID, &id) != nil {
			return
		}
		p.mu.Lock()
		cb := p.pending[id]
		delete(p.pending, id)
		p.mu.Unlock()
		if cb == nil {
			return
		}
		if m.Error != nil {
			cb(nil, m.Error)
		} else {
			cb(m.Result, nil)
		}
	}
}

// ---- handshake ----

func (p *process) initialize(ctx context.Context) error {
	_, err := p.call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "hoplet-agent", "title": nil, "version": "0"},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err != nil {
		return fmt.Errorf("initialize codex app-server: %w", err)
	}
	return p.notify("initialized", nil)
}

func (p *process) loadModels(ctx context.Context) error {
	raw, err := p.call(ctx, "model/list", map[string]any{"includeHidden": false})
	if err != nil {
		return fmt.Errorf("list codex models: %w", err)
	}
	var res struct {
		Data []struct {
			ID          string `json:"id"`
			Model       string `json:"model"`
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
			Hidden      bool   `json:"hidden"`
			Default     string `json:"defaultReasoningEffort"`
			Efforts     []struct {
				Effort string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("read codex model list: %w", err)
	}
	var models []modelInfo
	for _, m := range res.Data {
		if m.Hidden {
			continue
		}
		id := m.Model
		if id == "" {
			id = m.ID
		}
		if id == "" {
			continue
		}
		mi := modelInfo{ID: id, Name: m.DisplayName, Description: m.Description, DefaultEffort: m.Default}
		for _, e := range m.Efforts {
			if e.Effort != "" {
				mi.Efforts = append(mi.Efforts, e.Effort)
			}
		}
		models = append(models, mi)
	}
	p.d.setModels(models)
	return nil
}

// openThread starts the thread in spec.Dir, or resumes spec.SessionID, with the session's mode and model.
func (p *process) openThread(ctx context.Context, spec runtime.StartSpec) error {
	p.mu.Lock()
	m, _ := modeByValue(p.cfg[optMode])
	model := p.cfg[optModel]
	p.mu.Unlock()
	params := map[string]any{
		"cwd":               spec.Dir,
		"approvalPolicy":    m.approval,
		"sandbox":           m.sandbox,
		"approvalsReviewer": m.reviewer,
	}
	if model != valueDefault {
		params["model"] = model
	}
	if cfg := threadConfig(p.d.hookCommand(), p.rules != nil, spec.MCP); cfg != nil {
		params["config"] = cfg
	}
	method := "thread/start"
	if !spec.New {
		method = "thread/resume"
		params["threadId"] = spec.SessionID
		params["excludeTurns"] = true
	}
	raw, err := p.call(ctx, method, params)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	var res struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Thread.ID == "" {
		return fmt.Errorf("%s: no thread in the answer", method)
	}
	p.mu.Lock()
	p.threadID, p.threadModel = res.Thread.ID, res.Model
	p.mu.Unlock()
	return nil
}

// ---- runtime.Process ----

func (p *process) Prompt(pr runtime.Prompt) error {
	return p.startTurn(pr, nil)
}

// startTurn writes turn/start with the session's settings as overrides. onStart runs when Codex accepted the
// turn. A refusal ends the turn as a failed one.
func (p *process) startTurn(pr runtime.Prompt, onStart func()) error {
	input, err := p.input(pr)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if p.done {
		p.mu.Unlock()
		return errExited
	}
	m, _ := modeByValue(p.cfg[optMode])
	model, effort, collab := p.cfg[optModel], p.cfg[optEffort], p.cfg[optCollaboration]
	settingsModel := model
	if model == valueDefault {
		settingsModel = p.threadModel
	}
	thread := p.threadID
	p.turnPending, p.turnPlan, p.planText, p.lastError, p.turnID = true, collab == collabPlan, "", "", ""
	p.mu.Unlock()

	params := map[string]any{
		"threadId":          thread,
		"input":             input,
		"approvalPolicy":    m.approval,
		"approvalsReviewer": m.reviewer,
		"sandboxPolicy":     map[string]any{"type": m.policy},
	}
	if model != valueDefault {
		params["model"] = model
	}
	var settingsEffort any
	if effort != valueDefault && p.d.supportsEffort(settingsModel, effort) {
		params["effort"] = effort
		settingsEffort = effort
	}
	params["collaborationMode"] = map[string]any{
		"mode": collab,
		"settings": map[string]any{
			"model":                  settingsModel,
			"reasoning_effort":       settingsEffort,
			"developer_instructions": nil,
		},
	}
	p.callAsync("turn/start", params, func(raw json.RawMessage, err error) {
		if err != nil {
			p.mu.Lock()
			p.turnPending, p.turnID = false, ""
			p.mu.Unlock()
			p.emit(runtime.TurnResult{Failed: true, Error: err.Error()})
			p.emit(runtime.StateChanged{State: runtime.StateReady})
			return
		}
		var res struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(raw, &res)
		p.mu.Lock()
		if p.turnPending && p.turnID == "" {
			p.turnID = res.Turn.ID
		}
		p.mu.Unlock()
		if onStart != nil {
			onStart()
		}
	})
	return nil
}

// input is the text of a prompt and its images as files Codex reads.
func (p *process) input(pr runtime.Prompt) ([]any, error) {
	in := []any{map[string]any{"type": "text", "text": pr.Text, "text_elements": []any{}}}
	for _, img := range pr.Images {
		path, err := p.writeImage(img)
		if err != nil {
			return nil, err
		}
		in = append(in, map[string]any{"type": "localImage", "path": path})
	}
	return in, nil
}

func (p *process) writeImage(img runtime.Image) (string, error) {
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}[img.MediaType]
	if ext == "" {
		return "", fmt.Errorf("unsupported image type %q", img.MediaType)
	}
	if err := os.MkdirAll(p.d.imageDir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256(img.Data)
	path := filepath.Join(p.d.imageDir, hex.EncodeToString(sum[:8])+ext)
	if err := os.WriteFile(path, img.Data, 0o600); err != nil {
		return "", err
	}
	p.mu.Lock()
	p.images = append(p.images, path)
	p.mu.Unlock()
	return path, nil
}

// Steer writes the message into the running turn; with no turn running, or when Codex finds none, it runs as a
// new turn (ADR 0020).
func (p *process) Steer(pr runtime.Prompt) error {
	p.mu.Lock()
	turn, thread := p.turnID, p.threadID
	p.mu.Unlock()
	if turn == "" {
		return p.startTurn(pr, nil)
	}
	input, err := p.input(pr)
	if err != nil {
		return err
	}
	p.callAsync("turn/steer", map[string]any{"threadId": thread, "expectedTurnId": turn, "input": input}, func(_ json.RawMessage, err error) {
		if err != nil && strings.Contains(err.Error(), "no active turn") {
			_ = p.startTurn(pr, nil)
		}
	})
	return nil
}

func (p *process) Set(id string, value any) error {
	s, ok := value.(string)
	if !ok || !validValue(id, s) {
		return fmt.Errorf("codex: %q is not a value of %q", value, id)
	}
	p.mu.Lock()
	p.cfg[id] = s
	p.mu.Unlock()
	return nil
}

func (p *process) Interrupt() error {
	p.mu.Lock()
	turn, thread := p.turnID, p.threadID
	var plans []string
	if turn == "" {
		for id := range p.plans {
			plans = append(plans, id)
			delete(p.plans, id)
		}
	}
	p.mu.Unlock()
	p.endHookAsks()
	if turn != "" {
		p.callAsync("turn/interrupt", map[string]any{"threadId": thread, "turnId": turn}, nil)
		return nil
	}
	if len(plans) > 0 {
		go func() {
			for _, id := range plans {
				p.emit(runtime.PermissionCancelled{RequestID: id})
			}
			p.emit(runtime.StateChanged{State: runtime.StateReady})
		}()
	}
	return nil
}

func (p *process) Close() error {
	p.stdinOne.Do(func() { _ = p.stdin.Close() })
	return nil
}

func (p *process) Kill() error {
	p.quitOnce.Do(func() { close(p.quit) })
	_ = p.Close()
	if p.cmd.Process != nil {
		if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return p.cmd.Process.Kill()
		}
	}
	return nil
}

// ---- permissions ----

func (p *process) handleRequest(id json.RawMessage, method string, params json.RawMessage) {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
	default:
		_ = p.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "unsupported request " + method}})
		return
	}
	var q struct {
		ThreadID  string  `json:"threadId"`
		TurnID    string  `json:"turnId"`
		ItemID    string  `json:"itemId"`
		Command   string  `json:"command"`
		Cwd       string  `json:"cwd"`
		Reason    *string `json:"reason"`
		GrantRoot *string `json:"grantRoot"`
	}
	_ = json.Unmarshal(params, &q)
	tool, input := "commandExecution", map[string]any{"command": q.Command, "cwd": q.Cwd}
	if method == "item/fileChange/requestApproval" {
		tool, input = "fileChange", map[string]any{}
		if q.GrantRoot != nil {
			input["grantRoot"] = *q.GrantRoot
		}
	}
	if q.Reason != nil {
		input["reason"] = *q.Reason
	}
	if p.takeHookOK(q.ItemID) {
		// The user or an allow rule approved this item at the hook; Codex's own approval follows that answer.
		_ = p.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"decision": "accept"}})
		return
	}
	reqID := string(id)
	p.mu.Lock()
	p.requests[reqID] = &serverReq{id: id, turnID: q.TurnID}
	p.mu.Unlock()
	p.emit(runtime.PermissionRequest{RequestID: reqID, Tool: tool, ToolUseID: q.ItemID, Input: mustJSON(input)})
}

// Answer responds to an approval of Codex, or to the plan exit the driver raised.
func (p *process) Answer(requestID string, v runtime.Verdict) error {
	if strings.HasPrefix(requestID, planPrefix) {
		return p.answerPlan(requestID, v)
	}
	if strings.HasPrefix(requestID, hookAskPrefix) {
		return p.answerHook(requestID, v)
	}
	p.mu.Lock()
	req := p.requests[requestID]
	if req == nil || req.answered {
		p.mu.Unlock()
		return fmt.Errorf("codex: no request %q is waiting", requestID)
	}
	req.answered = true
	turn, thread := req.turnID, p.threadID
	if turn == "" {
		turn = p.turnID
	}
	p.mu.Unlock()

	decision := "decline"
	if v.Allow {
		decision = "accept"
	}
	if err := p.write(map[string]any{"jsonrpc": "2.0", "id": req.id, "result": map[string]any{"decision": decision}}); err != nil {
		return err
	}
	if !v.Allow && strings.TrimSpace(v.Message) != "" && turn != "" {
		input := []any{map[string]any{"type": "text", "text": v.Message, "text_elements": []any{}}}
		p.callAsync("turn/steer", map[string]any{"threadId": thread, "expectedTurnId": turn, "input": input}, nil)
	}
	return nil
}

const planPrefix = "plan:"

// raisePlanExit asks the user to leave plan mode with the plan the turn produced.
func (p *process) raisePlanExit(turnID, plan string) {
	id := planPrefix + turnID
	p.mu.Lock()
	p.plans[id] = plan
	p.mu.Unlock()
	p.emit(runtime.PermissionRequest{
		RequestID: id, Tool: "plan", ToolUseID: turnID,
		Input: mustJSON(map[string]any{"plan": plan}),
		PlanExit: &runtime.PlanExit{
			Plan: plan, Option: optMode, Modes: planExitModes(), Default: modeWorkspaceWrite,
		},
	})
}

// answerPlan: an approval implements the plan in default collaboration with the chosen mode; a refusal's
// message is the next turn, still in plan mode.
func (p *process) answerPlan(requestID string, v runtime.Verdict) error {
	mode := v.Mode
	if v.Allow {
		if mode == "" {
			mode = modeWorkspaceWrite
		}
		if _, ok := modeByValue(mode); !ok || mode == modeFullAccess {
			return fmt.Errorf("codex: %q is not a mode to leave the plan to", mode)
		}
	}
	p.mu.Lock()
	_, ok := p.plans[requestID]
	delete(p.plans, requestID)
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("codex: no plan %q is waiting", requestID)
	}
	if !v.Allow {
		return p.startTurn(runtime.Prompt{Text: v.Message}, nil)
	}
	p.mu.Lock()
	p.cfg[optCollaboration], p.cfg[optMode] = valueDefault, mode
	p.mu.Unlock()
	return p.startTurn(runtime.Prompt{Text: "Implement the plan."}, func() {
		p.emit(runtime.ConfigChanged{ID: optCollaboration, Value: valueDefault})
		p.emit(runtime.ConfigChanged{ID: optMode, Value: mode})
	})
}

// ---- notifications ----

func (p *process) handleNote(method string, params json.RawMessage) {
	var head struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(params, &head)
	p.mu.Lock()
	foreign := p.threadID != "" && head.ThreadID != "" && head.ThreadID != p.threadID
	p.mu.Unlock()
	if foreign {
		return
	}
	switch method {
	case "turn/started":
		var n struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(params, &n)
		p.mu.Lock()
		p.turnID, p.turnPending = n.Turn.ID, true
		p.mu.Unlock()
		p.emit(runtime.StateChanged{State: runtime.StateRunning})
	case "item/agentMessage/delta":
		var n struct {
			Delta string `json:"delta"`
		}
		_ = json.Unmarshal(params, &n)
		if n.Delta != "" {
			p.emit(runtime.Output{Kind: runtime.OutputText, Text: n.Delta})
		}
	case "item/started":
		p.itemStarted(params)
	case "item/completed":
		p.itemCompleted(params)
	case "error":
		var n struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(params, &n)
		if n.Error.Message != "" {
			p.mu.Lock()
			p.lastError = n.Error.Message
			p.mu.Unlock()
		}
	case "thread/settings/updated":
		p.settingsUpdated(params)
	case "turn/completed":
		p.turnCompleted(params)
	case "serverRequest/resolved":
		p.requestResolved(params)
	case "warning", "configWarning":
		p.hookWarning(method, params)
	}
}

// hookWarning logs a warning of Codex that is about hooks: a hook Codex refused to load means the rules are not
// enforced. Other warnings are Codex's own.
func (p *process) hookWarning(method string, params json.RawMessage) {
	if p.rules == nil {
		return
	}
	var n struct {
		Message string `json:"message"`
		Summary string `json:"summary"`
		Details string `json:"details"`
	}
	_ = json.Unmarshal(params, &n)
	text := strings.TrimSpace(n.Message + " " + n.Summary + " " + n.Details)
	if strings.Contains(strings.ToLower(text), "hook") {
		if len(text) > 300 {
			text = strings.ToValidUTF8(text[:300], "")
		}
		log.Printf("[agent] Codex reported on its hooks (%s): %s", method, text)
	}
}

type item struct {
	Type             string `json:"type"`
	ID               string `json:"id"`
	Command          string `json:"command"`
	Cwd              string `json:"cwd"`
	Status           string `json:"status"`
	AggregatedOutput string `json:"aggregatedOutput"`
	ExitCode         *int   `json:"exitCode"`
	Text             string `json:"text"`
	Changes          []struct {
		Path string `json:"path"`
	} `json:"changes"`
}

func (p *process) itemStarted(params json.RawMessage) {
	var n struct {
		Item item `json:"item"`
	}
	if json.Unmarshal(params, &n) != nil {
		return
	}
	switch n.Item.Type {
	case "commandExecution":
		p.emit(runtime.Output{Kind: runtime.OutputToolCall, Tool: "commandExecution", ToolUseID: n.Item.ID,
			Input: mustJSON(map[string]any{"command": n.Item.Command, "cwd": n.Item.Cwd})})
	case "fileChange":
		paths := []string{}
		for _, c := range n.Item.Changes {
			paths = append(paths, c.Path)
		}
		p.emit(runtime.Output{Kind: runtime.OutputToolCall, Tool: "fileChange", ToolUseID: n.Item.ID,
			Input: mustJSON(map[string]any{"paths": paths})})
	}
}

func (p *process) itemCompleted(params json.RawMessage) {
	var n struct {
		Item item `json:"item"`
	}
	if json.Unmarshal(params, &n) != nil {
		return
	}
	switch n.Item.Type {
	case "commandExecution", "fileChange":
		p.checkHookRan(n.Item.ID)
	}
	switch n.Item.Type {
	case "commandExecution":
		text, cut := n.Item.AggregatedOutput, false
		if len(text) > runtime.MaxOutputText {
			text, cut = strings.ToValidUTF8(text[:runtime.MaxOutputText], ""), true
		}
		bad := n.Item.Status == "failed" || n.Item.Status == "declined" || (n.Item.ExitCode != nil && *n.Item.ExitCode != 0)
		p.emit(runtime.Output{Kind: runtime.OutputToolResult, ToolUseID: n.Item.ID, Text: text, IsError: bad, Truncated: cut})
	case "fileChange":
		bad := n.Item.Status == "failed" || n.Item.Status == "declined"
		p.emit(runtime.Output{Kind: runtime.OutputToolResult, ToolUseID: n.Item.ID, Text: n.Item.Status, IsError: bad})
	case "plan":
		p.mu.Lock()
		p.planText = n.Item.Text
		p.mu.Unlock()
	}
}

func (p *process) turnCompleted(params json.RawMessage) {
	var n struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(params, &n)
	p.mu.Lock()
	plan := ""
	if p.turnPlan && n.Turn.Status == "completed" {
		plan = p.planText
	}
	errMsg := p.lastError
	p.turnID, p.turnPending, p.turnPlan, p.planText, p.lastError = "", false, false, "", ""
	p.mu.Unlock()

	failed := n.Turn.Status == "failed"
	if failed && n.Turn.Error != nil && n.Turn.Error.Message != "" {
		errMsg = n.Turn.Error.Message
	}
	if !failed {
		errMsg = ""
	} else if errMsg == "" {
		errMsg = "the turn failed"
	}
	p.emit(runtime.TurnResult{Failed: failed, Error: errMsg})
	if plan != "" {
		p.raisePlanExit(n.Turn.ID, plan)
		return
	}
	p.emit(runtime.StateChanged{State: runtime.StateReady})
}

func (p *process) requestResolved(params json.RawMessage) {
	var n struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	_ = json.Unmarshal(params, &n)
	key := string(n.RequestID)
	p.mu.Lock()
	req := p.requests[key]
	delete(p.requests, key)
	idle := len(p.requests) == 0 && p.turnPending
	p.mu.Unlock()
	if req == nil {
		return
	}
	if !req.answered {
		p.emit(runtime.PermissionCancelled{RequestID: key})
	}
	if idle {
		p.emit(runtime.StateChanged{State: runtime.StateRunning})
	}
}

// settingsUpdated reports the settings Codex changed itself as ConfigChanged. The model and the effort are
// reported as concrete values even when the session leaves them to Codex, so only a change of the reported
// value counts.
func (p *process) settingsUpdated(params json.RawMessage) {
	var n struct {
		Settings struct {
			Approval string `json:"approvalPolicy"`
			Reviewer string `json:"approvalsReviewer"`
			Sandbox  struct {
				Type string `json:"type"`
			} `json:"sandboxPolicy"`
			Model  string `json:"model"`
			Effort string `json:"effort"`
			Collab struct {
				Mode string `json:"mode"`
			} `json:"collaborationMode"`
		} `json:"threadSettings"`
	}
	if json.Unmarshal(params, &n) != nil {
		return
	}
	s := n.Settings
	var changes []runtime.ConfigChanged
	p.mu.Lock()
	if mode, ok := modeOf(s.Approval, s.Sandbox.Type, s.Reviewer); ok && mode != p.cfg[optMode] {
		p.cfg[optMode] = mode
		changes = append(changes, runtime.ConfigChanged{ID: optMode, Value: mode})
	}
	if c := s.Collab.Mode; (c == valueDefault || c == collabPlan) && c != p.cfg[optCollaboration] {
		p.cfg[optCollaboration] = c
		changes = append(changes, runtime.ConfigChanged{ID: optCollaboration, Value: c})
	}
	for _, o := range []struct{ id, v string }{{optModel, s.Model}, {optEffort, s.Effort}} {
		if o.v == "" {
			continue
		}
		prev := p.reported[o.id]
		p.reported[o.id] = o.v
		if prev != "" && prev != o.v && p.cfg[o.id] != o.v {
			p.cfg[o.id] = o.v
			changes = append(changes, runtime.ConfigChanged{ID: o.id, Value: o.v})
		}
	}
	p.mu.Unlock()
	for _, c := range changes {
		p.emit(c)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}
