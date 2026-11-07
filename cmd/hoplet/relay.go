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
	"runtime"
	"strings"
	"syscall"

	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/relay"
	"github.com/yangflow/hoplet/internal/systemd"
)

const relayUsage = `usage: hoplet relay --domain NAME [--domain NAME] [--listen ADDR] [--dir DIR]
       hoplet relay install --domain NAME [--domain NAME] [--listen ADDR]
       hoplet relay uninstall
       hoplet relay allow ID | deny ID | list [--json] [--dir DIR]

Without a subcommand, runs a relay in the foreground until SIGTERM or
SIGINT. It gets a Let's Encrypt certificate for each --domain on its own
(the TLS-ALPN-01 challenge, on the --listen port, which must be 443 to the
outside); a connection for any other name gets the first domain's
certificate. The relay forwards only for the hosts in its allowlist. Its
directory holds the certificates and the allowlist: /var/lib/hoplet-relay
when the relay is installed as a service, else $HOPLET_HOME/relay.

install   run as root on Linux: install this binary as the system service
          hoplet-relay, which runs as its own user, and start it
uninstall run as root: stop the service and remove it; its directory stays
allow     let the host with this ID register (its ID: hoplet device list)
deny      remove a host from the allowlist
list      the hosts in the allowlist
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
		case "install":
			return a.relayInstall(args[1:])
		case "uninstall":
			return a.relayUninstall(args[1:])
		case "help", "-h", "--help":
			fmt.Fprint(a.stdout, relayUsage)
			return 0
		}
	}
	fs := a.flags("relay", relayUsage)
	var names domains
	fs.Var(&names, "domain", "a domain name the relay serves; repeat for a backup name")
	listen := fs.String("listen", ":443", "the address to listen on")
	dir := fs.String("dir", "", "the relay's directory")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	if len(names) == 0 {
		fmt.Fprint(a.stderr, relayUsage)
		return 2
	}
	d, err := a.relayDir(*dir)
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

// relayDir returns dir or, when it is empty, the service's directory if
// the relay is installed as a service, else $HOPLET_HOME/relay.
func (a app) relayDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	if rs, err := a.relaySystem(); err == nil {
		if _, err := os.Stat(rs.manager().Path()); err == nil {
			return rs.dir, nil
		}
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
	dir := fs.String("dir", "", "the relay's directory")
	asJSON := fs.Bool("json", false, "print JSON (list)")
	n := 1
	if cmd == "list" {
		n = 0
	}
	if code, ok := parse(fs, args, n, n); !ok {
		return code
	}
	d, err := a.relayDir(*dir)
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

// relaySystem is where `relay install` puts the relay's service.
type relaySystem struct {
	root  bool   // the command runs as root
	units string // where system units go
	dir   string // the service's directory
	// exe is the binary the service runs; empty means this one, which must
	// be installed where the service's own user can run it.
	exe string
	run systemd.Runner
}

// realRelaySystem is this system's, on Linux.
func realRelaySystem() (relaySystem, error) {
	if runtime.GOOS != "linux" {
		return relaySystem{}, errors.New("installing the relay is supported on Linux only")
	}
	return relaySystem{root: os.Geteuid() == 0, units: systemd.SystemUnits, dir: systemd.RelayDir, run: runCommand}, nil
}

func (rs relaySystem) manager() systemd.Manager {
	return systemd.Manager{Dir: rs.units, Name: systemd.RelayUnit, Run: rs.run}
}

// installer returns the relay system, checked to be one the command may
// change.
func (a app) relayInstaller(name string) (relaySystem, error) {
	rs, err := a.relaySystem()
	if err != nil {
		return relaySystem{}, err
	}
	if !rs.root {
		return relaySystem{}, fmt.Errorf("run it as root: sudo hoplet %s", name)
	}
	return rs, nil
}

func (a app) relayInstall(args []string) int {
	fs := a.flags("relay install", relayUsage)
	var names domains
	fs.Var(&names, "domain", "a domain name the relay serves; repeat for a backup name")
	listen := fs.String("listen", ":443", "the address to listen on")
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	if len(names) == 0 {
		fmt.Fprint(a.stderr, relayUsage)
		return 2
	}
	rs, err := a.relayInstaller("relay install")
	if err != nil {
		return a.fail("relay install", err)
	}
	exe := rs.exe
	if exe == "" {
		if exe, err = os.Executable(); err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			return a.fail("relay install", err)
		}
		if transientBinary(exe) {
			return a.fail("relay install", fmt.Errorf("%s is a temporary binary; install a built one in /usr/local/bin", exe))
		}
		if err := othersCanRun(exe); err != nil {
			return a.fail("relay install", err)
		}
	}
	unit, err := systemd.RelayService(systemd.Relay{Exe: exe, Domains: names, Listen: *listen})
	if err != nil {
		return a.fail("relay install", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	m := rs.manager()
	if err := m.Install(ctx, unit); err != nil {
		return a.fail("relay install", err)
	}
	fmt.Fprintf(a.stdout, "installed %s\nthe relay serves %s on %s; its directory is %s\n", m.Path(), strings.Join(names, ", "), *listen, rs.dir)
	fmt.Fprintln(a.stdout, "next, for each host that registers: sudo hoplet relay allow ID (its ID: hoplet device list); logs: journalctl -u "+systemd.RelayUnit)
	return 0
}

func (a app) relayUninstall(args []string) int {
	fs := a.flags("relay uninstall", relayUsage)
	if code, ok := parse(fs, args, 0, 0); !ok {
		return code
	}
	rs, err := a.relayInstaller("relay uninstall")
	if err != nil {
		return a.fail("relay uninstall", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if err := rs.manager().Uninstall(ctx); err != nil {
		return a.fail("relay uninstall", err)
	}
	fmt.Fprintf(a.stdout, "uninstalled; %s keeps the certificates and the allowlist\n", rs.dir)
	return 0
}

// othersCanRun says why the relay's own user cannot run exe: it, or a
// directory above it, is not executable by other users.
func othersCanRun(exe string) error {
	for p := exe; ; p = filepath.Dir(p) {
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		if fi.Mode().Perm()&0o001 == 0 {
			return fmt.Errorf("%s is not accessible to other users (mode %v), and the relay runs as its own user; install the binary in /usr/local/bin", p, fi.Mode().Perm())
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}
