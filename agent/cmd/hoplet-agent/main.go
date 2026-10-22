package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/configs"
	"github.com/yangflow/hoplet/agent/internal/daemon"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
	"github.com/yangflow/hoplet/agent/internal/runtime/codex"
	"github.com/yangflow/hoplet/agent/internal/sessions"
)

const version = "0.8.0" // x-release-please-version

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[hoplet-agent] ")

	root := newRoot()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func newRoot() *cobra.Command {
	configPath := filepath.Join(config.DefaultHome(), "config.json")
	var cfg *config.Config

	root := &cobra.Command{
		Use:               "hoplet-agent",
		Short:             "Sync local vendor sessions to a Hoplet core",
		Args:              cobra.NoArgs,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Root with no subcommand only prints usage — no config needed.
			if cmd.Parent() == nil {
				return nil
			}
			loaded, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			cfg = loaded
			return nil
		},
	}
	root.PersistentFlags().StringVar(&configPath, "config", configPath, "path to the config file")
	scan := func() []provider.Source {
		return daemon.Scan(cfg, context.Background())
	}
	home := func() string { return cfg.HopletHome }
	root.AddCommand(
		daemon.NewCommand(func() *config.Config { return cfg }, version),
		projects.NewCommand(home, os.Stdout),
		configs.NewCommand(home, os.Stdin, os.Stdout),
		sessions.NewCommand(home, scan, os.Stdout),
		newHookCommand(),
	)
	return root
}

// newHookCommand is `hoplet-agent hook codex`, the command Codex runs before each tool call (ADR 0032). It reads
// the hook input on stdin, asks the daemon and prints the decision. It needs no config file, always exits 0 and
// denies on every error.
func newHookCommand() *cobra.Command {
	hook := &cobra.Command{
		Use:    "hook",
		Short:  "Commands the coding agents run as hooks",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	hook.AddCommand(&cobra.Command{
		Use:           "codex",
		Short:         "The PreToolUse hook of Codex",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		// The hook must not depend on the config file: no config means deny, which the hook does itself.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		Run: func(*cobra.Command, []string) {
			codex.RunHook(os.Stdin, os.Stdout, os.Getenv, codex.HookClientTimeout)
		},
	})
	return hook
}
