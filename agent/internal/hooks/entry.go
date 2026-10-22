// Package hooks runs the script hooks of ADR 0031: programs the operator declares in the agent's config.json
// and the agent runs, as the operator and with a clean environment, when the core sends it a lifecycle event.
//
// An Entry is read and checked when the agent starts; nothing the core or the web UI sends can add, change or
// point one. A Runner executes an entry's program with no shell and no argument, the event on standard input,
// its own process group, a timeout, a queue of 16 and a journal under <hoplet_home>/hooks/ that lets the next
// start-up kill what a crash left.
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Limits of ADR 0031.
const (
	DefaultTimeoutMS = 10000
	MaxTimeoutMS     = 60000
	QueueSize        = 16
	StderrBytes      = 1024
	MaxHooks         = 20
	AllEvents        = "*"
)

// EventTypes are the lifecycle events a hook can list (shared/src/rest/hooks.ts).
var EventTypes = []string{
	"session.started", "turn.completed", "session.ended", "permission.requested", "permission.resolved",
	"session.error", "handoff.started", "handoff.completed",
}

// TestEvent is the event of Send test event; every entry runs for it whatever it lists.
const TestEvent = "hook.test"

var (
	idPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// Entry is one script hook of the `hooks` list in config.json.
type Entry struct {
	ID        string   `json:"id"`
	Command   string   `json:"command"`
	Events    []string `json:"events"`
	Projects  []string `json:"projects,omitempty"`
	TimeoutMS int      `json:"timeout_ms,omitempty"`
	Env       []string `json:"env,omitempty"`
}

// Validate checks the entries and fills in the timeout default. It refuses an entry whose command is not an
// absolute path to a regular, executable file that neither group nor others can write.
func Validate(entries []Entry) error {
	if len(entries) > MaxHooks {
		return fmt.Errorf("at most %d hooks", MaxHooks)
	}
	seen := map[string]bool{}
	for i := range entries {
		e := &entries[i]
		if !idPattern.MatchString(e.ID) {
			return fmt.Errorf("hook %d: id %q must be a slug of lower-case letters, digits and hyphens", i, e.ID)
		}
		if seen[e.ID] {
			return fmt.Errorf("hook %q: id is used twice", e.ID)
		}
		seen[e.ID] = true
		if err := checkCommand(e.Command); err != nil {
			return fmt.Errorf("hook %q: command: %w", e.ID, err)
		}
		if err := checkEvents(e.Events); err != nil {
			return fmt.Errorf("hook %q: events: %w", e.ID, err)
		}
		for _, p := range e.Projects {
			if !filepath.IsAbs(p) || filepath.Clean(p) != p {
				return fmt.Errorf("hook %q: projects: %q must be a clean absolute path", e.ID, p)
			}
		}
		if e.TimeoutMS == 0 {
			e.TimeoutMS = DefaultTimeoutMS
		}
		if e.TimeoutMS < 0 || e.TimeoutMS > MaxTimeoutMS {
			return fmt.Errorf("hook %q: timeout_ms must be between 1 and %d", e.ID, MaxTimeoutMS)
		}
		for _, n := range e.Env {
			if !envPattern.MatchString(n) || strings.HasPrefix(n, "HOPLET_") {
				return fmt.Errorf("hook %q: env: %q is not a variable name it may pass", e.ID, n)
			}
		}
	}
	return nil
}

func checkCommand(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%q is not an absolute path", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", path)
	}
	perm := info.Mode().Perm()
	if perm&0o111 == 0 {
		return fmt.Errorf("%q is not executable", path)
	}
	if perm&0o022 != 0 {
		return fmt.Errorf("%q is writable by group or others", path)
	}
	return nil
}

func checkEvents(events []string) error {
	if len(events) == 0 {
		return fmt.Errorf("list at least one event type, or %q", AllEvents)
	}
	for _, ev := range events {
		if ev == AllEvents {
			if len(events) != 1 {
				return fmt.Errorf("%q stands alone", AllEvents)
			}
			continue
		}
		ok := false
		for _, t := range EventTypes {
			ok = ok || t == ev
		}
		if !ok {
			return fmt.Errorf("%q is not an event type", ev)
		}
	}
	return nil
}

// Info is what device.hello announces of an entry.
type Info struct {
	ID        string
	Command   string
	Events    []string
	Projects  []string
	TimeoutMS int
	Env       []string
	Runs      int64
	Failed    int64
	Dropped   int64
}
