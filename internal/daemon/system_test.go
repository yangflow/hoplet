package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

// countingSource is an env.Source that hands out a new environment on
// every refresh, or fails while fail is set.
type countingSource struct {
	mu    sync.Mutex
	n     int
	fail  bool
	reads []bool // the refresh flag of each call
}

func (s *countingSource) Environment(_ context.Context, refresh bool) (env.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, refresh)
	if s.fail {
		return env.Snapshot{}, errors.New("zsh: .zshrc: syntax error")
	}
	s.n++
	return env.Snapshot{
		Shell: "/bin/zsh", Vars: []string{"PATH=" + os.Getenv("PATH"), "GEN=" + strings.Repeat("x", s.n)},
		Dropped: []string{"GITHUB_TOKEN"}, Proxy: "http://127.0.0.1:7890", ProxySource: env.ProxyFromSystem,
	}, nil
}

func TestEnvRefresh(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	src := &countingSource{}
	cfg.Env = src
	d := start(t, cfg)
	c := dial(t, h, "device:a")

	info, err := c.RefreshEnv(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	if info.Shell != "/bin/zsh" || info.Vars != 2 || !slices.Equal(info.Withheld, []string{"GITHUB_TOKEN"}) ||
		info.ProxySource != "system" || info.Proxy != "http://127.0.0.1:7890" || info.Error != "" {
		t.Fatalf("info %+v", info)
	}
	if !slices.Equal(src.reads, []bool{false, true}) {
		t.Fatalf("source reads %v, want a cached read at start and one refresh", src.reads)
	}
	if got := d.mgr.Env(); !slices.Contains(got, "GEN=xx") {
		t.Fatalf("manager environment %v", got)
	}

	src.mu.Lock()
	src.fail = true
	src.mu.Unlock()
	if _, err := c.RefreshEnv(timeout(t)); apiCode(err) != proto.CodeInternal || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("failed refresh: %v", err)
	}
	if got := d.mgr.Env(); !slices.Contains(got, "GEN=xx") {
		t.Fatalf("a failed refresh changed the environment: %v", got)
	}
}

func TestStartWithUnreadableShellUsesMinimalEnv(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	cfg.Env = &countingSource{fail: true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer srv.Close()
	cfg.ProbeURL = srv.URL
	d := start(t, cfg)
	if got := d.mgr.Env(); !slices.Equal(got, minimalEnv()) {
		t.Fatalf("environment %v, want the minimal one", got)
	}
	c := dial(t, h, "device:a")
	r, err := c.Doctor(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]proto.Check{}
	for _, ch := range r.Checks {
		checks[ch.Name] = ch
	}
	if e := checks["environment"]; e.Status != proto.CheckFail || !strings.Contains(e.Detail, "syntax error") {
		t.Fatalf("environment check %+v", e)
	}
	if n := checks["network"]; n.Status != proto.CheckOK || !strings.Contains(n.Detail, "directly") || !strings.Contains(n.Detail, "HTTP 404") {
		t.Fatalf("network check %+v", n)
	}
	if f := checks["harness fake"]; f.Status != proto.CheckOK || f.Detail != "version 1" {
		t.Fatalf("harness check %+v", f)
	}
}

func TestDoctorReportsProxyFailureAndMissingHarnesses(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	cfg.Harnesses = nil
	// A proxy nobody listens on.
	ln := httptest.NewServer(http.NotFoundHandler())
	dead := ln.URL
	ln.Close()
	cfg.Env = env.Static{"HTTPS_PROXY=" + dead}
	cfg.ProbeURL = "https://api.example.invalid"
	start(t, cfg)
	c := dial(t, h, "device:a")
	r, err := c.Doctor(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, ch := range r.Checks {
		names = append(names, ch.Name)
		switch ch.Name {
		case "network":
			if ch.Status != proto.CheckFail || !strings.Contains(ch.Detail, "cannot reach") {
				t.Errorf("network %+v", ch)
			}
		case "harnesses":
			if ch.Status != proto.CheckWarn {
				t.Errorf("harnesses %+v", ch)
			}
		}
	}
	if !slices.Equal(names, []string{"environment", "network", "harnesses"}) {
		t.Fatalf("checks %v", names)
	}
}

func TestSessionState(t *testing.T) {
	h, script := newHome(t, fake.Script{Session: "n1", Turns: []fake.Turn{approvalTurn}})
	start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	sess := create(t, c, h)
	st, err := c.Session(timeout(t), sess.ID)
	if err != nil || st.Session.ID != sess.ID || len(st.Pending) != 0 || st.Queue == nil {
		t.Fatalf("idle state %+v %v", st, err)
	}
	w := watch(t, c, sess.ID, 0)
	if _, err := c.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "go", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	w.until(func(w *watcher) bool { return w.count(proto.TypeApprovalRequest) == 1 })
	if _, err := c.Send(timeout(t), proto.SendParams{Session: sess.ID, Text: "next", Key: "k2"}); err != nil {
		t.Fatal(err)
	}
	st, err = c.Session(timeout(t), sess.ID)
	if err != nil || len(st.Pending) != 1 || st.Pending[0].ID != "a1" || len(st.Pending[0].Options) != 2 ||
		len(st.Queue) != 1 || st.Queue[0].Key != "k2" || st.Session.Status != proto.StatusWaiting {
		t.Fatalf("waiting state %+v %v", st, err)
	}
	if _, err := c.Session(timeout(t), "nope"); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("unknown session: %v", err)
	}
}
