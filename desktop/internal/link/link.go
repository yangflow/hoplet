// Package link keeps the desktop connected to the local daemon and
// following its session catalog.
package link

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/yangflow/hoplet/internal/client"
	"github.com/yangflow/hoplet/internal/home"
	"github.com/yangflow/hoplet/proto"
)

// Retry delays after a failed or lost connection.
const (
	firstRetry = time.Second
	lastRetry  = 30 * time.Second
)

// Handler receives what Watch learns. Its functions are called from
// Watch's goroutine, one at a time.
type Handler struct {
	// Connected reports a new connection, with the daemon's host name.
	Connected func(c *client.Client, host string)
	// Lost reports that there is no connection and why.
	Lost func(err error)
	// Item delivers a catalog item.
	Item func(proto.CatalogItem)
}

// Dialer connects to the daemon.
type Dialer func(ctx context.Context) (*client.Client, error)

// HomeDialer dials the daemon of the home directory dir as this desktop.
func HomeDialer(dir string) Dialer {
	return func(ctx context.Context) (*client.Client, error) {
		o, err := client.HomeOptions(dir, deviceID())
		if err != nil {
			return nil, err
		}
		return client.Dial(ctx, o)
	}
}

// DefaultDialer dials the daemon of the user's Hoplet home.
func DefaultDialer() (Dialer, error) {
	dir, err := home.Dir()
	if err != nil {
		return nil, err
	}
	return HomeDialer(dir), nil
}

func deviceID() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		h = "localhost"
	}
	return "desktop@" + h
}

// Watch connects, follows the catalog and, when the connection fails or
// ends, connects again, resuming from the last catalog version it
// delivered. It returns when ctx ends.
func Watch(ctx context.Context, dial Dialer, h Handler) {
	var version uint64
	wait := firstRetry
	for ctx.Err() == nil {
		c, err := dial(ctx)
		if err == nil {
			wait = firstRetry
			h.Connected(c, c.Hello().Host)
			err = follow(ctx, c, &version, h.Item)
			c.Close()
		}
		if ctx.Err() != nil {
			return
		}
		h.Lost(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, lastRetry)
	}
}

// follow delivers catalog items until the subscription ends.
func follow(ctx context.Context, c *client.Client, version *uint64, item func(proto.CatalogItem)) error {
	sub, err := c.SubscribeCatalog(ctx, *version)
	if err != nil {
		return err
	}
	for {
		var it proto.CatalogItem
		if err := sub.Next(ctx, &it); err != nil {
			if errors.Is(err, client.ErrEnded) {
				return errors.New("link: the daemon ended the catalog subscription")
			}
			return err
		}
		*version = it.Version
		item(it)
	}
}

// Follow delivers a session's subscription items, from after seq, until
// the subscription or ctx ends; it returns why it ended.
func Follow(ctx context.Context, c *client.Client, id proto.SessionID, after uint64, item func(proto.SessionItem)) error {
	sub, err := c.SubscribeSession(ctx, proto.SessionParams{Session: id, AfterSeq: after})
	if err != nil {
		return err
	}
	for {
		var it proto.SessionItem
		if err := sub.Next(ctx, &it); err != nil {
			return err
		}
		item(it)
	}
}
