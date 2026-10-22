package codex

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeTarget struct {
	reply hookReply
	block chan struct{}
	got   chan json.RawMessage
	ctxs  chan context.Context
}

func (f *fakeTarget) decideHook(ctx context.Context, in json.RawMessage) hookReply {
	if f.got != nil {
		f.got <- in
	}
	if f.ctxs != nil {
		f.ctxs <- ctx
	}
	if f.block != nil {
		<-f.block
	}
	return f.reply
}

// shortHome is a state root short enough for a socket path on macOS.
func shortHome(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "hk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func startServer(t *testing.T) *HookServer {
	t.Helper()
	s, err := ListenHook(filepath.Join(shortHome(t), "h"))
	if err != nil {
		t.Skipf("no hook server here: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func call(t *testing.T, path string, payload []byte) (hookReply, bool) {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	// The server may answer an oversize request before it has read all of it.
	_, _ = c.Write(payload)
	if uc, ok := c.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	b, _ := io.ReadAll(c)
	if len(b) == 0 {
		return hookReply{}, false
	}
	var r hookReply
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("reply %q: %v", b, err)
	}
	return r, true
}

func req(nonce string, input string) []byte {
	b, _ := json.Marshal(map[string]any{"nonce": nonce, "input": json.RawMessage(input)})
	return b
}

func TestListenHookModes(t *testing.T) {
	s := startServer(t)
	if st, _ := os.Stat(filepath.Dir(s.Path())); st.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %v", st.Mode().Perm())
	}
	if st, _ := os.Stat(s.Path()); st.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v", st.Mode().Perm())
	}
	if filepath.Base(s.Path()) != HookSockName {
		t.Errorf("name %s", s.Path())
	}
}

func TestListenHookReplacesStaleSocketButNotAFile(t *testing.T) {
	home := shortHome(t)
	s, err := ListenHook(home)
	if err != nil {
		t.Skip(err)
	}
	// Leave the socket file behind as a crash would.
	if ul, ok := s.ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	_ = s.Close()
	_ = os.Remove(s.Path())
	l, err := net.Listen("unix", s.Path())
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()
	s2, err := ListenHook(home)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	_ = s2.Close()
	if err := os.WriteFile(filepath.Join(home, HookSockName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenHook(home); err == nil {
		t.Error("a regular file was removed")
	}
}

func TestListenHookRefusesALongPath(t *testing.T) {
	if _, err := ListenHook(filepath.Join(shortHome(t), strings.Repeat("d", 120))); err == nil {
		t.Error("a path over the limit was accepted")
	}
}

func TestHookServerDecides(t *testing.T) {
	s := startServer(t)
	tg := &fakeTarget{reply: hookReply{Decision: hookAllow}, got: make(chan json.RawMessage, 1)}
	nonce, _ := newNonce()
	unregister := s.register(nonce, tg)
	r, ok := call(t, s.Path(), req(nonce, `{"tool_name":"Bash"}`))
	if !ok || r.Decision != hookAllow {
		t.Fatalf("reply %+v %v", r, ok)
	}
	if got := <-tg.got; string(got) != `{"tool_name":"Bash"}` {
		t.Errorf("input %s", got)
	}
	unregister()
	if r, _ := call(t, s.Path(), req(nonce, `{"a":1}`)); r.Decision != hookDeny || r.Reason == "" {
		t.Errorf("unregistered nonce: %+v", r)
	}
}

func TestHookServerDeniesWhatItCannotTrust(t *testing.T) {
	s := startServer(t)
	nonce, _ := newNonce()
	s.register(nonce, &fakeTarget{reply: hookReply{Decision: hookAllow}})
	cases := map[string][]byte{
		"unknown nonce": req("not-a-nonce", `{"a":1}`),
		"empty nonce":   req("", `{"a":1}`),
		"not json":      []byte("hello"),
		"no input":      []byte(`{"nonce":"` + nonce + `"}`),
		"oversize":      req(nonce, `{"x":"`+strings.Repeat("a", MaxHookRequest)+`"}`),
		"empty":         nil,
	}
	for name, p := range cases {
		r, ok := call(t, s.Path(), p)
		if !ok || r.Decision != hookDeny || r.Reason == "" {
			t.Errorf("%s: %+v %v", name, r, ok)
		}
	}
}

func TestHookServerIgnoresAnotherUser(t *testing.T) {
	if !peerCredSupported {
		t.Skip("no peer credentials here")
	}
	path := filepath.Join(shortHome(t), "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Skip(err)
	}
	s := newHookServer(path, uint32(os.Getuid())+1, ln)
	go s.serve()
	t.Cleanup(func() { _ = s.Close() })
	nonce, _ := newNonce()
	s.register(nonce, &fakeTarget{reply: hookReply{Decision: hookAllow}})
	if r, ok := call(t, path, req(nonce, `{"a":1}`)); ok {
		t.Errorf("a caller of another user got %+v", r)
	}
}

func TestHookServerCloseEndsWaitingHooks(t *testing.T) {
	s := startServer(t)
	tg := &fakeTarget{block: make(chan struct{}), got: make(chan json.RawMessage, 1)}
	defer close(tg.block)
	nonce, _ := newNonce()
	s.register(nonce, tg)
	done := make(chan bool, 1)
	go func() { _, ok := call(t, s.Path(), req(nonce, `{"a":1}`)); done <- ok }()
	<-tg.got
	go func() { _ = s.Close() }()
	select {
	case ok := <-done:
		if ok {
			t.Error("a reply came after Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting hook was not ended")
	}
}

func TestHookServerGivesTheDecisionEightHundredSixtySeconds(t *testing.T) {
	s := startServer(t)
	tg := &fakeTarget{reply: hookReply{Decision: hookAllow}, ctxs: make(chan context.Context, 1)}
	nonce, _ := newNonce()
	s.register(nonce, tg)
	call(t, s.Path(), req(nonce, `{"a":1}`))
	dl, ok := (<-tg.ctxs).Deadline()
	if left := time.Until(dl); !ok || left < 850*time.Second || left > 860*time.Second {
		t.Errorf("deadline in %v", left)
	}
}

func TestNonceIsAtLeast128BitsAndUnique(t *testing.T) {
	a, _ := newNonce()
	b, _ := newNonce()
	if len(a) < 32 || a == b {
		t.Errorf("%q %q", a, b)
	}
}
