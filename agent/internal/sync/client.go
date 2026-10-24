package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yangflow/hoplet/agent/internal/config"
)

const (
	maxBatchBytes  = 1 << 20 // raw bytes carried per message
	maxResyncTries = 3
)

type replyKind int

const (
	replyAck replyKind = iota
	replyResync
	replyRejected
)

type reply struct {
	kind      replyKind
	sessionID string
	byteSize  int64
	reason    string
}

type connState struct {
	conn    *websocket.Conn
	replies chan reply
	gen     uint64
	done    chan struct{}

	// The writer (lanes.go) drains control before bulk; stop ends it.
	control  chan outbound
	bulk     chan outbound
	stop     chan struct{}
	stopOnce sync.Once

	// welcomed is set for a connection that got its welcome and replayed the journal; only such a connection
	// names its transcript backlog (backlogSent, set by Reconcile) and reports the running sessions.
	welcomed    bool
	backlogSent bool
	// reportOnce reports the running sessions once this connection caught up (ADR 0039).
	reportOnce sync.Once
}

type Client struct {
	cfg         *config.Config
	deviceID    string
	version     string
	hooks       Hooks
	handoffMu   sync.Mutex
	cloneMu     sync.Mutex
	clones      map[string]context.CancelFunc // requestId -> cancel of a running clone
	handoffTail chan struct{}

	cs      atomic.Pointer[connState]
	gen     atomic.Uint64
	mu      sync.RWMutex
	cursors map[string]int64  // sessionId -> server-received bytes (the sync cursor)
	titles  map[string]string // sessionId -> last title pushed to the core

	ownershipEpoch map[string]uint64
	// ownerEpochs is the core's owner epoch of each session, as of the welcome and the last commit.
	ownerEpochs map[string]int64
	// failedHandoffs are the handoffs whose failure was reported: their remaining chunks are dropped.
	failedHandoffs map[string]bool
	suppressed     map[string]bool
	rewritten      map[string]bool
	wake           chan struct{}
	sched          scheduler // used by Reconcile only

	// journal keeps the runtime events the core has not confirmed (journal.go, ADR 0039). sendMu orders a
	// journalled send against the replay after a reconnect; live is the connection that replay finished on.
	journal *journal
	sendMu  sync.Mutex
	live    *connState
	// mine is the sessions this device owned as of the welcome and its own commits; diverged is the sessions it
	// owned before a reconnect and another device owns now, with the cursor and epoch it held (guarded by mu).
	mine     map[string]bool
	diverged map[string]divergence
}

// divergence is where a session stood when this device stopped being its owner.
type divergence struct {
	cursor int64
	epoch  int64
}

// reportFallback is how long a reconnect waits for the transcript backlog before it reports the running sessions
// anyway.
const reportFallback = 60 * time.Second

func NewClient(cfg *config.Config, deviceID, version string, hooks Hooks) *Client {
	home := ""
	if cfg != nil {
		home = cfg.HopletHome
	}
	return &Client{
		cfg:            cfg,
		deviceID:       deviceID,
		version:        version,
		hooks:          hooks,
		clones:         map[string]context.CancelFunc{},
		cursors:        map[string]int64{},
		titles:         map[string]string{},
		ownershipEpoch: map[string]uint64{},
		ownerEpochs:    map[string]int64{},
		failedHandoffs: map[string]bool{},
		suppressed:     map[string]bool{},
		rewritten:      map[string]bool{},
		wake:           make(chan struct{}, 1),
		journal:        openJournal(home),
		mine:           map[string]bool{},
		diverged:       map[string]divergence{},
	}
}

func (c *Client) Wake() <-chan struct{} { return c.wake }

// DeviceID is the ID this client introduces itself with.
func (c *Client) DeviceID() string { return c.deviceID }

func (c *Client) Kick() { c.notifyWake() }

func (c *Client) notifyWake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Run 维持与服务端的长连接，断线自动重连（指数退避）。
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("[agent] disconnected: %v; retrying in %v", err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (c *Client) runOnce(ctx context.Context) error {
	wsURL, err := wsEndpoint(c.cfg.ServerURL)
	if err != nil {
		return fmt.Errorf("invalid server_url: %w", err)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	hdr := http.Header{"Authorization": {"Bearer " + c.cfg.Token}}
	conn, resp, err := dialer.DialContext(ctx, wsURL, hdr)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("authentication failed (401); check the token")
		}
		return err
	}

	cs := newConnState(conn, make(chan reply, 256))
	cs.gen = c.gen.Add(1)
	defer cs.close()

	// Introduce the device first (the core requires hello before session.append), then read welcome
	hello := DeviceHello{
		V:         ProtocolVersion,
		Type:      TypeDeviceHello,
		DeviceID:  c.deviceID,
		Name:      c.cfg.DeviceName,
		Version:   c.version,
		System:    &DeviceSystem{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Providers: c.providerList(),

		PreviewHost: c.hooks.PreviewHost,

		PermissionDeny: c.hooks.PermissionDeny,
	}
	if c.hooks.ScriptHooks != nil {
		hello.Hooks = c.hooks.ScriptHooks()
	}
	if c.hooks.Terminal.Enabled || c.hooks.SessionMCP {
		hello.Capabilities = &DeviceCapabilities{Terminal: c.hooks.Terminal.Enabled, SessionMcp: c.hooks.SessionMCP}
	}
	if err := c.writeMsg(cs, hello); err != nil {
		conn.Close()
		return fmt.Errorf("send hello: %w", err)
	}

	// Read welcome and refresh the cursors
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var w Welcome
	if err := conn.ReadJSON(&w); err != nil {
		conn.Close()
		return fmt.Errorf("waiting for welcome: %w", err)
	}
	conn.SetReadDeadline(time.Time{})
	if w.V != ProtocolVersion {
		conn.Close()
		return fmt.Errorf("the core speaks protocol version %d, this agent %d; upgrade both together", w.V, ProtocolVersion)
	}
	c.mu.Lock()
	for _, s := range w.Sessions {
		// A session this device owned that another device took while it was away: what it wrote since is
		// reported as diverged by Reconcile.
		if c.mine[s.SessionID] && s.OwnerDeviceID != "" && s.OwnerDeviceID != c.deviceID {
			c.diverged[s.SessionID] = divergence{cursor: c.cursors[s.SessionID], epoch: c.ownerEpochs[s.SessionID]}
		}
	}
	c.cursors = make(map[string]int64, len(w.Sessions))
	c.titles = make(map[string]string, len(w.Sessions))
	c.ownerEpochs = make(map[string]int64, len(w.Sessions))
	c.mine = make(map[string]bool, len(w.Sessions))
	for _, s := range w.Sessions {
		c.cursors[s.SessionID] = s.ByteSize
		c.titles[s.SessionID] = s.Title
		c.ownerEpochs[s.SessionID] = s.OwnerEpoch
		if s.OwnerDeviceID == c.deviceID {
			c.mine[s.SessionID] = true
			delete(c.diverged, s.SessionID)
			c.acceptOwnershipLocked(s.SessionID, s.ByteSize)
		}
	}
	c.mu.Unlock()

	// The journal first, in order, on the control lane: then the transcript backlog (Reconcile), then the state
	// of the running sessions (report).
	c.journal.welcome(w.EventSeq)
	go c.readLoop(cs)
	c.sendMu.Lock()
	replayed := c.journal.pending()
	for _, raw := range replayed {
		if err := c.writeMsg(cs, json.RawMessage(raw)); err != nil {
			break // they stay journalled for the next connection
		}
	}
	c.live = cs
	cs.welcomed = true
	c.sendMu.Unlock()
	if c.hooks.Runtime.Link != nil {
		c.hooks.Runtime.Link(true)
	}

	c.cs.Store(cs)
	log.Printf("[agent] connected to %s, server has %d sessions, %d journalled events replayed", wsURL, len(w.Sessions), len(replayed))
	c.notifyWake()
	fallback := time.AfterFunc(reportFallback, func() { c.report(cs) })
	defer fallback.Stop()
	if c.hooks.Connected != nil {
		go c.hooks.Connected()
	}
	if c.hooks.Handoff.Welcomed != nil {
		go c.hooks.Handoff.Welcomed(w.Sessions)
	}

	<-cs.done
	c.sendMu.Lock()
	if c.live == cs {
		c.live = nil
	}
	c.sendMu.Unlock()
	if c.hooks.Runtime.Link != nil {
		c.hooks.Runtime.Link(false)
	}
	c.cs.CompareAndSwap(cs, nil)
	if c.hooks.Ports.Watch != nil {
		c.hooks.Ports.Watch(nil) // the core asks again after it connects
	}
	if c.hooks.Terminal.Disconnected != nil {
		c.hooks.Terminal.Disconnected()
	}
	return errors.New("reader exited")
}

func (c *Client) readLoop(cs *connState) {
	defer close(cs.done)
	conn := cs.conn
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			if err := c.writeMsg(cs, PingMsg{V: ProtocolVersion, Type: TypePingMsg, T: time.Now().UnixMilli()}); err != nil {
				return
			}
		}
	}()
	for {
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		var raw json.RawMessage
		if err := conn.ReadJSON(&raw); err != nil {
			log.Printf("[agent] read loop ended: %v", err)
			return
		}
		var head struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			ByteSize  int64  `json:"byteSize"`
			Reason    string `json:"reason"`
			Code      string `json:"code"`
			Message   string `json:"message"`
			PromptID  string `json:"promptId"`
			Decision  string `json:"decision"`
			Mode      string `json:"mode"`
			RequestID string `json:"requestId"`
			Path      string `json:"path"`
			At        string `json:"at"`
			Base      bool   `json:"base"`
			Cursor    int64  `json:"cursor"`
			Limit     int    `json:"limit"`
			Origin    string `json:"origin"`
			CloneInto string `json:"cloneInto"`
			HandoffID string `json:"handoffId"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			continue
		}
		if isHandoffMessage(head.Type) {
			typ := head.Type
			c.enqueueHandoff(cs, func() { c.handleHandoff(cs, typ, raw) })
			continue
		}
		if head.Type == TypePortsWatch {
			c.handlePortsWatch(raw)
			continue
		}
		if strings.HasPrefix(head.Type, "terminal.") {
			// In order: the input after an open must find the terminal.
			c.handleTerminal(cs, head.Type, raw)
			continue
		}
		switch head.Type {
		case TypeRuntimePrompt:
			go c.handleRuntimePrompt(cs, raw)
			continue
		case TypeRuntimeSteer:
			go c.handleRuntimeSteer(cs, raw)
			continue
		case TypeRuntimePermissionAnswer:
			go c.handleRuntimeAnswer(head.SessionID, head.RequestID, head.Decision, head.Message, head.Mode)
			continue
		case TypeRuntimeStart:
			go c.handleRuntimeStart(cs, raw)
			continue
		case TypeRuntimeMcpToken:
			var tok RuntimeMcpToken
			if c.hooks.McpToken != nil && json.Unmarshal(raw, &tok) == nil {
				c.hooks.McpToken(tok)
			}
			continue
		case TypeRuntimeInterrupt:
			go c.handleRuntimeInterrupt(head.SessionID)
			continue
		case TypeRuntimeConfig:
			// In order with the prompt that follows it: a setting changed before a prompt holds for that turn.
			c.handleRuntimeConfig(raw)
			continue
		case TypeRuntimeStop:
			if c.hooks.Runtime.Stop != nil {
				go c.hooks.Runtime.Stop(head.SessionID)
			}
			continue
		}
		if head.Type == TypeSessionWorkspaceList {
			go c.handleWorkspace(cs, head.SessionID, head.RequestID)
			continue
		}
		if head.Type == TypeSessionWorkspaceFileRead {
			go c.handleWorkspaceFile(cs, head.SessionID, head.RequestID, head.Path, head.At)
			continue
		}
		if head.Type == TypeSessionWorkspaceDiffRead {
			go c.handleWorkspaceDiff(cs, head.SessionID, head.RequestID, head.Path, WorkspaceDiffPage{Base: head.Base, Cursor: head.Cursor, Limit: head.Limit})
			continue
		}
		if head.Type == TypeSessionWorkspaceChangesRead {
			go c.handleWorkspaceChanges(cs, head.SessionID, head.RequestID)
			continue
		}
		if head.Type == TypeSessionConfigRead {
			go c.handleConfigRead(cs, raw)
			continue
		}
		if head.Type == TypeRuntimeConfigPreview {
			go c.handleConfigPreview(cs, raw)
			continue
		}
		if head.Type == TypeRuntimeConfigApply {
			go c.handleConfigApply(cs, raw)
			continue
		}
		switch head.Type {
		case TypeDeviceProjectsList, TypeDeviceProjectsAdd, TypeDeviceProjectsRemove:
			go c.handleProjects(cs, raw)
			continue
		case TypeAttachmentPut:
			c.handleAttachment(cs, raw)
			continue
		case TypeHookEvent:
			c.handleHookEvent(raw)
			continue
		case TypeDeviceRules:
			// In order: a later push must not be overtaken by an earlier one.
			c.handleRules(cs, raw)
			continue
		case TypeDeviceProvidersUpdate:
			go c.handleProvidersUpdate(cs, raw)
			continue
		case TypeDeviceDirsList:
			go c.handleDirs(cs, head.RequestID, head.Path)
			continue
		case TypeDeviceCloneFind:
			go c.handleClone(cs, head.RequestID, head.Origin)
			continue
		case TypeDeviceCloneCheck:
			go c.handleCloneCheck(cs, head.RequestID, head.Origin, head.CloneInto)
			continue
		case TypeDeviceCloneStart:
			// Registered here, in order, so a cancel that follows finds it.
			ctx := c.beginClone(cs, head.RequestID)
			go c.handleCloneStart(ctx, cs, head.RequestID, head.HandoffID, head.Origin, head.CloneInto)
			continue
		case TypeDeviceCloneCancel:
			c.cancelClone(head.RequestID)
			continue
		case TypeDeviceSessionsImport, TypeDeviceSessionsRemove:
			go c.handleSessions(cs, raw)
			continue
		case TypeDeviceSessionInspect, TypeDeviceSessionDelete, TypeDeviceSessionForget:
			go c.handleSessionDeletion(cs, head.Type, raw)
			continue
		}
		if head.Type == TypeJournalAck {
			var ack JournalAck
			if json.Unmarshal(raw, &ack) == nil {
				c.journal.ack(ack.EventSeq)
			}
			continue
		}
		r := reply{sessionID: head.SessionID, byteSize: head.ByteSize, reason: head.Reason}
		switch head.Type {
		case TypeSessionAck:
			r.kind = replyAck
			c.mu.Lock()
			c.cursors[head.SessionID] = head.ByteSize
			c.mine[head.SessionID] = true
			c.mu.Unlock()
		case TypeSessionResync:
			r.kind = replyResync
			c.mu.Lock()
			c.cursors[head.SessionID] = head.ByteSize
			c.mu.Unlock()
		case TypeSessionRejected:
			r.kind = replyRejected
		case TypeErrorMsg:
			if head.Code == CodeProtocolVersionMismatch {
				log.Printf("[agent] the core refused protocol version %d: %s; upgrade the agent and the core together", ProtocolVersion, head.Message)
				continue
			}
			log.Printf("[agent] server error: %s %s", head.Code, head.Message)
			continue
		default:
			continue // welcome, appended broadcasts, pong and the like are not for the sender
		}
		select {
		case cs.replies <- r:
		case <-cs.done:
			return
		}
	}
}

// Called with mu held; late rejection of an older sync attempt cannot suppress this ownership epoch.
func (c *Client) acceptOwnershipLocked(sessionID string, cursor int64) {
	c.ownershipEpoch[sessionID]++
	c.cursors[sessionID] = cursor
	delete(c.suppressed, sessionID)
	delete(c.rewritten, sessionID)
	delete(c.titles, sessionID)
}

// SendJournaled journals msg and sends it when a connection is up (ADR 0039). An event the core does not confirm is
// sent again, marked replayed, after the next welcome; so a write that fails is not an error.
func (c *Client) SendJournaled(msg any) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	raw, err := c.journal.append(msg)
	if err != nil {
		return err
	}
	if c.live != nil {
		_ = c.writeMsg(c.live, json.RawMessage(raw))
	}
	return nil
}

// report has the agent report its running sessions on cs, once, after the journal and the transcript backlog;
// runtime.reported follows them.
func (c *Client) report(cs *connState) {
	if !cs.welcomed || c.cs.Load() != cs {
		return
	}
	cs.reportOnce.Do(func() {
		if c.hooks.Runtime.Connected != nil {
			go c.hooks.Runtime.Connected()
			return
		}
		_ = c.writeMsg(cs, RuntimeReported{V: ProtocolVersion, Type: TypeRuntimeReported})
	})
}
