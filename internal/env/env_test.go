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
		"shell wins":    {"export https_proxy=http://10.0.0.1:3128\nexport NO_PROXY=corp", "", ProxyFromShell, "http://10.0.0.1:3128", "corp,localhost,127.0.0.1,::1"},
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

// TestRealZsh reads a real zsh with throwaway user startup files in a
// temporary HOME and ZDOTDIR. The system files (/etc/zshenv, /etc/zprofile,
// /etc/zshrc) are read too, as they are for any zsh.
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
	t.Setenv("ZDOTDIR", dir)
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

func TestParseScutilScopedAndPAC(t *testing.T) {
	scoped := []byte(`<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
  }
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : 10.1.1.1
  __SCOPED__ : <dictionary> {
    en0 : <dictionary> {
      ExceptionsList : <array> {
        0 : scoped.example
      }
      HTTPEnable : 1
      HTTPPort : 9999
      HTTPProxy : 192.168.0.9
      HTTPSEnable : 1
      HTTPSPort : 9999
      HTTPSProxy : 192.168.0.9
    }
  }
}`)
	if p := parseScutil(scoped); p.URL != "http://10.1.1.1:8080" || !slices.Equal(p.NoProxy, []string{".local"}) {
		t.Fatalf("scoped: %+v", p)
	}
	pac := []byte(`<dictionary> {
  ProxyAutoConfigEnable : 1
  ProxyAutoConfigURLString : http://wpad.corp/proxy.pac
}`)
	if p := parseScutil(pac); p.URL != "" || p.PAC != "http://wpad.corp/proxy.pac" {
		t.Fatalf("pac: %+v", p)
	}
}

func TestPACSystemProxyIsANote(t *testing.T) {
	shell, dir := fakeShell(t, "")
	l := &Login{Home: t.TempDir(), UserHome: dir, Shell: shell, SystemProxy: func(context.Context) (SystemProxy, error) {
		return SystemProxy{PAC: "http://wpad.corp/proxy.pac"}, nil
	}}
	snap, err := l.Environment(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ProxySource != ProxyNone || snap.Proxy != "" || !strings.Contains(snap.Note, "PAC file (http://wpad.corp/proxy.pac)") {
		t.Fatalf("snapshot %+v", snap)
	}
}

func TestProxyPasswordNeverReachesTheCache(t *testing.T) {
	shell, dir := fakeShell(t, "export https_proxy=http://u:pw@127.0.0.1:3128\nexport GOPATH=/go")
	h := t.TempDir()
	l := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	snap, err := l.Environment(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup(snap.Vars, "HTTPS_PROXY"); v != "http://u:pw@127.0.0.1:3128" {
		t.Errorf("HTTPS_PROXY = %q", v)
	}
	if strings.Contains(snap.Proxy, "pw") || snap.ProxySource != ProxyFromShell {
		t.Errorf("proxy %q (%s)", snap.Proxy, snap.ProxySource)
	}
	b, err := os.ReadFile(filepath.Join(h, CacheFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "pw") || !strings.Contains(string(b), "needs_shell") {
		t.Fatalf("cache %s", b)
	}
	if n := runs(t, dir); n != 1 {
		t.Fatalf("runs %d", n)
	}

	// A fresh process reads the shell once more, then not again.
	l2 := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	for i := range 3 {
		s2, err := l2.Environment(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := lookup(s2.Vars, "https_proxy"); v != "http://u:pw@127.0.0.1:3128" {
			t.Fatalf("call %d: https_proxy = %q", i, v)
		}
	}
	if n := runs(t, dir); n != 2 {
		t.Fatalf("runs %d, want 2", n)
	}
}

func TestAllowedCredentialIsReadOncePerLogin(t *testing.T) {
	shell, dir := fakeShell(t, "export GH_TOKEN=ghp_allowed")
	h := t.TempDir()
	if err := os.WriteFile(config.Path(h), []byte("[env]\nallow = [\"GH_TOKEN\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	for range 3 {
		snap, err := l.Environment(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := lookup(snap.Vars, "GH_TOKEN"); v != "ghp_allowed" {
			t.Fatalf("GH_TOKEN = %q", v)
		}
	}
	if n := runs(t, dir); n != 1 {
		t.Fatalf("the shell ran %d times, want once", n)
	}
}

func TestCacheIsFilteredOnLoad(t *testing.T) {
	shell, dir := fakeShell(t, "")
	h := t.TempDir()
	planted := `{"shell": "` + shell + `", "vars": {"GOPATH": "/go", "GH_TOKEN": "planted", "TERM": "dumb", "HTTPS_PROXY": "http://u:pw@h:1"}, "resolved_at": "2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(h, CacheFile), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	c, ok, err := l.load()
	if err != nil || !ok {
		t.Fatalf("load: %v %v", ok, err)
	}
	if _, has := c.Vars["GH_TOKEN"]; has || c.Vars["TERM"] != "" || c.Vars["HTTPS_PROXY"] != "" || !c.NeedsShell || c.Vars["GOPATH"] != "/go" {
		t.Fatalf("not re-filtered: %+v", c)
	}
	if !slices.Equal(c.Dropped, []string{"GH_TOKEN"}) {
		t.Fatalf("dropped %v", c.Dropped)
	}
}

func TestLoginSeedsSessionVariables(t *testing.T) {
	t.Setenv("LANG", "zh_CN.UTF-8")
	t.Setenv("LC_ALL", "zh_CN.UTF-8")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/s")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	t.Setenv("NOT_FORWARDED", "x")
	shell, dir := fakeShell(t, "")
	l := &Login{Home: t.TempDir(), UserHome: dir, Shell: shell, SystemProxy: noProxy}
	snap, err := l.Environment(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"LANG": "zh_CN.UTF-8", "LC_ALL": "zh_CN.UTF-8", "SSH_AUTH_SOCK": "/tmp/s", "XDG_CONFIG_HOME": "/xdg"} {
		if v, _ := lookup(snap.Vars, k); v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
	for _, k := range []string{"TERM", "NOT_FORWARDED"} {
		if _, ok := lookup(snap.Vars, k); ok {
			t.Errorf("%s was passed on", k)
		}
	}
}

func TestParseUserShell(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"inline":    {"UserShell: /bin/zsh\n", "/bin/zsh"},
		"next line": {"UserShell:\n /opt/homebrew/bin/fish\n", "/opt/homebrew/bin/fish"},
		"missing":   {"No such key: UserShell\n", ""},
		"empty":     {"", ""},
	} {
		if got := parseDscl([]byte(tc.in)); got != tc.want {
			t.Errorf("dscl %s: %q, want %q", name, got, tc.want)
		}
	}
	passwd := "root:x:0:0:root:/root:/bin/bash\n# comment\nalice:x:1000:1000:Alice:/home/alice:/usr/bin/fish\nshort:x:1\n"
	for user, want := range map[string]string{"alice": "/usr/bin/fish", "root": "/bin/bash", "short": "", "nobody": ""} {
		if got := parsePasswd([]byte(passwd), user); got != want {
			t.Errorf("passwd %s: %q, want %q", user, got, want)
		}
	}
}

func TestUsableShell(t *testing.T) {
	dir := t.TempDir()
	exe, plain := filepath.Join(dir, "sh"), filepath.Join(dir, "plain")
	if err := os.WriteFile(exe, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{exe: true, plain: false, dir: false, "zsh": false, "": false, filepath.Join(dir, "none"): false} {
		if got := usableShell(path); got != want {
			t.Errorf("%q: %v", path, got)
		}
	}
	if s := UserShell(context.Background()); !strings.HasPrefix(s, "/") {
		t.Errorf("UserShell %q", s)
	}
}

func TestIsCredential(t *testing.T) {
	for name, want := range map[string]bool{
		"ANTHROPIC_API_KEY": true, "github_token": true, "AWS_SECRET_ACCESS_KEY": true, "PGPASSWORD": true,
		"MYSQL_PWD": true, "OP_SESSION_my": true, "BW_SESSION": true, "OPENAI_KEY": true, "GITHUB_PAT": true, "SENTRY_DSN": true, "BASIC_AUTH": true, "SESSION_COOKIE": true,
		"SSH_AUTH_SOCK": false, "GOPATH": false, "KEYCHAIN": false, "TERM": false, "SSH_KEY_PATH": false,
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

func TestSchemeLessProxyPasswordIsRedacted(t *testing.T) {
	shell, dir := fakeShell(t, "export https_proxy=u:pw@127.0.0.1:3128")
	h := t.TempDir()
	l := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	snap, err := l.Environment(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snap.Proxy, "pw") || !strings.Contains(snap.Proxy, "127.0.0.1:3128") || snap.ProxySource != ProxyFromShell {
		t.Fatalf("proxy %q (%s)", snap.Proxy, snap.ProxySource)
	}
	b, _ := os.ReadFile(filepath.Join(h, CacheFile))
	if strings.Contains(string(b), "pw") {
		t.Fatalf("cache %s", b)
	}
}

func TestRedactProxy(t *testing.T) {
	for in, want := range map[string]string{
		"http://u:pw@h:1":            "http://xxxxx@h:1",
		"u:pw@h:1":                   "http://xxxxx@h:1",
		"http://TOKEN123@proxy:8080": "http://xxxxx@proxy:8080",
		"http://h:1":                 "http://h:1",
		"h:1":                        "h:1",
		"":                           "",
		"http://u:p%zz@h:1":          "http://xxxxx@h:1",
		"http://u:p/w@h:1":           "http://xxxxx@h:1",
		"socks5h://a@b:p@c:2":        "socks5h://xxxxx@c:2",
		"u:p%zz@h:1":                 "xxxxx@h:1",
	} {
		if got := RedactProxy(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestUserinfoWithSlashIsASecret(t *testing.T) {
	vars := map[string]string{"HTTPS_PROXY": "http://u:p/w@h:3128", "NO_PROXY": "a,b", "GOPATH": "/go"}
	if !stripSecrets(vars) || len(vars) != 2 || vars["HTTPS_PROXY"] != "" {
		t.Fatalf("vars %v", vars)
	}
}

func TestFailedSecretReadFallsBackToTheCache(t *testing.T) {
	shell, dir := fakeShell(t, "export https_proxy=http://u:pw@127.0.0.1:3128\nexport GOPATH=/go")
	h := t.TempDir()
	first := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	if _, err := first.Environment(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	// The next process finds the shell broken.
	if err := os.WriteFile(shell, []byte("#!/bin/sh\necho 'zsh: syntax error' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	second := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	snap, err := second.Environment(context.Background(), false)
	if err != nil {
		t.Fatalf("fell over instead of using the cache: %v", err)
	}
	if v, _ := lookup(snap.Vars, "GOPATH"); v != "/go" {
		t.Errorf("cached GOPATH = %q", v)
	}
	if _, ok := lookup(snap.Vars, "https_proxy"); ok {
		t.Error("the proxy with a password came from nowhere")
	}
	if !strings.Contains(snap.Stale, "could not read") || !strings.Contains(snap.Stale, "syntax error") || strings.Contains(snap.Stale, "pw") {
		t.Errorf("stale %q", snap.Stale)
	}
	// A refresh is forced and still fails.
	if _, err := second.Environment(context.Background(), true); err == nil {
		t.Error("a failed refresh succeeded")
	}
}

func TestLastLineHidesCredentials(t *testing.T) {
	for in, hidden := range map[string]bool{
		"zsh: syntax error":                    false,
		"+zshrc:3> export GITHUB_TOKEN=ghp_xx": true,
		"export OPENAI_API_KEY='sk-x'":         true,
		"+/home/u/.zshrc:1> GOPATH=/go":        false,
		"first\nsecond":                        false,
	} {
		got := lastLine([]byte(in))
		if (strings.Contains(got, "hidden")) != hidden || strings.Contains(got, "ghp_") || strings.Contains(got, "sk-x") {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

func TestURLPasswordStaysInMemory(t *testing.T) {
	shell, dir := fakeShell(t, "export DATABASE_URL=postgres://u:pw@h/db\nexport PLAIN_URL=https://example.com/x\nexport GIT=ssh://git@github.com/x\nexport MYSQL_PWD=p1\nexport OP_SESSION_acme=p2\nexport BW_SESSION=p3\nexport GOPATH=/go")
	h := t.TempDir()
	l := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	snap, err := l.Environment(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup(snap.Vars, "DATABASE_URL"); v != "postgres://u:pw@h/db" {
		t.Errorf("DATABASE_URL = %q", v)
	}
	for _, k := range []string{"MYSQL_PWD", "OP_SESSION_acme", "BW_SESSION"} {
		if _, ok := lookup(snap.Vars, k); ok {
			t.Errorf("%s was passed on", k)
		}
	}
	if !slices.Equal(snap.Dropped, []string{"BW_SESSION", "MYSQL_PWD", "OP_SESSION_acme"}) {
		t.Errorf("dropped %v", snap.Dropped)
	}
	b, err := os.ReadFile(filepath.Join(h, CacheFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"pw", "p1", "p2", "p3", "DATABASE_URL"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("cache holds %q: %s", leak, b)
		}
	}
	for _, keep := range []string{"PLAIN_URL", "GIT", "GOPATH", "needs_shell"} {
		if !strings.Contains(string(b), keep) {
			t.Errorf("cache lacks %q: %s", keep, b)
		}
	}
}

func TestCacheWriteFailureKeepsTheSnapshot(t *testing.T) {
	shell, dir := fakeShell(t, "export GOPATH=/go")
	h := t.TempDir()
	// A directory where the cache file goes makes the final rename fail.
	if err := os.MkdirAll(filepath.Join(h, CacheFile, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	l := &Login{Home: h, UserHome: dir, Shell: shell, SystemProxy: noProxy}
	snap, err := l.Environment(context.Background(), false)
	if err != nil {
		t.Fatalf("a cache write failure lost the environment: %v", err)
	}
	if v, _ := lookup(snap.Vars, "GOPATH"); v != "/go" || !strings.Contains(snap.Stale, "could not write the environment cache") {
		t.Fatalf("vars/stale: %q %q", v, snap.Stale)
	}
}
