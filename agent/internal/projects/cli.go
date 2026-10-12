package projects

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"
)

type cli struct {
	home func() string
	out  io.Writer
}

// NewCommand is the projects CLI: list, add, and remove watched directories.
func NewCommand(home func() string, out io.Writer) *cobra.Command {
	p := &cli{home: home, out: out}
	cmd := &cobra.Command{
		Use:               "projects",
		Short:             "List, add, and remove watched project directories",
		SilenceUsage:      true,
		SilenceErrors:     true,
		Args:              cobra.NoArgs,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.AddCommand(
		&cobra.Command{
			Use:   "add <absolute-path>",
			Short: "Watch a project directory without importing sessions",
			Args:  cobra.ExactArgs(1),
			RunE:  p.runAdd,
		},
		&cobra.Command{
			Use:   "list",
			Short: "Print watched project directories",
			Args:  cobra.NoArgs,
			RunE:  p.runList,
		},
		&cobra.Command{
			Use:   "remove <absolute-path>",
			Short: "Stop watching a project directory",
			Args:  cobra.ExactArgs(1),
			RunE:  p.runRemove,
		},
	)
	return cmd
}

func (p *cli) runAdd(_ *cobra.Command, args []string) error {
	file, err := Load(p.home())
	if err != nil {
		return err
	}
	if err := file.Add(args[0]); err != nil {
		return err
	}
	return file.Save(p.home())
}

func (p *cli) runList(_ *cobra.Command, _ []string) error {
	file, err := Load(p.home())
	if err != nil {
		return err
	}
	if len(file.Items) == 0 {
		_, err := fmt.Fprintln(p.out, "(no projects listed)")
		return err
	}
	for _, it := range file.Items {
		if _, err := fmt.Fprintln(p.out, it.Path); err != nil {
			return err
		}
	}
	return nil
}

func (p *cli) runRemove(_ *cobra.Command, args []string) error {
	if !filepath.IsAbs(args[0]) {
		return fmt.Errorf("path must be absolute")
	}
	file, err := Load(p.home())
	if err != nil {
		return err
	}
	if err := file.RemovePath(args[0]); err != nil {
		return err
	}
	return file.Save(p.home())
}

// RunCLI runs the projects subcommand tree (tests).
func RunCLI(home string, args []string, out io.Writer) error {
	cmd := NewCommand(func() string { return home }, out)
	cmd.SetArgs(args)
	return cmd.Execute()
}
