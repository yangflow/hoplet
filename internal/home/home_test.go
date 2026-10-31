package home

import (
	"os"
	"path/filepath"
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
