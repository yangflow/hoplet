package home

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDirHonorsEnv(t *testing.T) {
	t.Setenv(Env, "rel")
	d, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(d) || filepath.Base(d) != "rel" {
		t.Fatalf("dir %q", d)
	}
	t.Setenv(Env, "")
	d, err = Dir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(d) != ".hoplet" {
		t.Fatalf("default dir %q", d)
	}
}

func TestEnsureTokenCreatesOnceWithMode0600(t *testing.T) {
	h := t.TempDir()
	if _, err := ReadToken(h); err == nil {
		t.Fatal("read a token that does not exist")
	}
	a, err := EnsureToken(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 64 {
		t.Fatalf("token %q", a)
	}
	b, err := EnsureToken(h)
	if err != nil || b != a {
		t.Fatalf("second call %q %v, want %q", b, err, a)
	}
	fi, err := os.Stat(TokenFile(h))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	if r, err := ReadToken(h); err != nil || r != a {
		t.Fatalf("read %q %v", r, err)
	}
	if err := os.WriteFile(TokenFile(h), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureToken(h); err == nil {
		t.Fatal("accepted an empty token file")
	}
}

func TestReadTokenRefusesSharedFile(t *testing.T) {
	h := t.TempDir()
	if _, err := EnsureToken(h); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(TokenFile(h), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(h); err == nil {
		t.Fatal("read a token other users can read")
	}
	if _, err := EnsureToken(h); err == nil {
		t.Fatal("EnsureToken accepted a token other users can read")
	}
}

func TestEnsureTokenRaceCreatesOnce(t *testing.T) {
	h := t.TempDir()
	const n = 8
	toks := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{}) // released together, so the calls overlap
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			toks[i], errs[i] = EnsureToken(h)
		}()
	}
	close(start)
	wg.Wait()
	want, err := ReadToken(h)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if toks[i] != want {
			t.Fatalf("call %d returned %q, want %q", i, toks[i], want)
		}
	}
	left, err := filepath.Glob(filepath.Join(h, ".token-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestEnsurePrivateInSubdirectory(t *testing.T) {
	h := t.TempDir()
	if _, err := ReadIdentity(h); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read missing identity: %v", err)
	}
	calls := 0
	fresh := func() (string, error) { calls++; return "abc", nil }
	v, err := EnsureIdentity(h, fresh)
	if err != nil || v != "abc" {
		t.Fatalf("ensure %q %v", v, err)
	}
	if v, err = EnsureIdentity(h, fresh); err != nil || v != "abc" || calls != 1 {
		t.Fatalf("second ensure %q %v, %d calls", v, err, calls)
	}
	fi, err := os.Stat(IdentityFile(h))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(IdentityFile(h)))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", di.Mode().Perm())
	}
	if err := os.Chmod(IdentityFile(h), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadIdentity(h); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("read shared identity: %v", err)
	}
}

func TestEnsurePrivateKeepsFreshError(t *testing.T) {
	h := t.TempDir()
	boom := errors.New("boom")
	if _, err := EnsureIdentity(h, func() (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(IdentityFile(h)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file after failed ensure: %v", err)
	}
}
