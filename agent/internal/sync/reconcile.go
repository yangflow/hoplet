package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// Reconcile aligns the session sources with the server cursors: any cursor < stream size sends its delta.
// Sessions that grew recently go first and each sends at most roundBytesPerSession; a session with more
// left wakes the next round at once. Reconcile is called from one goroutine.
func (c *Client) Reconcile(ctx context.Context, sources []provider.Source) {
	cs := c.cs.Load()
	if cs == nil {
		return
	}
	backlog := false
	defer func() {
		if backlog {
			c.notifyWake()
		}
	}()
	for _, src := range c.sched.order(ctx, sources, time.Now()) {
		if ctx.Err() != nil {
			return
		}
		c.mu.RLock()
		sup := c.suppressed[src.ID()]
		rewritten := c.rewritten[src.ID()]
		cursor := c.cursors[src.ID()]
		epoch := c.ownershipEpoch[src.ID()]
		c.mu.RUnlock()
		if sup || rewritten || c.sched.blockedAt(src.ID(), cursor) {
			continue
		}
		if size, err := src.Size(ctx); err == nil && size >= 0 && cursor >= size {
			if err := c.pushTitle(ctx, cs, src, cursor); err != nil {
				if errors.Is(err, errRejected) {
					c.mu.Lock()
					if c.ownershipEpoch[src.ID()] == epoch {
						c.suppressed[src.ID()] = true
					}
					c.mu.Unlock()
					log.Printf("[agent] session %s belongs to another device; skipping", shortID(src.ID()))
					continue
				}
				if errors.Is(err, errConnChanged) {
					return
				}
				log.Printf("[agent] title refresh %s failed: %v", shortID(src.ID()), err)
			}
			continue
		}
		capped, err := c.sendDelta(ctx, cs, src, cursor)
		backlog = backlog || capped
		if err != nil {
			var tooLarge *provider.LineTooLargeError
			if errors.As(err, &tooLarge) {
				if c.sched.block(src.ID(), tooLarge.Offset) {
					log.Printf("[agent] sync %s failed: %v; the session stays blocked at that offset", shortID(src.ID()), err)
				}
				continue
			}
			if errors.Is(err, provider.ErrBeyondEnd) {
				c.mu.Lock()
				if c.ownershipEpoch[src.ID()] == epoch {
					c.rewritten[src.ID()] = true
				}
				c.mu.Unlock()
				log.Printf("[agent] session %s shrank below the server cursor; skipping", shortID(src.ID()))
				continue
			}
			if errors.Is(err, errRejected) {
				c.mu.Lock()
				if c.ownershipEpoch[src.ID()] == epoch {
					c.suppressed[src.ID()] = true
				}
				c.mu.Unlock()
				log.Printf("[agent] session %s belongs to another device; skipping", shortID(src.ID()))
				continue
			}
			if errors.Is(err, errConnChanged) {
				return
			}
			log.Printf("[agent] sync %s failed: %v", shortID(src.ID()), err)
		}
	}
}

var (
	errRejected    = errors.New("rejected")
	errConnChanged = errors.New("connection changed")
	errResyncLoop  = errors.New("too many resyncs")
)

// sendDelta sends the bytes after cursor in batches of at most maxBatchBytes (a longer line goes alone) and
// reports whether it stopped at roundBytesPerSession with more possibly left.
func (c *Client) sendDelta(ctx context.Context, cs *connState, src provider.Source, cursor int64) (bool, error) {
	resyncs := 0
	sentBytes := int64(0)
	batches := 0
	capped := false
	start := cursor

	for {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if c.cs.Load() != cs {
			return false, errConnChanged
		}
		payload, err := src.Read(ctx, cursor, maxBatchBytes)
		if err != nil {
			return false, err
		}
		if len(payload) == 0 {
			break // nothing new (or a partial tail only); wait for the next poll
		}

		var gzBuf bytes.Buffer
		gz := gzip.NewWriter(&gzBuf)
		if _, err := gz.Write(payload); err != nil {
			return false, err
		}
		if err := gz.Close(); err != nil {
			return false, err
		}

		msg := SessionAppend{
			V:          ProtocolVersion,
			Type:       TypeSessionAppend,
			SessionID:  src.ID(),
			ProjectDir: src.Dir(),
			FilePath:   "",
			ByteStart:  cursor,
			Encoding:   EncodingGzipBase64,
			Lines:      base64.StdEncoding.EncodeToString(gzBuf.Bytes()),
			Title:      provider.PeekTitle(ctx, src),
		}
		if err := c.writeMsg(cs, msg); err != nil {
			return false, fmt.Errorf("send: %w", err)
		}

		// Wait for this session's reply (replies of other sessions are dropped; the server cursor wins)
		var rep reply
	wait:
		for {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-cs.done:
				return false, errConnChanged
			case r := <-cs.replies:
				if r.sessionID == src.ID() {
					rep = r
					break wait
				}
			}
		}
		switch rep.kind {
		case replyRejected:
			return false, errRejected
		case replyResync:
			resyncs++
			if resyncs > maxResyncTries {
				return false, errResyncLoop
			}
			log.Printf("[agent] session %s resync, resending from %d", shortID(src.ID()), rep.byteSize)
			cursor = rep.byteSize
			continue
		case replyAck:
			expect := cursor + int64(len(payload))
			if rep.byteSize != expect {
				log.Printf("[agent] warning: ack byte mismatch expect=%d got=%d; server wins", expect, rep.byteSize)
			}
			cursor = rep.byteSize
			sentBytes += int64(len(payload))
			batches++
			capped = sentBytes >= roundBytesPerSession
			if msg.Title != "" {
				c.mu.Lock()
				c.titles[src.ID()] = msg.Title
				c.mu.Unlock()
			}
		}
		if capped {
			break
		}
	}

	if sentBytes > 0 {
		log.Printf("[agent] synced %s: +%d bytes (%d batches, from %d)", shortID(src.ID()), sentBytes, batches, start)
	}
	return capped, nil
}

// pushTitle sends a zero-byte append that only refreshes the store title when PeekTitle
// differs from the last title we pushed (caught-up sessions after an on-disk ai-title).
func (c *Client) pushTitle(ctx context.Context, cs *connState, src provider.Source, cursor int64) error {
	title := provider.PeekTitle(ctx, src)
	if title == "" {
		return nil
	}
	c.mu.RLock()
	last := c.titles[src.ID()]
	c.mu.RUnlock()
	if title == last {
		return nil
	}
	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	if err := gz.Close(); err != nil {
		return err
	}
	msg := SessionAppend{
		V:          ProtocolVersion,
		Type:       TypeSessionAppend,
		SessionID:  src.ID(),
		ProjectDir: src.Dir(),
		FilePath:   "",
		ByteStart:  cursor,
		Encoding:   EncodingGzipBase64,
		Lines:      base64.StdEncoding.EncodeToString(gzBuf.Bytes()),
		Title:      title,
	}
	if err := c.writeMsg(cs, msg); err != nil {
		return fmt.Errorf("send title: %w", err)
	}
	var rep reply
wait:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-cs.done:
			return errConnChanged
		case r := <-cs.replies:
			if r.sessionID == src.ID() {
				rep = r
				break wait
			}
		}
	}
	switch rep.kind {
	case replyRejected:
		return errRejected
	case replyResync:
		// Title-only; next Reconcile will send real bytes from the new cursor.
		return nil
	case replyAck:
		c.mu.Lock()
		c.titles[src.ID()] = title
		c.mu.Unlock()
		return nil
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// agentSocketPath is the core's agent WebSocket; the device token goes in the Authorization header only.
const agentSocketPath = "/ws/agent"

func wsEndpoint(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	if !strings.HasSuffix(u.Path, agentSocketPath) {
		u.Path = strings.TrimSuffix(u.Path, "/") + agentSocketPath
	}
	return u.String(), nil
}
