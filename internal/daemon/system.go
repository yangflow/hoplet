package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/session"
	"github.com/yangflow/hoplet/proto"
)

// DefaultProbeURL is what doctor reaches to check the network path
// harnesses use.
const DefaultProbeURL = "https://api.anthropic.com"

// minimalEnvKeys are the daemon's own variables harnesses get when the
// login shell cannot be read.
var minimalEnvKeys = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "TMPDIR"}

func minimalEnv() []string {
	var out []string
	for _, k := range minimalEnvKeys {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// doctorTimeout bounds the whole doctor run; defaultProbeTimeout bounds each
// check in it (the network probe and every harness probe).
const (
	doctorTimeout       = 15 * time.Second
	defaultProbeTimeout = 5 * time.Second
)

// system keeps the harness environment and answers doctor.
type system struct {
	src          env.Source
	mgr          *session.Manager
	harnesses    map[string]harness.Harness
	probeURL     string
	probeTimeout time.Duration // per check; defaultProbeTimeout when zero
	log          *slog.Logger

	// refreshMu serialises refreshes so that the snapshot and the
	// manager's environment are applied in the order they were read.
	refreshMu sync.Mutex

	mu   sync.Mutex
	snap env.Snapshot
	err  error // why the login shell could not be read
}

func (s *system) timeout() time.Duration {
	if s.probeTimeout > 0 {
		return s.probeTimeout
	}
	return defaultProbeTimeout
}

// resolve reads the environment; on failure the previous one stays, or the
// minimal one when there is none yet.
func resolve(ctx context.Context, src env.Source, refresh bool) (env.Snapshot, error) {
	snap, err := src.Environment(ctx, refresh)
	if err != nil {
		return env.Snapshot{Vars: minimalEnv(), ProxySource: env.ProxyNone}, err
	}
	return snap, nil
}

func (s *system) set(snap env.Snapshot, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap, s.err = snap, err
}

// state copies the current snapshot and read error.
func (s *system) state() (env.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.snap
	snap.Vars = slices.Clone(snap.Vars)
	snap.Dropped = slices.Clone(snap.Dropped)
	return snap, s.err
}

// refresh reads the login shell again and gives harnesses started from now
// on the new environment. On failure nothing changes.
func (s *system) refresh(ctx context.Context) (proto.EnvInfo, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	snap, err := s.src.Environment(ctx, true)
	if err != nil {
		return proto.EnvInfo{}, err
	}
	s.set(snap, nil)
	s.mgr.SetEnv(snap.Vars)
	s.log.Info("environment refreshed", "vars", len(snap.Vars), "withheld", snap.Dropped, "proxy", redactURL(snap.Proxy))
	return infoOf(snap, nil), nil
}

func (s *system) info() proto.EnvInfo { return infoOf(s.state()) }

func infoOf(snap env.Snapshot, readErr error) proto.EnvInfo {
	info := proto.EnvInfo{
		Shell: snap.Shell, Vars: len(snap.Vars), Withheld: nonNil(slices.Clone(snap.Dropped)),
		Proxy: redactURL(snap.Proxy), ProxySource: string(snap.ProxySource), Note: snap.Note, Warning: snap.Stale, ResolvedAt: snap.ResolvedAt,
	}
	if readErr != nil {
		info.Error = readErr.Error()
	}
	return info
}

// redactURL hides a proxy URL's password (see env.RedactProxy).
func redactURL(s string) string { return env.RedactProxy(s) }

// localeVars are the variables that select a locale.
var localeVars = []string{"LANG", "LC_ALL", "LC_CTYPE"}

func hasLocale(vars []string) bool {
	for _, kv := range vars {
		if k, v, _ := strings.Cut(kv, "="); v != "" && slices.Contains(localeVars, k) {
			return true
		}
	}
	return false
}

// environmentCheck summarises the harness environment: fail when the login
// shell could not be read, warn when it is stale or lacks a locale.
func environmentCheck(snap env.Snapshot, info proto.EnvInfo) proto.Check {
	if info.Error != "" {
		return proto.Check{Name: "environment", Status: proto.CheckFail,
			Detail: "the login shell could not be read, so harnesses get a minimal environment: " + info.Error}
	}
	status := proto.CheckOK
	d := fmt.Sprintf("%s: %d variables", info.Shell, info.Vars)
	if len(info.Withheld) > 0 {
		d += "; credentials withheld: " + strings.Join(info.Withheld, ", ")
	}
	if info.Warning != "" {
		status = proto.CheckWarn
		d += "; " + info.Warning
	}
	if !hasLocale(snap.Vars) {
		status = proto.CheckWarn
		d += "; no LANG, LC_ALL or LC_CTYPE: harnesses run in the C locale; set LANG in your shell startup files"
	}
	return proto.Check{Name: "environment", Status: status, Detail: d}
}

// doctor checks the environment, the network path and the harnesses. The
// whole run is bounded by doctorTimeout and every check by the probe
// timeout; checks run concurrently and keep their order.
func (s *system) doctor(ctx context.Context) proto.DoctorReport {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	snap, readErr := s.state()
	info := infoOf(snap, readErr)
	var r proto.DoctorReport
	r.Checks = append(r.Checks, environmentCheck(snap, info))
	kinds := make([]string, 0, len(s.harnesses))
	for k := range s.harnesses {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	checks := make([]proto.Check, 1+len(kinds))
	var wg sync.WaitGroup
	wg.Go(func() { checks[0] = s.probe(ctx, snap, info) })
	for i, k := range kinds {
		wg.Go(func() { checks[1+i] = s.probeHarness(ctx, k, s.harnesses[k]) })
	}
	wg.Wait()
	r.Checks = append(r.Checks, checks...)
	if len(kinds) == 0 {
		r.Checks = append(r.Checks, proto.Check{Name: "harnesses", Status: proto.CheckWarn, Detail: "no harness adapters are registered yet"})
	}
	return r
}

// probeHarness asks a harness for its version within the probe timeout. A
// harness that ignores its context is abandoned, not waited for.
func (s *system) probeHarness(ctx context.Context, kind string, h harness.Harness) proto.Check {
	name := "harness " + kind
	ctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	type result struct {
		info harness.Info
		err  error
	}
	done := make(chan result, 1)
	go func() {
		info, err := h.Probe(ctx)
		done <- result{info, err}
	}()
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		select {
		case res = <-done: // answered at the deadline
		default:
			res.err = ctx.Err()
		}
	}
	switch {
	case res.err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return proto.Check{Name: name, Status: proto.CheckFail, Detail: fmt.Sprintf("did not answer within %v", s.timeout())}
	case res.err != nil:
		return proto.Check{Name: name, Status: proto.CheckFail, Detail: res.err.Error()}
	}
	c := proto.Check{Name: name, Status: proto.CheckOK, Detail: "version " + res.info.Version}
	if !res.info.Supported {
		c.Status, c.Detail = proto.CheckWarn, c.Detail+" is not verified; new turns are refused"
	}
	if res.info.Notice != "" {
		c.Detail += "; " + res.info.Notice
		if res.info.Attention {
			c.Status = proto.CheckWarn
		}
	}
	return c
}

// vars is the environment harnesses start with now.
func (s *system) vars() []string {
	snap, _ := s.state()
	return slices.Clone(snap.Vars)
}
