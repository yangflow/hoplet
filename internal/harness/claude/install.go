package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/yangflow/hoplet/internal/harness/verified"
)

// releaseURL is where the official builds are published.
const releaseURL = "https://downloads.claude.ai/claude-code-releases"

// maxManifest bounds the size of a release manifest.
const maxManifest = 1 << 20

// releaseVersion is the shape of a published version; it becomes part of
// URLs and file names.
var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// ErrChecksum marks a download that does not match the release manifest.
var ErrChecksum = errors.New("claude: checksum mismatch")

// Source says where and how to fetch a release; the zero value is the
// official release for this machine.
type Source struct {
	BaseURL  string       // default: the official release host
	Platform string       // default: this machine's
	Client   *http.Client // default: http.DefaultClient
	// Verify checks a downloaded binary further; the default is the
	// system's code signature check on macOS and nothing elsewhere.
	Verify func(ctx context.Context, path string) error
}

// resolved fills in the defaults.
func (s Source) resolved() (Source, error) {
	if s.BaseURL == "" {
		s.BaseURL = releaseURL
	}
	if s.Client == nil {
		s.Client = http.DefaultClient
	}
	if s.Verify == nil {
		s.Verify = verifySignature
	}
	if s.Platform == "" {
		p, err := platform()
		if err != nil {
			return Source{}, err
		}
		s.Platform = p
	}
	return s, nil
}

// platform names this machine the way releases do.
func platform() (string, error) {
	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("claude: no release for architecture %s", runtime.GOARCH)
	}
	switch runtime.GOOS {
	case "darwin":
		return "darwin-" + arch, nil
	case "linux":
		for _, libc := range []string{"/lib/libc.musl-x86_64.so.1", "/lib/libc.musl-aarch64.so.1"} {
			if _, err := os.Stat(libc); err == nil {
				return "linux-" + arch + "-musl", nil
			}
		}
		return "linux-" + arch, nil
	default:
		return "", fmt.Errorf("claude: no release for %s", runtime.GOOS)
	}
}

// get fetches url, refusing any status but 200.
func (s Source) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("claude: fetch %s: %s", url, resp.Status)
	}
	return resp, nil
}

// stable returns the version the stable channel points at.
func (s Source) stable(ctx context.Context) (string, error) {
	resp, err := s.get(ctx, s.BaseURL+"/stable")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", fmt.Errorf("claude: stable channel: %w", err)
	}
	v := strings.TrimSpace(string(b))
	if !releaseVersion.MatchString(v) {
		return "", fmt.Errorf("claude: stable channel: unexpected version %q", v)
	}
	return v, nil
}

// fetch downloads version v to a new executable temporary file in dir,
// checked against the release manifest, and returns its path.
func (s Source) fetch(ctx context.Context, v, dir string) (string, error) {
	resp, err := s.get(ctx, s.BaseURL+"/"+v+"/manifest.json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var m struct {
		Platforms map[string]struct {
			Checksum string `json:"checksum"`
			Size     int64  `json:"size"`
		} `json:"platforms"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifest)).Decode(&m); err != nil {
		return "", fmt.Errorf("claude: manifest of %s: %w", v, err)
	}
	want, ok := m.Platforms[s.Platform]
	if !ok || want.Checksum == "" || want.Size <= 0 {
		return "", fmt.Errorf("claude: release %s has no build for %s", v, s.Platform)
	}
	bin, err := s.get(ctx, s.BaseURL+"/"+v+"/"+s.Platform+"/claude")
	if err != nil {
		return "", err
	}
	defer bin.Body.Close()
	out, err := os.CreateTemp(dir, ".claude-*")
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	sum := sha256.New()
	_, cerr := io.Copy(io.MultiWriter(out, sum), io.LimitReader(bin.Body, want.Size+1))
	if err := errors.Join(cerr, out.Chmod(0o755), out.Sync(), out.Close()); err != nil {
		return "", errors.Join(fmt.Errorf("claude: download %s: %w", v, err), os.Remove(out.Name()))
	}
	if got := hex.EncodeToString(sum.Sum(nil)); !strings.EqualFold(got, want.Checksum) {
		return "", errors.Join(fmt.Errorf("%w: claude %s for %s", ErrChecksum, v, s.Platform), os.Remove(out.Name()))
	}
	return out.Name(), nil
}

// Checker verifies a claude binary on this machine; SelfCheck is the real
// one.
type Checker func(ctx context.Context, bin string) error

// ErrSelfCheck marks an install refused because the claude binary failed
// the self-check (errors.Is). Other install errors are not the version's
// fault.
var ErrSelfCheck = verified.ErrSelfCheck

// selfCheckError is a failed self-check of one version.
type selfCheckError struct {
	version string
	err     error
}

func (e *selfCheckError) Error() string {
	return fmt.Sprintf("claude %s failed the self-check, so Hoplet keeps its current copy: %v", e.version, e.err)
}

func (e *selfCheckError) Unwrap() []error { return []error{ErrSelfCheck, e.err} }

// Installed describes an install.
type Installed struct {
	Version string `json:"version"`
	// Checked is set when the version passed the self-check here, because
	// this adapter has no recordings of it.
	Checked bool `json:"checked,omitempty"`
}

// Install downloads the version the official stable channel points at
// into Hoplet's home as harness/claude/bin/claude-<version>, points
// harness/claude/bin/claude at it and removes older copies. It does nothing
// when that version is already in place. See install.
func Install(ctx context.Context, home string, src Source, check Checker) (Installed, error) {
	src, err := src.resolved()
	if err != nil {
		return Installed{}, err
	}
	v, err := src.stable(ctx)
	if err != nil {
		return Installed{}, err
	}
	return install(ctx, home, src, v, check)
}

// install puts version v in place. A version this adapter has no
// recordings of, and that has not passed the self-check here before, is
// installed only after check passes; the pass is remembered. Hoplet's copy
// is pinned: claude's own updater is off for it.
//
// The download is checked and run from a private temporary file, which is
// then renamed into place: what passed the checks is the file that gets
// installed. Installs for one home are serialized by a lock file, because
// the CLI upgrade and the daemon's follow-up may run at once: install waits
// for another install of the same home, and ctx does not interrupt that
// wait (the other install's deadline bounds it).
func install(ctx context.Context, home string, src Source, v string, check Checker) (Installed, error) {
	bin := BinaryPath(home)
	dir := filepath.Dir(bin)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	unlock, err := verified.Lock(ctx, filepath.Join(dir, ".install.lock"), nil)
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	if target, err := os.Readlink(bin); err == nil && target == "claude-"+v {
		if _, err := os.Stat(bin); err == nil {
			return Installed{Version: v}, nil
		}
	}
	tmp, err := src.fetch(ctx, v, dir)
	if err != nil {
		return Installed{}, err
	}
	defer os.Remove(tmp) // a no-op once renamed into place
	if err := src.Verify(ctx, tmp); err != nil {
		return Installed{}, err
	}
	got, err := version(ctx, tmp)
	if err != nil {
		return Installed{}, err
	}
	if got != v {
		return Installed{}, fmt.Errorf("claude: release %s reports version %s", v, got)
	}
	out := Installed{Version: v}
	if !recorded(v) && !localVerified(bin, v) {
		if check == nil {
			return Installed{}, fmt.Errorf("claude: claude %s is not verified and no self-check was given", v)
		}
		if err := check(ctx, tmp); err != nil {
			return Installed{}, &selfCheckError{version: v, err: err}
		}
		out.Checked = true
	}
	dst := filepath.Join(dir, "claude-"+v)
	if err := os.Rename(tmp, dst); err != nil {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	if out.Checked {
		if err := verified.Add(stateDir(bin), v); err != nil {
			return Installed{}, err
		}
	}
	// Publish the link atomically: a daemon starting a session meanwhile
	// sees the old binary or the new one, never neither.
	link := bin + ".new"
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	if err := os.Symlink(filepath.Base(dst), link); err != nil {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	if err := os.Rename(link, bin); err != nil {
		return Installed{}, errors.Join(fmt.Errorf("claude: %w", err), os.Remove(link))
	}
	prune(dir, filepath.Base(dst))
	return out, nil
}

// prune removes the copies other than keep, each hundreds of megabytes,
// and the temporary copies a crashed install left behind. The caller holds
// the install lock, so no temporary copy but its own exists. Processes
// still running an old copy keep it open until they exit; a session whose
// daemon resolved the link just before a prune may fail to start once, and
// the next start uses the new copy. It is best effort: the install is
// done, and a leftover copy only costs disk.
func prune(dir, keep string) {
	for _, pattern := range []string{"claude-*", ".claude-*"} {
		old, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, p := range old {
			if filepath.Base(p) != keep {
				_ = os.Remove(p)
			}
		}
	}
}

// Follow moves Hoplet's copy to the version of the official stable channel,
// when Hoplet has a copy, the versions differ and the new one passes the
// self-check (unless this adapter is verified against it). A version that
// failed is tried at most once a day, even across daemon restarts, and not
// again after verified.MaxAttempts self-check failures in a row; one
// failure may be transient. Only self-check failures count: any other
// failure, a failed download included, is retried. It records the outcome
// for doctor and returns it; nothing to do is not an outcome, and neither
// is a cancelled run or a channel that cannot be read.
func Follow(ctx context.Context, home string, src Source, check Checker) (Update, bool) {
	info, err := (&Harness{Binary: BinaryPath(home)}).Probe(ctx)
	if err != nil {
		return Update{}, false // no copy of Hoplet's yet: installing is the user's call
	}
	current := info.Version
	src, err = src.resolved()
	if err != nil {
		return Update{}, false
	}
	v, err := src.stable(ctx)
	if err != nil || v == current {
		return Update{}, false
	}
	last, hasLast := lastUpdate(BinaryPath(home))
	now := time.Now()
	due, failedBefore := verified.Due(last, hasLast, v, now)
	if !due {
		return Update{}, false
	}
	_, err = install(ctx, home, src, v, check)
	if err != nil && ctx.Err() != nil {
		return Update{}, false // shutting down: not the version's fault
	}
	u := verified.Next(last, failedBefore, current, v, err, now)
	_ = verified.Save(stateDir(BinaryPath(home)), u) // doctor shows it; losing it costs a re-check
	return u, true
}
