package env

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/config"
)

// fakeShell writes a shell that, like a real login shell, prints noise
// from its startup files, exports variables and then runs the command it
// was given with -c. It counts its runs in dir/runs.
func fakeShell(t *testing.T, exports string) (shell, dir string) {
	t.Helper()
	dir = t.TempDir()
	shell = filepath.Join(dir, "fakesh")
	script := "#!/bin/sh\n" +
		"echo run >> " + filepath.Join(dir, "runs") + "\n" +
		"echo 'welcome from .zshrc'\n" +
		exports + "\n" +
		"export HOPLET_INTERNAL=1\n" +
		"[ \"$3\" = -c ] || exit 9\n" +
		"exec /bin/sh -c \"$4\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return shell, dir
}

func runs(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "run\n")
}

func noProxy(context.Context) (SystemProxy, error) { return SystemProxy{}, nil }

func lookup(vars []string, k string) (string, bool) {
	for _, kv := range vars {
		if n, v, _ := strings.Cut(kv, "="); n == k {
			return v, true
		}
	}
	return "", false
}

func TestLoginReadsTheShellOnceAndWithholdsCredentials(t *testing.T) {
	shell, dir := fakeShell(t, `export GOPATH=/go
export MULTI='line one
line two'
export ANTHROPIC_API_KEY=sk-secret
export GITHUB_TOKEN=ghp_secret
export SSH_AUTH_SOCK=/tmp/agent`)
	h := t.TempDir()
	l := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	snap, err := l.Environment(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"GOPATH": "/go", "MULTI": "line one\nline two", "SSH_AUTH_SOCK": "/tmp/agent", "HOME": dir, "SHELL": shell} {
		if v, ok := lookup(snap.Vars, k); !ok || v != want {
			t.Errorf("%s = %q (%v), want %q", k, v, ok, want)
		}
	}
	for _, k := range []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "HOPLET_INTERNAL", "PWD", "SHLVL", "_"} {
		if _, ok := lookup(snap.Vars, k); ok {
			t.Errorf("%s was passed on", k)
		}
	}
	if !slices.Equal(snap.Dropped, []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN"}) {
		t.Errorf("dropped %v", snap.Dropped)
	}
	if !slices.IsSorted(snap.Vars) || snap.ProxySource != ProxyNone || snap.Shell != shell || snap.ResolvedAt.IsZero() {
		t.Errorf("snapshot %+v", snap)
	}
	cache, err := os.ReadFile(filepath.Join(h, CacheFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cache), "secret") {
		t.Fatalf("a credential reached the cache: %s", cache)
	}
	if fi, _ := os.Stat(filepath.Join(h, CacheFile)); fi.Mode().Perm() != 0o600 {
		t.Errorf("cache mode %v", fi.Mode().Perm())
	}

	again, err := l.Environment(context.Background(), false)
	if err != nil || !slices.Equal(again.Vars, snap.Vars) {
		t.Fatalf("cached read %v", err)
	}
	if n := runs(t, dir); n != 1 {
		t.Fatalf("the shell ran %d times, want once", n)
	}
	if _, err := l.Environment(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if n := runs(t, dir); n != 2 {
		t.Fatalf("refresh did not read the shell again: %d runs", n)
	}
}

func TestAllowedCredentialsPassButAreNotCached(t *testing.T) {
	shell, dir := fakeShell(t, "export GH_TOKEN=ghp_allowed\nexport NPM_TOKEN=npm_secret")
	h := t.TempDir()
	if err := os.WriteFile(config.Path(h), []byte("[env]\nallow = [\"GH_TOKEN\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	snap, err := l.Environment(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup(snap.Vars, "GH_TOKEN"); v != "ghp_allowed" {
		t.Errorf("GH_TOKEN = %q", v)
	}
	if _, ok := lookup(snap.Vars, "NPM_TOKEN"); ok || !slices.Equal(snap.Dropped, []string{"NPM_TOKEN"}) {
		t.Errorf("NPM_TOKEN passed or not reported: %v", snap.Dropped)
	}
	if n := runs(t, dir); n != 1 {
		t.Errorf("the shell ran %d times on a first read, want once", n)
	}
	var c cached
	b, _ := os.ReadFile(filepath.Join(h, CacheFile))
	if err := json.Unmarshal(b, &c); err != nil || c.Vars["GH_TOKEN"] != "" {
		t.Fatalf("cache %s %v", b, err)
	}
}

func TestProxyPrecedence(t *testing.T) {
	system := func(context.Context) (SystemProxy, error) {
		return SystemProxy{URL: "http://127.0.0.1:7890", NoProxy: []string{".local"}}, nil
	}
	for name, tc := range map[string]struct {
		exports, config string
		source          ProxySource
		https, noProxy  string
	}{
		"system":        {"", "", ProxyFromSystem, "http://127.0.0.1:7890", "localhost,127.0.0.1,::1,.local"},
		"shell wins":    {"export https_proxy=http://10.0.0.1:3128\nexport NO_PROXY=corp", "", ProxyFromShell, "", "corp"},
		"config wins":   {"export HTTPS_PROXY=http://10.0.0.1:3128", "[network]\nproxy = \"socks5h://u:pw@127.0.0.1:1080\"\nno_proxy = [\".corp\"]\n", ProxyFromConfig, "socks5h://u:pw@127.0.0.1:1080", "localhost,127.0.0.1,::1,.corp"},
		"config direct": {"export HTTPS_PROXY=http://10.0.0.1:3128", "[network]\nproxy = \"direct\"\n", ProxyFromConfig, "", ""},
	} {
		shell, dir := fakeShell(t, tc.exports)
		h := t.TempDir()
		if tc.config != "" {
			if err := os.WriteFile(config.Path(h), []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		l := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: system}
		snap, err := l.Environment(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		if snap.ProxySource != tc.source {
			t.Errorf("%s: source %s, want %s", name, snap.ProxySource, tc.source)
		}
		if tc.https != "" {
			for _, k := range proxyVars {
				if v, _ := lookup(snap.Vars, k); v != tc.https {
					t.Errorf("%s: %s = %q, want %q", name, k, v, tc.https)
				}
			}
		}
		if v, _ := lookup(snap.Vars, "NO_PROXY"); v != tc.noProxy {
			t.Errorf("%s: NO_PROXY = %q, want %q", name, v, tc.noProxy)
		}
		if name == "config direct" {
			if _, ok := lookup(snap.Vars, "HTTPS_PROXY"); ok || snap.Proxy != "" {
				t.Errorf("direct kept a proxy: %+v", snap)
			}
		}
		if name == "config wins" && strings.Contains(snap.Proxy, "pw") {
			t.Errorf("proxy password not redacted: %q", snap.Proxy)
		}
	}
}

func TestLoginFailures(t *testing.T) {
	dir := t.TempDir()
	silent := filepath.Join(dir, "silent")
	if err := os.WriteFile(silent, []byte("#!/bin/sh\necho oops >&2\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := login(context.Background(), silent, dir); err == nil || !strings.Contains(err.Error(), "oops") {
		t.Fatalf("failing shell: %v", err)
	}
	if _, err := login(context.Background(), filepath.Join(dir, "missing"), dir); err == nil {
		t.Fatal("missing shell")
	}
	// A startup file that errors after the environment was printed is fine.
	late := filepath.Join(dir, "late")
	if err := os.WriteFile(late, []byte("#!/bin/sh\nexport X=1\n/bin/sh -c \"$4\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	vars, err := login(context.Background(), late, dir)
	if err != nil || vars["X"] != "1" {
		t.Fatalf("late failure: %v %v", vars, err)
	}
}

// TestRealZsh reads a real zsh with throwaway startup files.
func TestRealZsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("no zsh")
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		".zprofile": "export FROM_ZPROFILE=yes\n",
		".zshrc":    "export FROM_ZSHRC=yes\necho rc noise\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vars, err := login(context.Background(), zsh, dir)
	if err != nil {
		t.Fatal(err)
	}
	if vars["FROM_ZPROFILE"] != "yes" || vars["FROM_ZSHRC"] != "yes" {
		t.Fatalf("startup files not read: %v", vars)
	}
}

func TestParseScutil(t *testing.T) {
	out := []byte(`<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
    2 : 10.0.0.0/8
    3 : intranet
  }
  FTPPassive : 1
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : 10.1.1.1
  HTTPSEnable : 0
  SOCKSEnable : 1
  SOCKSPort : 1080
  SOCKSProxy : 127.0.0.1
}`)
	p := parseScutil(out)
	if p.URL != "http://10.1.1.1:8080" || !slices.Equal(p.NoProxy, []string{".local", "10.0.0.0/8", "intranet"}) {
		t.Fatalf("parsed %+v", p)
	}
	if p := parseScutil([]byte("<dictionary> {\n  SOCKSEnable : 1\n  SOCKSPort : 1080\n  SOCKSProxy : 127.0.0.1\n}")); p.URL != "socks5h://127.0.0.1:1080" {
		t.Fatalf("socks only: %+v", p)
	}
	if p := parseScutil([]byte("<dictionary> {\n  FTPPassive : 1\n}")); p.URL != "" {
		t.Fatalf("none: %+v", p)
	}
}

func TestIsCredential(t *testing.T) {
	for name, want := range map[string]bool{
		"ANTHROPIC_API_KEY": true, "github_token": true, "AWS_SECRET_ACCESS_KEY": true, "PGPASSWORD": true,
		"SSH_AUTH_SOCK": false, "GOPATH": false, "KEYCHAIN": false, "TERM": false,
	} {
		if got := IsCredential(name); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestStatic(t *testing.T) {
	s := Static{"PATH=/bin"}
	snap, err := s.Environment(context.Background(), true)
	if err != nil || !slices.Equal(snap.Vars, []string{"PATH=/bin"}) {
		t.Fatalf("%+v %v", snap, err)
	}
}
