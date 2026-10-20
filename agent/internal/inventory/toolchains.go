package inventory

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Env is what detection runs against: where tools are looked up and how a command is run. Tests replace it.
type Env struct {
	LookPath func(name string) (string, error)
	// Run starts a command and returns its standard output and standard error; the context carries the timeout.
	Run func(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}

func systemEnv() *Env {
	return &Env{LookPath: exec.LookPath, Run: runCommand}
}

// maxOutput caps what is read from a command.
const maxOutput = 64 << 10

type capped struct{ bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxOutput - c.Len(); room > 0 {
		c.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// runCommand starts a command with no terminal and the C locale, so that output has one format.
func runCommand(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	var out, errOut capped
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

// tool is one entry of the fixed list: the command that prints its version and where the version is printed.
type tool struct {
	name   string
	args   []string
	stderr bool
	parse  func(out string) string
}

var tools = []tool{
	{name: "git", args: []string{"--version"}, parse: parseVersion},
	{name: "node", args: []string{"--version"}, parse: parseVersion},
	{name: "pnpm", args: []string{"--version"}, parse: parseVersion},
	{name: "npm", args: []string{"--version"}, parse: parseVersion},
	{name: "bun", args: []string{"--version"}, parse: parseVersion},
	{name: "python3", args: []string{"--version"}, parse: parseVersion},
	{name: "go", args: []string{"version"}, parse: parseVersion},
	{name: "cargo", args: []string{"--version"}, parse: parseVersion},
	{name: "rustc", args: []string{"--version"}, parse: parseVersion},
	{name: "java", args: []string{"-version"}, stderr: true, parse: parseJavaVersion},
	{name: "docker", args: []string{"--version"}, parse: parseVersion},
	{name: "gh", args: []string{"--version"}, parse: parseVersion},
}

var versionRe = regexp.MustCompile(`\d+(?:\.\d+)+`)

// parseVersion is the first dotted number of the output ("git version 2.45.1 (Apple Git-146)" is 2.45.1).
func parseVersion(out string) string {
	return clip(versionRe.FindString(out), versionMax)
}

var javaRe = regexp.MustCompile(`(?m)^\S*\s*version "([^"]+)"`)

// parseJavaVersion reads the line `openjdk version "21.0.2" 2024-01-16`, so that a line such as `Picked up
// JAVA_TOOL_OPTIONS: ...` before it is not taken for the version.
func parseJavaVersion(out string) string {
	if m := javaRe.FindStringSubmatch(out); m != nil {
		return parseVersion(m[1])
	}
	return ""
}

// detectToolchains runs the version command of every tool of the list that is on the PATH, concurrently and
// bounded; a tool that is missing, fails, times out or prints no version is left out. The order is the list's.
func detectToolchains(ctx context.Context, env *Env) []Toolchain {
	found := make([]string, len(tools))
	forEach(len(tools), func(i int) {
		t := tools[i]
		path, err := env.LookPath(t.name)
		if err != nil || path == "" {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, commandTimeout)
		defer cancel()
		stdout, stderr, err := env.Run(cctx, path, t.args...)
		if err != nil {
			return
		}
		out := stdout
		if t.stderr {
			out = stderr
		}
		found[i] = strings.TrimSpace(t.parse(out))
	})
	list := []Toolchain{}
	for i, v := range found {
		if v != "" && len(list) < MaxToolchains {
			list = append(list, Toolchain{Name: clip(tools[i].name, toolMax), Version: v})
		}
	}
	return list
}
