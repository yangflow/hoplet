package ports

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSelectByDirectory(t *testing.T) {
	snap := Snapshot{Listeners: []Listener{
		{PID: 10, Port: 5173, Class: Loopback, Name: "node", Cwd: "/work/app"},
		{PID: 11, Port: 3000, Class: Any, Name: "next", Cwd: "/work/app/web/sub"},
		{PID: 12, Port: 4000, Class: Any, Name: "other", Cwd: "/work/app2"},
		{PID: 13, Port: 4001, Class: Any, Name: "other", Cwd: "/work"},
		{PID: 14, Port: 4002, Class: Any, Name: "unknown", Cwd: ""},
		{PID: 15, Port: 5432, Class: Loopback, Name: "postgres", Cwd: "/var"},
	}}
	got := Select(snap, "/work/app", nil)
	want := []Port{{3000, Any, "next"}, {5173, Loopback, "node"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSelectResolvesSymlinkedDirectory(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(real)
	snap := Snapshot{Listeners: []Listener{{PID: 2, Port: 8080, Class: Loopback, Name: "srv", Cwd: filepath.Join(resolved, "x")}}}
	if got := Select(snap, link, nil); len(got) != 1 {
		t.Fatalf("a directory reached through a symlink matched nothing: %+v", got)
	}
}

func TestSelectDescendants(t *testing.T) {
	snap := Snapshot{
		Listeners: []Listener{
			{PID: 40, Port: 8000, Class: Loopback, Name: "python3", Cwd: "/elsewhere"},
			{PID: 50, Port: 9000, Class: Loopback, Name: "ruby", Cwd: "/elsewhere"},
			{PID: 60, Port: 9100, Class: Loopback, Name: "stranger", Cwd: "/elsewhere"},
		},
		Parents: map[int]int{40: 30, 30: 20, 20: 1, 50: 5, 5: 1, 60: 1},
	}
	got := Select(snap, "/work/app", []int{0, 20, 5})
	want := []Port{{8000, Loopback, "python3"}, {9000, Loopback, "ruby"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := Select(snap, "/work/app", []int{0, -1}); len(got) != 0 {
		t.Fatalf("no root and a foreign directory still matched: %+v", got)
	}
}

func TestSelectOnePortOnBothStacks(t *testing.T) {
	snap := Snapshot{Listeners: []Listener{
		{PID: 7, Port: 5173, Class: Loopback, Name: "node", Cwd: "/w"},
		{PID: 7, Port: 5173, Class: Any, Name: "node", Cwd: "/w"},
	}}
	got := Select(snap, "/w", nil)
	if len(got) != 1 || got[0].Address != Any {
		t.Fatalf("got %+v, want one port reaching any", got)
	}
}

func TestSelectCapsAtSixteen(t *testing.T) {
	var ls []Listener
	for p := 3000; p < 3030; p++ {
		ls = append(ls, Listener{PID: 1000 + p, Port: p, Class: Loopback, Name: "n", Cwd: "/w"})
	}
	got := Select(Snapshot{Listeners: ls}, "/w", nil)
	if len(got) != MaxPorts || got[0].Port != 3000 || got[MaxPorts-1].Port != 3015 {
		t.Fatalf("got %d ports: %+v", len(got), got)
	}
}

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"node":                           "node",
		"/usr/local/bin/node":            "node",
		"no\x00de\x1b[31m\n":             "node[31m",
		"bad\xffutf":                     "badutf",
		strings.Repeat("a", 100):         strings.Repeat("a", 64),
		strings.Repeat("\U0001F600", 40): strings.Repeat("\U0001F600", 32),
	}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"*": Any, "0.0.0.0": Any, "[::]": Any, "127.0.0.1": Loopback, "127.1.2.3": Loopback, "[::1]": Loopback,
		"192.168.1.5": Other, "[fe80::1]": Other, "[::ffff:127.0.0.1]": Loopback, "100.64.0.9": Other,
	}
	for in, want := range cases {
		if got := classify(in); got != want {
			t.Errorf("classify(%q) = %q, want %q", in, got, want)
		}
	}
}

const lsofListen = `p100
cnode
R50
f21
n*:5173
f22
n[::1]:5173
p200
ccom.docker.backend
R1
f9
n127.0.0.1:8080
p300
cweird name
R100
f5
n192.168.1.5:9000
`

func TestParseLsofListeners(t *testing.T) {
	ls, parents := parseLsofListeners(lsofListen)
	want := []Listener{
		{PID: 100, Port: 5173, Class: Any, Name: "node"},
		{PID: 100, Port: 5173, Class: Loopback, Name: "node"},
		{PID: 200, Port: 8080, Class: Loopback, Name: "com.docker.backend"},
		{PID: 300, Port: 9000, Class: Other, Name: "weird name"},
	}
	if !reflect.DeepEqual(ls, want) {
		t.Fatalf("got %+v", ls)
	}
	if parents[100] != 50 || parents[300] != 100 {
		t.Fatalf("parents = %v", parents)
	}
}

func TestParseLsofCwd(t *testing.T) {
	got := parseLsofCwd("p100\nfcwd\nn/work/my app\np200\nfcwd\nn/\n")
	if got[100] != "/work/my app" || got[200] != "/" {
		t.Fatalf("got %v", got)
	}
}

func TestParsePS(t *testing.T) {
	got := parsePS("    1     0\n  345   100\nbroken\n")
	if got[345] != 100 || got[1] != 0 || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

const procTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1435 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 11111 1 0000000000000000 100 0 0 10 0
   1: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 22222 1 0000000000000000 100 0 0 10 0
   2: 0501A8C0:2328 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 33333 1 0000000000000000 100 0 0 10 0
   3: 0100007F:9999 0100007F:1435 01 00000000:00000000 00:00000000 00000000  1000        0 44444 1 0000000000000000 100 0 0 10 0
   4: 0100007F:2328 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 0 1 0000000000000000 100 0 0 10 0
`

const procTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:1435 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 55555 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000000000000:0BB8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 66666 1 0000000000000000 100 0 0 10 0
   2: 0000000000000000FFFF00000100007F:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 77777 1 0000000000000000 100 0 0 10 0
   3: 000080FE000000000000000001000000:1F91 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 88888 1 0000000000000000 100 0 0 10 0
`

func TestParseProcNetTCP(t *testing.T) {
	got := parseProcNetTCP(procTCP)
	want := map[string]socket{
		"11111": {5173, Loopback},
		"22222": {80, Any},
		"33333": {9000, Other},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tcp: got %+v", got)
	}
	got6 := parseProcNetTCP(procTCP6)
	want6 := map[string]socket{
		"55555": {5173, Loopback},
		"66666": {3000, Any},
		"77777": {8080, Loopback},
		"88888": {8081, Other},
	}
	if !reflect.DeepEqual(got6, want6) {
		t.Fatalf("tcp6: got %+v", got6)
	}
}

func TestParseStatParent(t *testing.T) {
	if n, ok := parseStatParent("123 (my (weird) proc) S 77 123 123 0 -1"); !ok || n != 77 {
		t.Fatalf("got %d %v", n, ok)
	}
	if _, ok := parseStatParent("garbage"); ok {
		t.Fatal("garbage parsed")
	}
}

// fakeScanner serves the snapshot it holds and counts the scans.
type fakeScanner struct {
	mu    sync.Mutex
	snap  Snapshot
	err   error
	scans int
}

func (f *fakeScanner) Scan(context.Context) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans++
	return f.snap, f.err
}

func (f *fakeScanner) set(s Snapshot, err error) {
	f.mu.Lock()
	f.snap, f.err = s, err
	f.mu.Unlock()
}

type sent struct {
	id    string
	ports []Port
}

type harness struct {
	w    *Watcher
	scan *fakeScanner
	mu   sync.Mutex
	got  []sent
	now  time.Time
}

func newHarness() *harness {
	h := &harness{scan: &fakeScanner{}, now: time.Unix(1000, 0)}
	h.w = New(Config{
		Scanner: h.scan,
		Dir:     func(string) (string, error) { return "/w", nil },
		Send: func(id string, p []Port) {
			h.mu.Lock()
			h.got = append(h.got, sent{id, p})
			h.mu.Unlock()
		},
		Now:  func() time.Time { return h.now },
		Logf: func(string, ...any) {},
	})
	return h
}

func (h *harness) sent() []sent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]sent(nil), h.got...)
}

func serving(port int) Snapshot {
	return Snapshot{Listeners: []Listener{{PID: 9, Port: port, Class: Loopback, Name: "node", Cwd: "/w"}}}
}

func TestWatcherScansOnlyWhileWatched(t *testing.T) {
	h := newHarness()
	h.w.Tick(context.Background())
	if h.scan.scans != 0 {
		t.Fatal("scanned with nothing watched")
	}
	h.w.Watch([]string{"s1"})
	h.w.Tick(context.Background())
	h.w.Watch(nil)
	h.w.Tick(context.Background())
	if h.scan.scans != 1 {
		t.Fatalf("scans = %d, want 1", h.scan.scans)
	}
}

func TestWatcherReportsFirstThenOnlyChanges(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	h.w.Watch([]string{"s1"})
	h.w.Tick(ctx)
	if got := h.sent(); len(got) != 1 || len(got[0].ports) != 0 {
		t.Fatalf("the first report, empty as it is, is missing: %+v", got)
	}
	h.now = h.now.Add(3 * time.Second)
	h.w.Tick(ctx)
	if len(h.sent()) != 1 {
		t.Fatal("an unchanged list was sent again")
	}
	h.scan.set(serving(5173), nil)
	h.now = h.now.Add(3 * time.Second)
	h.w.Tick(ctx)
	got := h.sent()
	if len(got) != 2 || !reflect.DeepEqual(got[1].ports, []Port{{5173, Loopback, "node"}}) {
		t.Fatalf("got %+v", got)
	}
}

func TestWatcherSendsAtMostOncePerSecond(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	h.w.Watch([]string{"s1"})
	h.w.Tick(ctx)
	h.scan.set(serving(5173), nil)
	h.now = h.now.Add(400 * time.Millisecond)
	h.w.Tick(ctx)
	if len(h.sent()) != 1 {
		t.Fatal("a change 400 ms after the last report was sent")
	}
	h.now = h.now.Add(700 * time.Millisecond)
	h.w.Tick(ctx)
	if len(h.sent()) != 2 {
		t.Fatalf("the held change was not sent once the second passed: %+v", h.sent())
	}
}

func TestWatcherReportsAgainAfterTheSetIsReplaced(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	h.scan.set(serving(5173), nil)
	h.w.Watch([]string{"s1", "s2"})
	h.w.Tick(ctx)
	if len(h.sent()) != 2 {
		t.Fatalf("got %+v", h.sent())
	}
	h.w.Watch([]string{"s2", "s3"})
	h.w.Tick(ctx)
	got := h.sent()
	if len(got) != 3 || got[2].id != "s3" {
		t.Fatalf("only the new session should be reported: %+v", got)
	}
	h.w.Watch(nil)
	h.w.Watch([]string{"s2"})
	h.w.Tick(ctx)
	if got := h.sent(); len(got) != 4 || got[3].id != "s2" {
		t.Fatalf("a session watched again was not reported again: %+v", got)
	}
}

func TestWatcherKeepsStateWhenAScanFails(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	h.w.Watch([]string{"s1"})
	h.scan.set(serving(5173), nil)
	h.w.Tick(ctx)
	h.scan.set(Snapshot{}, context.DeadlineExceeded)
	h.now = h.now.Add(3 * time.Second)
	h.w.Tick(ctx)
	if len(h.sent()) != 1 {
		t.Fatal("a failed scan produced a report")
	}
}

func TestWatcherRunScansOnWatch(t *testing.T) {
	h := newHarness()
	h.w.cfg.Interval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.w.Run(ctx)
	h.w.Watch([]string{"s1"})
	deadline := time.Now().Add(3 * time.Second)
	for len(h.sent()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(h.sent()) == 0 {
		t.Fatal("a new watch did not start a scan")
	}
}
