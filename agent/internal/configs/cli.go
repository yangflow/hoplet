package configs

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type cli struct {
	home func() string
	in   io.Reader
	out  io.Writer
}

// NewCommand is the provider CLI: list, add, set, env, unset and remove configurations. It never prints a
// value of an environment variable. in is where `env` reads a value from.
func NewCommand(home func() string, in io.Reader, out io.Writer) *cobra.Command {
	p := &cli{home: home, in: in, out: out}
	cmd := &cobra.Command{
		Use:               "provider",
		Short:             "Manage the provider configurations of this machine",
		SilenceUsage:      true,
		SilenceErrors:     true,
		Args:              cobra.NoArgs,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		RunE:              func(c *cobra.Command, _ []string) error { return c.Help() },
	}
	cmd.SetOut(out)
	cmd.SetErr(out)

	add := &cobra.Command{
		Use:   "add <id>",
		Short: "Add a configuration over Claude Code or Codex",
		Args:  cobra.ExactArgs(1),
		RunE:  p.runAdd,
	}
	add.Flags().String("extends", "", "the vendor: claude or codex")
	add.Flags().String("name", "", "display name (default: the id)")
	add.Flags().String("command", "", "absolute path of the executable (default: the agent's claude_bin or codex_bin)")
	add.Flags().String("preset", "", "start from a preset: "+strings.Join(PresetNames(), ", "))

	set := &cobra.Command{
		Use:   "set <id>",
		Short: "Change the name, command, enabled flag or defaults of a configuration",
		Args:  cobra.ExactArgs(1),
		RunE:  p.runSet,
	}
	set.Flags().String("name", "", "display name")
	set.Flags().String("command", "", "absolute path of the executable; empty restores the default")
	set.Flags().String("enabled", "", "true or false")
	set.Flags().StringArray("default", nil, "default of a session option, as option=value (repeatable)")

	cmd.AddCommand(
		&cobra.Command{Use: "list", Short: "Print the configurations, with the names of their variables and whether each is set", Args: cobra.NoArgs, RunE: p.runList},
		add,
		set,
		&cobra.Command{
			Use:   "env <id> <NAME>",
			Short: "Set an environment variable of a configuration; the value is read from standard input",
			Args:  cobra.ExactArgs(2),
			RunE:  p.runEnv,
		},
		&cobra.Command{Use: "unset <id> <NAME>", Short: "Remove an environment variable from a configuration", Args: cobra.ExactArgs(2), RunE: p.runUnset},
		&cobra.Command{Use: "remove <id>", Short: "Remove a configuration; the built-ins can only be disabled", Args: cobra.ExactArgs(1), RunE: p.runRemove},
	)
	return cmd
}

func (p *cli) load() (*File, error) { return Load(p.home()) }

func (p *cli) runList(_ *cobra.Command, _ []string) error {
	f, err := p.load()
	if err != nil {
		return err
	}
	environ := os.Environ()
	for _, c := range f.List() {
		state := "enabled"
		if !c.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(p.out, "%s\t%s\textends %s\t%s\n", c.ID, c.Name, c.Extends, state)
		if c.Command != "" {
			fmt.Fprintf(p.out, "  command %s\n", c.Command)
		}
		for _, n := range c.EnvNames() {
			set := "unset"
			if c.EnvSet(n, environ) {
				set = "set"
			}
			fmt.Fprintf(p.out, "  env %s (%s)\n", n, set)
		}
		ids := make([]string, 0, len(c.Defaults))
		for k := range c.Defaults {
			ids = append(ids, k)
		}
		sortStrings(ids)
		for _, k := range ids {
			fmt.Fprintf(p.out, "  default %s=%v\n", k, c.Defaults[k])
		}
	}
	return nil
}

func (p *cli) runAdd(c *cobra.Command, args []string) error {
	f, err := p.load()
	if err != nil {
		return err
	}
	cfg := Configuration{ID: args[0], Env: map[string]string{}, Defaults: map[string]any{}}
	cfg.Extends, _ = c.Flags().GetString("extends")
	cfg.Name, _ = c.Flags().GetString("name")
	cfg.Command, _ = c.Flags().GetString("command")
	if name, _ := c.Flags().GetString("preset"); name != "" {
		pr, ok := PresetByName(name)
		if !ok {
			return fmt.Errorf("%w: unknown preset %q (have %s)", ErrInvalid, name, strings.Join(PresetNames(), ", "))
		}
		if cfg.Extends == "" {
			cfg.Extends = Claude
		}
		if cfg.Name == "" {
			cfg.Name = pr.Name
		}
		pr.Apply(&cfg)
	}
	if cfg.Extends == "" {
		return fmt.Errorf("%w: --extends claude|codex is required", ErrInvalid)
	}
	if err := f.Add(cfg); err != nil {
		return err
	}
	if err := f.Save(p.home()); err != nil {
		return err
	}
	for _, n := range cfg.EnvNames() {
		if cfg.Env[n] == "" {
			fmt.Fprintf(p.out, "set %s with: hoplet-agent provider env %s %s\n", n, cfg.ID, n)
		}
	}
	return nil
}

func (p *cli) runSet(c *cobra.Command, args []string) error {
	f, err := p.load()
	if err != nil {
		return err
	}
	var patch Patch
	if c.Flags().Changed("name") {
		v, _ := c.Flags().GetString("name")
		patch.Name = &v
	}
	if c.Flags().Changed("command") {
		v, _ := c.Flags().GetString("command")
		patch.Command = &v
	}
	if c.Flags().Changed("enabled") {
		v, _ := c.Flags().GetString("enabled")
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%w: --enabled is true or false", ErrInvalid)
		}
		patch.Enabled = &b
	}
	defs, _ := c.Flags().GetStringArray("default")
	if len(defs) > 0 {
		patch.Defaults = map[string]any{}
	}
	for _, d := range defs {
		k, v, ok := strings.Cut(d, "=")
		if !ok || k == "" {
			return fmt.Errorf("%w: --default is option=value", ErrInvalid)
		}
		patch.Defaults[k] = ParseDefault(v)
	}
	if err := f.Update(args[0], patch); err != nil {
		return err
	}
	return f.Save(p.home())
}

// ParseDefault reads the value of a default: true and false are booleans, anything else a string.
func ParseDefault(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	return v
}

func (p *cli) runEnv(_ *cobra.Command, args []string) error {
	value, err := readValue(p.in)
	if err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("%w: no value on standard input", ErrInvalid)
	}
	f, err := p.load()
	if err != nil {
		return err
	}
	if err := f.SetEnv(args[0], args[1], value); err != nil {
		return err
	}
	return f.Save(p.home())
}

// readValue reads one line (without its line break) and nothing more.
func readValue(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (p *cli) runUnset(_ *cobra.Command, args []string) error {
	f, err := p.load()
	if err != nil {
		return err
	}
	if err := f.UnsetEnv(args[0], args[1]); err != nil {
		return err
	}
	return f.Save(p.home())
}

func (p *cli) runRemove(_ *cobra.Command, args []string) error {
	f, err := p.load()
	if err != nil {
		return err
	}
	if err := f.Remove(args[0]); err != nil {
		return err
	}
	return f.Save(p.home())
}
