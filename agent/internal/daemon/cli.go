package daemon

import (
	"github.com/spf13/cobra"

	"github.com/yangflow/hoplet/agent/internal/config"
)

func NewCommand(cfg func() *config.Config, version string) *cobra.Command {
	return &cobra.Command{
		Use:               "daemon",
		Short:             "Run the sync daemon in the foreground",
		Args:              cobra.NoArgs,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		RunE: func(_ *cobra.Command, _ []string) error {
			return Run(cfg(), version)
		},
	}
}
