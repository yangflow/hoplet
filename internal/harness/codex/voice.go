package codex

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/voice"
	"github.com/yangflow/hoplet/proto"
)

// voiceInstructions is what the realtime session's backing thread is told.
const voiceInstructions = `You are the voice of Hoplet on a phone call. Keep replies short and spoken.
You can only list and read Hoplet sessions, send a message to an existing session, and hand a task off to a new Code session.
Before you send or hand off, read out the target and the content and wait for the user to agree aloud.
If you cannot tell which session the user means, ask; never guess. Do not read session IDs aloud; use titles.
You cannot answer approvals: read out what is waiting and tell the user to answer on a screen.`

// voiceToolServer is the name of the MCP server carrying Hoplet's tools.
const voiceToolServer = "hoplet"

// voiceOffFeatures are Codex's own capabilities a call's model must not have:
// it reaches Hoplet's tools and nothing else.
var voiceOffFeatures = []string{
	"apps", "browser_use", "browser_use_external", "code_mode_host", "computer_use", "goals", "hooks", "image_generation",
	"in_app_browser", "memories", "multi_agent", "plugins", "shell_snapshot", "shell_tool", "skill_search", "sleep_tool",
	"tool_suggest", "unified_exec", "view_image",
}

// voiceThreadConfig turns off everything but Hoplet's tools. servers are the
// MCP servers the user configured.
func voiceThreadConfig(servers []string) map[string]any {
	cfg := map[string]any{"web_search": "disabled"}
	for _, f := range voiceOffFeatures {
		cfg["features."+f] = false
	}
	for _, s := range servers {
		if s != voiceToolServer {
			cfg["mcp_servers."+s+".enabled"] = false
		}
	}
	return cfg
}

// VoiceProvider offers voice calls through Codex's subscription voice. It
// runs a process of its own for each call; nothing about the call touches
// sessions.
type VoiceProvider struct {
	Binary string
	Env    env.Source

	// open replaces the process (tests); nil means running Binary.
	open func(ctx context.Context, args []string) (*voiceProcess, error)
}

var _ voice.Provider = (*VoiceProvider)(nil)

// voiceProcess is a running app-server.
type voiceProcess struct {
	in   io.WriteCloser
	out  io.Reader
	stop func() error
}

// Name implements voice.Provider.
func (p *VoiceProvider) Name() string { return "Codex" }

// Probe implements voice.Provider.
func (p *VoiceProvider) Probe(ctx context.Context) error {
	c, err := p.connect(ctx, nil)
	if err != nil {
		return err
	}
	defer c.close()
	return c.check(ctx)
}

// Start implements voice.Provider. All checks run on the call's own process.
func (p *VoiceProvider) Start(ctx context.Context, req voice.StartRequest) (voice.Call, error) {
	home, args, err := voiceHome(req.Tools)
	if err != nil {
		return nil, err
	}
	c, err := p.connect(ctx, args)
	if err != nil {
		_ = os.RemoveAll(home)
		return nil, err
	}
	c.dir = home
	if err := c.check(ctx); err != nil {
		c.close()
		return nil, err
	}
	if err := c.begin(ctx, req); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

// voiceHome makes a call's private directory, holding the empty directory
// the thread works in and the tool token, and returns it with the command
// line that gives the call its tools.
func voiceHome(tools []harness.MCPServer) (string, []string, error) {
	home, err := os.MkdirTemp("", "hoplet-voice-")
	if err != nil {
		return "", nil, fmt.Errorf("codex: voice directory: %w", err)
	}
	args, files, err := mcpOverrides(harness.Spec{Dir: home, MCP: tools})
	if err == nil {
		err = os.Mkdir(filepath.Join(home, voiceWorkDir), 0o700)
	}
	for name, b := range files {
		if err == nil {
			err = os.WriteFile(filepath.Join(home, name), b, 0o600)
		}
	}
	if err != nil {
		_ = os.RemoveAll(home)
		return "", nil, fmt.Errorf("codex: voice directory: %w", err)
	}
	return home, args, nil
}

const voiceWorkDir = "work"

func (p *VoiceProvider) connect(ctx context.Context, args []string) (*voiceConn, error) {
	open := p.open
	if open == nil {
		open = p.run
	}
	proc, err := open(ctx, args)
	if err != nil {
		return nil, err
	}
	c := &voiceConn{
		proc: proc, pending: map[int]chan message{},
		ready: make(chan struct{}), ended: make(chan struct{}), closed: make(chan struct{}),
	}
	go c.read()
	if err := c.initialize(ctx); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

// run starts Binary, after making sure the package has what a call needs.
func (p *VoiceProvider) run(ctx context.Context, args []string) (*voiceProcess, error) {
	if _, err := os.Stat(p.Binary); err != nil {
		return nil, &proto.APIError{Code: proto.CodeVoiceNoHarness, Message: "no codex"}
	}
	host := filepath.Join(filepath.Dir(filepath.Dir(p.Binary)), filepath.FromSlash(voiceHost))
	if _, err := os.Stat(host); err != nil {
		return nil, &proto.APIError{Code: proto.CodeVoiceNoComponent, Message: "no voice component"}
	}
	snap, err := p.Env.Environment(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("codex: voice environment: %w", err)
	}
	// Not ctx: the process outlives the request that starts the call.
	cmd := launchCommand(context.Background(), harness.Launch{Command: slices.Concat([]string{p.Binary}, args, []string{"app-server"}), Env: snap.Vars, Dir: os.TempDir()})
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: voice stdin: %w", err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: voice stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex: start voice process: %w", err)
	}
	return &voiceProcess{in: in, out: out, stop: func() error {
		_ = cmd.Process.Kill()
		_ = cmd.Wait() // killed on purpose; its status says nothing
		return nil
	}}, nil
}

// voiceConn is one app-server connection and, once begun, one call.
type voiceConn struct {
	proc *voiceProcess

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int
	pending map[int]chan message
	thread  string
	session string
	started bool
	answer  string
	err     error

	readyOnce, endOnce, closeOnce sync.Once
	ready, ended, closed          chan struct{}
	dir                           string // the call's private directory
	servers                       []string
	transcript                    func(role proto.Role, id, text string)
}

func (c *voiceConn) read() {
	defer close(c.closed)
	r := bufio.NewReader(c.proc.out)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			c.end(&proto.APIError{Code: proto.CodeVoiceMedia, Message: "codex ended"})
			return
		}
		var m message
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			c.refuse(m.ID)
		case m.Method != "":
			c.notification(m)
		default:
			c.response(m)
		}
	}
}

// refuse answers a request from the server: a call asks the user nothing.
func (c *voiceConn) refuse(id json.RawMessage) {
	_ = c.write(message{ID: id, Error: &rpcError{Code: -32601, Message: "not supported"}})
}

func (c *voiceConn) response(m message) {
	id, err := strconv.Atoi(string(m.ID))
	if err != nil {
		return
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func (c *voiceConn) notification(m message) {
	switch m.Method {
	case "thread/realtime/started":
		var p struct {
			RealtimeSessionID string `json:"realtimeSessionId"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		c.mu.Lock()
		if p.RealtimeSessionID == c.session {
			c.started = true
		}
		c.mu.Unlock()
		c.checkReady()
	case "thread/realtime/sdp":
		var p struct {
			SDP string `json:"sdp"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		c.mu.Lock()
		c.answer = p.SDP
		c.mu.Unlock()
		c.checkReady()
	case "thread/realtime/error":
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(m.Params, &p)
		c.end(classify(p.Message))
	case "thread/realtime/closed":
		c.end(nil)
	case "thread/realtime/item/completed":
		c.segment(m.Params)
	}
}

// segment passes a final piece of transcript on. Nothing else about the
// item is kept.
func (c *voiceConn) segment(params json.RawMessage) {
	var p struct {
		Item struct {
			Type string     `json:"type"`
			ID   string     `json:"id"`
			Role proto.Role `json:"role"`
			Text string     `json:"text"`
		} `json:"item"`
	}
	if c.transcript == nil || json.Unmarshal(params, &p) != nil {
		return
	}
	it := p.Item
	if it.Type != "transcriptSegment" || it.ID == "" || strings.TrimSpace(it.Text) == "" ||
		(it.Role != proto.RoleUser && it.Role != proto.RoleAssistant) {
		return
	}
	c.transcript(it.Role, it.ID, it.Text)
}

func (c *voiceConn) checkReady() {
	c.mu.Lock()
	ok := c.started && c.answer != ""
	c.mu.Unlock()
	if ok {
		c.readyOnce.Do(func() { close(c.ready) })
	}
}

func (c *voiceConn) end(err error) {
	c.endOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.ended)
	})
}

func (c *voiceConn) write(m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("codex: encode: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.proc.in.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("codex: write: %w", err)
	}
	return nil
}

func (c *voiceConn) request(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("codex: encode %s: %w", method, err)
	}
	ch := make(chan message, 1)
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(message{ID: json.RawMessage(strconv.Itoa(id)), Method: method, Params: raw}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("codex: %s: %w", method, ctx.Err())
	case <-c.closed:
		return fmt.Errorf("codex: %s: the app-server ended", method)
	case m := <-ch:
		if m.Error != nil {
			return classify(m.Error.Message)
		}
		if result != nil {
			if err := json.Unmarshal(m.Result, result); err != nil {
				return fmt.Errorf("codex: decode %s: %w", method, err)
			}
		}
		return nil
	}
}

func (c *voiceConn) initialize(ctx context.Context) error {
	if err := c.request(ctx, "initialize", initializeParams{
		ClientInfo:   clientInfo{Name: "hoplet", Title: "Hoplet", Version: "1"},
		Capabilities: clientCapabilities{ExperimentalAPI: true},
	}, nil); err != nil {
		return err
	}
	return c.write(message{Method: "initialized"})
}

// check refuses anything but a ChatGPT login on Codex's own service. Only
// the fields named here are looked at.
func (c *voiceConn) check(ctx context.Context) error {
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err := c.request(ctx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
		return err
	}
	if account.Account == nil || account.Account.Type != "chatgpt" {
		return &proto.APIError{Code: proto.CodeVoiceNotSubscribed, Message: "not a chatgpt login"}
	}
	var config struct {
		Config struct {
			ModelProvider  string                     `json:"model_provider"`
			ModelProviders map[string]json.RawMessage `json:"model_providers"`
			MCPServers     map[string]struct{}        `json:"mcp_servers"`
			WSBase         string                     `json:"experimental_realtime_ws_base_url"`
			WebRTCBase     string                     `json:"experimental_realtime_webrtc_call_base_url"`
			WSModel        string                     `json:"experimental_realtime_ws_model"`
		} `json:"config"`
	}
	if err := c.request(ctx, "config/read", map[string]any{"includeLayers": false, "cwd": os.TempDir()}, &config); err != nil {
		return err
	}
	cfg := config.Config
	for name := range cfg.MCPServers {
		c.servers = append(c.servers, name)
	}
	_, overridden := cfg.ModelProviders["openai"]
	if (cfg.ModelProvider != "" && cfg.ModelProvider != "openai") || overridden || cfg.WSBase != "" || cfg.WebRTCBase != "" || cfg.WSModel != "" {
		return &proto.APIError{Code: proto.CodeVoiceNotSubscribed, Message: "a custom service is configured"}
	}
	return nil
}

// begin opens the thread and starts the call. The response to the start is
// only an acknowledgement: the call is up once both notifications arrived.
func (c *voiceConn) begin(ctx context.Context, req voice.StartRequest) error {
	c.transcript = req.Transcript
	var th struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		ModelProvider string `json:"modelProvider"`
	}
	if err := c.request(ctx, "thread/start", threadParams{
		Cwd: filepath.Join(c.dir, voiceWorkDir), ApprovalPolicy: "never", Sandbox: "read-only", ApprovalsReviewer: "user", Ephemeral: true,
		DeveloperInstructions: voiceInstructions, Config: voiceThreadConfig(c.servers),
	}, &th); err != nil {
		return err
	}
	if th.ModelProvider != "openai" {
		return &proto.APIError{Code: proto.CodeVoiceNotSubscribed, Message: "a custom service is configured"}
	}
	session := rand.Text()
	c.mu.Lock()
	c.thread, c.session = th.Thread.ID, session
	c.mu.Unlock()
	params := map[string]any{
		"threadId":              th.Thread.ID,
		"transport":             map[string]any{"type": "webrtc", "sdp": req.Offer},
		"version":               "v3",
		"realtimeSessionId":     session,
		"outputModality":        "audio",
		"clientManagedHandoffs": false,
		"includeStartupContext": false,
	}
	if req.Voice != "" {
		params["voice"] = req.Voice
	}
	if err := c.request(ctx, "thread/realtime/start", params, nil); err != nil {
		return err
	}
	select {
	case <-c.ready:
		return nil
	case <-c.ended:
		if err := c.Err(); err != nil {
			return err
		}
		return errors.New("codex: the call closed before it began")
	case <-ctx.Done():
		return fmt.Errorf("codex: voice negotiation: %w", ctx.Err())
	}
}

// Answer implements voice.Call.
func (c *voiceConn) Answer() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.answer
}

// Done implements voice.Call.
func (c *voiceConn) Done() <-chan struct{} { return c.ended }

// Err implements voice.Call.
func (c *voiceConn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Stop implements voice.Call.
func (c *voiceConn) Stop(ctx context.Context) error {
	c.mu.Lock()
	thread := c.thread
	c.mu.Unlock()
	var err error
	select {
	case <-c.ended: // the service already ended it
	default:
		err = c.request(ctx, "thread/realtime/stop", map[string]any{"threadId": thread}, nil)
	}
	// The process goes either way, and takes the call with it.
	c.end(nil)
	c.close()
	return err
}

func (c *voiceConn) close() {
	c.closeOnce.Do(func() {
		_ = c.proc.in.Close()
		_ = c.proc.stop()
		<-c.closed
		if c.dir != "" {
			_ = os.RemoveAll(c.dir)
		}
	})
}

var statusPattern = regexp.MustCompile(`status (\d{3})`)

// classify reduces the service's text to a fixed code; the text itself goes
// no further.
func classify(text string) error {
	code := proto.CodeVoiceMedia
	if m := statusPattern.FindStringSubmatch(text); m != nil {
		switch m[1] {
		case "404":
			code = proto.CodeVoiceNotEnabled
		case "429":
			code = proto.CodeVoiceQuota
		}
	}
	if low := strings.ToLower(text); strings.Contains(low, "quota") || strings.Contains(low, "usage limit") {
		code = proto.CodeVoiceQuota
	}
	return &proto.APIError{Code: code, Message: "the voice service refused the call"}
}
