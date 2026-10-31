package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/yangflow/hoplet/proto"
)

const testTimeout = 10 * time.Second

// fakeConn is the daemon side of one connection.
type fakeConn struct {
	t  *testing.T
	ws *websocket.Conn
}

func (f *fakeConn) read() proto.ClientFrame {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	_, data, err := f.ws.Read(ctx)
	if err != nil {
		f.t.Errorf("fake read: %v", err)
		return proto.ClientFrame{}
	}
	var fr proto.ClientFrame
	if err := json.Unmarshal(data, &fr); err != nil {
		f.t.Errorf("fake decode: %v", err)
	}
	return fr
}

func (f *fakeConn) write(fr proto.ServerFrame) {
	f.t.Helper()
	b, err := json.Marshal(fr)
	if err != nil {
		f.t.Errorf("fake encode: %v", err)
		return
	}
	f.writeRaw(b)
}

func (f *fakeConn) writeRaw(b []byte) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := f.ws.Write(ctx, websocket.MessageText, b); err != nil {
		f.t.Errorf("fake write: %v", err)
	}
}

func (f *fakeConn) ok(id uint64, result any) {
	f.t.Helper()
	b, err := json.Marshal(result)
	if err != nil {
		f.t.Errorf("fake result: %v", err)
	}
	f.write(proto.ServerFrame{ID: id, Result: b})
}

func (f *fakeConn) fail(id uint64, code proto.ErrorCode) {
	f.write(proto.ServerFrame{ID: id, Error: &proto.APIError{Code: code, Message: "x"}})
}

// serveHello answers the hello request.
func (f *fakeConn) serveHello() {
	f.t.Helper()
	fr := f.read()
	if fr.Method != proto.MethodHello {
		f.t.Errorf("first frame method = %q", fr.Method)
	}
	f.ok(fr.ID, proto.HelloResult{Host: "fake", ProtocolMajor: proto.ProtocolMajor})
}

// startFake serves one connection with script and returns the client dialed
// to it. The script runs after hello and must return before the test ends.
func startFake(t *testing.T, script func(f *fakeConn)) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "fc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	scriptDone := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc(proto.APIPath, func(w http.ResponseWriter, r *http.Request) {
		defer close(scriptDone)
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer func() { _ = ws.CloseNow() }()
		f := &fakeConn{t: t, ws: ws}
		f.serveHello()
		script(f)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: testTimeout}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		select {
		case <-scriptDone:
		case <-time.After(testTimeout):
			t.Error("fake script did not finish")
		}
		_ = srv.Close()
		wg.Wait()
	})
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	c, err := Dial(ctx, Options{Socket: sock, Token: "t", DeviceID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func bg() context.Context { return context.Background() }

func TestCallFailsWhenConnectionClosed(t *testing.T) {
	gotReq := make(chan struct{})
	c := startFake(t, func(f *fakeConn) {
		f.read()
		close(gotReq)
		_ = f.ws.Close(websocket.StatusGoingAway, "bye")
	})
	err := c.Call(bg(), proto.MethodSessionsList, struct{}{}, nil)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("pending call err = %v, want ErrClosed", err)
	}
	<-gotReq
	if err := c.Call(bg(), proto.MethodSessionsList, struct{}{}, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("later call err = %v, want ErrClosed", err)
	}
	select {
	case <-c.Done():
	case <-time.After(testTimeout):
		t.Fatal("Done not closed")
	}
}

func TestMalformedFrameClosesConnection(t *testing.T) {
	c := startFake(t, func(f *fakeConn) {
		f.read()
		f.writeRaw([]byte("not json"))
		// Wait for the client to drop the connection.
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		_, _, _ = f.ws.Read(ctx)
	})
	err := c.Call(bg(), proto.MethodSessionsList, struct{}{}, nil)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

func TestSubscriptionItemsThenError(t *testing.T) {
	sent := make(chan struct{})
	release := make(chan struct{})
	c := startFake(t, func(f *fakeConn) {
		fr := f.read()
		f.ok(fr.ID, struct{}{})
		f.write(proto.ServerFrame{Sub: fr.ID, Item: json.RawMessage(`1`)})
		f.write(proto.ServerFrame{Sub: fr.ID, Item: json.RawMessage(`2`)})
		f.write(proto.ServerFrame{Sub: fr.ID, End: true, Error: &proto.APIError{Code: proto.CodeBusy, Message: "x"}})
		close(sent)
		<-release
	})
	s, err := c.Subscribe(bg(), proto.MethodSubscribeCatalog, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	<-sent
	for want := 1; want <= 2; want++ {
		var n int
		if err := s.Next(bg(), &n); err != nil || n != want {
			t.Fatalf("Next = %d, %v; want %d", n, err, want)
		}
	}
	var n int
	err = s.Next(bg(), &n)
	var ae *proto.APIError
	if !errors.As(err, &ae) || ae.Code != proto.CodeBusy {
		t.Fatalf("Next err = %v, want busy APIError", err)
	}
	close(release)
}

func TestCancelSubscription(t *testing.T) {
	canceled := make(chan proto.ClientFrame, 1)
	c := startFake(t, func(f *fakeConn) {
		fr := f.read()
		f.ok(fr.ID, struct{}{})
		f.write(proto.ServerFrame{Sub: fr.ID, Item: json.RawMessage(`1`)})
		cf := f.read()
		canceled <- cf
		f.write(proto.ServerFrame{Sub: fr.ID, End: true})
		// The next frame must be the ordinary call, not a second cancel.
		next := f.read()
		if next.Method != proto.MethodSessionsList {
			f.t.Errorf("next frame = %+v, want sessions.list", next)
		}
		f.ok(next.ID, []proto.Session{})
	})
	s, err := c.Subscribe(bg(), proto.MethodSubscribeCatalog, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(bg()); err != nil {
		t.Fatal(err)
	}
	cf := <-canceled
	if !cf.Cancel || cf.Sub != s.id {
		t.Fatalf("cancel frame = %+v, sub id %d", cf, s.id)
	}
	var n int
	if err := s.Next(bg(), &n); err != nil || n != 1 {
		t.Fatalf("Next = %d, %v", n, err)
	}
	if err := s.Next(bg(), &n); !errors.Is(err, ErrEnded) {
		t.Fatalf("Next err = %v, want ErrEnded", err)
	}
	if err := s.Cancel(bg()); err != nil {
		t.Fatalf("second Cancel = %v", err)
	}
	if _, err := c.Sessions(bg()); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribeCtxCanceledSendsCancel(t *testing.T) {
	gotReq := make(chan struct{})
	c := startFake(t, func(f *fakeConn) {
		fr := f.read()
		close(gotReq)
		cf := f.read()
		if !cf.Cancel || cf.Sub != fr.ID {
			f.t.Errorf("cancel frame = %+v, request id %d", cf, fr.ID)
		}
		next := f.read()
		f.ok(next.ID, []proto.Session{})
	})
	ctx, cancel := context.WithCancel(bg())
	go func() {
		<-gotReq
		cancel()
	}()
	_, err := c.Subscribe(ctx, proto.MethodSubscribeCatalog, struct{}{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := c.Sessions(bg()); err != nil {
		t.Fatalf("call after canceled subscribe: %v", err)
	}
}

func TestFailedSubscribe(t *testing.T) {
	c := startFake(t, func(f *fakeConn) {
		fr := f.read()
		f.fail(fr.ID, proto.CodeInvalid)
		next := f.read()
		f.ok(next.ID, []proto.Session{})
	})
	_, err := c.Subscribe(bg(), proto.MethodSubscribeCatalog, struct{}{})
	var ae *proto.APIError
	if !errors.As(err, &ae) || ae.Code != proto.CodeInvalid {
		t.Fatalf("err = %v, want invalid APIError", err)
	}
	if _, err := c.Sessions(bg()); err != nil {
		t.Fatal(err)
	}
}

func TestCallTimeoutKeepsConnection(t *testing.T) {
	gotReq := make(chan struct{})
	c := startFake(t, func(f *fakeConn) {
		f.read() // withheld
		close(gotReq)
		next := f.read()
		f.ok(next.ID, []proto.Session{})
	})
	ctx, cancel := context.WithCancel(bg())
	go func() {
		<-gotReq
		cancel()
	}()
	err := c.Call(ctx, proto.MethodSessionsList, struct{}{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := c.Sessions(bg()); err != nil {
		t.Fatalf("call after timeout: %v", err)
	}
}

func TestConcurrentCallsAndClose(t *testing.T) {
	const n = 20
	started := make(chan struct{}, n)
	c := startFake(t, func(f *fakeConn) {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			_, data, err := f.ws.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			var fr proto.ClientFrame
			if json.Unmarshal(data, &fr) != nil {
				return
			}
			b, _ := json.Marshal([]proto.Session{})
			out, _ := json.Marshal(proto.ServerFrame{ID: fr.ID, Result: b})
			wctx, wcancel := context.WithTimeout(context.Background(), testTimeout)
			err = f.ws.Write(wctx, websocket.MessageText, out)
			wcancel()
			if err != nil {
				return
			}
		}
	})
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			errs <- c.Call(bg(), proto.MethodSessionsList, struct{}{}, nil)
		}()
	}
	<-started
	if err := c.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, ErrClosed) {
			t.Errorf("call err = %v, want nil or ErrClosed", err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
}
