package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelayAllowlist(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOPLET_HOME", h)
	const a, b = "abcdefghijklmnopqrstuvwxyz", "234567abcdefghijklmnopqrst"
	mustCLI(t, "relay", "allow", a)
	mustCLI(t, "relay", "allow", b)
	if out := mustCLI(t, "relay", "list"); out != a+"\n"+b+"\n" {
		t.Fatalf("list: %q", out)
	}
	fi, err := os.Stat(filepath.Join(h, "relay", "allow"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("allowlist mode: %v %v", fi.Mode().Perm(), err)
	}
	mustCLI(t, "relay", "deny", a)
	if out := mustCLI(t, "relay", "list", "--json"); !strings.Contains(out, b) || strings.Contains(out, a) {
		t.Fatalf("list after deny: %q", out)
	}
	if code, _, errs := cli(t, "relay", "allow", "not-an-id"); code != 1 || !strings.Contains(errs, "not a host ID") {
		t.Fatalf("bad ID: %d %q", code, errs)
	}
	other := t.TempDir()
	mustCLI(t, "relay", "allow", "--dir", other, a)
	if out := mustCLI(t, "relay", "list", "--dir", other); out != a+"\n" {
		t.Fatalf("list --dir: %q", out)
	}
}

func TestRelayUsageErrors(t *testing.T) {
	t.Setenv("HOPLET_HOME", t.TempDir())
	for _, args := range [][]string{
		{"relay"},
		{"relay", "--listen", "127.0.0.1:0"},
		{"relay", "--domain", "https://relay.example.com"},
		{"relay", "allow"},
		{"relay", "list", "extra"},
		{"relay", "install"},
		{"relay", "install", "--domain", "relay.example.com", "extra"},
		{"relay", "uninstall", "extra"},
		{"--host", "b", "relay", "list"},
	} {
		if code, _, _ := cli(t, args...); code != 2 {
			t.Errorf("hoplet %s: exit %d, want 2", strings.Join(args, " "), code)
		}
	}
}

func TestRelayInstall(t *testing.T) {
	h, userHome := testHome(t)
	units := filepath.Join(userHome, "etc", "systemd", "system")
	unit := filepath.Join(units, "hoplet-relay.service")
	dir := filepath.Join(userHome, "var", "lib", "hoplet-relay")

	if code, _, errs := cli(t, "relay", "install", "--domain", "relay.example.com"); code != 1 || !strings.Contains(errs, "sudo hoplet relay install") {
		t.Fatalf("install without root: exit %d %q", code, errs)
	}
	if code, _, errs := cli(t, "relay", "uninstall"); code != 1 || !strings.Contains(errs, "run it as root") {
		t.Fatalf("uninstall without root: exit %d %q", code, errs)
	}
	t.Setenv("HOPLET_TEST_ROOT", "1")

	out := mustCLI(t, "relay", "install", "--domain", "relay.example.com", "--domain", "relay2.example.com")
	if !strings.Contains(out, "installed "+unit) || !strings.Contains(out, "relay.example.com, relay2.example.com on :443") || !strings.Contains(out, "sudo hoplet relay allow") {
		t.Errorf("install: %q", out)
	}
	b, err := os.ReadFile(unit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ExecStart=/usr/local/bin/hoplet relay --domain relay.example.com --domain relay2.example.com --listen :443 --dir /var/lib/hoplet-relay\n") ||
		!strings.Contains(string(b), "DynamicUser=yes") {
		t.Errorf("unit:\n%s", b)
	}
	log, _ := os.ReadFile(filepath.Join(userHome, "systemctl.log"))
	if string(log) != "systemctl daemon-reload\nsystemctl enable hoplet-relay.service\nsystemctl restart hoplet-relay.service\n" {
		t.Errorf("systemctl calls:\n%s", log)
	}

	// With the service installed, the allowlist commands default to its
	// directory.
	const id = "abcdefghijklmnopqrstuvwxyz"
	mustCLI(t, "relay", "allow", id)
	if b, err := os.ReadFile(filepath.Join(dir, "allow")); err != nil || string(b) != id+"\n" {
		t.Errorf("allowlist in the service's directory: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(h, "relay")); !os.IsNotExist(err) {
		t.Errorf("allowlist under the home too: %v", err)
	}

	if out := mustCLI(t, "relay", "uninstall"); !strings.Contains(out, dir+" keeps") {
		t.Errorf("uninstall: %q", out)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Errorf("unit after uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "allow")); err != nil {
		t.Errorf("the service's directory went with it: %v", err)
	}
	if out := mustCLI(t, "relay", "list"); out != "" {
		t.Errorf("list after uninstall reads the home's allowlist: %q", out)
	}
}

func TestOthersCanRun(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "opt", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bin, "hoplet")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	// The temporary directory itself may be private; check from below it.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{{exe, 0o750}, {bin, 0o700}} {
		if err := os.Chmod(tc.path, tc.mode); err != nil {
			t.Fatal(err)
		}
		err := othersCanRun(exe)
		if err == nil || !strings.Contains(err.Error(), tc.path+" is not accessible to other users") {
			t.Errorf("%s at %v: %v", tc.path, tc.mode, err)
		}
		if err := os.Chmod(tc.path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := othersCanRun(filepath.Join(bin, "missing")); err == nil {
		t.Error("a missing binary passed")
	}
	if err := othersCanRun("/bin/sh"); err != nil {
		t.Errorf("/bin/sh: %v", err)
	}
}
