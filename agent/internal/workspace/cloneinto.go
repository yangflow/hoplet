package workspace

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cloning the repository on a machine that has none (ADR 0024). The clone is a full `git clone --no-checkout`
// of an https or ssh origin, run with no way to ask a question, into a directory this code creates inside the
// browse root.

// Reasons of a check (device.clone.checked).
const (
	CloneOK               = "ok"
	CloneNoOrigin         = "no-origin"
	CloneNoAccess         = "no-access"
	CloneDestinationTaken = "destination-taken"
)

// Reasons of a failed clone (device.clone.failed), besides CloneNoAccess and CloneDestinationTaken.
const (
	CloneNoSpace   = "no-space"
	CloneTimeout   = "timeout"
	CloneGitFailed = "git-failed"
	CloneCancelled = "cancelled"
)

// Details of CloneDestinationTaken.
const (
	DetailExists      = "exists"
	DetailOutsideRoot = "outside-root"
	DetailSymlink     = "symlink"
	DetailNotWritable = "not-writable"
)

// CloneTotalTimeout and CloneStallTimeout end a clone: 600 s in all, and 60 s without a sign of life from
// git. The stall timer restarts on every line git writes.
const (
	CloneTotalTimeout = 600 * time.Second
	CloneStallTimeout = 60 * time.Second
	// CloneCheckTimeout bounds the reachability probe of a check.
	CloneCheckTimeout = 30 * time.Second
	// CloneDetailMax is the longest git text that leaves the machine.
	CloneDetailMax = 2048
	// cloneProgressEvery spaces the progress reports: at most four a second.
	cloneProgressEvery = 250 * time.Millisecond
)

const cloneJournalSuffix = ".clone.json"

// CloneFailure is the error of a failed clone; Reason is one of the Clone… reasons and Detail is git's text
// with credentials removed.
type CloneFailure struct {
	Reason string
	Detail string
}

func (f *CloneFailure) Error() string {
	if f.Detail == "" {
		return f.Reason
	}
	return f.Reason + ": " + f.Detail
}

// CloneCheck is the answer to "can this origin be cloned into this destination": CloneInto is the destination
// that was checked (the one asked for, or the suggestion).
type CloneCheck struct {
	Reason    string
	Detail    string
	CloneInto string
}

// Cloner clones into the browse root. Home is the state root that holds the journals.
type Cloner struct {
	Home string
	// Root is the browse root.
	Root string
	// Watched lists the watched project paths, which the suggested destination sits beside.
	Watched func() []string
	// Register makes the finished clone a watched project.
	Register func(path string) error

	// Total and Stall override the timeouts; zero means the defaults.
	Total time.Duration
	Stall time.Duration

	// allowLocal lets tests clone a local path over the file protocol; production never sets it.
	allowLocal bool

	mu sync.Mutex
}

// ---- origin ----

// CloneableOrigin reports whether raw is an origin that may be cloned: an https URL, an ssh URL or an
// scp-like `user@host:path`, without a password, a token, a query or a fragment. It returns the origin as
// given when it is.
func CloneableOrigin(raw string) (string, bool) {
	return cloneableOrigin(raw, false)
}

func cloneableOrigin(raw string, allowLocal bool) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 || strings.HasPrefix(raw, "-") {
		return "", false
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f || r == ' ' || r == '\\' {
			return "", false
		}
	}
	if allowLocal && filepath.IsAbs(raw) {
		return raw, true
	}
	if !strings.Contains(raw, "://") {
		host, path, ok := strings.Cut(raw, ":")
		if !ok || host == "" || path == "" || strings.ContainsAny(host, "/") || strings.HasPrefix(path, ":") {
			return "", false
		}
		if user, h, found := strings.Cut(host, "@"); found && (user == "" || h == "" || strings.Contains(h, "@")) {
			return "", false
		}
		return raw, true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return "", false
	}
	switch u.Scheme {
	case "https":
		if u.User != nil {
			return "", false
		}
	case "ssh":
		if u.User != nil {
			if _, hasPassword := u.User.Password(); hasPassword {
				return "", false
			}
		}
	default:
		return "", false
	}
	return raw, true
}

// RepoName is the name a clone of origin gets: the last element of its path without `.git`.
func RepoName(origin string) string {
	n := normalizeOrigin(origin)
	n = n[strings.LastIndex(n, "/")+1:]
	var b strings.Builder
	for _, r := range n {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), ".")
	if name == "" {
		return "repo"
	}
	return name
}

// ---- destination ----

// SuggestDestination is where a clone of repoName goes: beside the watched projects when at least two of
// them share a parent directory inside the browse root (the parent most of them share), else directly under
// the browse root. A name that is taken gets a number.
func SuggestDestination(watched []string, root, repoName string) string {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = filepath.Clean(root)
	}
	realRoot = filepath.Clean(realRoot)
	count := map[string]int{}
	for _, p := range watched {
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue
		}
		parent := filepath.Dir(filepath.Clean(real))
		if parent != realRoot && !within(realRoot, parent) {
			continue
		}
		count[parent]++
	}
	base := realRoot
	best := 1
	var parents []string
	for p := range count {
		parents = append(parents, p)
	}
	sort.Slice(parents, func(i, j int) bool {
		if count[parents[i]] != count[parents[j]] {
			return count[parents[i]] > count[parents[j]]
		}
		if len(parents[i]) != len(parents[j]) {
			return len(parents[i]) < len(parents[j])
		}
		return parents[i] < parents[j]
	})
	if len(parents) > 0 && count[parents[0]] > best {
		base = parents[0]
	}
	dest := filepath.Join(base, repoName)
	for n := 2; n < 100; n++ {
		if _, err := os.Lstat(dest); err != nil {
			break
		}
		dest = filepath.Join(base, repoName+"-"+strconv.Itoa(n))
	}
	return dest
}

// within reports whether p lies strictly under dir (both clean).
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// CheckDestination says whether dest may be created: ok, or the Detail… that refuses it. dest must not
// exist, lie inside the browse root, have a parent that is a directory the agent can write, and be reached
// through no symlink.
func CheckDestination(root, dest string) (string, bool) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return DetailNotWritable, false
	}
	realRoot = filepath.Clean(realRoot)
	if dest == "" || len(dest) > 4096 || strings.ContainsRune(dest, 0) || !filepath.IsAbs(dest) {
		return DetailOutsideRoot, false
	}
	dest = filepath.Clean(dest)
	if dest == realRoot {
		return DetailExists, false
	}
	if !within(realRoot, dest) {
		return DetailOutsideRoot, false
	}
	// Every directory from the root down to the parent is a real directory, not a link.
	rel, _ := filepath.Rel(realRoot, filepath.Dir(dest))
	dir := realRoot
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			dir = filepath.Join(dir, part)
			info, err := os.Lstat(dir)
			if err != nil {
				return DetailNotWritable, false
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return DetailSymlink, false
			}
			if !info.IsDir() {
				return DetailNotWritable, false
			}
		}
	}
	if info, err := os.Lstat(dest); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return DetailSymlink, false
		}
		return DetailExists, false
	} else if !errors.Is(err, os.ErrNotExist) {
		return DetailNotWritable, false
	}
	probe, err := os.MkdirTemp(dir, ".hoplet-write-")
	if err != nil {
		return DetailNotWritable, false
	}
	_ = os.Remove(probe)
	return "", true
}

// ---- git ----

func (c *Cloner) env() []string {
	allow := "https:ssh"
	if c.allowLocal {
		allow += ":file"
	}
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=true",
		"SSH_ASKPASS=true",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes",
		"GIT_ALLOW_PROTOCOL="+allow,
		"LC_ALL=C",
		"GIT_OPTIONAL_LOCKS=0",
	)
}

// cloneArgs are placed before every git subcommand of a clone: only https and ssh, no hooks, no monitor.
func (c *Cloner) cloneArgs() []string {
	args := append([]string(nil), gitArgs...)
	args = append(args,
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "protocol.ssh.allow=always",
	)
	if c.allowLocal {
		args = append(args, "-c", "protocol.file.allow=always")
	}
	return args
}

// Check answers whether origin can be cloned into cloneInto (empty: a suggestion), without writing anything.
func (c *Cloner) Check(ctx context.Context, origin, cloneInto string) CloneCheck {
	origin, ok := cloneableOrigin(origin, c.allowLocal)
	var watched []string
	if c.Watched != nil {
		watched = c.Watched()
	}
	dest := cloneInto
	if dest == "" {
		name := "repo"
		if ok {
			name = RepoName(origin)
		}
		dest = SuggestDestination(watched, c.Root, name)
	}
	res := CloneCheck{CloneInto: dest}
	if !ok {
		res.Reason = CloneNoOrigin
		return res
	}
	if detail, fine := CheckDestination(c.Root, dest); !fine {
		res.Reason, res.Detail = CloneDestinationTaken, detail
		return res
	}
	probe, cancel := context.WithTimeout(ctx, CloneCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(probe, "git", append(c.cloneArgs(), "ls-remote", "--", origin, "HEAD")...)
	cmd.Env = c.env()
	cmd.WaitDelay = 2 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[agent] clone check %s: %v: %s", StripSecrets(origin), err, StripSecrets(stderr.String()))
		res.Reason = CloneNoAccess
		return res
	}
	res.Reason = CloneOK
	return res
}

// ---- journal ----

type cloneJournal struct {
	HandoffID   string `json:"handoffId"`
	Origin      string `json:"origin"`
	Destination string `json:"destination"`
	// Complete is set once git has finished; a journal that never got there belongs to an unfinished clone.
	Complete bool `json:"complete,omitempty"`
}

func (c *Cloner) journalPath(handoffID string) string {
	return filepath.Join(c.Home, "handoff", handoffID+cloneJournalSuffix)
}

func (c *Cloner) writeJournal(j cloneJournal) error {
	dir := filepath.Join(c.Home, "handoff")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".clone-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.journalPath(j.HandoffID))
}

// Recover removes the directory of every clone that never completed, then drops its journal. The directory is
// removed only when it is still this clone's own: inside the browse root, not a link, and empty or holding
// nothing but a `.git` that has no remote yet or this origin's. A journal marked complete is dropped without
// touching the directory; the clone is registered again in case the crash came before that.
func (c *Cloner) Recover() {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, _ := os.ReadDir(filepath.Join(c.Home, "handoff"))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), cloneJournalSuffix) {
			continue
		}
		path := filepath.Join(c.Home, "handoff", e.Name())
		raw, err := os.ReadFile(path)
		var j cloneJournal
		if err != nil || json.Unmarshal(raw, &j) != nil {
			log.Printf("[agent] ignoring clone journal %s", e.Name())
			_ = os.Remove(path)
			continue
		}
		if j.Complete {
			if c.Register != nil {
				if err := c.Register(j.Destination); err != nil {
					log.Printf("[agent] clone %s: registering %s: %v", j.HandoffID, j.Destination, err)
				}
			}
		} else if c.ownsDirectory(j) {
			if err := os.RemoveAll(j.Destination); err != nil {
				log.Printf("[agent] clone %s: removing %s: %v", j.HandoffID, j.Destination, err)
				continue
			}
			log.Printf("[agent] clone %s was interrupted; removed %s", j.HandoffID, StripSecrets(j.Destination))
		}
		_ = os.Remove(path)
	}
}

// ownsDirectory reports whether j.Destination is an unfinished clone of j.Origin that this code made.
func (c *Cloner) ownsDirectory(j cloneJournal) bool {
	realRoot, err := filepath.EvalSymlinks(c.Root)
	if err != nil || !filepath.IsAbs(j.Destination) {
		return false
	}
	dest := filepath.Clean(j.Destination)
	if !within(filepath.Clean(realRoot), dest) {
		return false
	}
	info, err := os.Lstat(dest)
	if err != nil || !info.IsDir() {
		return false
	}
	names, err := os.ReadDir(dest)
	if err != nil {
		return false
	}
	if len(names) == 0 {
		return true
	}
	for _, n := range names {
		if n.Name() != ".git" || !n.IsDir() {
			return false
		}
	}
	cfg, err := os.ReadFile(filepath.Join(dest, ".git", "config"))
	if err != nil || !bytes.Contains(cfg, []byte("[remote \"origin\"]")) {
		return true
	}
	return bytes.Contains(cfg, []byte(j.Origin))
}

// ---- clone ----

// Clone clones origin into dest and registers the result. progress is called at most four times a second with
// the bytes received and the total when git has said it. It returns the path of the clone, or a *CloneFailure;
// a cancelled ctx (cause irrelevant) ends the clone as CloneCancelled. Whatever failed, the directory this
// call created is gone when it returns.
func (c *Cloner) Clone(ctx context.Context, handoffID, origin, dest string, progress func(received, total int64)) (string, error) {
	if !opaqueID.MatchString(handoffID) {
		return "", &CloneFailure{Reason: CloneGitFailed, Detail: "invalid-handoff-id"}
	}
	origin, ok := cloneableOrigin(origin, c.allowLocal)
	if !ok {
		return "", &CloneFailure{Reason: CloneGitFailed, Detail: CloneNoOrigin}
	}
	if _, fine := CheckDestination(c.Root, dest); !fine {
		return "", &CloneFailure{Reason: CloneDestinationTaken}
	}
	dest = filepath.Clean(dest)
	label := StripSecrets(origin)

	c.mu.Lock()
	// The journal first, then the directory: a crash between them leaves a journal for a path that is not
	// there, which Recover drops.
	if err := c.writeJournal(cloneJournal{HandoffID: handoffID, Origin: origin, Destination: dest}); err != nil {
		c.mu.Unlock()
		return "", &CloneFailure{Reason: CloneGitFailed, Detail: "journal"}
	}
	err := os.Mkdir(dest, 0o777)
	c.mu.Unlock()
	if err != nil {
		_ = os.Remove(c.journalPath(handoffID))
		if errors.Is(err, os.ErrExist) {
			return "", &CloneFailure{Reason: CloneDestinationTaken, Detail: DetailExists}
		}
		return "", &CloneFailure{Reason: reasonOf(err.Error()), Detail: StripSecrets(err.Error())}
	}
	cleanup := func() {
		removeAll(dest)
		_ = os.Remove(c.journalPath(handoffID))
	}

	total, stall := c.Total, c.Stall
	if total <= 0 {
		total = CloneTotalTimeout
	}
	if stall <= 0 {
		stall = CloneStallTimeout
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	totalTimer := time.AfterFunc(total, func() { cancel(errCloneTimeout) })
	defer totalTimer.Stop()
	stallTimer := time.AfterFunc(stall, func() { cancel(errCloneTimeout) })
	defer stallTimer.Stop()

	args := append(c.cloneArgs(), "clone", "--no-checkout", "--progress", "--", origin, dest)
	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Env = c.env()
	cmd.WaitDelay = 2 * time.Second
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cleanup()
		return "", &CloneFailure{Reason: CloneGitFailed}
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		if runCtx.Err() != nil {
			return "", &CloneFailure{Reason: CloneCancelled}
		}
		return "", &CloneFailure{Reason: CloneGitFailed, Detail: StripSecrets(err.Error())}
	}
	log.Printf("[agent] clone %s: %s into %s", handoffID, label, dest)

	tail := newTail()
	last := time.Time{}
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	sc.Split(splitProgress)
	for sc.Scan() {
		stallTimer.Reset(stall)
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if received, tot, isProgress := parseProgress(line); isProgress {
			if progress != nil && time.Since(last) >= cloneProgressEvery {
				last = time.Now()
				progress(received, tot)
			}
			continue
		}
		tail.add(line)
	}
	_, _ = io.Copy(io.Discard, stderr)
	waitErr := cmd.Wait()

	if cause := context.Cause(runCtx); cause != nil && (waitErr != nil || runCtx.Err() != nil) {
		cleanup()
		if errors.Is(cause, errCloneTimeout) {
			log.Printf("[agent] clone %s: timed out", handoffID)
			return "", &CloneFailure{Reason: CloneTimeout}
		}
		return "", &CloneFailure{Reason: CloneCancelled}
	}
	if waitErr != nil {
		text := tail.text()
		cleanup()
		reason := reasonOf(text)
		log.Printf("[agent] clone %s: %s: %v: %s", handoffID, label, waitErr, StripSecrets(text))
		failure := &CloneFailure{Reason: reason}
		if reason == CloneGitFailed {
			failure.Detail = StripSecrets(text)
		}
		return "", failure
	}
	if err := c.writeJournal(cloneJournal{HandoffID: handoffID, Origin: origin, Destination: dest, Complete: true}); err != nil {
		cleanup()
		return "", &CloneFailure{Reason: CloneGitFailed, Detail: "journal"}
	}
	if c.Register != nil {
		if err := c.Register(dest); err != nil {
			cleanup()
			log.Printf("[agent] clone %s: registering %s: %v", handoffID, dest, err)
			return "", &CloneFailure{Reason: CloneGitFailed, Detail: "register-failed"}
		}
	}
	_ = os.Remove(c.journalPath(handoffID))
	return dest, nil
}

var errCloneTimeout = errors.New("clone-timeout")

func removeAll(path string) {
	for i := 0; i < 5; i++ {
		if err := os.RemoveAll(path); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.Printf("[agent] could not remove %s", path)
}

// networkFailures are what git and its transports say when the connection failed rather than the access.
var networkFailures = []string{
	"ssl_error_syscall",
	"ssl_connect",
	"ssl_error",
	"gnutls",
	"could not resolve host",
	"connection timed out",
	"operation timed out",
	"connection reset",
	"connection refused",
	"early eof",
	"rpc failed",
	"failed to connect to",
	"network is unreachable",
	"no route to host",
	"the remote end hung up unexpectedly",
}

// reasonOf maps what git said to a failure reason. A network failure is git-failed: it is not the user's
// credentials, and trying again may work.
func reasonOf(text string) string {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "no space left"), strings.Contains(t, "enospc"), strings.Contains(t, "disk quota"):
		return CloneNoSpace
	case strings.Contains(t, "already exists and is not an empty"):
		return CloneDestinationTaken
	case containsAny(t, accessDenials):
		return CloneNoAccess
	case containsAny(t, networkFailures):
		return CloneGitFailed
	case containsAny(t, unreadable):
		return CloneNoAccess
	}
	return CloneGitFailed
}

// accessDenials are what git says when the remote refused or asked for credentials. They win over a network
// phrase in the same output.
var accessDenials = []string{
	"permission denied",
	"authentication failed",
	"terminal prompts disabled",
	"could not read username",
	"could not read password",
	"repository not found",
	"host key verification failed",
	"access denied",
}

// unreadable is what git says last when it could not read the remote for a reason it did not name. With no
// network phrase before it, the access is the likelier cause.
var unreadable = []string{"could not read from remote", "unable to access"}

func containsAny(text string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(text, n) {
			return true
		}
	}
	return false
}

// splitProgress splits git's stderr at both \n and \r: a progress line is rewritten in place with \r.
func splitProgress(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

var (
	progressLine = regexp.MustCompile(`^(?:remote: )?(?:Counting|Compressing|Enumerating|Receiving|Resolving) (?:objects|deltas)\b`)
	receivedSize = regexp.MustCompile(`^Receiving objects:\s+(\d+)%[^,]*,\s+([0-9.]+)\s+(B|KiB|MiB|GiB|TiB|bytes)\b`)
)

// parseProgress recognises a git progress line. received is the size of the pack so far (0 before git says);
// total is -1 because git reports a percentage, never a total.
func parseProgress(line string) (received, total int64, ok bool) {
	if !progressLine.MatchString(line) && !strings.HasPrefix(line, "Cloning into") {
		return 0, -1, false
	}
	if m := receivedSize.FindStringSubmatch(line); m != nil {
		f, _ := strconv.ParseFloat(m[2], 64)
		mult := map[string]float64{"B": 1, "bytes": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}[m[3]]
		received = int64(f * mult)
	}
	return received, -1, true
}

// tail keeps the last few lines git wrote that are not progress.
type tail struct{ lines []string }

func newTail() *tail { return &tail{} }

func (t *tail) add(line string) {
	t.lines = append(t.lines, line)
	if len(t.lines) > 12 {
		t.lines = t.lines[1:]
	}
}

func (t *tail) text() string { return strings.Join(t.lines, "\n") }

// ---- credentials ----

var (
	textUserinfo  = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]*@`)
	secretAssign  = regexp.MustCompile(`(?i)\b((?:access_|auth_|private_)?token|password|passwd|secret|api[_-]?key)=[^\r\n]*`)
	authHeader    = regexp.MustCompile(`(?i)(Authorization:)[^\r\n]*`)
	bearerToken   = regexp.MustCompile(`(?i)\b(Bearer)\s+[^\r\n]*`)
	removedMarker = "[removed]"
)

// StripSecrets removes what git's text may carry of a credential: `user:secret@` in a URL, whatever follows
// `token=`, `password=` and the like, an Authorization header and a Bearer token; then it cuts the text at
// CloneDetailMax bytes. All git text that is shown or logged goes through it.
func StripSecrets(s string) string {
	s = textUserinfo.ReplaceAllString(s, "${1}")
	s = secretAssign.ReplaceAllString(s, "${1}="+removedMarker)
	s = authHeader.ReplaceAllString(s, "${1} "+removedMarker)
	s = bearerToken.ReplaceAllString(s, "${1} "+removedMarker)
	if len(s) > CloneDetailMax {
		s = s[:CloneDetailMax]
		for len(s) > 0 && !utf8Valid(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

func utf8Valid(s string) bool {
	return strings.ToValidUTF8(s, "�") == s
}
