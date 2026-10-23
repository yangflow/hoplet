package daemon

import (
	"log"
	"os"
	"path/filepath"

	"github.com/yangflow/hoplet/agent/internal/localsock"
	"github.com/yangflow/hoplet/agent/internal/mcp"
	"github.com/yangflow/hoplet/agent/internal/runtime"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// mcpHost is the daemon's side of the session MCP server (ADR 0034): the socket the `hoplet-agent mcp` processes
// call, the session tokens held in memory and the core client that uses them.
type mcpHost struct {
	server *mcp.Server
	tokens *mcp.Tokens
	exe    string
}

// openMCP starts the MCP host when `session_mcp` is on. Without the socket or the binary the switch stays off for
// this run, and the agent does not announce the capability.
func (b *bridge) openMCP() {
	if !b.cfg.SessionMCP {
		return
	}
	exe, err := os.Executable()
	if err == nil {
		if r, e := filepath.EvalSymlinks(exe); e == nil {
			exe = r
		}
		exe, err = filepath.Abs(exe)
	}
	if err != nil {
		log.Printf("[agent] session_mcp is off: the agent binary is not known (%v)", err)
		return
	}
	tokens := mcp.NewTokens(func(requestID, sessionID string) error {
		return b.send(sync.RuntimeMcpTokenRequest{
			V: sync.ProtocolVersion, Type: sync.TypeRuntimeMcpTokenRequest, RequestID: requestID, SessionID: sessionID,
		})
	})
	srv, err := mcp.Listen(b.cfg.HopletHome, mcp.NewCore(b.cfg.ServerURL, tokens))
	if err != nil {
		log.Printf("[agent] session_mcp is off: %v", err)
		return
	}
	b.mcp = &mcpHost{server: srv, tokens: tokens, exe: exe}
}

// mcpSpec is what a Claude process gets to reach the server: the command that serves MCP, the socket and a nonce
// that names the session on it. The nonce is new for every process, and Release ends it with the process.
func (b *bridge) mcpSpec(sessionID string) *runtime.MCPSpec {
	h := b.mcp
	if h == nil {
		return nil
	}
	nonce, err := localsock.NewNonce()
	if err != nil {
		log.Printf("[agent] a session starts without the MCP server: %v", err)
		return nil
	}
	unregister := h.server.Register(nonce, sessionID)
	return &runtime.MCPSpec{
		Command:     h.exe,
		Args:        []string{"mcp"},
		Env:         map[string]string{mcp.SockEnv: h.server.Path(), mcp.NonceEnv: nonce},
		Nonce:       nonce,
		ReadAllowed: true,
		Release: func() {
			unregister()
			h.tokens.Forget(sessionID)
		},
	}
}

// mcpToken hands the core's answer to the call that waits for it.
func (b *bridge) mcpToken(t sync.RuntimeMcpToken) {
	if h := b.mcp; h != nil {
		h.tokens.Deliver(mcp.TokenReply{
			RequestID: t.RequestID, SessionID: t.SessionID, Token: t.Token, ExpiresAt: t.ExpiresAt, Reason: t.Reason,
		})
	}
}
