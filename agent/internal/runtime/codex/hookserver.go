package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/yangflow/hoplet/agent/internal/localsock"
)

// The rule hook of Codex (ADR 0029, ADR 0032): Codex runs `hoplet-agent hook codex` before each tool call, and
// that short-lived process asks the daemon over a Unix socket. The nonce in the hook's environment names the
// Codex process; nothing the hook says about the session is believed.
const (
	// HookSockName is the socket's file name in the agent's state root.
	HookSockName = "hook.sock"
	// HookSockEnv and HookNonceEnv are the variables the daemon puts in the environment of a Codex process: the
	// socket to ask and the nonce that names the process.
	HookSockEnv  = "HOPLET_HOOK_SOCK"
	HookNonceEnv = "HOPLET_HOOK_NONCE"

	// MaxHookRequest bounds one request on the socket; a larger one is a deny.
	MaxHookRequest = 1 << 20
	// maxHookReply bounds the reply the hook reads.
	maxHookReply = 64 << 10
	// hookTimeoutSeconds is the timeout Codex gives the hook command. Codex lets a call through when the hook
	// times out, so the hook and the daemon answer before it.
	hookTimeoutSeconds = 900
	// HookClientTimeout is when the hook gives up on the daemon and prints a deny.
	HookClientTimeout = 870 * time.Second
	// hookAskTimeout is when the daemon resolves a decision that waits for a person: an unanswered ask is a deny.
	hookAskTimeout = 860 * time.Second
	// hookReadTimeout bounds how long a connection may take to send its request.
	hookReadTimeout = 10 * time.Second
)

// Decisions of a hook reply. Codex supports only deny as a hook decision: an allow and a pass both print nothing
// that decides and Codex goes on with its own approval flow. They differ in the daemon, which answers Codex's
// approval of an allowed call by the rule (hookAllow) and leaves the approval of a pass to the user.
const (
	hookAllow = "allow"
	hookDeny  = "deny"
	hookPass  = "pass"
)

// hookRequest is what the hook sends: the nonce and the hook input exactly as Codex wrote it.
type hookRequest struct {
	Nonce string          `json:"nonce"`
	Input json.RawMessage `json:"input"`
}

// hookReply is the daemon's answer. The reason names a failure or a rule, never the input.
type hookReply struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

func denyReply(reason string) hookReply { return hookReply{Decision: hookDeny, Reason: reason} }

// hookTarget decides the hook calls of one Codex process.
type hookTarget interface {
	decideHook(ctx context.Context, input json.RawMessage) hookReply
}

// HookServer listens on the hook socket and answers the hooks of the Codex processes that registered.
type HookServer struct {
	path string
	uid  uint32
	ln   net.Listener

	askTimeout time.Duration

	mu      sync.Mutex
	targets map[string]hookTarget
	conns   map[net.Conn]struct{}
	closed  bool
	wg      sync.WaitGroup
}

// ListenHook creates the socket `<home>/hook.sock` (the directory mode 0700, the socket 0600) and starts
// answering. It fails when the path is too long for a Unix socket; the caller then runs without the hook.
func ListenHook(home string) (*HookServer, error) {
	ln, path, err := localsock.Listen(home, HookSockName)
	if err != nil {
		return nil, fmt.Errorf("the hook socket: %w", err)
	}
	s := newHookServer(path, uint32(os.Getuid()), ln)
	go s.serve()
	return s, nil
}

func newHookServer(path string, uid uint32, ln net.Listener) *HookServer {
	return &HookServer{path: path, uid: uid, ln: ln, askTimeout: hookAskTimeout, targets: map[string]hookTarget{}, conns: map[net.Conn]struct{}{}}
}

// Path is the socket's path.
func (s *HookServer) Path() string { return s.path }

// Close stops listening, ends the connections still open (their hooks fail closed) and removes the socket.
func (s *HookServer) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	err := s.ln.Close()
	s.wg.Wait()
	_ = os.Remove(s.path)
	return err
}

// newNonce is a random 256-bit value, hex encoded.
func newNonce() (string, error) { return localsock.NewNonce() }

// register names target by nonce until the returned function is called.
func (s *HookServer) register(nonce string, target hookTarget) func() {
	s.mu.Lock()
	s.targets[nonce] = target
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.targets, nonce)
		s.mu.Unlock()
	}
}

func (s *HookServer) target(nonce string) hookTarget {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.targets[nonce]
}

func (s *HookServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = c.Close()
			return
		}
		s.conns[c] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.conns, c)
				s.mu.Unlock()
				_ = c.Close()
			}()
			s.handle(c)
		}()
	}
}

// handle answers one connection: it checks who is calling, reads one bounded request and writes one reply. A
// caller of another user gets no reply.
func (s *HookServer) handle(c net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[agent] the Codex rule hook failed on a call: %T", r)
			s.reply(c, denyReply("Hoplet could not check this call against the permission rules"))
		}
	}()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return
	}
	if uid, err := peerUID(uc); err != nil || uid != s.uid {
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(hookReadTimeout))
	raw, err := io.ReadAll(io.LimitReader(c, MaxHookRequest+1))
	if err != nil {
		s.reply(c, denyReply("Hoplet could not read the hook request"))
		return
	}
	if len(raw) > MaxHookRequest {
		s.reply(c, denyReply("The hook request is larger than Hoplet accepts"))
		return
	}
	var req hookRequest
	if err := json.Unmarshal(raw, &req); err != nil || req.Nonce == "" || len(req.Input) == 0 {
		s.reply(c, denyReply("Hoplet could not read the hook request"))
		return
	}
	t := s.target(req.Nonce)
	if t == nil {
		s.reply(c, denyReply("Hoplet does not know the Codex process that made this call"))
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithTimeout(context.Background(), s.askTimeout)
	defer cancel()
	s.reply(c, t.decideHook(ctx, req.Input))
}

func (s *HookServer) reply(c net.Conn, r hookReply) {
	if r.Decision == hookDeny && r.Reason == "" {
		r.Reason = "Denied by Hoplet"
	}
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write(b)
}
