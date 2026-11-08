package codex

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yangflow/hoplet/internal/harness/verified"
)

const (
	currentLink = "current"
	releaseURL  = "https://github.com/openai/codex/releases/download"
	sumsFile    = "codex-package_SHA256SUMS"
	// voiceHost is the voice component inside the package.
	voiceHost = "codex-resources/voice/bin/codex-voice-host"

	maxSums     = 1 << 20
	maxPackage  = 1 << 30
	maxUnpacked = 4 << 30
)

// ErrChecksum says a downloaded package did not match its published sum.
var ErrChecksum = errors.New("codex: the package does not match its published checksum")

// Source is where Install fetches from; the zero value is the official
// release for this machine.
type Source struct {
	BaseURL string
	Target  string
	Client  *http.Client
}

// Installed describes an install.
type Installed struct {
	Version string `json:"version"`
	// Voice says the package carries its voice component.
	Voice bool `json:"voice"`
	// Current is set when the pinned version was already in place.
	Current bool `json:"current,omitempty"`
}

// record is what Install leaves next to the package it unpacked.
type record struct {
	Version string `json:"version"`
	Target  string `json:"target"`
	SHA256  string `json:"sha256"`
	Voice   bool   `json:"voice"`
}

const recordFile = "hoplet-install.json"

type manifest struct {
	Version    string `json:"version"`
	Target     string `json:"target"`
	Entrypoint string `json:"entrypoint"`
}

// target names this machine in the release's file names.
func target() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "aarch64-apple-darwin", nil
	case "darwin/amd64":
		return "x86_64-apple-darwin", nil
	case "linux/arm64":
		return "aarch64-unknown-linux-musl", nil
	case "linux/amd64":
		return "x86_64-unknown-linux-musl", nil
	}
	return "", fmt.Errorf("codex: no package for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// Install puts the pinned official package under Hoplet's home as
// harness/codex/<version> and points harness/codex/current at it. Nothing
// is switched unless the package matches its published SHA-256 and carries
// the pinned version; installs for one home are serialized by a lock file.
func Install(ctx context.Context, home string, src Source) (Installed, error) {
	dir := filepath.Dir(filepath.Dir(filepath.Dir(BinaryPath(home))))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Installed{}, fmt.Errorf("codex: %w", err)
	}
	unlock, err := verified.Lock(ctx, filepath.Join(dir, ".install.lock"), nil)
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	if v, err := version(ctx, BinaryPath(home)); err == nil && v == PinnedVersion {
		return Installed{Version: v, Voice: hasVoice(filepath.Join(dir, currentLink)), Current: true}, nil
	}
	if src.Target == "" {
		if src.Target, err = target(); err != nil {
			return Installed{}, err
		}
	}
	if src.BaseURL == "" {
		src.BaseURL = releaseURL
	}
	if src.Client == nil {
		src.Client = http.DefaultClient
	}
	staging, err := os.MkdirTemp(dir, ".new-*")
	if err != nil {
		return Installed{}, fmt.Errorf("codex: %w", err)
	}
	defer os.RemoveAll(staging) // a no-op once renamed into place
	rec, err := fetch(ctx, src, dir, staging)
	if err != nil {
		return Installed{}, err
	}
	dst := filepath.Join(dir, rec.Version)
	if err := os.RemoveAll(dst); err != nil { // only a broken earlier install gets here
		return Installed{}, fmt.Errorf("codex: %w", err)
	}
	if err := os.Rename(staging, dst); err != nil {
		return Installed{}, fmt.Errorf("codex: %w", err)
	}
	// Publish the link atomically: a daemon starting a session meanwhile
	// sees the old copy or the new one, never neither.
	link := filepath.Join(dir, currentLink+".new")
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Installed{}, fmt.Errorf("codex: %w", err)
	}
	if err := os.Symlink(rec.Version, link); err != nil {
		return Installed{}, fmt.Errorf("codex: %w", err)
	}
	if err := os.Rename(link, filepath.Join(dir, currentLink)); err != nil {
		return Installed{}, errors.Join(fmt.Errorf("codex: %w", err), os.Remove(link))
	}
	prune(dir, rec.Version)
	return Installed{Version: rec.Version, Voice: rec.Voice}, nil
}

// fetch downloads, verifies and unpacks the package into staging.
func fetch(ctx context.Context, src Source, dir, staging string) (record, error) {
	base := src.BaseURL + "/rust-v" + PinnedVersion + "/"
	name := "codex-package-" + src.Target + ".tar.gz"
	var sums strings.Builder
	if err := download(ctx, src.Client, base+sumsFile, &sums, maxSums); err != nil {
		return record{}, err
	}
	want, err := sumFor(sums.String(), name)
	if err != nil {
		return record{}, err
	}
	archive, err := os.CreateTemp(dir, ".dl-*")
	if err != nil {
		return record{}, fmt.Errorf("codex: %w", err)
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	h := sha256.New()
	if err := download(ctx, src.Client, base+name, io.MultiWriter(archive, h), maxPackage); err != nil {
		return record{}, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if sum != want {
		return record{}, fmt.Errorf("%w: %s", ErrChecksum, name)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return record{}, fmt.Errorf("codex: %w", err)
	}
	if err := unpack(archive, staging); err != nil {
		return record{}, err
	}
	if err := checkPackage(ctx, staging, src.Target); err != nil {
		return record{}, err
	}
	rec := record{Version: PinnedVersion, Target: src.Target, SHA256: sum, Voice: hasVoice(staging)}
	b, err := json.Marshal(rec)
	if err != nil {
		return record{}, fmt.Errorf("codex: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, recordFile), b, 0o600); err != nil {
		return record{}, fmt.Errorf("codex: %w", err)
	}
	return rec, nil
}

func download(ctx context.Context, c *http.Client, url string, w io.Writer, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("codex: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("codex: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("codex: download %s: %s", url, resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("codex: download: %w", err)
	}
	if n > limit {
		return fmt.Errorf("codex: %s is larger than expected", url)
	}
	return nil
}

func sumFor(sums, name string) (string, error) {
	for line := range strings.Lines(sums) {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("codex: no published checksum for %s", name)
}

// unpack extracts a gzipped tar into dir. Only directories, regular files
// and symlinks staying inside dir are accepted.
func unpack(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("codex: unpack: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("codex: unpack: %w", err)
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if !filepath.IsLocal(name) {
			return fmt.Errorf("codex: unpack: unsafe path %q", h.Name)
		}
		p := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o750); err != nil {
				return fmt.Errorf("codex: unpack: %w", err)
			}
		case tar.TypeSymlink:
			if !filepath.IsLocal(filepath.Join(filepath.Dir(name), filepath.FromSlash(h.Linkname))) || filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("codex: unpack: unsafe link %q", h.Name)
			}
			if err := mkParent(p); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, p); err != nil {
				return fmt.Errorf("codex: unpack: %w", err)
			}
		case tar.TypeReg:
			if total += h.Size; total > maxUnpacked {
				return errors.New("codex: unpack: the package is larger than expected")
			}
			if err := mkParent(p); err != nil {
				return err
			}
			if err := writeEntry(p, tr, h); err != nil {
				return err
			}
		default:
			return fmt.Errorf("codex: unpack: unexpected entry %q", h.Name)
		}
	}
}

func mkParent(p string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return fmt.Errorf("codex: unpack: %w", err)
	}
	return nil
}

func writeEntry(p string, r io.Reader, h *tar.Header) error {
	mode := os.FileMode(0o644)
	if h.Mode&0o111 != 0 {
		mode = 0o755
	}
	f, err := os.OpenFile(filepath.Clean(p), os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("codex: unpack: %w", err)
	}
	_, cerr := io.CopyN(f, r, h.Size)
	if err := errors.Join(cerr, f.Close()); err != nil {
		return fmt.Errorf("codex: unpack %s: %w", h.Name, err)
	}
	return nil
}

// checkPackage refuses a package that is not the pinned version for target.
func checkPackage(ctx context.Context, dir, tgt string) error {
	b, err := fs.ReadFile(os.DirFS(dir), "codex-package.json")
	if err != nil {
		return fmt.Errorf("codex: package manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("codex: package manifest: %w", err)
	}
	if m.Version != PinnedVersion || m.Target != tgt || m.Entrypoint != "bin/codex" {
		return fmt.Errorf("codex: unexpected package: version %q, target %q, entrypoint %q", m.Version, m.Target, m.Entrypoint)
	}
	v, err := version(ctx, filepath.Join(dir, "bin", "codex"))
	if err != nil {
		return err
	}
	if v != PinnedVersion {
		return fmt.Errorf("codex: the package runs as version %s, want %s", v, PinnedVersion)
	}
	return nil
}

func hasVoice(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(voiceHost)))
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// prune removes other versions and what a crashed install left behind. It
// is best effort: the install is done, and a leftover only costs disk.
func prune(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		stale := strings.HasPrefix(n, ".new-") || strings.HasPrefix(n, ".dl-") || (n != keep && e.IsDir() && n[0] >= '0' && n[0] <= '9')
		if stale {
			_ = os.RemoveAll(filepath.Join(dir, n))
		}
	}
}
