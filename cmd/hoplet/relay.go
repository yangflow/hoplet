package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/relay"
)

const relayUsage = `usage: hoplet relay --domain NAME [--domain NAME] [--listen ADDR] [--dir DIR]
       hoplet relay allow ID | deny ID | list [--json] [--dir DIR]

Without a subcommand, runs a relay in the foreground until SIGTERM or
SIGINT. It gets a Let's Encrypt certificate for each --domain on its own
(the TLS-ALPN-01 challenge, on the --listen port, which must be 443 to the
outside); a connection for any other name gets the first domain's
certificate. The relay forwards only for the hosts in its allowlist. Its
directory, default $HOPLET_HOME/relay, holds the certificates and the
allowlist.

allow   let the host with this ID register (its ID: hoplet device list)
deny    remove a host from the allowlist
list    the hosts in the allowlist
`

// domains collects repeated --domain flags.
type domains []string

func (d *domains) String() string { return strings.Join(*d, ",") }

func (d *domains) Set(v string) error {
	v = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(v), "."))
	if v == "" || strings.ContainsAny(v, ":/ ") {
		return errors.New("want a domain name, such as relay.example.com")
	}
	*d = append(*d, v)
	return nil
}

func (a app) relayCmd(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "allow", "deny", "list":
			return a.relayAllow(args[0], args[1:])
		case "help", "-h", "--help":
			fmt.Fprint(a.stdout, relayUsage)
			return 0
		}
	}
	fs := a.flags("relay", relayUsage)
	var names domains
	fs.Var(&names, "domain", "a domain name the relay serves; repeat for a backup name")
	listen := fs.String("listen", ":443", "the address to listen on")
	dir := fs.String("dir", "", "the relay's directory (default $HOPLET_HOME/relay)")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	if len(names) == 0 {
		fmt.Fprint(a.stderr, relayUsage)
		return 2
	}
	d, err := relayDir(*dir)
	if err != nil {
		return a.fail("relay", err)
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return a.fail("relay", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return a.fail("relay", err)
	}
	logger := slog.New(slog.NewJSONHandler(a.stderr, nil))
	m := relay.CertManager(d, names)
	logger.Info("relay started", "listen", ln.Addr().String(), "domains", []string(names), "dir", d)
	s := &relay.Server{Dir: d, Logger: logger}
	if err := s.Serve(ctx, ln, relay.TLSConfig(m.GetCertificate, names)); err != nil {
		return a.fail("relay", err)
	}
	return 0
}

// relayDir returns dir, or $HOPLET_HOME/relay when it is empty.
func relayDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	h, err := home.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "relay"), nil
}

func (a app) relayAllow(cmd string, args []string) int {
	name := "relay " + cmd
	fs := a.flags(name, relayUsage)
	dir := fs.String("dir", "", "the relay's directory (default $HOPLET_HOME/relay)")
	asJSON := fs.Bool("json", false, "print JSON (list)")
	n := 1
	if cmd == "list" {
		n = 0
	}
	if code, ok := parse(fs, args, n, n); !ok {
		return code
	}
	d, err := relayDir(*dir)
	if err != nil {
		return a.fail(name, err)
	}
	switch cmd {
	case "allow":
		err = relay.Allow(d, noise.HostID(fs.Arg(0)))
	case "deny":
		err = relay.Deny(d, noise.HostID(fs.Arg(0)))
	default:
		var ids []noise.HostID
		if ids, err = relay.List(d); err == nil {
			if *asJSON {
				if ids == nil {
					ids = []noise.HostID{}
				}
				a.printJSON(ids)
			} else {
				for _, id := range ids {
					fmt.Fprintln(a.stdout, id)
				}
			}
		}
	}
	if err != nil {
		return a.fail(name, err)
	}
	return 0
}
