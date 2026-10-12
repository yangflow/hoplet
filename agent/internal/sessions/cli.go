package sessions

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/provider"
)

type cli struct {
	home    func() string
	sources func() []provider.Source
	out     io.Writer
}

// NewCommand is the sessions CLI: list, import, and remove import marks.
func NewCommand(home func() string, sources func() []provider.Source, out io.Writer) *cobra.Command {
	if sources == nil {
		sources = func() []provider.Source { return nil }
	}
	s := &cli{home: home, sources: sources, out: out}
	cmd := &cobra.Command{
		Use:               "sessions",
		Short:             "List, import, and remove session import marks",
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
			Use:   "list [absolute-path]",
			Short: "Print pending and imported sessions for watched projects",
			Args:  cobra.MaximumNArgs(1),
			RunE:  s.runList,
		},
		&cobra.Command{
			Use:   "import <absolute-path> <session-id>… | import <session-id>…",
			Short: "Mark session ids that may append",
			Long: `Mark session ids that may append.

With a project path:
  hoplet-agent sessions import /abs/path <session-id>…

Or with session ids alone (each id must belong to exactly one listed project):
  hoplet-agent sessions import <session-id>…`,
			Args: cobra.MinimumNArgs(1),
			RunE: s.runImport,
		},
		&cobra.Command{
			Use:   "remove <absolute-path> <session-id>… | remove <session-id>…",
			Short: "Clear imported marks so sessions stop appending",
			Long: `Clear imported session ids (the project directory stays listed; ids become pending again).

  hoplet-agent sessions remove /abs/path <session-id>…
  hoplet-agent sessions remove <session-id>…`,
			Args: cobra.MinimumNArgs(1),
			RunE: s.runRemove,
		},
	)
	return cmd
}

func (s *cli) runList(_ *cobra.Command, args []string) error {
	file, err := projects.Load(s.home())
	if err != nil {
		return err
	}
	srcs := s.sources()
	items := file.Items
	if len(args) == 1 {
		if !filepath.IsAbs(args[0]) {
			return fmt.Errorf("path must be absolute")
		}
		p := projects.Real(args[0])
		var filtered []projects.Item
		for _, it := range items {
			if projects.Real(it.Path) == p {
				filtered = append(filtered, it)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("unknown project")
		}
		items = filtered
	}
	if len(items) == 0 {
		_, err := fmt.Fprintln(s.out, "(no projects listed)")
		return err
	}
	for _, it := range items {
		if _, err := fmt.Fprintln(s.out, it.Path); err != nil {
			return err
		}
		pend := file.Sessions(it.Path, srcs)
		if len(pend) == 0 {
			if _, err := fmt.Fprintln(s.out, "  (no local sessions)"); err != nil {
				return err
			}
			continue
		}
		rows := make([][]string, 0, len(pend))
		for _, p := range pend {
			state := "pending"
			if p.Imported {
				state = "imported"
			}
			title := ""
			for _, src := range srcs {
				if src.ID() == p.SessionID {
					title = provider.PeekTitle(context.Background(), src)
					break
				}
			}
			if title == "" {
				title = "—"
			}
			rows = append(rows, []string{state, title, p.SessionID})
		}
		if err := writeTable(s.out, []string{"STATE", "TITLE", "SESSION"}, rows); err != nil {
			return err
		}
	}
	return nil
}

func (s *cli) runImport(_ *cobra.Command, args []string) error {
	file, err := projects.Load(s.home())
	if err != nil {
		return err
	}
	path, ids, err := resolveSessionImport(file, s.sources(), args)
	if err != nil {
		return err
	}
	if err := file.Import(path, ids); err != nil {
		return err
	}
	return file.Save(s.home())
}

func (s *cli) runRemove(_ *cobra.Command, args []string) error {
	file, err := projects.Load(s.home())
	if err != nil {
		return err
	}
	path, ids, err := resolveSessionRemove(file, s.sources(), args)
	if err != nil {
		return err
	}
	if err := file.ClearImports(path, ids); err != nil {
		return err
	}
	if err := file.Save(s.home()); err != nil {
		return err
	}
	return nil
}

func resolveSessionImport(file *projects.File, sources []provider.Source, args []string) (string, []string, error) {
	if filepath.IsAbs(args[0]) {
		if len(args) < 2 {
			return "", nil, fmt.Errorf("import <absolute-path> <session-id>…")
		}
		return args[0], args[1:], nil
	}
	return resolveSessionIDs(file, sources, args, "import")
}

func resolveSessionRemove(file *projects.File, sources []provider.Source, args []string) (string, []string, error) {
	if filepath.IsAbs(args[0]) {
		if len(args) < 2 {
			return "", nil, fmt.Errorf("remove <absolute-path> <session-id>…")
		}
		return args[0], args[1:], nil
	}
	return resolveSessionIDs(file, sources, args, "remove")
}

func resolveSessionIDs(file *projects.File, sources []provider.Source, args []string, verb string) (string, []string, error) {
	byID := map[string]string{}
	for _, src := range sources {
		byID[src.ID()] = projects.Clean(projects.CwdOf(src))
	}
	for _, it := range file.Items {
		p := projects.Real(it.Path)
		for _, id := range it.Imported {
			if _, ok := byID[id]; !ok {
				byID[id] = p
			}
		}
	}
	listed := map[string]struct{}{}
	for _, it := range file.Items {
		listed[projects.Clean(it.Path)] = struct{}{}
		listed[projects.Real(it.Path)] = struct{}{}
	}
	var path string
	for _, id := range args {
		cwd, ok := byID[id]
		if !ok || cwd == "" {
			return "", nil, fmt.Errorf("unknown session %s", id)
		}
		if _, ok := listed[cwd]; !ok {
			return "", nil, fmt.Errorf("session %s is under %s, which is not listed (projects add first)", id, cwd)
		}
		if path == "" {
			path = cwd
		} else if path != cwd {
			return "", nil, fmt.Errorf("session ids span multiple projects; pass one path: %s <path> <session-id>…", verb)
		}
	}
	return path, args, nil
}

func writeTable(out io.Writer, headers []string, rows [][]string) error {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if n := utf8.RuneCountInString(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	pad := func(s string, w int) string {
		n := utf8.RuneCountInString(s)
		if n >= w {
			return s
		}
		return s + strings.Repeat(" ", w-n)
	}
	parts := make([]string, len(headers))
	for i, h := range headers {
		parts[i] = pad(h, widths[i])
	}
	if _, err := fmt.Fprintf(out, "  %s\n", strings.Join(parts, "  ")); err != nil {
		return err
	}
	for _, row := range rows {
		for i, cell := range row {
			parts[i] = pad(cell, widths[i])
		}
		if _, err := fmt.Fprintf(out, "  %s\n", strings.Join(parts, "  ")); err != nil {
			return err
		}
	}
	return nil
}

// RunCLI runs the sessions subcommand tree (tests).
func RunCLI(home string, args []string, sources []provider.Source, out io.Writer) error {
	cmd := NewCommand(func() string { return home }, func() []provider.Source { return sources }, out)
	cmd.SetArgs(args)
	return cmd.Execute()
}
