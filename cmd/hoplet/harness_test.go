package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yangflow/hoplet/internal/config"
	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness/claude"
)

// harnessApp is a command line whose self-check succeeds unless fail is
// set, counting the binaries it checked.
func harnessApp(out, errb *bytes.Buffer, checked *[]string, fail error) app {
	a := testApp(out, errb)
	a.env = env.Static{"PATH=/usr/bin:/bin"}
	a.selfCheck = func(_ context.Context, bin string, vars []string) error {
		if !slices.Contains(vars, "PATH=/usr/bin:/bin") {
			return errors.New("the self-check did not get the harness environment")
		}
		*checked = append(*checked, bin)
		return fail
	}
	return a
}

// claudeRelease is a fake Claude Code release host; publish moves its
// stable channel.
type claudeRelease struct {
	t      *testing.T
	stable atomic.Value
	bodies sync.Map
	source claude.Source
}

func newClaudeRelease(t *testing.T) *claudeRelease {
	t.Helper()
	r := &claudeRelease{t: t}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/"), "/")
		if parts[0] == "stable" {
			fmt.Fprintln(w, r.stable.Load())
			return
		}
		v, ok := r.bodies.Load(parts[0])
		if !ok {
			http.NotFound(w, req)
			return
		}
		body, _ := v.([]byte)
		if parts[len(parts)-1] == "claude" {
			_, _ = w.Write(body)
			return
		}
		sum := sha256.Sum256(body)
		fmt.Fprintf(w, `{"platforms":{"test":{"checksum":%q,"size":%d}}}`, hex.EncodeToString(sum[:]), len(body))
	}))
	t.Cleanup(srv.Close)
	r.source = claude.Source{BaseURL: srv.URL, Platform: "test", Client: srv.Client(), Verify: func(context.Context, string) error { return nil }}
	return r
}

func (r *claudeRelease) publish(version string) {
	r.t.Helper()
	body, err := os.ReadFile(fakeClaudeBin(r.t, version))
	if err != nil {
		r.t.Fatal(err)
	}
	r.bodies.Store(version, body)
	r.stable.Store(version)
}

func fakeClaudeBin(t *testing.T, version string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '"+version+" (Claude Code)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestHarnessInstall(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOPLET_HOME", h)
	var checked []string
	rel := newClaudeRelease(t)
	hoplet := func(fail error, args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		a := harnessApp(&out, &errb, &checked, fail)
		a.claudeRelease = rel.source
		code := a.run(args)
		return code, out.String(), errb.String()
	}
	rel.publish("2.1.312")
	if code, out, errs := hoplet(nil, "harness", "install", "claude"); code != 0 || out != "installed claude 2.1.312 at "+claude.BinaryPath(h)+"\n" || len(checked) != 0 {
		t.Fatalf("install: exit %d %q %q, checked %v", code, out, errs, checked)
	}
	var got map[string]any
	_, out, _ := hoplet(nil, "harness", "upgrade", "claude", "--json")
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["version"] != "2.1.312" || got["checked"] != false {
		t.Fatalf("upgrade --json %v %v", got, err)
	}

	// A version without recordings needs the self-check.
	rel.publish("2.1.400")
	if code, _, errs := hoplet(errors.New("no approval came"), "harness", "upgrade", "claude"); code != 1 || !strings.Contains(errs, "failed the self-check") || len(checked) != 1 {
		t.Fatalf("failing upgrade: exit %d %q, checked %v", code, errs, checked)
	}
	if code, out, errs := hoplet(nil, "harness", "upgrade", "claude"); code != 0 || out != "claude 2.1.400 passed the self-check\ninstalled claude 2.1.400 at "+claude.BinaryPath(h)+"\n" || !strings.Contains(errs, "self-check") {
		t.Fatalf("passing upgrade: exit %d %q %q", code, out, errs)
	}

	for _, args := range [][]string{{"harness"}, {"harness", "install"}, {"harness", "install", "nope"}, {"harness", "install", "codex", "extra"}, {"harness", "remove", "claude"}} {
		if code, _, _ := cli(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, out, _ := cli(t, "harness", "help"); code != 0 || !strings.Contains(out, "self-check") {
		t.Errorf("harness help: exit %d %q", code, out)
	}
}

func TestDaemonFollowsTheStableChannel(t *testing.T) {
	h := t.TempDir()
	var checked []string
	var out, errb bytes.Buffer
	a := harnessApp(&out, &errb, &checked, nil)
	rel := newClaudeRelease(t)
	a.claudeRelease = rel.source
	rel.publish("2.1.312")
	if _, err := claude.Install(context.Background(), h, rel.source, nil); err != nil {
		t.Fatal(err)
	}
	rel.publish("2.1.400")
	vars := []string{"PATH=/usr/bin:/bin"}
	logs := &bytes.Buffer{}

	// Turned off in the configuration: nothing happens.
	a.selfCheck = func(_ context.Context, bin string, _ []string) error {
		checked = append(checked, bin)
		return nil
	}
	work := a.maintenance(h, slog.New(slog.NewTextHandler(logs, nil)))
	if err := os.WriteFile(config.Path(h), []byte("[harness.claude]\nfollow = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work(context.Background(), vars)
	if target, err := os.Readlink(claude.BinaryPath(h)); len(checked) != 0 || logs.Len() != 0 || err != nil || target != "claude-2.1.312" {
		t.Fatalf("followed although turned off: %v, log %q, link %q %v", checked, logs, target, err)
	}

	if err := os.Remove(config.Path(h)); err != nil {
		t.Fatal(err)
	}
	work(context.Background(), vars)
	if len(checked) != 1 || !strings.Contains(logs.String(), "claude followed") {
		t.Fatalf("checked %v, log %s", checked, logs)
	}
	if target, err := os.Readlink(claude.BinaryPath(h)); err != nil || target != "claude-2.1.400" {
		t.Fatalf("link %q %v", target, err)
	}
}

func TestMaintenanceIsOffWithoutASelfCheck(t *testing.T) {
	var out, errb bytes.Buffer
	if testApp(&out, &errb).maintenance(t.TempDir(), slog.Default()) != nil {
		t.Fatal("maintenance without a self-check")
	}
}

func TestDevSanitize(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "rec.jsonl")
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(claude.Entry{Dir: "in", Line: `{"type":"user","message":{"role":"user","content":"look at ` + dir + `/a and ` + userHome + `/b"}}`})
	if err := os.WriteFile(in, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fixture.jsonl")
	mustCLI(t, "dev", "sanitize", "--in", in, "--out", out, "--dir", dir)
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(dir)) || bytes.Contains(b, []byte(userHome)) || !bytes.Contains(b, []byte("/work/a")) || !bytes.Contains(b, []byte("/Users/me/b")) {
		t.Fatalf("fixture %s", b)
	}
	for _, args := range [][]string{{"dev"}, {"dev", "record"}, {"dev", "sanitize", "--in", in}} {
		if code, _, _ := cli(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestDevSanitizeHermesAndUnknownHarness(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "spool.jsonl")
	line, _ := json.Marshal(map[string]any{"k": "stdout", "d": `{"jsonrpc":"2.0","method":"event","params":{"type":"message.delta","payload":{"text":"in ` + dir + `/a"}}}`})
	if err := os.WriteFile(in, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fixture.jsonl")
	mustCLI(t, "dev", "sanitize", "--harness", "hermes", "--in", in, "--out", out, "--dir", dir)
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(dir)) || !bytes.Contains(b, []byte("/work/a")) || !bytes.Contains(b, []byte(`"dir":"out"`)) {
		t.Fatalf("fixture %s", b)
	}
	code, _, errText := cli(t, "dev", "sanitize", "--harness", "foo", "--in", in, "--out", out, "--dir", dir)
	if code != 2 || !strings.Contains(errText, `unknown harness "foo"`) {
		t.Fatalf("unknown harness: exit %d, stderr %q", code, errText)
	}
}

func TestDevRecordUsesTheHarnessEnvironment(t *testing.T) {
	t.Setenv("HOPLET_HOME", t.TempDir())
	t.Setenv("FAKE_API_KEY", "leak")
	dir := t.TempDir()
	dump := filepath.Join(dir, "env.txt")
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nenv > '"+dump+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	steps := filepath.Join(dir, "steps.json")
	if err := os.WriteFile(steps, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	a := testApp(&out, &errb)
	a.env = env.Static{"PATH=/usr/bin:/bin", "FROM_HARNESS_ENV=1"}
	rec := filepath.Join(dir, "rec.jsonl")
	a.run([]string{"dev", "record", "--steps", steps, "--out", rec, "--dir", dir, "--bin", bin})
	b, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("claude did not run: %v (%s)", err, errb.String())
	}
	if !bytes.Contains(b, []byte("FROM_HARNESS_ENV=1")) || bytes.Contains(b, []byte("FAKE_API_KEY")) {
		t.Fatalf("recorder environment:\n%s", b)
	}
}

// fakeHermesDir writes a hermes executable printing version into a new
// directory.
func fakeHermesDir(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hermes"), []byte("#!/bin/sh\necho 'Hermes Agent v"+version+" (2026.9.24)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHarnessVerifyHermes(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOPLET_HOME", h)
	dir := fakeHermesDir(t, "0.23.0")
	var out, errb bytes.Buffer
	var fail error
	runs := 0
	a := testApp(&out, &errb)
	a.env = env.Static{"PATH=" + dir}
	a.hermesCheck = func(_ context.Context, bin string, vars []string) error {
		if bin != filepath.Join(dir, "hermes") || !slices.Contains(vars, "PATH="+dir) {
			return errors.New("the self-check did not get the harness environment")
		}
		runs++
		return fail
	}
	verify := func() (int, string) {
		out.Reset()
		return a.run([]string{"harness", "verify", "hermes"}), out.String()
	}
	fail = errors.New("no approval came")
	errb.Reset()
	if code, got := verify(); code != 1 || !strings.Contains(got, "failed the self-check") {
		t.Fatalf("failing: exit %d %q", code, got)
	}
	if !strings.Contains(errb.String(), "checking Hermes") {
		t.Fatalf("a check ran without saying so: %q", errb.String())
	}
	fail = nil
	if code, got := verify(); code != 0 || got != "hermes 0.23.0 passed the self-check\n" {
		t.Fatalf("passing: exit %d %q", code, got)
	}
	errb.Reset()
	if code, got := verify(); code != 0 || !strings.Contains(got, "needs no check") || runs != 2 {
		t.Fatalf("again: exit %d %q, %d runs", code, got, runs)
	}
	if strings.Contains(errb.String(), "checking Hermes") {
		t.Fatalf("announced a check that did not run: %q", errb.String())
	}
}

func TestDaemonChecksANewHermes(t *testing.T) {
	h := t.TempDir()
	dir := fakeHermesDir(t, "0.23.0")
	var out, errb bytes.Buffer
	runs := 0
	a := testApp(&out, &errb)
	a.hermesCheck = func(context.Context, string, []string) error { runs++; return nil }
	logs := &bytes.Buffer{}
	work := a.maintenance(h, slog.New(slog.NewTextHandler(logs, nil)))
	vars := []string{"PATH=" + dir}
	if err := os.WriteFile(config.Path(h), []byte("[harness.hermes]\nfollow = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work(context.Background(), vars)
	if runs != 0 || logs.Len() != 0 {
		t.Fatalf("checked although turned off: %d runs, log %s", runs, logs)
	}
	if err := os.Remove(config.Path(h)); err != nil {
		t.Fatal(err)
	}
	work(context.Background(), vars)
	if runs != 1 || !strings.Contains(logs.String(), "hermes passed the self-check") {
		t.Fatalf("%d runs, log %s", runs, logs)
	}
}

func TestMaintenanceFollowsEachHarnessOnItsOwn(t *testing.T) {
	const off = "follow = false\n"
	for name, c := range map[string]struct {
		config     string
		wantHermes int
	}{
		"both on":     {"", 1},
		"claude off":  {"[harness.claude]\n" + off, 1},
		"hermes off":  {"[harness.hermes]\n" + off, 0},
		"load failed": {"this is not toml", 0},
	} {
		t.Run(name, func(t *testing.T) {
			h := t.TempDir()
			var out, errb bytes.Buffer
			var claudes, hermeses int
			a := testApp(&out, &errb)
			a.selfCheck = func(context.Context, string, []string) error { claudes++; return nil }
			a.hermesCheck = func(context.Context, string, []string) error { hermeses++; return nil }
			if c.config != "" {
				if err := os.WriteFile(config.Path(h), []byte(c.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			logs := &bytes.Buffer{}
			a.maintenance(h, slog.New(slog.NewTextHandler(logs, nil)))(context.Background(), []string{"PATH=" + fakeHermesDir(t, "0.23.0")})
			if hermeses != c.wantHermes {
				t.Errorf("hermes checks: %d, want %d (log %s)", hermeses, c.wantHermes, logs)
			}
			// claude has no binary on this PATH, so its follow-up does nothing
			// here; it must not have run its check either way.
			if claudes != 0 {
				t.Errorf("claude self-check ran %d times", claudes)
			}
			if name == "load failed" && !strings.Contains(logs.String(), "skipped") {
				t.Errorf("no warning for the config error: %s", logs)
			}
		})
	}
}

func TestDaemonChecksAHermesVersionOnce(t *testing.T) {
	h := t.TempDir()
	var out, errb bytes.Buffer
	runs := 0
	a := testApp(&out, &errb)
	a.hermesCheck = func(context.Context, string, []string) error { runs++; return nil }
	work := a.maintenance(h, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	vars := []string{"PATH=" + fakeHermesDir(t, "0.23.0")}
	work(context.Background(), vars)
	work(context.Background(), vars)
	if runs != 1 {
		t.Fatalf("%d checks of one version, want 1", runs)
	}
}

func TestMaintenancePlan(t *testing.T) {
	off := false
	var claudeOff, hermesOff, bothOff config.Config
	claudeOff.Harness.Claude.Follow = &off
	hermesOff.Harness.Hermes.Follow = &off
	bothOff.Harness.Claude.Follow, bothOff.Harness.Hermes.Follow = &off, &off
	for name, c := range map[string]struct {
		cfg                    config.Config
		loadErr                error
		haveClaude, haveHermes bool
		wantClaude, wantHermes bool
	}{
		"both on":              {config.Config{}, nil, true, true, true, true},
		"claude off":           {claudeOff, nil, true, true, false, true},
		"hermes off":           {hermesOff, nil, true, true, true, false},
		"both off":             {bothOff, nil, true, true, false, false},
		"load error":           {config.Config{}, errors.New("bad toml"), true, true, false, false},
		"no claude self-check": {config.Config{}, nil, false, true, false, true},
		"no hermes self-check": {config.Config{}, nil, true, false, true, false},
	} {
		gotClaude, gotHermes := maintenancePlan(c.cfg, c.loadErr, c.haveClaude, c.haveHermes)
		if gotClaude != c.wantClaude || gotHermes != c.wantHermes {
			t.Errorf("%s: claude %v hermes %v, want %v %v", name, gotClaude, gotHermes, c.wantClaude, c.wantHermes)
		}
	}
}

func TestProxyClient(t *testing.T) {
	if proxyClient([]string{"PATH=/bin"}) != nil {
		t.Fatal("a client without a proxy")
	}
	c := proxyClient([]string{"https_proxy=http://127.0.0.1:7890"})
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport %T", c.Transport)
	}
	u, err := tr.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "example.com"}})
	if err != nil || u.Host != "127.0.0.1:7890" {
		t.Fatalf("proxy %v %v", u, err)
	}
}
