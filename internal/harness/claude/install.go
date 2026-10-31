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
	"time"
)

// Checker verifies a claude binary on this machine; SelfCheck is the real
// one.
type Checker func(ctx context.Context, bin string) error

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
func Install(ctx context.Context, src, home string, check Checker) (Installed, error) {
	src, err := filepath.EvalSymlinks(src)
	if err != nil {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	v, err := version(ctx, src)
	if err != nil {
		return Installed{}, err
	}
	out := Installed{Version: v}
	bin := BinaryPath(home)
	if !verified(v) && !localVerified(bin, v) {
		if err := check(ctx, src); err != nil {
			return Installed{}, fmt.Errorf("claude %s failed the self-check, so Hoplet keeps its current copy: %w", v, err)
		}
		out.Checked = true
	}
	dir := filepath.Dir(bin)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	dst := filepath.Join(dir, "claude-"+v)
	if err := copyFile(src, dst); err != nil {
		return Installed{}, err
	}
	if out.Checked {
		if err := addVerified(bin, v); err != nil {
			return Installed{}, err
		}
	}
	// Publish the link atomically: a daemon starting a session meanwhile
	// sees the old binary or the new one, never neither.
	tmp := bin + ".new"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	if err := os.Symlink(filepath.Base(dst), tmp); err != nil {
		return Installed{}, fmt.Errorf("claude: %w", err)
	}
	if err := os.Rename(tmp, bin); err != nil {
		return Installed{}, errors.Join(fmt.Errorf("claude: %w", err), os.Remove(tmp))
	}
	prune(dir, filepath.Base(dst))
	return out, nil
}

// prune removes the copies other than keep, each hundreds of megabytes;
// processes still running one keep it open until they exit. It is best
// effort: the install is done, and a leftover copy only costs disk.
func prune(dir, keep string) {
	old, _ := filepath.Glob(filepath.Join(dir, "claude-*"))
	for _, p := range old {
		if filepath.Base(p) != keep {
			_ = os.Remove(p)
		}
	}
}

// Follow moves Hoplet's copy to the claude the user runs: the claude found
// on path (the harness environment's PATH), when Hoplet has a copy, the
// versions differ and the user's passes the self-check (unless this
// adapter is verified against it). A version that failed is not checked
// again. It records the outcome for
// doctor and returns it; nothing to do is not an outcome.
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
	if last, ok := lastUpdate(BinaryPath(home)); ok && last.Version == v && last.Error != "" {
		return Update{}, false // this version already failed; don't spend quota again
	}
	u := Update{At: time.Now().UTC().Truncate(time.Second), From: current, Version: v}
	if _, err := Install(ctx, src, home, check); err != nil {
		u.Error = err.Error()
	}
	if b, err := json.MarshalIndent(u, "", "  "); err == nil {
		_ = writeAtomic(updateFile(BinaryPath(home)), b, 0o600) // doctor shows it; losing it costs a re-check
	}
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

// copyFile copies src to dst through a temporary file, executable.
func copyFile(src, dst string) error {
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".claude-*")
	if err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	defer os.Remove(out.Name())
	_, cerr := io.Copy(out, in)
	if err := errors.Join(cerr, out.Chmod(0o755), out.Sync(), out.Close()); err != nil {
		return fmt.Errorf("claude: copy %s: %w", src, err)
	}
	if err := os.Rename(out.Name(), dst); err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	return nil
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
