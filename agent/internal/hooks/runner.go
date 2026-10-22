package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
)

// What a run that has no exit code reports as its error (the values are those of hook.result in shared/src/wire).
const (
	ErrTimeout     = "timeout"
	ErrSpawnFailed = "spawn-failed"
	ErrDropped     = "dropped"
)

// killGrace is the time between SIGTERM and SIGKILL of a script that overran its timeout.
const killGrace = 2 * time.Second

// passedEnv are the variables of the agent's own environment a script gets.
var passedEnv = []string{"PATH", "HOME", "USER", "LANG", "TZ", "TMPDIR"}

// Result is the outcome of one event for one hook: what hook.result carries.
type Result struct {
	HookID     string
	EventID    string
	OK         bool
	ExitCode   *int
	Error      string
	DurationMS int64
	Stderr     string
}

// Options configure a Runner.
type Options struct {
	// Home is hoplet_home: the working directory of an event outside every watched project, and the place of
	// the journal.
	Home string
	// Paths are the projects the machine watches.
	Paths func() []string
	// Report receives every result; it is called from the hook's own goroutine.
	Report func(Result)
	// Lookup reads the agent's environment; os.LookupEnv when nil.
	Lookup func(name string) (string, bool)
}

type job struct {
	eventID   string
	eventType string
	cwd       string
	body      []byte
}

type hook struct {
	entry   Entry
	queue   chan job
	runs    atomic.Int64
	failed  atomic.Int64
	dropped atomic.Int64
}

// Runner runs the script hooks of one agent.
type Runner struct {
	opts   Options
	hooks  map[string]*hook
	order  []string
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New makes a Runner for entries that Validate accepted and starts one worker per entry.
func New(entries []Entry, opts Options) *Runner {
	if opts.Lookup == nil {
		opts.Lookup = os.LookupEnv
	}
	if opts.Paths == nil {
		opts.Paths = func() []string { return nil }
	}
	if opts.Report == nil {
		opts.Report = func(Result) {}
	}
	r := &Runner{opts: opts, hooks: make(map[string]*hook, len(entries))}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	for _, e := range entries {
		h := &hook{entry: e, queue: make(chan job, QueueSize)}
		r.hooks[e.ID] = h
		r.order = append(r.order, e.ID)
		r.wg.Add(1)
		go r.work(h)
	}
	return r
}

// Close kills the scripts that run and stops the workers.
func (r *Runner) Close() {
	r.cancel()
	r.wg.Wait()
}

// Info lists the entries with their counters since the agent started.
func (r *Runner) Info() []Info {
	out := make([]Info, 0, len(r.order))
	for _, id := range r.order {
		h := r.hooks[id]
		out = append(out, Info{
			ID: id, Command: h.entry.Command,
			Events:    slices.Clone(h.entry.Events),
			Projects:  slices.Clone(h.entry.Projects),
			TimeoutMS: h.entry.TimeoutMS,
			Env:       slices.Clone(h.entry.Env),
			Runs:      h.runs.Load(), Failed: h.failed.Load(), Dropped: h.dropped.Load(),
		})
	}
	return out
}

// envelope is the part of an event the agent reads; the rest goes to the script untouched.
type envelope struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Session struct {
		Project struct {
			Path string `json:"path"`
		} `json:"project"`
	} `json:"session"`
}

// Handle takes one event for the hook hookID. It never blocks: the event is queued for the hook's worker, or
// reported dropped when the hook is not one of this machine's, does not take the event, or has 16 events waiting.
func (r *Runner) Handle(hookID string, event json.RawMessage) {
	var ev envelope
	if err := json.Unmarshal(event, &ev); err != nil || ev.ID == "" {
		log.Printf("[hooks] an event for %q could not be read", hookID)
		return
	}
	h := r.hooks[hookID]
	path := ev.Session.Project.Path
	if h == nil || !h.takes(ev.Type, path) {
		r.opts.Report(Result{HookID: hookID, EventID: ev.ID, Error: ErrDropped})
		return
	}
	j := job{eventID: ev.ID, eventType: ev.Type, cwd: r.cwd(path), body: bytes.Clone(event)}
	select {
	case h.queue <- j:
	default:
		h.dropped.Add(1)
		r.opts.Report(Result{HookID: hookID, EventID: ev.ID, Error: ErrDropped})
	}
}

// takes says whether the entry lists the event type and applies to the project.
func (h *hook) takes(eventType, path string) bool {
	if eventType == TestEvent {
		return true
	}
	if !slices.Contains(h.entry.Events, AllEvents) && !slices.Contains(h.entry.Events, eventType) {
		return false
	}
	if len(h.entry.Projects) == 0 {
		return true
	}
	if path == "" {
		return false
	}
	for _, p := range h.entry.Projects {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// cwd is the project directory when the machine watches it, else hoplet_home.
func (r *Runner) cwd(path string) string {
	if path != "" {
		clean := filepath.Clean(path)
		for _, p := range r.opts.Paths() {
			if filepath.Clean(p) == clean {
				if info, err := os.Stat(clean); err == nil && info.IsDir() {
					return clean
				}
			}
		}
	}
	return r.opts.Home
}

func (r *Runner) work(h *hook) {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case j := <-h.queue:
			res := r.run(h, j)
			if r.ctx.Err() != nil {
				return
			}
			r.opts.Report(res)
		}
	}
}

func (r *Runner) environ(h *hook, j job) []string {
	var env []string
	seen := map[string]bool{}
	for _, n := range append(slices.Clone(passedEnv), h.entry.Env...) {
		if seen[n] {
			continue
		}
		seen[n] = true
		if v, ok := r.opts.Lookup(n); ok {
			env = append(env, n+"="+v)
		}
	}
	return append(env, "HOPLET_HOOK_ID="+h.entry.ID, "HOPLET_EVENT_ID="+j.eventID, "HOPLET_EVENT_TYPE="+j.eventType)
}

// run executes the program once. The event goes to standard input, nothing reaches argv, and the script has a
// process group of its own that is killed as a whole.
func (r *Runner) run(h *hook, j job) Result {
	res := Result{HookID: h.entry.ID, EventID: j.eventID}
	began := time.Now()
	h.runs.Add(1)
	fail := func() Result {
		res.DurationMS = time.Since(began).Milliseconds()
		h.failed.Add(1)
		return res
	}

	var stderr capture
	cmd := exec.Command(h.entry.Command)
	cmd.Dir = j.cwd
	cmd.Env = r.environ(h, j)
	cmd.Stdin = bytes.NewReader(j.body)
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = killGrace // a descendant that keeps the pipe open must not hold the hook
	if err := cmd.Start(); err != nil {
		log.Printf("[hooks] %s: start: %v", h.entry.ID, err)
		res.Error = ErrSpawnFailed
		return fail()
	}
	pid := cmd.Process.Pid
	runID := newRunID()
	start, _ := startTime(pid)
	if err := writeRecord(r.opts.Home, record{RunID: runID, HookID: h.entry.ID, PID: pid, Start: start}); err != nil {
		log.Printf("[hooks] %s: journal: %v", h.entry.ID, err)
	}
	defer removeRecord(r.opts.Home, runID)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(time.Duration(h.entry.TimeoutMS) * time.Millisecond)
	defer timer.Stop()
	timedOut := false
	select {
	case <-done:
	case <-timer.C:
		timedOut = true
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(killGrace):
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			<-done
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL) // whatever the script left in its group
	case <-r.ctx.Done():
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-done
		return res
	}

	res.Stderr = stderr.String()
	res.DurationMS = time.Since(began).Milliseconds()
	if timedOut {
		res.Error = ErrTimeout
		h.failed.Add(1)
		return res
	}
	code := exitCode(cmd.ProcessState)
	res.ExitCode = &code
	res.OK = code == 0
	if !res.OK {
		h.failed.Add(1)
	}
	return res
}

// exitCode is the exit status of a finished process; a signal counts as 128 plus its number, as a shell reports it.
func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

// capture keeps the first StderrBytes of what is written and swallows the rest, so a chatty script is never
// blocked on a full pipe.
type capture struct {
	mu        sync.Mutex
	buf       []byte
	truncated bool
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room := StderrBytes - len(c.buf)
	if room > 0 {
		c.buf = append(c.buf, p[:min(room, len(p))]...)
	}
	if len(p) > room {
		c.truncated = true
	}
	return len(p), nil
}

// String is the kept text, cut at a character boundary and valid UTF-8.
func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.buf
	if c.truncated {
		for i := 0; i < utf8.UTFMax && len(b) > 0; i++ {
			if r, size := utf8.DecodeLastRune(b); r == utf8.RuneError && size <= 1 {
				b = b[:len(b)-1]
			} else {
				break
			}
		}
	}
	return strings.ToValidUTF8(string(b), "�")
}
