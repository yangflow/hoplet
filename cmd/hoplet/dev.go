package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/claude"
	"github.com/yangflow/hoplet/internal/harness/codex"
	"github.com/yangflow/hoplet/internal/harness/hermes"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/tools"
	"github.com/yangflow/hoplet/proto"
)

const devUsage = `usage: hoplet dev record [--harness claude|codex] --steps FILE --out FILE [--dir DIR] [--bin PATH]
                         [--permission MODE] [--plan] [--effort LEVEL] [--resume ID] [--persona TEXT] [--mcp COMMAND]
       hoplet dev sanitize [--harness claude|codex|hermes] --in FILE --out FILE --dir DIR

record runs the harness the way Hoplet does, plays the steps (a JSON list,
see the adapter's Step) and writes every line both ways to --out. It uses
your login and quota. --resume continues a Codex thread; --persona and --mcp give a new one
an instruction and a tool server. A Hermes recording is the spool of a
real Hoplet session (sessions/ID/spool.jsonl in Hoplet's home). Recordings
hold personal paths: sanitize them (with the --dir they were made in)
before committing them as contract-test fixtures.
`

func (a app) dev(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, devUsage)
		return 2
	}
	switch args[0] {
	case "record":
		return a.devRecord(args[1:])
	case "sanitize":
		return a.devSanitize(args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(a.stdout, devUsage)
		return 0
	}
	fmt.Fprint(a.stderr, devUsage)
	return 2
}

func (a app) devRecord(args []string) int {
	fs := a.flags("dev record", devUsage)
	stepsPath := fs.String("steps", "", "the steps file")
	outPath := fs.String("out", "", "where to write the recording")
	dir := fs.String("dir", ".", "working directory of the session")
	bin := fs.String("bin", "", "the claude binary (default: claude on PATH)")
	perm := fs.String("permission", "confirm", "permission mode")
	plan := fs.Bool("plan", false, "start in plan mode")
	effort := fs.String("effort", "", "effort level")
	kind := fs.String("harness", claude.Kind, "claude or codex")
	resume := fs.String("resume", "", "the Codex thread to resume")
	persona := fs.String("persona", "", "a persona for a new Codex thread")
	mcpServer := fs.String("mcp", "", "command of an MCP server to give Codex as hoplet")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	if *stepsPath == "" || *outPath == "" || (*kind != claude.Kind && *kind != codex.Kind) {
		fs.Usage()
		return 2
	}
	b, err := os.ReadFile(*stepsPath)
	if err != nil {
		return a.fail("dev record", err)
	}
	if *bin == "" {
		if *bin, err = exec.LookPath(*kind); err != nil {
			return a.fail("dev record", fmt.Errorf("no %s on PATH; give --bin", *kind))
		}
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return a.fail("dev record", err)
	}
	h, err := home.Dir()
	if err != nil {
		return a.fail("dev record", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	vars, err := a.harnessEnv(ctx, h)
	if err != nil {
		return a.fail("dev record", fmt.Errorf("read the harness environment: %w", err))
	}
	out, err := os.Create(*outPath)
	if err != nil {
		return a.fail("dev record", err)
	}
	spec := harness.Spec{Mode: proto.PermissionMode(*perm), Plan: *plan, Effort: proto.Effort(*effort), Resume: *resume}
	if *kind == codex.Kind {
		spec.Persona = *persona
		if *mcpServer != "" {
			spec.MCP = []harness.MCPServer{{Name: "hoplet", Command: strings.Fields(*mcpServer), Env: []string{tools.TokenEnv + "=record"}}}
		}
		var steps []codex.Step
		if err = json.Unmarshal(b, &steps); err == nil {
			err = codex.Record(ctx, codex.RecordConfig{Binary: *bin, Dir: abs, Env: vars, Spec: spec, Steps: steps}, out)
		}
	} else {
		var steps []claude.Step
		if err = json.Unmarshal(b, &steps); err == nil {
			err = claude.Record(ctx, claude.RecordConfig{Binary: *bin, Dir: abs, Env: vars, Spec: spec, Steps: steps}, out)
		}
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(*outPath)
		return a.fail("dev record", err)
	}
	fmt.Fprintf(a.stdout, "recorded %s\n", *outPath)
	return 0
}

func (a app) devSanitize(args []string) int {
	fs := a.flags("dev sanitize", devUsage)
	inPath := fs.String("in", "", "the recording")
	outPath := fs.String("out", "", "where to write the fixture")
	dir := fs.String("dir", "", "the working directory the recording was made in")
	kind := fs.String("harness", claude.Kind, "claude, codex or hermes")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	sanitize := map[string]func(io.Reader, io.Writer, string, string) error{
		claude.Kind: claude.Sanitize,
		codex.Kind:  codex.Sanitize,
		hermes.Kind: func(in io.Reader, out io.Writer, dir, home string) error {
			// The shell this runs in has the environment Hermes ran with.
			return hermes.Sanitize(in, out, dir, home, os.Environ())
		},
	}[*kind]
	if sanitize == nil {
		fmt.Fprintf(a.stderr, "hoplet dev sanitize: unknown harness %q\n", *kind)
		fs.Usage()
		return 2
	}
	if *inPath == "" || *outPath == "" || *dir == "" {
		fs.Usage()
		return 2
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return a.fail("dev sanitize", err)
	}
	in, err := os.Open(*inPath)
	if err != nil {
		return a.fail("dev sanitize", err)
	}
	defer in.Close()
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return a.fail("dev sanitize", err)
	}
	out, err := os.Create(*outPath)
	if err != nil {
		return a.fail("dev sanitize", err)
	}
	err = sanitize(in, out, abs, userHome)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(*outPath)
		return a.fail("dev sanitize", err)
	}
	fmt.Fprintf(a.stdout, "sanitized %s\n", *outPath)
	return 0
}
