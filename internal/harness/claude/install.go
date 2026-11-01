package claude

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/yangflow/hoplet/internal/harness/verified"
)

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

// Install copies the claude binary at src into Hoplet's home as
// harness/claude/bin/claude-<version>, points harness/claude/bin/claude at
// it and removes older copies. A version this adapter has no recordings
// of, and that has not passed the self-check here before, is installed
// only after check passes; the pass is remembered. Hoplet's copy is
// pinned: claude's own updater is off for it.
//
// The version is read and the check runs against a private copy of src
// made first, which is then renamed into place: what passed the check is
// the file that gets installed, even if src changes meanwhile. Installs
// for one home are serialized by a lock file, because the CLI upgrade and
// the daemon's follow-up may run at once: Install waits for another install
// of the same home, and ctx does not interrupt that wait (the other
// install's self-check bounds it).
func Install(ctx context.Context, src, home string, check Checker) (Installed, error) {
	src, err := filepath.EvalSymlinks(src)
	if err != nil {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	bin := BinaryPath(home)
	dir := filepath.Dir(bin)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	unlock, err := verified.Lock(dir)
	if err != nil {
		return Installed{}, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	tmp, err := copyFile(src, dir)
	if err != nil {
		return Installed{}, err
	}
	defer os.Remove(tmp) // a no-op once renamed into place
	v, err := version(ctx, tmp)
	if err != nil {
		return Installed{}, err
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

// Follow moves Hoplet's copy to the claude the user runs: the claude found
// on path (the harness environment's PATH), when Hoplet has a copy, the
// versions differ and the user's passes the self-check (unless this
// adapter is verified against it). A version that failed is tried at most
// once a day, even across daemon restarts, and not again after
// verified.MaxAttempts self-check failures in a row; one failure may be
// transient. Only self-check failures count: any other failure is retried.
// It records the outcome for doctor and returns it; nothing to
// do is not an outcome, and neither is a cancelled run.
func Follow(ctx context.Context, home, path string, check Checker) (Update, bool) {
	src, err := lookPath(path)
	if err != nil {
		return Update{}, false // no claude of the user's: nothing to follow
	}
	v, err := version(ctx, src)
	if err != nil {
		return Update{}, false
	}
	info, err := (&Harness{Binary: BinaryPath(home)}).Probe(ctx)
	if err != nil {
		return Update{}, false // no copy of Hoplet's yet: installing is the user's call
	}
	current := info.Version
	if v == current {
		return Update{}, false
	}
	last, hasLast := lastUpdate(BinaryPath(home))
	now := time.Now()
	due, failedBefore := verified.Due(last, hasLast, v, now)
	if !due {
		return Update{}, false
	}
	inst, err := Install(ctx, src, home, check)
	if err != nil && ctx.Err() != nil {
		return Update{}, false // shutting down: not the version's fault
	}
	if err == nil {
		v = inst.Version // what was installed, in case src changed since it was read
	}
	u := verified.Next(last, failedBefore, current, v, err, now)
	_ = verified.Save(stateDir(BinaryPath(home)), u) // doctor shows it; losing it costs a re-check
	return u, true
}

// lookPath finds claude in a PATH value.
func lookPath(path string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, "claude")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// copyFile copies src to a new executable temporary file in dir and
// returns its path.
func copyFile(src, dir string) (string, error) {
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".claude-*")
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	_, cerr := io.Copy(out, in)
	if err := errors.Join(cerr, out.Chmod(0o755), out.Sync(), out.Close()); err != nil {
		return "", errors.Join(fmt.Errorf("claude: copy %s: %w", src, err), os.Remove(out.Name()))
	}
	return out.Name(), nil
}
