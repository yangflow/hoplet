package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/claude"
	"github.com/yangflow/hoplet/proto"
)

const devUsage = `usage: hoplet dev record --steps FILE --out FILE [--dir DIR] [--bin PATH]
                         [--permission MODE] [--plan] [--effort LEVEL]
       hoplet dev sanitize --in FILE --out FILE --dir DIR

record runs claude the way Hoplet does, plays the steps (a JSON list, see
the Claude Code adapter's Step) and writes every line both ways to --out.
It uses your claude login and quota. The recording holds personal paths:
sanitize it (with the --dir it was recorded in) before committing it as a
contract-test fixture.
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
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	if *stepsPath == "" || *outPath == "" {
		fs.Usage()
		return 2
	}
	b, err := os.ReadFile(*stepsPath)
	if err != nil {
		return a.fail("dev record", err)
	}
	var steps []claude.Step
	if err := json.Unmarshal(b, &steps); err != nil {
		return a.fail("dev record", fmt.Errorf("%s: %w", *stepsPath, err))
	}
	if *bin == "" {
		if *bin, err = exec.LookPath("claude"); err != nil {
			return a.fail("dev record", errors.New("no claude on PATH; give --bin"))
		}
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return a.fail("dev record", err)
	}
	out, err := os.Create(*outPath)
	if err != nil {
		return a.fail("dev record", err)
	}
	defer out.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err = claude.Record(ctx, claude.RecordConfig{
		Binary: *bin, Dir: abs, Env: recordEnv(),
		Spec:  harness.Spec{Mode: proto.PermissionMode(*perm), Plan: *plan, Effort: proto.Effort(*effort)},
		Steps: steps,
	}, out)
	if err != nil {
		return a.fail("dev record", err)
	}
	fmt.Fprintf(a.stdout, "recorded %s\n", *outPath)
	return 0
}

// recordEnv is this process's environment minus what the adapter drops
// itself; a recording runs where its user runs it, logged in as them.
func recordEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOPLET_") {
			out = append(out, kv)
		}
	}
	return out
}

func (a app) devSanitize(args []string) int {
	fs := a.flags("dev sanitize", devUsage)
	inPath := fs.String("in", "", "the recording")
	outPath := fs.String("out", "", "where to write the fixture")
	dir := fs.String("dir", "", "the working directory the recording was made in")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
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
	out, err := os.Create(*outPath)
	if err != nil {
		return a.fail("dev sanitize", err)
	}
	defer out.Close()
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return a.fail("dev sanitize", err)
	}
	if err := claude.Sanitize(in, out, abs, userHome); err != nil {
		return a.fail("dev sanitize", err)
	}
	fmt.Fprintf(a.stdout, "sanitized %s\n", *outPath)
	return 0
}
