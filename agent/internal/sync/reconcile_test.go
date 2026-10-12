package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/provider"
	claudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"
)

type testSource struct{ data []byte }

func (s testSource) ID() string            { return "s1" }
func (s testSource) Dir() string           { return "p" }
func (s testSource) Cwd() string           { return "" }
func (s testSource) InProject(string) bool { return false }

// Title makes a caught-up session push its title, as a vendor source that names one does.
func (s testSource) Title(context.Context) string        { return "a title" }
func (s testSource) Size(context.Context) (int64, error) { return int64(len(s.data)), nil }
func (s testSource) Read(_ context.Context, offset, max int64) ([]byte, error) {
	if offset > int64(len(s.data)) {
		return nil, provider.ErrBeyondEnd
	}
	end := min(int64(len(s.data)), offset+max)
	return s.data[offset:end], nil
}

func TestReconcileResyncCursor(t *testing.T) {
	src := testSource{[]byte("{\"type\":\"ai-title\",\"aiTitle\":\"中文标题\"}\n\n")}
	size := int64(len(src.data))
	for _, initial := range []int64{0, size + 10} {
		t.Run(map[bool]string{true: "duplicate-at-end", false: "metadata-ahead"}[initial == 0], func(t *testing.T) {
			requests := make(chan SessionAppend, 8)
			up := websocket.Upgrader{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := up.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				for n := 0; ; n++ {
					var msg SessionAppend
					if conn.ReadJSON(&msg) != nil {
						return
					}
					requests <- msg
					cursor := size
					if initial > size && n == 0 {
						cursor = 0
					}
					kind := "session.ack"
					if n == 0 {
						kind = "session.resync"
					}
					if conn.WriteJSON(map[string]any{"type": kind, "sessionId": "s1", "byteSize": cursor}) != nil {
						return
					}
				}
			}))
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[len("http"):], nil)
			if err != nil {
				t.Fatal(err)
			}
			c := NewClient(&config.Config{}, "dev", "test", Hooks{})
			cs := newConnState(conn, make(chan reply, 8))
			c.cs.Store(cs)
			c.cursors["s1"] = initial
			go c.readLoop(cs)
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c.Reconcile(ctx, []provider.Source{src})
			c.Reconcile(ctx, []provider.Source{src})
			c.mu.RLock()
			got := c.cursors["s1"]
			c.mu.RUnlock()
			if got != size {
				t.Fatalf("cached cursor=%d want=%d", got, size)
			}
			first := <-requests
			if first.ByteStart != initial {
				t.Fatalf("first offset=%d", first.ByteStart)
			}
			second := <-requests
			want := size
			if initial > size {
				want = 0
			}
			if second.ByteStart != want {
				t.Fatalf("retry offset=%d want=%d", second.ByteStart, want)
			}
		})
	}
}

func TestReconnectWelcomeAfterLostAck(t *testing.T) {
	src := testSource{[]byte("{\"type\":\"ai-title\",\"aiTitle\":\"Reconnect title\"}\n")}
	size := int64(len(src.data))
	up := websocket.Upgrader{}
	var stored atomic.Int64
	requests := make(chan SessionAppend, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var hello DeviceHello
		if conn.ReadJSON(&hello) != nil {
			return
		}
		conn.WriteJSON(map[string]any{"v": ProtocolVersion, "type": TypeWelcome, "sessions": []map[string]any{{"sessionId": "s1", "byteSize": stored.Load()}}})
		for {
			var raw json.RawMessage
			if conn.ReadJSON(&raw) != nil {
				return
			}
			var msg SessionAppend
			json.Unmarshal(raw, &msg)
			if msg.Type != "session.append" {
				continue
			}
			requests <- msg
			if stored.Load() == 0 {
				stored.Store(size)
				return
			} // Persisted bytes, lost acknowledgement.
			conn.WriteJSON(map[string]any{"type": "session.ack", "sessionId": "s1", "byteSize": stored.Load()})
		}
	}))
	defer srv.Close()
	c := NewClient(&config.Config{ServerURL: srv.URL, Token: "test"}, "dev", "test", Hooks{})
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		done := make(chan error, 1)
		go func() { done <- c.runOnce(ctx) }()
		select {
		case <-c.Wake():
		case <-ctx.Done():
			t.Fatal("connect timed out")
		}
		c.Reconcile(ctx, []provider.Source{src})
		msg := <-requests
		want := int64(0)
		if attempt == 1 {
			want = size
		}
		if msg.ByteStart != want {
			t.Fatalf("attempt %d offset=%d want=%d", attempt, msg.ByteStart, want)
		}
		cancel()
		if cs := c.cs.Load(); cs != nil {
			cs.conn.Close()
		}
		<-done
	}
}

func TestOversizedSourceLogsAndContinues(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blocked.jsonl"), bytes.Repeat([]byte("x"), int(provider.MaxLineBytes)), 0600); err != nil {
		t.Fatal(err)
	}
	sources, err := claudeprovider.New(root, nil).Scan(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("scan: %d err=%v", len(sources), err)
	}
	up := websocket.Upgrader{}
	var sent atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var msg SessionAppend
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		if msg.SessionID != "s1" {
			t.Errorf("unsupported source sent: %s", msg.SessionID)
			return
		}
		packed, err := base64.StdEncoding.DecodeString(msg.Lines)
		if err != nil {
			t.Error(err)
			return
		}
		gz, err := gzip.NewReader(bytes.NewReader(packed))
		if err != nil {
			t.Error(err)
			return
		}
		raw, err := io.ReadAll(gz)
		gz.Close()
		if err != nil {
			t.Error(err)
			return
		}
		if string(raw) != "{}\n" {
			t.Errorf("rewritten bytes: %q", raw)
		}
		sent.Add(1)
		conn.WriteJSON(map[string]any{"type": "session.ack", "sessionId": "s1", "byteSize": len(raw)})
	}))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	c := NewClient(&config.Config{}, "dev", "test", Hooks{})
	cs := newConnState(conn, make(chan reply, 8))
	c.cs.Store(cs)
	go c.readLoop(cs)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Reconcile(ctx, append(sources, testSource{[]byte("{}\n")}))
	c.Reconcile(ctx, append(sources, testSource{[]byte("{}\n")}))
	if strings.Count(logs.String(), "sync blocked failed") != 1 {
		t.Fatalf("a blocked line must be reported once: %s", logs.String())
	}
	if sent.Load() != 1 || c.cursors["s1"] != 3 || c.cursors["blocked"] != 0 {
		t.Fatalf("sent=%d cursors=%v", sent.Load(), c.cursors)
	}
	if !strings.Contains(logs.String(), "sync blocked failed") || !strings.Contains(logs.String(), "offset 0, limit 33554432") {
		t.Fatalf("missing visible error: %s", logs.String())
	}
}

func TestLateRejectionCannotSuppressNewOwnership(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		t.Run(map[bool]string{false: "delta", true: "title"}[metadata], func(t *testing.T) {
			requests := make(chan SessionAppend, 4)
			up := websocket.Upgrader{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := up.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				for {
					var msg SessionAppend
					if conn.ReadJSON(&msg) != nil {
						return
					}
					requests <- msg
				}
			}))
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[len("http"):], nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			c := NewClient(&config.Config{}, "dev", "test", Hooks{})
			cs := newConnState(conn, make(chan reply, 4))
			c.cs.Store(cs)
			src := testSource{[]byte("{\"type\":\"ai-title\",\"aiTitle\":\"fixture\"}\n")}
			cursor := int64(0)
			if metadata {
				cursor = int64(len(src.data))
				c.cursors["s1"] = cursor
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			reconciled := make(chan struct{})
			go func() { c.Reconcile(ctx, []provider.Source{src}); close(reconciled) }()
			select {
			case <-requests:
			case <-ctx.Done():
				t.Fatal("initial request missing")
			}
			// Force ownership activation before delivering an older request's rejection.
			c.mu.Lock()
			c.acceptOwnershipLocked("s1", cursor)
			c.mu.Unlock()
			cs.replies <- reply{kind: replyRejected, sessionID: "s1", reason: "not-owner"}
			<-reconciled
			retry := make(chan struct{})
			go func() { c.Reconcile(ctx, []provider.Source{src}); close(retry) }()
			select {
			case msg := <-requests:
				if msg.ByteStart != cursor {
					t.Fatalf("retry cursor=%d want=%d", msg.ByteStart, cursor)
				}
			case <-ctx.Done():
				t.Fatal("new owner was suppressed by late rejection")
			}
			cs.replies <- reply{kind: replyAck, sessionID: "s1", byteSize: int64(len(src.data))}
			<-retry
		})
	}
}
