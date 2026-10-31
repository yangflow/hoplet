package client

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/daemon"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/proto"
)

func startDaemon(t *testing.T) (string, *daemon.Daemon) {
	t.Helper()
	h, err := os.MkdirTemp("", "hc") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(h) })
	// No harness: these tests never start a session.
	d, err := daemon.Start(context.Background(), daemon.Config{Home: h, Version: "test", Exe: "/nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return h, d
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestDialFailsWithoutDaemon(t *testing.T) {
	h := t.TempDir()
	if _, err := HomeOptions(h, "d"); err == nil {
		t.Fatal("options without a token file")
	}
	if _, err := Dial(ctx(t), Options{Socket: home.Socket(h), Token: "x", DeviceID: "d"}); err == nil {
		t.Fatal("dialed a missing socket")
	}
}

func TestWrongTokenIsRefused(t *testing.T) {
	h, _ := startDaemon(t)
	_, err := Dial(ctx(t), Options{Socket: home.Socket(h), Token: "wrong", DeviceID: "d"})
	var ae *proto.APIError
	if !errors.As(err, &ae) || ae.Code != proto.CodeUnauthorized {
		t.Fatalf("dial with a wrong token: %v", err)
	}
}

func TestCallsAndSubscriptionsFailOnceTheDaemonIsGone(t *testing.T) {
	h, d := startDaemon(t)
	o, err := HomeOptions(h, "d")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Dial(ctx(t), o)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Hello().Version != "test" || c.Hello().ProtocolMajor != proto.ProtocolMajor {
		t.Fatalf("hello %+v", c.Hello())
	}
	sub, err := c.SubscribeCatalog(ctx(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	var item proto.CatalogItem
	if err := sub.Next(ctx(t), &item); err != nil || !item.Snapshot {
		t.Fatalf("first item %+v %v", item, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	<-c.Done()
	if err := sub.Next(ctx(t), &item); !errors.Is(err, ErrClosed) {
		t.Fatalf("next after the daemon left: %v", err)
	}
	if _, err := c.Sessions(ctx(t)); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after the daemon left: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close of a lost connection: %v", err)
	}
}

func TestCloseEndsCalls(t *testing.T) {
	h, _ := startDaemon(t)
	o, err := HomeOptions(h, "d")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Dial(ctx(t), o)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Sessions(ctx(t)); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after close: %v", err)
	}
}
