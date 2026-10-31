package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness"
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
	src.mu.Lock()
	reads := slices.Clone(src.reads)
	src.mu.Unlock()
	if !slices.Equal(reads, []bool{false, true}) {
		t.Fatalf("source reads %v, want a cached read at start and one refresh", reads)
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

// blockingHarness is a harness whose Probe waits until its context ends.
type blockingHarness struct{ *fake.Harness }

func (blockingHarness) Probe(ctx context.Context) (harness.Info, error) {
	<-ctx.Done()
	return harness.Info{}, ctx.Err()
}

// stubHarness answers Probe with a fixed result.
type stubHarness struct {
	*fake.Harness
	info harness.Info
	err  error
}

func (h stubHarness) Probe(context.Context) (harness.Info, error) { return h.info, h.err }

func checksByName(r proto.DoctorReport) map[string]proto.Check {
	m := map[string]proto.Check{}
	for _, ch := range r.Checks {
		m[ch.Name] = ch
	}
	return m
}

func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(srv.Close)
	return srv
}

func TestDoctorIsBoundedWhenAProbeHangs(t *testing.T) {
	srv := okServer(t)
	s := &system{
		harnesses:    map[string]harness.Harness{"stuck": blockingHarness{&fake.Harness{}}, "ok": stubHarness{&fake.Harness{}, harness.Info{Version: "2", Supported: true}, nil}},
		probeURL:     srv.URL,
		probeTimeout: 50 * time.Millisecond,
	}
	s.set(env.Snapshot{Shell: "/bin/zsh", ProxySource: env.ProxyNone}, nil)
	start := time.Now()
	r := s.doctor(context.Background())
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("doctor took %v", d)
	}
	var names []string
	for _, ch := range r.Checks {
		names = append(names, ch.Name)
	}
	if !slices.Equal(names, []string{"environment", "network", "harness ok", "harness stuck"}) {
		t.Fatalf("checks %v", names)
	}
	c := checksByName(r)
	if st := c["harness stuck"]; st.Status != proto.CheckFail || !strings.Contains(st.Detail, "did not answer within 50ms") {
		t.Fatalf("stuck %+v", st)
	}
	if ok := c["harness ok"]; ok.Status != proto.CheckOK {
		t.Fatalf("ok %+v", ok)
	}
}

func TestDoctorHarnessProbeResults(t *testing.T) {
	srv := okServer(t)
	s := &system{
		harnesses: map[string]harness.Harness{
			"broken": stubHarness{&fake.Harness{}, harness.Info{}, errors.New("binary missing")},
			"old":    stubHarness{&fake.Harness{}, harness.Info{Version: "0.1", Supported: false}, nil},
		},
		probeURL: srv.URL,
	}
	s.set(env.Snapshot{ProxySource: env.ProxyNone}, nil)
	c := checksByName(s.doctor(context.Background()))
	if b := c["harness broken"]; b.Status != proto.CheckFail || b.Detail != "binary missing" {
		t.Errorf("broken %+v", b)
	}
	if o := c["harness old"]; o.Status != proto.CheckWarn || !strings.Contains(o.Detail, "0.1") {
		t.Errorf("old %+v", o)
	}
}

func TestDoctorWarnsWhenTheSystemProxyIsAPACFile(t *testing.T) {
	srv := okServer(t)
	s := &system{probeURL: srv.URL}
	s.set(env.Snapshot{ProxySource: env.ProxyNone, Note: "the system proxy is a PAC file (http://wpad/p.pac); set network.proxy in config.toml"}, nil)
	n := checksByName(s.doctor(context.Background()))["network"]
	if n.Status != proto.CheckWarn || !strings.Contains(n.Detail, "PAC file") || !strings.Contains(n.Detail, "reached") {
		t.Fatalf("network %+v", n)
	}
}

func TestDoctorProbesThroughAWorkingProxyAndRedactsIt(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.String())
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	withPassword := strings.Replace(proxy.URL, "http://", "http://u:pw@", 1)
	s := &system{probeURL: "http://probe.example.invalid/"}
	s.set(env.Snapshot{
		Vars: []string{"HTTP_PROXY=" + withPassword}, Proxy: withPassword, ProxySource: env.ProxyFromConfig,
	}, nil)
	r := s.doctor(context.Background())
	n := checksByName(r)["network"]
	if n.Status != proto.CheckOK || !strings.Contains(n.Detail, "through") || !strings.Contains(n.Detail, "HTTP 204") || strings.Contains(n.Detail, "pw") {
		t.Fatalf("network %+v", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(seen, []string{"http://probe.example.invalid/"}) {
		t.Fatalf("the proxy saw %v", seen)
	}
}

func TestProxyFor(t *testing.T) {
	vars := []string{"HTTPS_PROXY=http://s:1", "http_proxy=http://p:2", "NO_PROXY=localhost, .corp,internal.example"}
	for _, tc := range []struct{ url, want string }{
		{"https://api.example.com", "http://s:1"},
		{"http://api.example.com", "http://p:2"},
		{"https://localhost", ""},
		{"https://git.corp", ""},
		{"https://corp", ""},
		{"https://a.internal.example", ""},
		{"https://notcorp.com", "http://s:1"},
		{"ftp://x.example.com", ""},
	} {
		u, _ := url.Parse(tc.url)
		got := ""
		if p := proxyFor(vars, u); p != nil {
			got = p.String()
		}
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.url, got, tc.want)
		}
	}
	u, _ := url.Parse("http://x.example.com")
	if p := proxyFor([]string{"ALL_PROXY=socks5h://s:3", "NO_PROXY=*"}, u); p != nil {
		t.Errorf("wildcard NO_PROXY: %v", p)
	}
	if p := proxyFor([]string{"all_proxy=socks5h://s:3"}, u); p == nil || p.Host != "s:3" {
		t.Errorf("ALL_PROXY: %v", p)
	}
}

// snapSource hands out one fixed snapshot.
type snapSource struct{ snap env.Snapshot }

func (s snapSource) Environment(context.Context, bool) (env.Snapshot, error) { return s.snap, nil }

func TestRefreshRedactsTheProxyPassword(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	cfg.Env = snapSource{env.Snapshot{
		Shell: "/bin/zsh", Vars: []string{"PATH=/bin"},
		Proxy: "http://u:pw@127.0.0.1:7890", ProxySource: env.ProxyFromShell,
	}}
	start(t, cfg)
	c := dial(t, h, "device:a")
	info, err := c.RefreshEnv(timeout(t))
	if err != nil || strings.Contains(info.Proxy, "pw") || !strings.Contains(info.Proxy, "127.0.0.1:7890") {
		t.Fatalf("info %+v %v", info, err)
	}
}

func TestLaterRefreshClearsAStartupReadError(t *testing.T) {
	h, script := newHome(t, echoScript)
	cfg := testConfig(h, script)
	src := &countingSource{fail: true}
	cfg.Env = src
	d := start(t, cfg)
	c := dial(t, h, "device:a")
	if info := d.srv.sys.info(); info.Error == "" {
		t.Fatalf("startup read error missing: %+v", info)
	}
	src.mu.Lock()
	src.fail = false
	src.mu.Unlock()
	info, err := c.RefreshEnv(timeout(t))
	if err != nil || info.Error != "" {
		t.Fatalf("refresh %+v %v", info, err)
	}
	if got := d.srv.sys.info(); got.Error != "" {
		t.Fatalf("error not cleared: %+v", got)
	}
}

func TestDoctorProbesThroughASchemeLessProxy(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.String())
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	bare := "u:pw@" + strings.TrimPrefix(proxy.URL, "http://")
	s := &system{probeURL: "http://probe.example.invalid/"}
	s.set(env.Snapshot{Vars: []string{"HTTP_PROXY=" + bare, "LANG=en_US.UTF-8"}, Proxy: bare, ProxySource: env.ProxyFromShell}, nil)
	n := checksByName(s.doctor(context.Background()))["network"]
	if n.Status != proto.CheckOK || !strings.Contains(n.Detail, "through") || strings.Contains(n.Detail, "pw") {
		t.Fatalf("network %+v", n)
	}
	if info := s.info(); strings.Contains(info.Proxy, "pw") {
		t.Fatalf("info proxy %q", info.Proxy)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("the proxy saw %v", seen)
	}
}

func TestDoctorEnvironmentWarnings(t *testing.T) {
	s := &system{probeURL: okServer(t).URL}
	s.set(env.Snapshot{Shell: "/bin/zsh", Vars: []string{"PATH=/bin"}, ProxySource: env.ProxyNone}, nil)
	e := checksByName(s.doctor(context.Background()))["environment"]
	if e.Status != proto.CheckWarn || !strings.Contains(e.Detail, "C locale") {
		t.Fatalf("no locale: %+v", e)
	}
	s.set(env.Snapshot{Shell: "/bin/zsh", Vars: []string{"LC_CTYPE=en_US.UTF-8"}, ProxySource: env.ProxyNone}, nil)
	if e := checksByName(s.doctor(context.Background()))["environment"]; e.Status != proto.CheckOK {
		t.Fatalf("with locale: %+v", e)
	}
	s.set(env.Snapshot{Shell: "/bin/zsh", Vars: []string{"LANG=C"}, ProxySource: env.ProxyNone,
		Stale: "could not read /bin/zsh again (boom); using the environment cached at 2026-01-01T00:00:00Z"}, nil)
	info := s.info()
	if info.Error != "" || !strings.Contains(info.Warning, "boom") {
		t.Fatalf("info %+v", info)
	}
	if e := checksByName(s.doctor(context.Background()))["environment"]; e.Status != proto.CheckWarn || !strings.Contains(e.Detail, "boom") {
		t.Fatalf("stale: %+v", e)
	}
}

// ignoringHarness is a harness whose Probe ignores its context and blocks
// until released.
type ignoringHarness struct {
	*fake.Harness
	release chan struct{}
}

func (h ignoringHarness) Probe(context.Context) (harness.Info, error) {
	<-h.release
	return harness.Info{}, nil
}

func TestDoctorAbandonsAProbeThatIgnoresItsContext(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s := &system{
		harnesses:    map[string]harness.Harness{"deaf": ignoringHarness{&fake.Harness{}, release}},
		probeURL:     okServer(t).URL,
		probeTimeout: 50 * time.Millisecond,
	}
	s.set(env.Snapshot{ProxySource: env.ProxyNone}, nil)
	start := time.Now()
	c := checksByName(s.doctor(context.Background()))
	if time.Since(start) > 5*time.Second {
		t.Fatal("doctor waited for the deaf probe")
	}
	if d := c["harness deaf"]; d.Status != proto.CheckFail || !strings.Contains(d.Detail, "did not answer") {
		t.Fatalf("deaf %+v", d)
	}
}
