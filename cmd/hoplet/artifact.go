package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/proto"
)

const artifactUsage = `usage: hoplet artifact <command> [flags]

commands:
  publish  SESSION PATH [--name NAME]
  list     SESSION

publish adds a version of an artifact to a session. PATH is a file or a
directory. A file is the version's entry and names the artifact by its file
name. A directory's index.html is the entry; every regular file under the
directory goes with it, leaving out files and directories whose names start
with a dot, and the directory's name names the artifact. A name the session
already has adds a version to that artifact. Publish works only on this
machine.

list prints each artifact of a session at its latest version.

SESSION may be a unique prefix of at least 4 characters. Every command
takes --json for machine-readable output.
`

func (a app) artifact(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		if len(args) == 0 {
			fmt.Fprint(a.stderr, artifactUsage)
			return 2
		}
		fmt.Fprint(a.stdout, artifactUsage)
		return 0
	}
	cmds := map[string]func([]string) int{"publish": a.artifactPublish, "list": a.artifactList}
	cmd, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(a.stderr, "hoplet artifact: unknown command %q\n", args[0])
		return 2
	}
	return cmd(args[1:])
}

func (a app) artifactPublish(args []string) int {
	fs := a.flags("artifact publish", artifactUsage)
	name := fs.String("name", "", "the artifact's name (default: the file or directory name)")
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 2, 2); !ok {
		return code
	}
	if a.host != "" {
		fmt.Fprintln(a.stderr, "hoplet artifact publish: publish only works on this machine")
		return 2
	}
	p, err := publishParams(fs.Arg(1), *name)
	if err != nil {
		fmt.Fprintf(a.stderr, "hoplet artifact publish: %v\n", err)
		return 1
	}
	return a.withClient("artifact publish", commandTimeout, func(ctx context.Context, c *client.Client) error {
		id, err := resolveID(ctx, c, fs.Arg(0))
		if err != nil {
			return err
		}
		p.Session = id
		v, err := c.PublishArtifact(ctx, p)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(v)
			return nil
		}
		fmt.Fprintf(a.stdout, "published %s v%d (%s, %d files) %s\n", v.Name, v.Version, v.Kind, v.Files, v.ID)
		return nil
	})
}

// publishParams turns a PATH argument into a publish's entry and files.
func publishParams(path, name string) (proto.ArtifactsPublishParams, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return proto.ArtifactsPublishParams{}, fmt.Errorf("resolve %s: %w", path, err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return proto.ArtifactsPublishParams{}, err
	}
	if name == "" {
		name = filepath.Base(abs)
	}
	if !fi.IsDir() {
		return proto.ArtifactsPublishParams{Name: name, Entry: abs}, nil
	}
	entry := filepath.Join(abs, "index.html")
	if fi, err := os.Stat(entry); err != nil || !fi.Mode().IsRegular() {
		return proto.ArtifactsPublishParams{}, fmt.Errorf("%s has no index.html", path)
	}
	var files []string
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == abs {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && p != entry {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return proto.ArtifactsPublishParams{}, fmt.Errorf("read %s: %w", path, err)
	}
	return proto.ArtifactsPublishParams{Name: name, Entry: entry, Files: files}, nil
}

func (a app) artifactList(args []string) int {
	fs := a.flags("artifact list", artifactUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	return a.withClient("artifact list", commandTimeout, func(ctx context.Context, c *client.Client) error {
		id, err := resolveID(ctx, c, fs.Arg(0))
		if err != nil {
			return err
		}
		list, err := c.Artifacts(ctx, id)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(list)
			return nil
		}
		if len(list) == 0 {
			fmt.Fprintln(a.stdout, "no artifacts")
			return nil
		}
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tKIND\tVERSION\tUPDATED\tID")
		for _, v := range list {
			fmt.Fprintf(tw, "%s\t%s\tv%d\t%s\t%s\n", v.Name, v.Kind, v.Version, v.UpdatedAt.Local().Format("01-02 15:04"), v.ID)
		}
		return tw.Flush()
	})
}
