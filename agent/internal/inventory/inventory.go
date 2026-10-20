// Package inventory detects what the machine offers: the developer tools it has with their versions, its GPUs,
// and the `origin` of each watched project, for the capabilities of Settings › Machines (#70). It starts only
// the version commands of a fixed list of tools, `nvidia-smi` or `system_profiler`, and `git config` for the
// remotes; no environment variable, file content or command line is read or reported, and a remote URL loses its
// user, password, query and fragment before it leaves the package.
package inventory

import (
	"context"
	"log"
	"time"
	"unicode/utf16"
)

const (
	// DefaultInterval is the time between two reports (shared/src/wire INVENTORY_INTERVAL_MS).
	DefaultInterval = time.Hour
	// MaxToolchains, MaxGPUs and MaxRepositories are the limits of one report (INVENTORY_*_MAX).
	MaxToolchains   = 32
	MaxGPUs         = 8
	MaxRepositories = 200

	// commandTimeout bounds each command the detection starts.
	commandTimeout = 5 * time.Second
	// concurrency bounds the commands running at once.
	concurrency = 4
	// settle is how long a refresh waits so that several changes become one report.
	settle = 2 * time.Second
	// fresh is how long detected tools and GPUs are reused by a refresh; a reconnect loop does not start
	// the commands again and again.
	fresh = 5 * time.Minute

	nameMax    = 128
	toolMax    = 32
	versionMax = 64
	pathMax    = 4096
	originMax  = 2048
)

// Toolchain is one tool found on the machine.
type Toolchain struct {
	Name, Version string
}

// GPU is one graphics processor; Memory is in bytes, nil when unknown.
type GPU struct {
	Name   string
	Memory *uint64
}

// Repository is one watched project; Origin is nil without an `origin` remote or outside a git repository.
type Repository struct {
	Path   string
	Origin *string
}

// Report is one detection.
type Report struct {
	At           time.Time
	Toolchains   []Toolchain
	GPUs         []GPU
	Repositories []Repository
}

// Config wires a Collector to the machine and to the core connection.
type Config struct {
	// Projects returns the paths of the watched projects.
	Projects func() []string
	// Send delivers a report. A report sent while the core is away is dropped, never queued.
	Send func(Report)
	// Origin returns the origin URL of the repository that contains dir, "" when there is none.
	Origin func(ctx context.Context, dir string) string

	// Env is the machine the detection looks at; the real one when nil.
	Env *Env

	Interval time.Duration
	Now      func() time.Time
	Logf     func(format string, args ...any)
}

// Collector detects and reports the inventory: at start, on Refresh (after each connection and when the watched
// projects change) and every Interval.
type Collector struct {
	cfg     Config
	trigger chan struct{}

	machineAt time.Time
	tools     []Toolchain
	gpus      []GPU
}

// New returns a Collector with the defaults filled in.
func New(cfg Config) *Collector {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.Env == nil {
		cfg.Env = systemEnv()
	}
	return &Collector{cfg: cfg, trigger: make(chan struct{}, 1)}
}

// Refresh asks for a report soon; calls that arrive together make one.
func (c *Collector) Refresh() {
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}

// Run reports at once, then after every Refresh and every Interval, until ctx ends.
func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(c.cfg.Interval)
	defer t.Stop()
	for {
		c.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.trigger:
			select {
			case <-ctx.Done():
				return
			case <-time.After(settle):
			}
			select {
			case <-c.trigger:
			default:
			}
		}
	}
}

// Tick detects once and sends the report.
func (c *Collector) Tick(ctx context.Context) {
	c.cfg.Send(c.Detect(ctx))
}

// Detect builds a report. Tools and GPUs of a detection less than five minutes old are reused.
func (c *Collector) Detect(ctx context.Context) Report {
	now := c.cfg.Now()
	if c.machineAt.IsZero() || now.Sub(c.machineAt) >= fresh {
		c.tools = detectToolchains(ctx, c.cfg.Env)
		c.gpus = detectGPUs(ctx, c.cfg.Env)
		c.machineAt = now
	}
	var paths []string
	if c.cfg.Projects != nil {
		paths = c.cfg.Projects()
	}
	return Report{
		At:           now,
		Toolchains:   c.tools,
		GPUs:         c.gpus,
		Repositories: detectRepositories(ctx, paths, c.cfg.Origin),
	}
}

// clip cuts s to at most n UTF-16 code units, the unit the wire limits count, without splitting a character.
func clip(s string, n int) string {
	units := 0
	for i, r := range s {
		w := len(utf16.Encode([]rune{r}))
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

// forEach runs fn for 0..n-1, at most `concurrency` at a time, and waits for all.
func forEach(n int, fn func(i int)) {
	sem := make(chan struct{}, concurrency)
	done := make(chan struct{}, n)
	for i := range n {
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; done <- struct{}{} }()
			fn(i)
		}()
	}
	for range n {
		<-done
	}
}
