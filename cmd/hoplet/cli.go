package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/proto"
)

// flags returns a flag set that prints usage to stderr.
func (a app) flags(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(a.stderr, usage)
		fs.PrintDefaults()
	}
	return fs
}

// parse parses args, accepting flags before and after the positional
// arguments (everything after "--" is positional), and checks that there
// are between min and max positional arguments (max < 0: no limit). ok is
// false when the command must exit with code.
func parse(fs *flag.FlagSet, args []string, min, max int) (code int, ok bool) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0, false
			}
			return 2, false
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			pos = append(pos, rest...)
			break
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
	if len(pos) < min || (max >= 0 && len(pos) > max) {
		fs.Usage()
		return 2, false
	}
	// Leave the positional arguments in fs.Args for the caller.
	if err := fs.Parse(append([]string{"--"}, pos...)); err != nil {
		return 2, false
	}
	return 0, true
}

// fail prints err and returns exit code 1.
func (a app) fail(cmd string, err error) int {
	fmt.Fprintf(a.stderr, "hoplet %s: %v\n", cmd, err)
	return 1
}

// deviceID names this command line to the daemon (it decides approvals).
func deviceID() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		h = "localhost"
	}
	return "cli@" + h
}

// errNoDaemon explains a failed connection.
var errNoDaemon = errors.New("the daemon is not running (start it with `hoplet daemon install`)")

func dialHome(ctx context.Context, h string) (*client.Client, error) {
	o, err := client.HomeOptions(h, deviceID())
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNoDaemon
	}
	if err != nil {
		return nil, err
	}
	c, err := client.Dial(ctx, o)
	var ae *proto.APIError
	if err != nil && !errors.As(err, &ae) {
		return nil, fmt.Errorf("%w: %w", errNoDaemon, err)
	}
	return c, err
}

// connect dials the daemon of the default home.
func connect(ctx context.Context) (*client.Client, error) {
	h, err := home.Dir()
	if err != nil {
		return nil, err
	}
	return dialHome(ctx, h)
}

// newKey returns an idempotency key for one command.
func newKey() string { return "cli-" + rand.Text() }

func (a app) printJSON(v any) {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// resolveID accepts a session ID or a unique prefix of at least 4
// characters.
func resolveID(ctx context.Context, c *client.Client, arg string) (proto.SessionID, error) {
	ss, err := c.Sessions(ctx)
	if err != nil {
		return "", err
	}
	var matches []proto.SessionID
	for _, s := range ss {
		if string(s.ID) == arg {
			return s.ID, nil
		}
		if len(arg) >= 4 && strings.HasPrefix(string(s.ID), arg) {
			matches = append(matches, s.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no session %q", arg)
	default:
		return "", fmt.Errorf("%q matches %d sessions", arg, len(matches))
	}
}
