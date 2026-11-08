package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/pair"
	"github.com/yangflow/hoplet/proto"
)

const pairUsage = `usage: hoplet pair [--json]

Prints a pairing code for this machine. On the other machine, run
hoplet host add CODE within ten minutes; the code works once, and a new
hoplet pair replaces it. The code carries network.routes and
network.relays from config.toml: set one of them first (network.routes
with network.listen).
`

const hostUsage = `usage: hoplet host <command> [flags]

commands:
  add     CODE [--name N]   pair with the host that printed CODE (hoplet pair)
  list    [--json]          the hosts this machine reaches
  remove  NAME|ID           forget a host

hoplet --host NAME session ... and hoplet --host NAME device ... run on a
host added here.
`

const deviceUsage = `usage: hoplet [--host NAME] device <command> [flags]

commands:
  list    [--json]   the devices paired with this host
  revoke  ID         end a device's access and its connections

ID may be a unique prefix of at least 4 characters.
`

func (a app) pair(args []string) int {
	fs := a.flags("pair", pairUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	return a.withClient("pair", commandTimeout, func(ctx context.Context, c *client.Client) error {
		pc, err := c.StartPairing(ctx)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(pc)
			return nil
		}
		fmt.Fprintln(a.stdout, pc.Code)
		fmt.Fprintf(a.stdout, "\nOn the other machine, run hoplet host add with this code before %s.\n", pc.ExpiresAt.Local().Format("15:04"))
		return nil
	})
}

// subcommand runs the command args[0] names among cmds, or prints usage.
func (a app) subcommand(name, usage string, cmds map[string]func([]string) int, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usage)
		return 2
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(a.stdout, usage)
		return 0
	}
	cmd, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(a.stderr, "hoplet %s: unknown command %q\n", name, args[0])
		return 2
	}
	return cmd(args[1:])
}

func (a app) hostCmd(args []string) int {
	return a.subcommand("host", hostUsage, map[string]func([]string) int{
		"add": a.hostAdd, "list": a.hostList, "remove": a.hostRemove,
	}, args)
}

func (a app) hostAdd(args []string) int {
	fs := a.flags("host add", hostUsage)
	name := fs.String("name", "", "the name to reach the host by (default: the host's own)")
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	code, err := pair.Parse(fs.Arg(0))
	if err != nil {
		return a.fail("host add", err)
	}
	// An expired code would be refused; say so without using the network.
	if !time.Now().Before(code.Expires) {
		return a.fail("host add", fmt.Errorf("the pairing code expired at %s; run hoplet pair on the host again", code.Expires.Local().Format("15:04")))
	}
	n := strings.TrimSpace(*name)
	if n == "" {
		n = code.Name
	}
	return a.withClient("host add", commandTimeout, func(ctx context.Context, local *client.Client) error {
		// The name is checked first: a conflict found after pairing would
		// have used up the code.
		hosts, err := local.Hosts(ctx)
		if err != nil {
			return err
		}
		for _, h := range hosts {
			if h.Name == n && h.ID != string(code.ID) {
				return fmt.Errorf("a host named %q exists already (%s); choose another name with --name", n, h.ID)
			}
		}
		h, err := home.Dir()
		if err != nil {
			return err
		}
		id, err := noise.LoadIdentity(h)
		if err != nil {
			return err
		}
		hostname, _ := os.Hostname()
		r := client.Remote{Routes: code.Routes, HostKey: code.Key, Identity: id, TLS: a.relayTLS}
		rc, err := client.Pair(ctx, r, code.PSK, pair.DisplayName(hostname))
		if errors.Is(err, noise.ErrPairingRefused) {
			return fmt.Errorf("%w; run `hoplet pair` on the host for a new code", err)
		}
		if err != nil {
			return err
		}
		_ = rc.Close()
		if err := local.AddHost(ctx, proto.Host{ID: string(code.ID), Key: code.Key, Name: n, Routes: code.Routes}); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "Added host %s (%s).\n", n, code.ID)
		return nil
	})
}

func (a app) hostList(args []string) int {
	fs := a.flags("host list", hostUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	return a.withClient("host list", commandTimeout, func(ctx context.Context, c *client.Client) error {
		hosts, err := c.Hosts(ctx)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(hosts)
			return nil
		}
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tID\tADDED\tROUTES")
		for _, h := range hosts {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", h.Name, h.ID, h.AddedAt.Local().Format("2006-01-02 15:04"), strings.Join(h.Routes, " "))
		}
		return tw.Flush()
	})
}

func (a app) hostRemove(args []string) int {
	fs := a.flags("host remove", hostUsage)
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	return a.withClient("host remove", commandTimeout, func(ctx context.Context, c *client.Client) error {
		return c.RemoveHost(ctx, fs.Arg(0))
	})
}

func (a app) device(args []string) int {
	return a.subcommand("device", deviceUsage, map[string]func([]string) int{
		"list": a.deviceList, "revoke": a.deviceRevoke,
	}, args)
}

func (a app) deviceList(args []string) int {
	fs := a.flags("device list", deviceUsage)
	asJSON := fs.Bool("json", false, "print JSON")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	return a.withClient("device list", commandTimeout, func(ctx context.Context, c *client.Client) error {
		ds, err := c.Devices(ctx)
		if err != nil {
			return err
		}
		if *asJSON {
			a.printJSON(ds)
			return nil
		}
		tw := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tPAIRED\tREVOKED")
		for _, d := range ds {
			revoked := "-"
			if d.RevokedAt != nil {
				revoked = d.RevokedAt.Local().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.ID, d.Name, d.PairedAt.Local().Format("2006-01-02 15:04"), revoked)
		}
		return tw.Flush()
	})
}

func (a app) deviceRevoke(args []string) int {
	fs := a.flags("device revoke", deviceUsage)
	if code, ok := parse(fs, args, 1, 1); !ok {
		return code
	}
	return a.withClient("device revoke", commandTimeout, func(ctx context.Context, c *client.Client) error {
		return c.RevokeDevice(ctx, fs.Arg(0))
	})
}
