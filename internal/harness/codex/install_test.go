package codex

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testTarget = "test-target"

type tarFile struct {
	name, body string
	mode       int64
	link       string
	typ        byte
}

func script(v string) string { return "#!/bin/sh\necho codex-cli " + v + "\n" }

func packageFiles(v string) []tarFile {
	return []tarFile{
		{name: "codex-package.json", mode: 0o644, body: fmt.Sprintf(`{"version":%q,"target":%q,"entrypoint":"bin/codex"}`, v, testTarget)},
		{name: "bin/codex", mode: 0o755, body: script(v)},
		{name: voiceHost, mode: 0o755, body: "x"},
	}
}

func tarball(t *testing.T, files []tarFile) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		typ := f.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: typ, Linkname: f.link}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func release(t *testing.T, pkg []byte, sum string) Source {
	t.Helper()
	if sum == "" {
		s := sha256.Sum256(pkg)
		sum = hex.EncodeToString(s[:])
	}
	name := "codex-package-" + testTarget + ".tar.gz"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rust-v" + PinnedVersion + "/" + sumsFile:
			fmt.Fprintf(w, "%s  other.tar.gz\n%s  %s\n", strings.Repeat("0", 64), sum, name)
		case "/rust-v" + PinnedVersion + "/" + name:
			_, _ = w.Write(pkg)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return Source{BaseURL: srv.URL, Target: testTarget, Client: srv.Client()}
}

func TestInstall(t *testing.T) {
	home := t.TempDir()
	got, err := Install(context.Background(), home, release(t, tarball(t, packageFiles(PinnedVersion)), ""))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != PinnedVersion || !got.Voice || got.Current {
		t.Fatalf("got %+v", got)
	}
	info, err := (&Harness{Binary: BinaryPath(home)}).Probe(context.Background())
	if err != nil || info.Version != PinnedVersion || !info.Supported {
		t.Fatalf("got %+v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(BinaryPath(home))), recordFile)); err != nil {
		t.Fatal(err)
	}
	again, err := Install(context.Background(), home, Source{BaseURL: "http://127.0.0.1:0", Target: testTarget})
	if err != nil || !again.Current {
		t.Fatalf("a second install should not download: %+v, %v", again, err)
	}
}

func TestInstallReportsMissingVoice(t *testing.T) {
	got, err := Install(context.Background(), t.TempDir(), release(t, tarball(t, packageFiles(PinnedVersion)[:2]), ""))
	if err != nil || got.Voice {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestFailedInstallsDoNotSwitch(t *testing.T) {
	good := tarball(t, packageFiles(PinnedVersion))
	tests := []struct {
		name string
		src  func(t *testing.T) Source
		want string
	}{
		{"checksum", func(t *testing.T) Source { return release(t, good, strings.Repeat("a", 64)) }, "checksum"},
		{"wrong version", func(t *testing.T) Source { return release(t, tarball(t, packageFiles("9.9.9")), "") }, "unexpected package"},
		{"traversal", func(t *testing.T) Source {
			return release(t, tarball(t, []tarFile{{name: "../evil", mode: 0o644, body: "x"}}), "")
		}, "unsafe path"},
		{"escaping link", func(t *testing.T) Source {
			return release(t, tarball(t, []tarFile{{name: "l", typ: tar.TypeSymlink, link: "../../x"}}), "")
		}, "unsafe link"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			// An older copy is in place and must keep working.
			dir := filepath.Join(home, "harness", Kind)
			old := filepath.Join(dir, "0.1.0", "bin")
			if err := os.MkdirAll(old, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(old, "codex"), []byte(script("0.1.0")), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("0.1.0", filepath.Join(dir, currentLink)); err != nil {
				t.Fatal(err)
			}
			_, err := Install(context.Background(), home, tt.src(t))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v", err)
			}
			if tt.name == "checksum" && !errors.Is(err, ErrChecksum) {
				t.Fatalf("got %v", err)
			}
			if link, _ := os.Readlink(filepath.Join(dir, currentLink)); link != "0.1.0" {
				t.Fatalf("current moved to %q", link)
			}
			left, _ := filepath.Glob(filepath.Join(dir, ".new-*"))
			if len(left) != 0 {
				t.Fatalf("left %q", left)
			}
		})
	}
}

func TestInstallReplacesAnOlderCopy(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "harness", Kind)
	if err := os.MkdirAll(filepath.Join(dir, "0.1.0", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), home, release(t, tarball(t, packageFiles(PinnedVersion)), "")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "0.1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the old copy is still there: %v", err)
	}
}

func TestSanitize(t *testing.T) {
	in := strings.Join([]string{
		`{"dir":"out","line":"{\"id\":\"hoplet-abcd1234-1\",\"result\":{\"userAgent\":\"x\",\"codexHome\":\"/Users/alice/.codex\"}}"}`,
		`{"dir":"out","line":"{\"method\":\"account/updated\",\"params\":{\"email\":\"a@b\"}}"}`,
		`{"dir":"in","line":"{\"id\":\"hoplet-abcd1234-2\",\"method\":\"thread/start\",\"params\":{\"cwd\":\"/Users/alice/w\"}}"}`,
		`{"dir":"out","line":"{\"id\":\"hoplet-abcd1234-2\",\"result\":{\"thread\":{\"id\":\"t\",\"cwd\":\"/secret\"},\"model\":\"m\"}}"}`,
	}, "\n") + "\n"
	var out strings.Builder
	if err := Sanitize(strings.NewReader(in), &out, "/Users/alice/w", "/Users/alice"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, bad := range []string{"alice", "a@b", "/secret", "abcd1234", "codexHome"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, `/work`) || !strings.Contains(got, "hoplet-x-2") {
		t.Errorf("got:\n%s", got)
	}
}
