// Package daemon runs Hoplet's daemon: it owns the store and the session
// manager and serves the client protocol on a Unix socket.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/internal/session"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// ErrRunning is returned by Start when another daemon holds the home's lock.
var ErrRunning = errors.New("daemon: another daemon is running")

// Config configures a daemon.
type Config struct {
	// Home is the data directory (see package home); it is created 0700.
	Home string
	// Version is reported to clients.
	Version string
	// Exe is the hoplet binary that runs shims.
	Exe string
	// Harnesses are the available harnesses by kind.
	Harnesses map[string]harness.Harness
	// Env is the complete environment of harness processes.
	Env    []string
	Logger *slog.Logger

	// lim overrides the protocol limits (tests); zero fields keep defaults.
	lim limits
}

// Daemon is a running daemon.
type Daemon struct {
	log   *slog.Logger
	lock  *os.File
	store *store.Store
	mgr   *session.Manager
	srv   *server
	http  *http.Server
	queue *queuer

	cancel context.CancelFunc
	wg     sync.WaitGroup
	done   chan struct{} // closed when serving stops
	err    error         // why serving stopped, when not by Close

	closeOnce sync.Once
	closeErr  error
}

// Start takes the home's lock, opens the store, reattaches to running
// sessions and only then starts serving on the home's socket. It returns
// once the socket accepts connections.
func Start(ctx context.Context, cfg Config) (d *Daemon, err error) {
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if cfg.Home == "" || cfg.Exe == "" {
		return nil, errors.New("daemon: home and exe are required")
	}
	if err := os.MkdirAll(cfg.Home, 0o700); err != nil {
		return nil, fmt.Errorf("daemon: create home: %w", err)
	}
	if err := secureHome(cfg.Home); err != nil {
		return nil, err
	}
	lock, err := takeLock(home.Lock(cfg.Home))
	if err != nil {
		return nil, err
	}
	// cleanup undoes what Start has done so far when it fails.
	var cleanup []func() error
	defer func() {
		if err != nil {
			for i := len(cleanup) - 1; i >= 0; i-- {
				err = errors.Join(err, cleanup[i]())
			}
			err = errors.Join(err, lock.Close())
		}
	}()
	token, err := home.EnsureToken(cfg.Home)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, cfg.Home)
	if err != nil {
		return nil, err
	}
	cleanup = append(cleanup, st.Close)
	mgr, err := session.New(session.Config{
		Store: st, Root: home.Sessions(cfg.Home), Spawner: session.ExecSpawner{Exe: cfg.Exe},
		Harnesses: cfg.Harnesses, Env: cfg.Env, Logger: log,
	})
	if err != nil {
		return nil, err
	}
	cleanup = append(cleanup, mgr.Close)
	// Recover before serving: a command must never reach a session whose
	// shim the manager has not reattached to yet.
	if err := mgr.Recover(ctx); err != nil {
		return nil, fmt.Errorf("daemon: recover: %w", err)
	}
	ln, err := listen(home.Socket(cfg.Home))
	if err != nil {
		return nil, err
	}
	cleanup = append(cleanup, ln.Close)

	host, _ := os.Hostname()
	runCtx, cancel := context.WithCancel(context.Background())
	q := &queuer{store: st, mgr: mgr, log: log, locks: map[proto.SessionID]*sync.Mutex{}, waiting: map[proto.SessionID]bool{}}
	srv := &server{
		ctx: runCtx, store: st, mgr: mgr, queue: q, token: token,
		host: host, version: cfg.Version, log: log, lim: cfg.lim.withDefaults(),
	}
	d = &Daemon{
		log: log, lock: lock, store: st, mgr: mgr, srv: srv, queue: q,
		http:   &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second},
		cancel: cancel, done: make(chan struct{}),
	}
	d.wg.Add(2)
	go func() {
		defer d.wg.Done()
		q.run(runCtx)
	}()
	go func() {
		defer d.wg.Done()
		defer close(d.done)
		if err := d.http.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			d.err = err
			log.Error("serve", "err", err)
		}
	}()
	log.Info("daemon started", "home", cfg.Home, "pid", os.Getpid())
	return d, nil
}

// Done is closed when the daemon stops serving; Err then says why.
func (d *Daemon) Done() <-chan struct{} { return d.done }

// Err is why serving stopped, or nil if it stopped because of Close. Err
// waits for Done, so on a daemon still serving it blocks until it stops.
func (d *Daemon) Err() error {
	<-d.done
	return d.err
}

// Close stops serving, ends every connection, detaches from the sessions
// (their shims keep running) and closes the store. The socket file stays:
// the next daemon replaces it.
func (d *Daemon) Close() error {
	d.closeOnce.Do(func() {
		err := d.http.Close()
		d.cancel()
		d.srv.wait()
		d.wg.Wait()
		d.closeErr = errors.Join(err, d.mgr.Close(), d.store.Close(), d.lock.Close())
	})
	return d.closeErr
}

// takeLock opens path and takes an exclusive, non-blocking lock on it,
// recording the daemon's pid. The lock lasts as long as the file is open,
// so it is released however the process exits.
func takeLock(path string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemon: open lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrRunning
		}
		return nil, fmt.Errorf("daemon: lock: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, fmt.Errorf("daemon: write lock: %w", err)
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("daemon: write lock: %w", err)
	}
	return f, nil
}

// tmpSuffix names the temporary socket listen renames over the real one.
const tmpSuffix = ".new"

// maxSocketPath is the longest Unix socket path every supported platform
// takes (sun_path is 104 bytes on macOS, including the terminating NUL).
const maxSocketPath = 103

// listen listens on a temporary path next to sock and renames it over sock,
// so a client never finds the path missing or owned by a dead daemon once
// this one serves. The temporary path is fixed, which is safe because the
// daemon holds the home's lock, and a stale one left by a daemon that died
// before the rename is removed first. The socket file is never removed:
// removing it could take away a newer daemon's socket.
func listen(sock string) (net.Listener, error) {
	tmp := sock + tmpSuffix
	if len(tmp) > maxSocketPath {
		return nil, fmt.Errorf("daemon: socket path %s is too long for a Unix socket", sock)
	}
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("daemon: clear temporary socket: %w", err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: tmp, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("daemon: listen: %w", err)
	}
	ln.SetUnlinkOnClose(false)
	if err := os.Chmod(tmp, 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("daemon: chmod socket: %w", err), ln.Close(), os.Remove(tmp))
	}
	if err := os.Rename(tmp, sock); err != nil {
		return nil, errors.Join(fmt.Errorf("daemon: publish socket: %w", err), ln.Close(), os.Remove(tmp))
	}
	return ln, nil
}
