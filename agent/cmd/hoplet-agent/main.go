package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/daemon"
	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
	"github.com/yangflow/hoplet/agent/internal/sessions"
)

const version = "0.1.1" // x-release-please-version

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
		sessions.NewCommand(home, scan, os.Stdout),
	)
	return root
}
