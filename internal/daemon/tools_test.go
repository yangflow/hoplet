package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/frame"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// dialTools connects as the tool connection of the session holding token.
func dialTools(ctx context.Context, h, token string) (*client.Client, error) {
	return client.Dial(ctx, client.Options{Socket: home.Socket(h), SessionToken: token})
}

func wantUnauthorized(t *testing.T, err error) {
	t.Helper()
	var ae *proto.APIError
	if !errors.As(err, &ae) || ae.Code != proto.CodeUnauthorized {
		t.Fatalf("got %v, want unauthorized", err)
	}
}

func callTool(t *testing.T, c *client.Client, name, args string) proto.ToolsCallResult {
	t.Helper()
	var r proto.ToolsCallResult
	if err := c.Call(timeout(t), proto.MethodToolsCall, proto.ToolsCallParams{Name: name, Arguments: json.RawMessage(args)}, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestToolConnections(t *testing.T) {
	h, script := newHome(t, fake.Script{})
	d, err := Start(context.Background(), testConfig(h, script))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	c := dial(t, h, "test")
	a, b := create(t, c, h), create(t, c, h)
	now := time.Now()
	if err := d.store.SetSessionToken(timeout(t), a.ID, store.TokenHash("token-a"), now); err != nil {
		t.Fatal(err)
	}
	if err := d.store.SetSessionToken(timeout(t), b.ID, store.TokenHash("token-b"), now); err != nil {
		t.Fatal(err)
	}

	tc, err := dialTools(timeout(t), h, "token-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tc.Close() })

	var list proto.ToolsListResult
	if err := tc.Call(timeout(t), proto.MethodToolsList, nil, &list); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "sessions_list,session_get,artifact_publish,simulator_list,simulator_boot,simulator_launch,simulator_screenshot,simulator_open_url" {
		t.Fatalf("tools = %v", names)
	}

	r := callTool(t, tc, "sessions_list", "")
	var sessions []proto.Session
	if err := json.Unmarshal([]byte(r.Text), &sessions); r.IsError || err != nil || len(sessions) != 2 {
		t.Fatalf("sessions_list = %+v (%v)", r, err)
	}
	r = callTool(t, tc, "session_get", `{"id":"`+string(b.ID[:8])+`"}`)
	var st proto.SessionState
	if err := json.Unmarshal([]byte(r.Text), &st); r.IsError || err != nil || st.Session.ID != b.ID {
		t.Fatalf("session_get = %+v (%v)", r, err)
	}
	if r = callTool(t, tc, "session_get", `{"id":"zzzzzzzz"}`); !r.IsError {
		t.Fatalf("session_get of a missing session = %+v", r)
	}

	t.Run("session connection calls only tools", func(t *testing.T) {
		var out []proto.Session
		wantUnauthorized(t, tc.Call(timeout(t), proto.MethodSessionsList, nil, &out))
		_, err := tc.Subscribe(timeout(t), proto.MethodSubscribeCatalog, nil)
		wantUnauthorized(t, err)
	})
	t.Run("plain connection calls no tools", func(t *testing.T) {
		var r proto.ToolsCallResult
		wantUnauthorized(t, c.Call(timeout(t), proto.MethodToolsCall, proto.ToolsCallParams{Name: "sessions_list"}, &r))
	})
	t.Run("unknown token", func(t *testing.T) {
		_, err := dialTools(timeout(t), h, "token-x")
		wantUnauthorized(t, err)
	})

	if err := c.Archive(timeout(t), b.ID); err != nil {
		t.Fatal(err)
	}
	t.Run("archived session's token", func(t *testing.T) {
		_, err := dialTools(timeout(t), h, "token-b")
		wantUnauthorized(t, err)
	})

	// The token survives a daemon restart.
	tc.Close()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = start(t, testConfig(h, script))
	tc2, err := dialTools(timeout(t), h, "token-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tc2.Close() })
	if r := callTool(t, tc2, "sessions_list", "{}"); r.IsError || strings.Contains(r.Text, string(b.ID)) {
		t.Fatalf("after restart: %+v", r)
	}
}

// recordConn keeps what is written to it.
type recordConn struct {
	frame.Conn
	written chan []byte
}

func (r *recordConn) WriteFrame(_ context.Context, p []byte) error {
	r.written <- p
	return nil
}
func (r *recordConn) Close() error    { return nil }
func (r *recordConn) Shutdown() error { return nil }

func TestRemoteSessionTokenRefused(t *testing.T) {
	h, script := newHome(t, fake.Script{})
	d := start(t, testConfig(h, script))
	rc := &recordConn{written: make(chan []byte, 1)}
	c := newConn(d.srv, rc)
	c.remote = true
	go c.write()
	params, _ := json.Marshal(proto.HelloParams{ProtocolMajor: proto.ProtocolMajor, SessionToken: "token"})
	if c.hello(proto.ClientFrame{ID: 1, Method: proto.MethodHello, Params: params}) {
		t.Fatal("hello accepted")
	}
	var f proto.ServerFrame
	if err := json.Unmarshal(<-rc.written, &f); err != nil {
		t.Fatal(err)
	}
	if f.Error == nil || f.Error.Code != proto.CodeUnauthorized {
		t.Fatalf("frame = %+v", f)
	}
	c.cancel(context.Canceled)
}

// TestToolImageOverTheConnection sends a 1 MiB image from the daemon to a
// tool connection, the largest a screenshot is expected to be.
func TestToolImageOverTheConnection(t *testing.T) {
	h, script := newHome(t, fake.Script{})
	d := start(t, testConfig(h, script))
	c := dial(t, h, "test")
	s := create(t, c, h)
	if err := d.store.SetSessionToken(timeout(t), s.ID, store.TokenHash("token-img"), time.Now()); err != nil {
		t.Fatal(err)
	}
	tc, err := dialTools(timeout(t), h, "token-img")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tc.Close() })
	r := callTool(t, tc, "simulator_screenshot", fakeDeviceArgs)
	if r.IsError || len(r.Images) != 1 || r.Images[0].MIME != "image/png" || !bytes.Equal(r.Images[0].Data, fakeScreenshot()) {
		t.Fatalf("image result: error %v, %d images", r.IsError, len(r.Images))
	}

	// Several images in flight on one connection fit its outbound queue
	// and leave the connection usable.
	const n = 4
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var r proto.ToolsCallResult
			if err := tc.Call(timeout(t), proto.MethodToolsCall, proto.ToolsCallParams{Name: "simulator_screenshot", Arguments: json.RawMessage(fakeDeviceArgs)}, &r); err != nil {
				errs <- err
			} else if len(r.Images) != 1 || !bytes.Equal(r.Images[0].Data, fakeScreenshot()) {
				errs <- errors.New("image differs")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if r := callTool(t, tc, "simulator_screenshot", fakeDeviceArgs); r.IsError || len(r.Images) != 1 {
		t.Fatalf("connection after concurrent images: %+v", r.IsError)
	}
}
