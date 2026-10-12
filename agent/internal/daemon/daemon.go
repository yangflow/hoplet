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
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
	claudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"
	"github.com/yangflow/hoplet/agent/internal/sync"
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
		vendorList = []provider.Provider{claude}
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
	rt := newBridge(cfg, func(msg any) error { return client.Send(msg) }, func() { client.Kick() },
		func(sessionID string) { snaps.mark(sessionID) })
	defer rt.close()
	snaps = newSnapshotter(cfg.HopletHome, locator(cfg), nil)
	moves := newHandoffs(cfg, st, rt, snaps, locator(cfg))
	// An installation that was interrupted is removed before anything connects; one that finished waits
	// for the core to say whether its handoff committed.
	moves.recover(ctx)
	runtimeHooks := rt.hooks()
	runtimeHooks.Start = rt.starter(st)
	client = sync.NewClient(cfg, deviceID, version, sync.Hooks{
		Providers:      rt.providers(),
		Runtime:        runtimeHooks,
		Handoff:        moves.hooks(),
		Workspace:      workspaceList(cfg),
		File:           workspaceFile(cfg),
		Diff:           workspaceDiff(cfg),
		ProjectsList:   st.projectsList,
		ProjectsAdd:    st.projectsAdd,
		ProjectsRemove: st.projectsRemove,
		DirsList:       dirsList(cfg),
		CloneFind:      cloneFind(st),
		SessionsImport: st.sessionsImport,
		SessionsRemove: st.sessionsRemove,
	})
	st.setKick(client.Kick)
	moves.client = client
	snaps.up = client
	go snaps.run(ctx)
	go client.Run(ctx)

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
