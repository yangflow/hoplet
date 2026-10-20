// Package resources measures the load of the machine and of the sessions Hoplet started on it, for the
// resources panel of Settings › Machines (#88). One process listing per sample serves both: the whole
// machine's CPU, memory and the disk that holds the workspace, and for each session the sum over the process
// tree of its Claude Code process and its terminal shell. What leaves the package is numbers and session IDs;
// process names, command lines, environments and process IDs are never read or reported.
package resources

import (
	"context"
	"log"
	"sort"
	"time"
)

const (
	// DefaultInterval is the time between two samples (shared/src/wire RESOURCES_INTERVAL_MS).
	DefaultInterval = 15 * time.Second
	// MaxSessions is the most sessions one sample holds (RESOURCES_SESSIONS_MAX).
	MaxSessions = 64
	readTimeout = 10 * time.Second
)

// Proc is one process of a reading.
type Proc struct {
	PID, PPID int
	// RSS is the resident memory in bytes.
	RSS uint64
	// CPU is the share of the whole machine's CPU in percent since the previous reading; nil when it cannot be
	// told yet.
	CPU *float64
}

// Reading is what one pass over the machine sees.
type Reading struct {
	// CPU is the whole machine's CPU in percent; nil when it cannot be told yet (the first reading where a
	// difference to an earlier one is needed).
	CPU      *float64
	MemUsed  uint64
	MemTotal uint64
	Procs    []Proc
}

// Reader reads the machine. It keeps what it needs from the previous reading, so one Reader serves one Sampler.
// The implementations are in read_linux.go and read_darwin.go.
type Reader interface {
	Read(ctx context.Context) (Reading, error)
}

// SessionUsage is the load of one session's processes.
type SessionUsage struct {
	SessionID string
	// CPU is the share of the whole machine's CPU in percent; nil when no process of the session has a figure.
	CPU *float64
	// RSS is the resident memory in bytes.
	RSS uint64
	// Children is the number of descendant processes of the roots.
	Children int
}

// Sample is one report.
type Sample struct {
	At        time.Time
	CPU       *float64
	MemUsed   uint64
	MemTotal  uint64
	DiskFree  uint64
	DiskTotal uint64
	Sessions  []SessionUsage
}

// Config wires a Sampler to the machine and to the core connection.
type Config struct {
	Reader Reader
	// DiskPath is a path on the volume to measure: the agent's browse root.
	DiskPath string
	// Disk returns the free and the total bytes of the volume of path; statDisk when nil.
	Disk func(path string) (free, total uint64, err error)
	// Roots returns, for each session Hoplet runs a process for, the process IDs its tree starts at (its Claude
	// Code process, its terminal shell); 0 ones are ignored.
	Roots func() map[string][]int
	// Send delivers a sample. A sample sent while the core is away is dropped, never queued.
	Send func(Sample)

	Interval time.Duration
	Now      func() time.Time
	Logf     func(format string, args ...any)
}

// Sampler takes a sample every Interval.
type Sampler struct {
	cfg     Config
	lastErr string
}

// New returns a Sampler with the defaults filled in.
func New(cfg Config) *Sampler {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.Disk == nil {
		cfg.Disk = statDisk
	}
	return &Sampler{cfg: cfg}
}

// Run samples at once and then every Interval until ctx ends.
func (s *Sampler) Run(ctx context.Context) {
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick reads the machine once and sends the sample.
func (s *Sampler) Tick(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	reading, err := s.cfg.Reader.Read(rctx)
	cancel()
	if err != nil {
		s.fail("read", err)
		return
	}
	sample := Sample{
		At:       s.cfg.Now(),
		CPU:      reading.CPU,
		MemUsed:  reading.MemUsed,
		MemTotal: reading.MemTotal,
	}
	if free, total, err := s.cfg.Disk(s.cfg.DiskPath); err != nil {
		s.fail("disk", err)
	} else {
		sample.DiskFree, sample.DiskTotal = free, total
	}
	if s.cfg.Roots != nil {
		sample.Sessions = Sessions(reading.Procs, s.cfg.Roots())
	}
	s.lastErr = ""
	s.cfg.Send(sample)
}

// fail logs an error once until the sampler recovers.
func (s *Sampler) fail(what string, err error) {
	if msg := what + ": " + err.Error(); msg != s.lastErr {
		s.lastErr = msg
		s.cfg.Logf("[resources] %s", msg)
	}
}

// Sessions sums the processes under the roots of each session. A session none of whose roots is in procs is
// left out. The list holds the sessions with the most memory first and at most MaxSessions.
func Sessions(procs []Proc, roots map[string][]int) []SessionUsage {
	byPID := make(map[int]*Proc, len(procs))
	kids := make(map[int][]int, len(procs))
	for i := range procs {
		p := &procs[i]
		byPID[p.PID] = p
		kids[p.PPID] = append(kids[p.PPID], p.PID)
	}
	var out []SessionUsage
	for id, rs := range roots {
		seen := map[int]bool{}
		var queue []int
		found := 0
		for _, r := range rs {
			if r > 0 && byPID[r] != nil && !seen[r] {
				seen[r] = true
				queue = append(queue, r)
				found++
			}
		}
		if found == 0 {
			continue
		}
		u := SessionUsage{SessionID: id}
		var cpu float64
		known := false
		for len(queue) > 0 {
			pid := queue[0]
			queue = queue[1:]
			p := byPID[pid]
			u.RSS += p.RSS
			if p.CPU != nil {
				cpu += *p.CPU
				known = true
			}
			for _, k := range kids[pid] {
				if !seen[k] && byPID[k] != nil {
					seen[k] = true
					queue = append(queue, k)
				}
			}
		}
		u.Children = len(seen) - found
		if known {
			c := clampPercent(cpu)
			u.CPU = &c
		}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RSS != out[j].RSS {
			return out[i].RSS > out[j].RSS
		}
		return out[i].SessionID < out[j].SessionID
	})
	if len(out) > MaxSessions {
		out = out[:MaxSessions]
	}
	return out
}

func clampPercent(v float64) float64 {
	switch {
	case v < 0 || v != v:
		return 0
	case v > 100:
		return 100
	}
	return float64(int(v*10+0.5)) / 10
}
