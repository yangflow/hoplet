package link

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/daemon"
	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/proto"
)

// event is one call Watch made to its handler.
type event struct {
	kind string // connected, lost or item
	host string
	item proto.CatalogItem
}

func startDaemon(t *testing.T, dir string) *daemon.Daemon {
	t.Helper()
	// No session is created, so the daemon never runs Exe.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d, err := daemon.Start(context.Background(), daemon.Config{Home: dir, Version: "test", Exe: exe, Env: env.Static{"PATH=" + os.Getenv("PATH")}})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func next(t *testing.T, ch <-chan event) event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(15 * time.Second):
		t.Fatal("no event from Watch")
		return event{}
	}
}

func TestWatchReconnects(t *testing.T) {
	// Unix socket paths are short: keep the home out of t.TempDir.
	dir, err := os.MkdirTemp("", "dl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan event, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		Watch(ctx, HomeDialer(dir), Handler{
			Connected: func(_ *client.Client, host string) { ch <- event{kind: "connected", host: host} },
			Lost:      func(error) { ch <- event{kind: "lost"} },
			Item:      func(it proto.CatalogItem) { ch <- event{kind: "item", item: it} },
		})
	}()
	t.Cleanup(func() { cancel(); <-done })

	// No daemon yet: Watch reports why and keeps trying.
	if ev := next(t, ch); ev.kind != "lost" {
		t.Fatalf("before the daemon: %+v", ev)
	}
	d := startDaemon(t, dir)
	for ev := next(t, ch); ev.kind != "connected"; ev = next(t, ch) {
		if ev.kind != "lost" {
			t.Fatalf("waiting to connect: %+v", ev)
		}
	}
	if ev := next(t, ch); ev.kind != "item" || !ev.item.Snapshot {
		t.Fatalf("first item %+v", ev)
	}

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if ev := next(t, ch); ev.kind != "lost" {
		t.Fatalf("after the daemon stopped: %+v", ev)
	}
	d = startDaemon(t, dir)
	t.Cleanup(func() { d.Close() })
	for ev := next(t, ch); ev.kind != "connected"; ev = next(t, ch) {
		if ev.kind != "lost" {
			t.Fatalf("waiting to reconnect: %+v", ev)
		}
	}
	if ev := next(t, ch); ev.kind != "item" {
		t.Fatalf("item after reconnecting %+v", ev)
	}
}

func TestFollowUnknownSession(t *testing.T) {
	dir, err := os.MkdirTemp("", "dl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d := startDaemon(t, dir)
	t.Cleanup(func() { d.Close() })
	c, err := HomeDialer(dir)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = Follow(ctx, c, "missing", 0, func(proto.SessionItem) { t.Error("an item for a missing session") })
	var ae *proto.APIError
	if !errors.As(err, &ae) || ae.Code != proto.CodeNotFound {
		t.Fatalf("follow a missing session: %v", err)
	}
}
