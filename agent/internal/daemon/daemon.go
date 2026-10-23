package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"slices"
	gosync "sync"
	"syscall"
	"time"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/hooks"
	"github.com/yangflow/hoplet/agent/internal/ports"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
	claudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"
	codexprovider "github.com/yangflow/hoplet/agent/internal/provider/codex"
	"github.com/yangflow/hoplet/agent/internal/rules"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/terminal"
)

func Scan(cfg *config.Config, sctx context.Context) []provider.Source {
	var sources []provider.Source
	owners := map[string]string{}
	for _, prov := range vendors(cfg) {
		ss, err := prov.Scan(sctx)
		if err != nil {
			log.Printf("[agent] provider scan failed: %v", err)
			continue
		}
		if conf, ok := extraConfiguration(prov); ok {
			for _, s := range ss {
				owners[s.ID()] = conf
			}
		}
		sources = append(sources, ss...)
	}
	setScanOwners(owners)
	return sources
}

var (
	vendorMu   gosync.Mutex
	vendorCfg  *config.Config
	vendorList []provider.Provider
	// extras are the session sources of configurations that set their own CLAUDE_CONFIG_DIR or CODEX_HOME.
	extras []*extraSource
	// scanOwners maps a session found in an extra source to the configuration that owns it.
	scanOwners map[string]string
)

// vendors returns the providers of cfg: the vendors' own session stores, then the stores of configurations
// with a session directory of their own. They are kept for the life of the config so that their scan state
// survives from one scan to the next.
func vendors(cfg *config.Config) []provider.Provider {
	vendorMu.Lock()
	defer vendorMu.Unlock()
	return vendorsLocked(cfg)
}

func vendorsLocked(cfg *config.Config) []provider.Provider {
	if vendorCfg != cfg {
		vendorCfg = cfg
		extras, scanOwners = nil, nil
		claude := claudeprovider.New(cfg.ClaudeProjects, cfg.ExcludePrefixes)
		claude.SetProjectHints(func() []string { return watchedPaths(cfg.HopletHome) })
		codex := codexprovider.New(codexprovider.SessionsDir(cfg.CodexHome), cfg.ExcludePrefixes)
		vendorList = []provider.Provider{claude, codex}
	}
	out := append([]provider.Provider(nil), vendorList...)
	for _, e := range extras {
		out = append(out, e.p)
	}
	return out
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
	if n := hooks.Sweep(cfg.HopletHome); n > 0 {
		log.Printf("[hooks] killed %d script(s) of an earlier run", n)
	}
	scripts := newScriptHooks(cfg, st, func() *sync.Client { return client })
	defer scripts.Close()
	terms = newTerminals(cfg, deviceID, rt, moves, locator(cfg), func() *sync.Client { return client })
	moves.onPrepare = terms.handoffStarted
	defer terms.mgr.CloseAll(terminal.ReasonOwnerOffline)
	watcher := newPortWatcher(ports.NewScanner(), locator(cfg), rt, terms, func() *sync.Client { return client })
	sampler := newResourceSampler(cfg, rt, terms, func() *sync.Client { return client })
	inv := newInventory(st, func() *sync.Client { return client })
	cloner := newCloner(cfg, st)
	cloner.Recover()
	runtimeHooks := rt.hooks()
	runtimeHooks.Start = rt.starter(st)
	client = sync.NewClient(cfg, deviceID, version, sync.Hooks{
		Providers:            rt.providers,
		ProvidersUpdate:      rt.update,
		Rules:                rt.rules.Apply,
		SessionConfiguration: rt.sessionConfiguration,
		Runtime:              runtimeHooks,
		Connected:            inv.Refresh,
		Handoff:              moves.hooks(),
		Terminal:             terms.hooks(),
		SessionMCP:           rt.mcp != nil,
		McpToken:             rt.mcpToken,
		Ports:                sync.PortsHooks{Watch: watcher.Watch},
		PreviewHost:          cfg.PreviewHost,
		PermissionDeny:       rules.Announced(cfg.PermissionDeny),
		ScriptHooks:          announceScriptHooks(scripts),
		HookEvent:            scripts.Handle,
		Workspace:            workspaceList(cfg),
		File:                 workspaceFile(cfg),
		Diff:                 workspaceDiff(cfg),
		Changes:              workspaceChanges(cfg),
		ConfigRead:           configRead(cfg),
		ProjectsList:         st.projectsList,
		ProjectsAdd:          st.projectsAdd,
		ProjectsRemove:       st.projectsRemove,
		DirsList:             dirsList(cfg),
		AttachmentPut: func(p sync.AttachmentPut) (bool, error) {
			return rt.att.put(p.AttachmentID, p.MediaType, p.Seq, p.Last, p.Data)
		},
		CloneFind:      cloneFind(st),
		CloneCheck:     cloneCheck(cloner),
		CloneStart:     cloneStart(cloner),
		SessionsImport: st.sessionsImport,
		SessionsRemove: st.sessionsRemove,
		SessionInspect: sessionInspect(cfg),
		SessionDelete: terms.wrapDelete(func(ctx context.Context, id string, vendorFile, worktree, worktreeDirty bool) []sync.DeletionStep {
			steps := sessionDelete(cfg, st)(ctx, id, vendorFile, worktree, worktreeDirty)
			if len(steps) > 0 && steps[0].Status == sync.StatusDone {
				rt.forgetConfiguration(id) // the session is no longer on this machine's books
			}
			return steps
		}),
		SessionForget: func(_ context.Context, id string) error {
			rt.forgetConfiguration(id)
			return st.forgetSession(id)
		},
	})
	st.setKick(client.Kick)
	moves.client = client
	snaps.up = client
	go snaps.run(ctx)
	go client.Run(ctx)
	go watcher.Run(ctx)
	go inv.Run(ctx)
	if sampler != nil {
		go sampler.Run(ctx)
	}

	log.Printf("hoplet-agent %s started: server=%s projects=%s device=%s(%s)",
		version, cfg.ServerURL, cfg.ClaudeProjects, cfg.DeviceName, shortIDPrefix(deviceID))

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	watched := st.paths()
	for {
		select {
		case <-ctx.Done():
			log.Println("shutdown signal received")
			return nil
		case <-ticker.C:
		case <-client.Wake():
		}
		if rt.reload() {
			go announceProviders(client, rt)
		}
		synced := moves.filter(st.poll(Scan(cfg, ctx)))
		if now := st.paths(); !slices.Equal(now, watched) {
			watched = now
			inv.Refresh() // the watched projects changed; the report follows within seconds
		}
		rt.observe(ctx, synced)
		client.Reconcile(ctx, synced)
	}
}

// announceProviders tells the core the configurations after providers.json changed; a core that is not
// connected gets them in the hello of its next connection.
func announceProviders(client *sync.Client, rt *bridge) {
	err := client.Send(sync.DeviceProviders{V: sync.ProtocolVersion, Type: sync.TypeDeviceProviders, Providers: rt.providers()})
	if err != nil && !errors.Is(err, sync.ErrNotConnected) {
		log.Printf("[agent] providers not sent: %v", err)
	}
}

// shortIDPrefix returns the first 8 characters of id, or the entire id if shorter.
func shortIDPrefix(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
