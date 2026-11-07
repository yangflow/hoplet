package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// minMinor is the oldest git version the package works with, 2.38
	// (--path-format and --show-object-format for rev-parse).
	minMinor = 38
	// defaultMaxFile is the largest file a snapshot reads.
	defaultMaxFile = 50 << 20
	// defaultMaxPatch is the most patch text kept per repository.
	defaultMaxPatch = 3 << 20
	// callTimeout bounds each git process.
	callTimeout = 2 * time.Minute
	// maxOutput bounds the output read from commands that list things.
	maxOutput = 256 << 20
	// maxStderr bounds the error text kept from a failed command.
	maxStderr = 4 << 10
)

// ErrNoGit is returned when git is missing or older than 2.38.
var ErrNoGit = errors.New("workspace: git 2.38 or newer is required")

// quietConfig turns off every git feature that could run user code or write
// into the user's repository behind a read: hooks (git runs
// post-index-change whenever an index is written), core.ignoreStat (which
// would hide every edit to a tracked file), automatic gc and
// maintenance, the filesystem monitor, and index caches and splits.
var quietConfig = []string{
	"-c", "core.hooksPath=/dev/null", "-c", "core.ignoreStat=false", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "core.fsmonitor=false",
	"-c", "core.untrackedCache=false", "-c", "core.splitIndex=false", "-c", "index.threads=1",
}

// Git runs git for workspace snapshots and diffs. The zero MaxFile and
// MaxPatch mean 50 MiB and 3 MiB.
type Git struct {
	Bin      string   // the git executable
	Env      []string // base environment (GIT_* removed by the runner)
	MaxFile  int64    // files larger than this are skipped; 0 means 50 MiB
	MaxPatch int      // patch bytes kept per repo; 0 means 3 MiB

	// snapshotLimit is the most file content one snapshot may have to read
	// (0: 4 GiB); a repository needing more is too large to snapshot.
	snapshotLimit int64
	// filesPerChunk bounds the files hashed per round (0: 10000; a round is
	// also at most 256 MiB); each round is saved to Hoplet's index before the
	// next, so a snapshot cut short keeps its progress.
	filesPerChunk int

	// alt, when set, is the user's object directory, which diffs may read
	// next to Hoplet's (never written: git writes only to
	// GIT_OBJECT_DIRECTORY, and no alternates file is made).
	alt string

	// Test hooks.
	hashed     func(paths []string) // called with the files of each stored batch
	afterChunk func(n int)          // called after the n-th round was saved
}

func (g Git) maxFile() int64 { return orDefault(g.MaxFile, defaultMaxFile) }
func (g Git) maxPatch() int  { return orDefault(g.MaxPatch, defaultMaxPatch) }

// orDefault is v, or def when v is not set (zero).
func orDefault[T int | int64 | time.Duration](v, def T) T {
	if v > 0 {
		return v
	}
	return def
}

// FindGit finds git on the PATH in env and checks that it is at least
// version 2.38.
func FindGit(env []string) (Git, error) {
	bin, err := lookPath(env, "git")
	if err != nil {
		return Git{}, fmt.Errorf("%w: git was not found on PATH", ErrNoGit)
	}
	g := Git{Bin: bin, Env: env}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, err := g.Version(ctx)
	if err != nil {
		return Git{}, err
	}
	if !versionAtLeast(v, minMinor) {
		return Git{}, fmt.Errorf("%w: found git %s", ErrNoGit, v)
	}
	return g, nil
}

// lookPath finds an executable named name in the PATH of env.
func lookPath(env []string, name string) (string, error) {
	var path string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// Version returns git's version number, such as "2.47.1".
func (g Git) Version(ctx context.Context) (string, error) {
	if g.Bin == "" {
		return "", ErrNoGit
	}
	out, err := g.output(ctx, runOpts{}, "version")
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(out))
	if len(f) < 3 || f[0] != "git" || f[1] != "version" {
		return "", fmt.Errorf("workspace: unexpected git version output %q", bytes.TrimSpace(out))
	}
	return f[2], nil
}

// versionAtLeast reports whether git version v is at least 2.minor.
func versionAtLeast(v string, minor int) bool {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false
	}
	ma, err1 := strconv.Atoi(parts[0])
	mi, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return ma > 2 || (ma == 2 && mi >= minor)
}

// runOpts describes one git invocation.
type runOpts struct {
	Dir   string   // the worktree (git -C)
	Env   []string // added after the base environment
	Cfg   []string // extra git options (-c ...), see filterOverrides
	Stdin []byte
	// StdinReader, when set, is streamed to git instead of Stdin.
	StdinReader io.Reader
}

// environ is the base environment without any GIT_* variable, plus the
// settings that keep git quiet and non-interactive, plus extra.
func (g Git) environ(extra []string) []string {
	out := make([]string, 0, len(g.Env)+len(extra)+3)
	for _, kv := range g.Env {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "LC_ALL=") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "LC_ALL=C")
	return append(out, extra...)
}

// output runs git and returns its standard output, failing when it is
// larger than maxOutput.
func (g Git) output(ctx context.Context, o runOpts, args ...string) ([]byte, error) {
	out, more, err := g.run(ctx, o, maxOutput, args...)
	if err != nil {
		return nil, err
	}
	if more {
		return nil, fmt.Errorf("workspace: git %s: output is too large", args[0])
	}
	return out, nil
}

// run runs git and returns at most limit bytes of its standard output; more
// says there was more, in which case git is stopped.
func (g Git) run(ctx context.Context, o runOpts, limit int64, args ...string) (out []byte, more bool, err error) {
	if g.Bin == "" {
		return nil, false, ErrNoGit
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	full := append(append([]string(nil), quietConfig...), o.Cfg...)
	if o.Dir != "" {
		full = append(full, "-C", o.Dir)
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, g.Bin, full...)
	cmd.Env = g.environ(o.Env)
	cmd.WaitDelay = 5 * time.Second
	switch {
	case o.StdinReader != nil:
		cmd.Stdin = o.StdinReader
	case o.Stdin != nil:
		cmd.Stdin = bytes.NewReader(o.Stdin)
	}
	var stderr cappedBuffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, fmt.Errorf("workspace: git %s: %w", args[0], err)
	}
	if err := cmd.Start(); err != nil {
		return nil, false, fmt.Errorf("workspace: git %s: %w", args[0], err)
	}
	out, rerr := io.ReadAll(io.LimitReader(pipe, limit+1))
	if int64(len(out)) > limit {
		out, more = out[:limit], true
		cancel() // stop a process that is still writing
	}
	werr := cmd.Wait()
	switch {
	case more:
		return out, true, nil
	case ctx.Err() != nil:
		return nil, false, fmt.Errorf("workspace: git %s: %w", args[0], ctx.Err())
	case rerr != nil:
		return nil, false, fmt.Errorf("workspace: git %s: read output: %w", args[0], rerr)
	case werr != nil:
		return nil, false, fmt.Errorf("workspace: git %s: %w: %s", args[0], werr, strings.TrimSpace(stderr.String()))
	}
	return out, false, nil
}

// cappedBuffer keeps the first maxStderr bytes written to it.
type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := maxStderr - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// branch names the branch HEAD is on, or "HEAD" when it is detached; it is
// empty when git cannot say.
func (g Git) branch(ctx context.Context, r Repo) string {
	out, err := g.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, "symbolic-ref", "--short", "-q", "HEAD")
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return "HEAD"
	case err != nil:
		return ""
	}
	return strings.TrimSpace(string(out))
}

// filterOverrides returns the git options that replace every configured
// filter driver (clean, smudge, process) by an empty command, so that none
// runs. git runs clean filters when it compares or hashes content, which
// happens in more commands than one expects (ls-files, update-index and
// write-tree when they smudge racily clean entries), and a driver such as
// git-lfs writes into the repository. Every command of an operation gets
// these options.
func (g Git) filterOverrides(ctx context.Context, r Repo) ([]string, error) {
	out, err := g.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, "config", "-z", "--name-only", "--get-regexp", `^filter\.`)
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return nil, nil // no filter is configured
	}
	if err != nil {
		return nil, err
	}
	var args []string
	seen := map[string]bool{}
	for _, key := range strings.Split(string(out), "\x00") {
		rest, ok := strings.CutPrefix(key, "filter.")
		i := strings.LastIndexByte(rest, '.')
		if !ok || i < 0 || seen[rest[:i]] {
			continue
		}
		name := rest[:i]
		seen[name] = true
		for _, v := range []string{"clean=", "smudge=", "process=", "required=false"} {
			args = append(args, "-c", "filter."+name+"."+v)
		}
	}
	return args, nil
}

// repoOpts is the base of every command of an operation on r.
func (g Git) repoOpts(ctx context.Context, r Repo, env ...string) (runOpts, error) {
	cfg, err := g.filterOverrides(ctx, r)
	if err != nil {
		return runOpts{}, err
	}
	return runOpts{Dir: r.Abs, Cfg: cfg, Env: append(r.gitEnv(), env...)}, nil
}
