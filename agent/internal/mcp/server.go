package mcp

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

const (
	// maxRequest bounds one request on the socket.
	maxRequest = 64 << 10
	// readTimeout bounds how long a connection may take to send its request.
	readTimeout = 10 * time.Second
	// callBudget bounds one tool call in the daemon; the stdio server gives up a little later.
	callBudget = 55 * time.Second
)

// Backend performs a tool call for a session.
type Backend interface {
	Call(ctx context.Context, sessionID, tool string, args json.RawMessage) Reply
}

// Server listens on mcp.sock and answers the tool calls of the vendor processes that registered. Who calls is the
// socket's business (the directory 0700, the socket 0600, the peer's user); which session is the nonce's.
// Nothing the caller says about the session is believed.
type Server struct {
	path    string
	uid     uint32
	ln      net.Listener
	backend Backend

	mu       sync.Mutex
	sessions map[string]string // nonce -> session ID
	conns    map[net.Conn]struct{}
	closed   bool
	wg       sync.WaitGroup
}

// Listen creates `<home>/mcp.sock` (the directory mode 0700, the socket 0600) and starts answering. It fails when
// the path is too long for a Unix socket or the system cannot check who connects.
func Listen(home string, backend Backend) (*Server, error) {
	ln, path, err := localsock.Listen(home, SockName)
	if err != nil {
		return nil, fmt.Errorf("the MCP socket: %w", err)
	}
	s := newServer(path, uint32(os.Getuid()), ln, backend)
	go s.serve()
	return s, nil
}

func newServer(path string, uid uint32, ln net.Listener, backend Backend) *Server {
	return &Server{path: path, uid: uid, ln: ln, backend: backend, sessions: map[string]string{}, conns: map[net.Conn]struct{}{}}
}

// Path is the socket's path.
func (s *Server) Path() string { return s.path }

// Register names sessionID by nonce until the returned function is called.
func (s *Server) Register(nonce, sessionID string) (unregister func()) {
	s.mu.Lock()
	s.sessions[nonce] = sessionID
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.sessions, nonce)
		s.mu.Unlock()
	}
}

func (s *Server) session(nonce string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.sessions[nonce]
	return id, ok
}

// Close stops listening, ends the connections still open and removes the socket.
func (s *Server) Close() error {
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

func (s *Server) serve() {
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
func (s *Server) handle(c net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[agent] the MCP server failed on a call: %T", r)
			s.reply(c, errReply("Hoplet could not run this call"))
		}
	}()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return
	}
	if uid, err := localsock.PeerUID(uc); err != nil || uid != s.uid {
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(readTimeout))
	raw, err := io.ReadAll(io.LimitReader(c, maxRequest+1))
	if err != nil || len(raw) > maxRequest {
		s.reply(c, errReply("Hoplet could not read the request"))
		return
	}
	var req request
	if err := json.Unmarshal(raw, &req); err != nil || req.Nonce == "" || !IsReadTool(req.Tool) {
		s.reply(c, errReply("Hoplet could not read the request"))
		return
	}
	id, ok := s.session(req.Nonce)
	if !ok {
		s.reply(c, errReply("Hoplet does not know the session that made this call"))
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithTimeout(context.Background(), callBudget)
	defer cancel()
	rep := s.backend.Call(ctx, id, req.Tool, req.Args)
	rep.Text = scrub(rep.Text)
	s.reply(c, rep)
}

func (s *Server) reply(c net.Conn, r Reply) {
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write(b)
}
