package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/proto"
)

const companionUsage = `usage: hoplet companion <command> [flags]

commands:
  list     [--all]
  show     ID
  new      --harness KIND [--name NAME] [--permission MODE] [--model M] [--effort E]
  persona  ID [--set FILE|-]

A companion is a long-lived Personal assistant: a directory holding its
persona (persona.md) and the main session that talks as it. Its name is the
main session's title (hoplet session set ID --title NAME renames it).

persona prints the persona; --set replaces it with FILE, or standard input
for -, and says when the change takes effect: Claude Code reads it when the
main session next starts; Hermes keeps the persona a session began with, so
once the main session has run, a new main session is needed.

list leaves out companions whose main session is archived; --all lists them.
ID may be a unique prefix of at least 4 characters. Every command takes
--json for machine-readable output.
`

func (a app) companion(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) == 0 {
			fmt.Fprint(a.stderr, companionUsage)
			return 2
		}
		fmt.Fprint(a.stdout, companionUsage)
		return 0
	}
	cmds := map[string]func([]string) int{
		"list": a.companionList, "show": a.companionShow, "new": a.companionNew, "persona": a.companionPersona,
	}
	cmd, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(a.stderr, "hoplet companion: unknown command %q\n", args[0])
		return 2
	}
	return cmd(args[1:])
}

func (a app) companionList(args []string) int {
	fs := a.flags("companion list", companionUsage)
	all := fs.Bool("all", false, "include companions whose main session is archived")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	return a.withClient("companion list", commandTimeout, func(ctx context.Context, c *client.Client) error {
		cs, err := c.Companions(ctx, *all)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(cs)
			return nil
		}
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tHARNESS\tMAIN SESSION")
		for _, comp := range cs {
			main := string(comp.Main)
			if comp.ArchivedAt != nil {
				main += " (archived)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", comp.ID, comp.Name, comp.Harness.Kind, main)
		}
		return tw.Flush()
	})
}

func (a app) companionShow(args []string) int {
	fs := a.flags("companion show", companionUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	return a.withClient("companion show", commandTimeout, func(ctx context.Context, c *client.Client) error {
		comp, err := c.Companion(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(comp)
			return nil
		}
		a.printCompanion(comp)
		return nil
	})
}

func (a app) printCompanion(comp proto.Companion) {
	tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "companion\t%s\n", comp.ID)
	if comp.Name != "" {
		fmt.Fprintf(tw, "name\t%s\n", comp.Name)
	}
	fmt.Fprintf(tw, "harness\t%s\n", comp.Harness.Kind)
	fmt.Fprintf(tw, "directory\t%s\n", comp.Dir)
	fmt.Fprintf(tw, "main session\t%s\n", comp.Main)
	if comp.ArchivedAt != nil {
		fmt.Fprintf(tw, "archived\t%s\n", comp.ArchivedAt.Local().Format("2006-01-02 15:04"))
	}
	_ = tw.Flush()
}

func (a app) companionNew(args []string) int {
	fs := a.flags("companion new", companionUsage)
	kind := fs.String("harness", "", "harness kind")
	name := fs.String("name", "", "name, the main session's title")
	perm := fs.String("permission", "", "confirm, auto_edit, auto_review or full_auto (default: confirm)")
	model := fs.String("model", "", "model (default: the harness's)")
	effort := fs.String("effort", "", "low, medium, high, xhigh, max or ultra (default: the harness's)")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	if *kind == "" {
		fmt.Fprintln(a.stderr, "hoplet companion new: --harness is required")
		return 2
	}
	p := proto.CompanionCreateParams{
		Harness: *kind, Title: *name, Permission: proto.PermissionMode(*perm), Model: *model, Effort: proto.Effort(*effort),
	}
	return a.withClient("companion new", longCallTimeout, func(ctx context.Context, c *client.Client) error {
		out, err := c.CreateCompanion(ctx, p)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(out)
			return nil
		}
		a.printCompanion(out.Companion)
		return nil
	})
}

func (a app) companionPersona(args []string) int {
	fs := a.flags("companion persona", companionUsage)
	set := fs.String("set", "", "replace the persona with this file, or standard input for -")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	var text string
	if *set != "" {
		b, err := a.readInput(*set)
		if err != nil {
			return a.fail("companion persona", err)
		}
		text = string(b)
	}
	return a.withClient("companion persona", commandTimeout, func(ctx context.Context, c *client.Client) error {
		if *set == "" {
			p, err := c.Persona(ctx, fs.Arg(0))
			if err != nil {
				return err
			}
			if *asJSON {
				a.printJSON(p)
				return nil
			}
			_, err = io.WriteString(a.stdout, p.Text)
			return err
		}
		r, err := c.SetPersona(ctx, fs.Arg(0), text)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(r)
			return nil
		}
		switch r.Applies {
		case proto.AppliesNewMainSession:
			fmt.Fprintln(a.stdout, "Persona saved. The main session keeps the persona it started with; a new main session will use this one.")
		default:
			fmt.Fprintln(a.stdout, "Persona saved. The main session uses it from its next start.")
		}
		return nil
	})
}

// readInput reads the file at path, or standard input for -.
func (a app) readInput(path string) ([]byte, error) {
	if path == "-" {
		in := a.stdin
		if in == nil {
			in = os.Stdin
		}
		return io.ReadAll(in)
	}
	return os.ReadFile(filepath.Clean(path))
}
