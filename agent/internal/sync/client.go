package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
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
}

func NewClient(cfg *config.Config, deviceID, version string, hooks Hooks) *Client {
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
		Providers: c.hooks.Providers,
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
	c.cursors = make(map[string]int64, len(w.Sessions))
	c.titles = make(map[string]string, len(w.Sessions))
	c.ownerEpochs = make(map[string]int64, len(w.Sessions))
	for _, s := range w.Sessions {
		c.cursors[s.SessionID] = s.ByteSize
		c.titles[s.SessionID] = s.Title
		c.ownerEpochs[s.SessionID] = s.OwnerEpoch
		if s.OwnerDeviceID == c.deviceID {
			c.acceptOwnershipLocked(s.SessionID, s.ByteSize)
		}
	}
	c.mu.Unlock()

	c.cs.Store(cs)
	log.Printf("[agent] connected to %s, server has %d sessions", wsURL, len(w.Sessions))
	c.notifyWake()
	if c.hooks.Runtime.Connected != nil {
		go c.hooks.Runtime.Connected()
	}
	if c.hooks.Handoff.Welcomed != nil {
		go c.hooks.Handoff.Welcomed(w.Sessions)
	}

	go c.readLoop(cs)
	<-cs.done
	c.cs.CompareAndSwap(cs, nil)
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
			go c.handleWorkspaceDiff(cs, head.SessionID, head.RequestID, head.Path)
			continue
		}
		switch head.Type {
		case TypeDeviceProjectsList, TypeDeviceProjectsAdd, TypeDeviceProjectsRemove:
			go c.handleProjects(cs, raw)
			continue
		case TypeAttachmentPut:
			c.handleAttachment(cs, raw)
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
		r := reply{sessionID: head.SessionID, byteSize: head.ByteSize, reason: head.Reason}
		switch head.Type {
		case TypeSessionAck:
			r.kind = replyAck
			c.mu.Lock()
			c.cursors[head.SessionID] = head.ByteSize
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
