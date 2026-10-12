package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/provider"
	claudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"
)

// recorder is a core stand-in that acks every append at the cursor it holds and keeps what it received.
type recorder struct {
	mu    sync.Mutex
	order []string          // session ids in arrival order
	sizes map[string]int64  // session id -> stored bytes
	data  map[string][]byte // session id -> stored bytes
	msgs  []int             // raw bytes per message
}

func newClient(t *testing.T) (*Client, *recorder) {
	t.Helper()
	rec := &recorder{sizes: map[string]int64{}, data: map[string][]byte{}}
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadLimit(64 << 20) // the core's maxPayload
		for {
			var msg SessionAppend
			if conn.ReadJSON(&msg) != nil {
				return
			}
			if msg.Type != TypeSessionAppend {
				continue // pings
			}
			packed, _ := base64.StdEncoding.DecodeString(msg.Lines)
			gz, err := gzip.NewReader(bytes.NewReader(packed))
			if err != nil {
				t.Error(err)
				return
			}
			raw, _ := io.ReadAll(gz)
			rec.mu.Lock()
			kind := "session.ack"
			if msg.ByteStart != rec.sizes[msg.SessionID] {
				kind = "session.resync"
			} else {
				rec.data[msg.SessionID] = append(rec.data[msg.SessionID], raw...)
				rec.sizes[msg.SessionID] += int64(len(raw))
				rec.order = append(rec.order, msg.SessionID)
				rec.msgs = append(rec.msgs, len(raw))
			}
			size := rec.sizes[msg.SessionID]
			rec.mu.Unlock()
			if conn.WriteJSON(map[string]any{"type": kind, "sessionId": msg.SessionID, "byteSize": size}) != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := NewClient(&config.Config{}, "dev", "test", Hooks{})
	cs := newConnState(conn, make(chan reply, 8))
	t.Cleanup(cs.close)
	c.cs.Store(cs)
	go c.readLoop(cs)
	return c, rec
}

func userLine(text string) string {
	return `{"type":"user","cwd":"/work","message":{"content":"` + text + `"}}` + "\n"
}

func appendTo(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestUnchangedScansReadNoSessionBytes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-work")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	filler := strings.Repeat(`{"type":"assistant","message":{"content":"`+strings.Repeat("y", 900)+`"}}`+"\n", 1200) // ~1 MiB
	paths := map[string]string{}
	for _, id := range []string{"a", "b", "c"} {
		paths[id] = filepath.Join(dir, id+".jsonl")
		appendTo(t, paths[id], []byte(userLine("title of "+id+" session")+filler))
	}
	c, rec := newClient(t)
	claude := claudeprovider.New(root, nil)
	ctx := context.Background()
	round := func() {
		sources, err := claude.Scan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, src := range sources { // what the daemon asks of every source each round
			_ = provider.PeekTitle(ctx, src)
			if cw, ok := src.(interface{ Cwd() string }); ok {
				_ = cw.Cwd()
			}
		}
		c.Reconcile(ctx, sources)
	}
	round()
	for _, id := range []string{"a", "b", "c"} {
		fi, _ := os.Stat(paths[id])
		if rec.sizes[id] != fi.Size() {
			t.Fatalf("%s: stored %d of %d", id, rec.sizes[id], fi.Size())
		}
	}
	sent := len(rec.msgs)
	before := claudeprovider.SessionBytesRead()
	for i := 0; i < 5; i++ {
		round()
	}
	if got := claudeprovider.SessionBytesRead() - before; got != 0 {
		t.Fatalf("unchanged scans read %d session bytes", got)
	}
	if len(rec.msgs) != sent {
		t.Fatalf("unchanged scans sent %d messages", len(rec.msgs)-sent)
	}

	// A grown file is read from its cursor: only the appended line.
	add := []byte(userLine("one more turn"))
	appendTo(t, paths["b"], add)
	before = claudeprovider.SessionBytesRead()
	round()
	if got := claudeprovider.SessionBytesRead() - before; got != int64(len(add)) {
		t.Fatalf("grown file read %d bytes, want %d", got, len(add))
	}
	if rec.sizes["b"] != int64(len(rec.data["b"])) || !bytes.HasSuffix(rec.data["b"], add) {
		t.Fatal("appended line not stored at the end")
	}

	// A fresh provider has no state and changes nothing that is synced.
	claude = claudeprovider.New(root, nil)
	sent = len(rec.msgs)
	round()
	if len(rec.msgs) != sent {
		t.Fatalf("a provider without state sent %d messages", len(rec.msgs)-sent)
	}
}

// memSource is a growable in-memory stream.
type memSource struct {
	id   string
	data []byte
}

func (s *memSource) ID() string                          { return s.id }
func (s *memSource) Dir() string                         { return "p" }
func (s *memSource) Cwd() string                         { return "" }
func (s *memSource) InProject(string) bool               { return false }
func (s *memSource) Size(context.Context) (int64, error) { return int64(len(s.data)), nil }
func (s *memSource) Read(_ context.Context, offset, max int64) ([]byte, error) {
	if offset >= int64(len(s.data)) {
		return nil, nil
	}
	end := min(int64(len(s.data)), offset+max)
	cut := bytes.LastIndexByte(s.data[offset:end], '\n')
	if cut < 0 {
		return nil, nil
	}
	return s.data[offset : offset+int64(cut)+1], nil
}

func TestActiveSessionIsNotDelayedByAnImport(t *testing.T) {
	c, rec := newClient(t)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(1))
	var imp []byte
	for len(imp) < 3*roundBytesPerSession+1<<20 {
		b := make([]byte, 512<<10)
		rng.Read(b)
		imp = append(imp, []byte(`{"x":"`+base64.StdEncoding.EncodeToString(b)+`"}`+"\n")...)
	}
	importing := &memSource{id: "import", data: imp}
	active := &memSource{id: "active", data: []byte(userLine("first"))}
	sources := []provider.Source{importing, active}

	c.Reconcile(ctx, sources)
	if got := rec.sizes["import"]; got < roundBytesPerSession || got > roundBytesPerSession+maxBatchBytes {
		t.Fatalf("an import sends about the round bound, sent %d", got)
	}
	if rec.sizes["active"] != int64(len(active.data)) {
		t.Fatal("active session not synced")
	}
	select {
	case <-c.Wake():
	default:
		t.Fatal("a session with more left must wake the next round")
	}

	active.data = append(active.data, userLine("second")...)
	mark := len(rec.order)
	c.Reconcile(ctx, sources)
	if rec.order[mark] != "active" {
		t.Fatalf("the new line of an active session must go first, order %v", rec.order[mark:])
	}
	if rec.sizes["active"] != int64(len(active.data)) {
		t.Fatal("the new line was not sent in the next round")
	}
	for range 10 {
		c.Reconcile(ctx, sources)
	}
	if !bytes.Equal(rec.data["import"], imp) {
		t.Fatalf("import stored %d of %d bytes", len(rec.data["import"]), len(imp))
	}
}

func TestLongLinesGoAlone(t *testing.T) {
	c, rec := newClient(t)
	rng := rand.New(rand.NewSource(2))
	big := func(n int) []byte {
		b := make([]byte, n/4*3)
		rng.Read(b)
		return []byte(base64.StdEncoding.EncodeToString(b)[:n-1] + "\n")
	}
	var data []byte
	data = append(data, userLine("small before")...)
	l1 := big(1_200_000)
	l2 := big(20 << 20)
	data = append(data, l1...)
	data = append(data, userLine("between")...)
	data = append(data, l2...)
	data = append(data, userLine("small after")...)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "-work"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "-work", "long.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	claude := claudeprovider.New(root, nil)
	start := time.Now()
	for range 6 {
		sources, _ := claude.Scan(context.Background())
		c.Reconcile(context.Background(), sources)
	}
	if !bytes.Equal(rec.data["long"], data) {
		t.Fatalf("stored %d of %d bytes (%s)", len(rec.data["long"]), len(data), time.Since(start))
	}
	alone := map[int]bool{}
	for _, n := range rec.msgs {
		alone[n] = true
		if n > maxBatchBytes && n != len(l1) && n != len(l2) {
			t.Fatalf("message of %d bytes carries more than one oversized line", n)
		}
	}
	if !alone[len(l1)] || !alone[len(l2)] {
		t.Fatalf("long lines were not sent alone: %v", rec.msgs)
	}
}
