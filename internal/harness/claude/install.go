package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

// Checker verifies a claude binary on this machine; SelfCheck is the real
// one.
type Checker func(ctx context.Context, bin string) error

// ErrSelfCheck marks an install refused because the claude binary failed
// the self-check (errors.Is). Other install errors are not the version's
// fault.
var ErrSelfCheck = errors.New("claude: self-check failed")

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
	unlock, err := lockInstalls(dir)
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
	if !verified(v) && !localVerified(bin, v) {
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
		if err := addVerified(bin, v); err != nil {
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

// lockInstalls takes the exclusive install lock of the bin directory dir,
// waiting for another install to finish, and returns its release.
func lockInstalls(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Clean(filepath.Join(dir, ".install.lock")), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, errors.Join(fmt.Errorf("claude: lock installs: %w", err), f.Close())
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
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

// maxFollowAttempts is how many daily tries Follow gives a version that
// fails the self-check before giving up on it: three daily tries.
const maxFollowAttempts = 3

// followRetryAfter is how long Follow leaves a version that just failed
// alone; it keeps a daemon that restarts often from spending quota on the
// same version more than once a day.
const followRetryAfter = 20 * time.Hour

// Follow moves Hoplet's copy to the claude the user runs: the claude found
// on path (the harness environment's PATH), when Hoplet has a copy, the
// versions differ and the user's passes the self-check (unless this
// adapter is verified against it). A version that failed is tried at most
// once a day, even across daemon restarts, and not again after
// maxFollowAttempts self-check failures in a row; one failure may be
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
	failedBefore := hasLast && last.Version == v && last.Error != ""
	if failedBefore && last.Attempts >= maxFollowAttempts {
		return Update{}, false // given up on this version; don't spend quota again
	}
	if failedBefore && now.Sub(last.At) < followRetryAfter && !last.At.After(now) {
		return Update{}, false // tried recently, perhaps by a previous daemon
	}
	inst, err := Install(ctx, src, home, check)
	if err != nil && ctx.Err() != nil {
		return Update{}, false // shutting down: not the version's fault
	}
	if err == nil {
		v = inst.Version // what was installed, in case src changed since it was read
	}
	u := nextUpdate(last, failedBefore, current, v, err, now)
	if b, err := json.MarshalIndent(u, "", "  "); err == nil {
		_ = writeAtomic(updateFile(BinaryPath(home)), b, 0o600) // doctor shows it; losing it costs a re-check
	}
	return u, true
}

// nextUpdate is the record of a follow-up of current to v at now that
// ended with err (nil: installed). last is the previous record, and
// failedBefore says it was a failure of the same version. Only a failed
// self-check counts as an attempt; any other failure records zero, so it
// is retried.
func nextUpdate(last Update, failedBefore bool, current, v string, err error, now time.Time) Update {
	u := Update{At: now.UTC().Truncate(time.Second), From: current, Version: v}
	if err == nil {
		return u
	}
	u.Error = err.Error()
	if errors.Is(err, ErrSelfCheck) {
		u.Attempts = 1
		if failedBefore {
			u.Attempts = last.Attempts + 1
		}
	}
	return u
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

func addVerified(binary, v string) error {
	vs, err := readVerified(binary)
	if err != nil {
		return err
	}
	if slices.Contains(vs, v) {
		return nil
	}
	b, err := json.MarshalIndent(struct {
		Versions []string `json:"versions"`
	}{append(vs, v)}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(verifiedFile(binary), b, 0o600)
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(b)
	if err := errors.Join(werr, tmp.Chmod(mode), tmp.Sync(), tmp.Close()); err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	return nil
}
