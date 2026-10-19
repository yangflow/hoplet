package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	gosync "sync"
	"syscall"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/ports"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
	claudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"
	codexprovider "github.com/yangflow/hoplet/agent/internal/provider/codex"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/terminal"
)

func Scan(cfg *config.Config, sctx context.Context) []provider.Source {
	var sources []provider.Source
	for _, prov := range vendors(cfg) {
		ss, err := prov.Scan(sctx)
		if err != nil {
			log.Printf("[agent] provider scan failed: %v", err)
			continue
		}
		sources = append(sources, ss...)
	}
	return sources
}

var (
	vendorMu   gosync.Mutex
	vendorCfg  *config.Config
	vendorList []provider.Provider
)

// vendors returns the providers of cfg. They are kept for the life of the config so that their scan state
// survives from one scan to the next.
func vendors(cfg *config.Config) []provider.Provider {
	vendorMu.Lock()
	defer vendorMu.Unlock()
	if vendorCfg != cfg {
		vendorCfg = cfg
		claude := claudeprovider.New(cfg.ClaudeProjects, cfg.ExcludePrefixes)
		claude.SetProjectHints(func() []string { return watchedPaths(cfg.HopletHome) })
		codex := codexprovider.New(codexprovider.SessionsDir(cfg.CodexHome), cfg.ExcludePrefixes)
		vendorList = []provider.Provider{claude, codex}
	}
	return vendorList
}

func finders(cfg *config.Config) []provider.Finder {
	var out []provider.Finder
	for _, v := range vendors(cfg) {
		out = append(out, v)
	}
	return out
}

// vendorStore is the Store of the provider named name, or nil when there is none or the vendor is not native
// (ADR 0014).
func vendorStore(cfg *config.Config, name string) provider.Store {
	for _, v := range vendors(cfg) {
		if s, ok := v.(provider.Store); ok && v.Name() == name {
			return s
		}
	}
	return nil
}

func Run(cfg *config.Config, version string) error {
	deviceID, err := ensureDeviceID(cfg)
	if err != nil {
		return fmt.Errorf("device id: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	loaded, err := projects.Load(cfg.HopletHome)
	if err != nil {
		return fmt.Errorf("load projects: %w", err)
	}
	st := newStore(cfg, loaded)

	var client *sync.Client
	var snaps *snapshotter
	var terms *terminals
	rt := newBridge(cfg, func(msg any) error { return client.Send(msg) }, func() { client.Kick() },
		func(sessionID string) { snaps.mark(sessionID) },
		func(sessionID string) {
			if terms != nil {
				terms.turnStarted(sessionID)
			}
		})
	defer rt.close()
	snaps = newSnapshotter(cfg.HopletHome, locator(cfg), nil)
	moves := newHandoffs(cfg, st, rt, snaps, locator(cfg))
	// An installation that was interrupted is removed before anything connects; one that finished waits
	// for the core to say whether its handoff committed.
	moves.recover(ctx)
	// A shell an earlier run left behind is killed before anything connects, even when the terminal has since
	// been turned off.
	if n := terminal.Sweep(cfg.HopletHome); n > 0 {
		log.Printf("[terminal] killed %d shell(s) of an earlier run", n)
	}
	terms = newTerminals(cfg, deviceID, rt, moves, locator(cfg), func() *sync.Client { return client })
	moves.onPrepare = terms.handoffStarted
	defer terms.mgr.CloseAll(terminal.ReasonOwnerOffline)
	watcher := newPortWatcher(ports.NewScanner(), locator(cfg), rt, terms, func() *sync.Client { return client })
	sampler := newResourceSampler(cfg, rt, terms, func() *sync.Client { return client })
	cloner := newCloner(cfg, st)
	cloner.Recover()
	runtimeHooks := rt.hooks()
	runtimeHooks.Start = rt.starter(st)
	client = sync.NewClient(cfg, deviceID, version, sync.Hooks{
		Providers:      rt.providers(),
		Runtime:        runtimeHooks,
		Handoff:        moves.hooks(),
		Terminal:       terms.hooks(),
		Ports:          sync.PortsHooks{Watch: watcher.Watch},
		PreviewHost:    cfg.PreviewHost,
		Workspace:      workspaceList(cfg),
		File:           workspaceFile(cfg),
		Diff:           workspaceDiff(cfg),
		ProjectsList:   st.projectsList,
		ProjectsAdd:    st.projectsAdd,
		ProjectsRemove: st.projectsRemove,
		DirsList:       dirsList(cfg),
		AttachmentPut: func(p sync.AttachmentPut) (bool, error) {
			return rt.att.put(p.AttachmentID, p.MediaType, p.Seq, p.Last, p.Data)
		},
		CloneFind:      cloneFind(st),
		CloneCheck:     cloneCheck(cloner),
		CloneStart:     cloneStart(cloner),
		SessionsImport: st.sessionsImport,
		SessionsRemove: st.sessionsRemove,
		SessionInspect: sessionInspect(cfg),
		SessionDelete:  terms.wrapDelete(sessionDelete(cfg, st)),
		SessionForget:  func(_ context.Context, id string) error { return st.forgetSession(id) },
	})
	st.setKick(client.Kick)
	moves.client = client
	snaps.up = client
	go snaps.run(ctx)
	go client.Run(ctx)
	go watcher.Run(ctx)
	if sampler != nil {
		go sampler.Run(ctx)
	}

	log.Printf("hoplet-agent %s started: server=%s projects=%s device=%s(%s)",
		version, cfg.ServerURL, cfg.ClaudeProjects, cfg.DeviceName, shortIDPrefix(deviceID))

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("shutdown signal received")
			return nil
		case <-ticker.C:
		case <-client.Wake():
		}
		synced := moves.filter(st.poll(Scan(cfg, ctx)))
		rt.observe(ctx, synced)
		client.Reconcile(ctx, synced)
	}
}

// shortIDPrefix returns the first 8 characters of id, or the entire id if shorter.
func shortIDPrefix(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
