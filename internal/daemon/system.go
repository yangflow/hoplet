package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"

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

// system keeps the harness environment and answers doctor.
type system struct {
	src       env.Source
	mgr       *session.Manager
	harnesses map[string]harness.Harness
	probeURL  string
	log       *slog.Logger

	mu   sync.Mutex
	snap env.Snapshot
	err  error // why the login shell could not be read
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

// refresh reads the login shell again and gives harnesses started from now
// on the new environment. On failure nothing changes.
func (s *system) refresh(ctx context.Context) (proto.EnvInfo, error) {
	snap, err := s.src.Environment(ctx, true)
	if err != nil {
		return proto.EnvInfo{}, err
	}
	s.set(snap, nil)
	s.mgr.SetEnv(snap.Vars)
	s.log.Info("environment refreshed", "vars", len(snap.Vars), "withheld", snap.Dropped, "proxy", snap.Proxy)
	return s.info(), nil
}

func (s *system) info() proto.EnvInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	info := proto.EnvInfo{
		Shell: s.snap.Shell, Vars: len(s.snap.Vars), Withheld: nonNil(slices.Clone(s.snap.Dropped)),
		Proxy: s.snap.Proxy, ProxySource: string(s.snap.ProxySource), ResolvedAt: s.snap.ResolvedAt,
	}
	if s.err != nil {
		info.Error = s.err.Error()
	}
	return info
}

// proxyURL is the unredacted proxy harnesses use, nil for none.
func (s *system) proxyURL() *url.URL {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, kv := range s.snap.Vars {
		if v, ok := strings.CutPrefix(kv, "HTTPS_PROXY="); ok && v != "" {
			if u, err := url.Parse(v); err == nil {
				return u
			}
		}
	}
	return nil
}

// doctor checks the environment, the network path and the harnesses.
func (s *system) doctor(ctx context.Context) proto.DoctorReport {
	var r proto.DoctorReport
	info := s.info()
	if info.Error != "" {
		r.Checks = append(r.Checks, proto.Check{Name: "environment", Status: proto.CheckFail,
			Detail: "the login shell could not be read, so harnesses get a minimal environment: " + info.Error})
	} else {
		d := fmt.Sprintf("%s: %d variables", info.Shell, info.Vars)
		if len(info.Withheld) > 0 {
			d += "; credentials withheld: " + strings.Join(info.Withheld, ", ")
		}
		r.Checks = append(r.Checks, proto.Check{Name: "environment", Status: proto.CheckOK, Detail: d})
	}
	r.Checks = append(r.Checks, s.probe(ctx, info))
	kinds := make([]string, 0, len(s.harnesses))
	for k := range s.harnesses {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	if len(kinds) == 0 {
		r.Checks = append(r.Checks, proto.Check{Name: "harnesses", Status: proto.CheckWarn, Detail: "no harness adapters are registered yet"})
	}
	for _, k := range kinds {
		r.Checks = append(r.Checks, probeHarness(ctx, k, s.harnesses[k]))
	}
	return r
}

func probeHarness(ctx context.Context, kind string, h harness.Harness) proto.Check {
	name := "harness " + kind
	info, err := h.Probe(ctx)
	switch {
	case err != nil:
		return proto.Check{Name: name, Status: proto.CheckFail, Detail: err.Error()}
	case !info.Supported:
		return proto.Check{Name: name, Status: proto.CheckWarn, Detail: "version " + info.Version + " is outside the verified range; new turns are refused"}
	default:
		return proto.Check{Name: name, Status: proto.CheckOK, Detail: "version " + info.Version}
	}
}
