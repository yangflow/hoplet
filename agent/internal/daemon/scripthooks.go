package daemon

import (
	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/hooks"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// newScriptHooks starts the runner for the script hooks of config.json (ADR 0031). Results go to the core over
// the link; with the link down they are only counted.
func newScriptHooks(cfg *config.Config, st *store, client func() *sync.Client) *hooks.Runner {
	return hooks.New(cfg.Hooks, hooks.Options{
		Home:  cfg.HopletHome,
		Paths: st.paths,
		Report: func(r hooks.Result) {
			c := client()
			if c == nil {
				return
			}
			_ = c.SendHookResult(sync.HookResult{
				HookID: r.HookID, EventID: r.EventID, OK: r.OK, ExitCode: r.ExitCode,
				Error: r.Error, DurationMs: r.DurationMS, Stderr: r.Stderr,
			})
		},
	})
}

// announceScriptHooks is the list device.hello carries, with the counters as they are now.
func announceScriptHooks(r *hooks.Runner) func() []sync.HookAnnounce {
	return func() []sync.HookAnnounce {
		var out []sync.HookAnnounce
		for _, i := range r.Info() {
			out = append(out, sync.HookAnnounce{
				ID: i.ID, Command: i.Command, Events: i.Events, Projects: nonNil(i.Projects),
				TimeoutMs: i.TimeoutMS, Env: nonNil(i.Env), Runs: i.Runs, Failed: i.Failed, Dropped: i.Dropped,
			})
		}
		return out
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
