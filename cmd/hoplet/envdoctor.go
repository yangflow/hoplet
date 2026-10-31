package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/proto"
)

const envUsage = `usage: hoplet env refresh [--json]

Makes the daemon read your login shell's environment again (after you
change your shell startup files or config.toml). Harnesses started from now
on get it; running ones keep theirs. Credential-looking variables are
withheld unless config.toml's [env] allow lists them.
`

func writeJSONLine(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

func (a app) envCmd(args []string) int {
	if len(args) == 0 || args[0] != "refresh" {
		fmt.Fprint(a.stderr, envUsage)
		return 2
	}
	fs := a.flags("env refresh", envUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args[1:], 0, 0); !ok {
		return code
	}
	return a.withClient("env refresh", commandTimeout, func(ctx context.Context, c *client.Client) error {
		info, err := c.RefreshEnv(ctx)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(info)
			return nil
		}
		fmt.Fprintln(a.stdout, describeEnv(info))
		return nil
	})
}

func describeEnv(info proto.EnvInfo) string {
	s := fmt.Sprintf("%s: %d variables", info.Shell, info.Vars)
	if len(info.Withheld) > 0 {
		s += "; withheld: " + strings.Join(info.Withheld, ", ")
	}
	if info.Proxy != "" {
		s += fmt.Sprintf("; proxy %s (%s)", info.Proxy, info.ProxySource)
	} else {
		s += "; no proxy"
	}
	return s
}

const doctorUsage = `usage: hoplet doctor [--json]

Checks the home directory, the daemon, its LaunchAgent, the environment and
network path harnesses use, and the harness adapters. Exits 1 if any check
fails.
`

func (a app) doctor(args []string) int {
	fs := a.flags("doctor", doctorUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	checks := a.localChecks(ctx)
	failed := false
	for _, ch := range checks {
		failed = failed || ch.Status == proto.CheckFail
	}
	if *asJSON {
		a.printJSON(proto.DoctorReport{Checks: checks})
	} else {
		for _, ch := range checks {
			fmt.Fprintf(a.stdout, "%-4s  %-16s %s\n", ch.Status, ch.Name, ch.Detail)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// localChecks checks what the command line can see itself, then appends
// the daemon's own report.
func (a app) localChecks(ctx context.Context) []proto.Check {
	var out []proto.Check
	add := func(name string, st proto.CheckStatus, detail string) {
		out = append(out, proto.Check{Name: name, Status: st, Detail: detail})
	}
	h, err := home.Dir()
	if err != nil {
		add("home", proto.CheckFail, err.Error())
		return out
	}
	switch fi, err := os.Stat(h); {
	case err != nil:
		add("home", proto.CheckFail, err.Error())
	case fi.Mode().Perm() != 0o700:
		add("home", proto.CheckFail, fmt.Sprintf("%s has mode %v; want 0700 (the daemon fixes it when it starts)", h, fi.Mode().Perm()))
	default:
		add("home", proto.CheckOK, h)
	}
	if _, err := home.ReadToken(h); err != nil {
		add("token", proto.CheckFail, err.Error())
	} else {
		add("token", proto.CheckOK, home.TokenFile(h))
	}
	if runtime.GOOS == "darwin" {
		out = append(out, launchAgentCheck(ctx))
	}
	c, err := dialHome(ctx, h)
	if err != nil {
		add("daemon", proto.CheckFail, err.Error())
		return out
	}
	defer c.Close()
	if v := c.Hello().Version; v != version {
		add("daemon", proto.CheckWarn, fmt.Sprintf("serving version %s, this command line is %s; reinstall with `hoplet daemon install`", v, version))
	} else {
		add("daemon", proto.CheckOK, "version "+v+" serving at "+home.Socket(h))
	}
	r, err := c.Doctor(ctx)
	if err != nil {
		add("daemon checks", proto.CheckFail, err.Error())
		return out
	}
	return append(out, r.Checks...)
}

func launchAgentCheck(ctx context.Context) proto.Check {
	m, err := agents()
	if err != nil {
		return proto.Check{Name: "launch agent", Status: proto.CheckFail, Detail: err.Error()}
	}
	s, err := m.Status(ctx)
	if err != nil {
		return proto.Check{Name: "launch agent", Status: proto.CheckFail, Detail: err.Error()}
	}
	exe, _ := os.Executable()
	switch {
	case !s.Installed:
		return proto.Check{Name: "launch agent", Status: proto.CheckWarn, Detail: "not installed; the daemon will not start at login (hoplet daemon install)"}
	case !s.Loaded:
		return proto.Check{Name: "launch agent", Status: proto.CheckFail, Detail: m.Path() + " is installed but not loaded (hoplet daemon install)"}
	case exe != "" && s.Program != exe && !sameFile(s.Program, exe):
		return proto.Check{Name: "launch agent", Status: proto.CheckWarn, Detail: fmt.Sprintf("runs %s, not this binary %s", s.Program, exe)}
	default:
		return proto.Check{Name: "launch agent", Status: proto.CheckOK, Detail: fmt.Sprintf("%s, pid %d", s.State, s.PID)}
	}
}

func sameFile(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}
