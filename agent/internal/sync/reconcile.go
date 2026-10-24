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
	if cs.welcomed && !cs.backlogSent {
		cs.backlogSent = true
		c.sendBacklog(ctx, cs, sources)
	}
	for _, src := range c.sched.order(ctx, sources, time.Now()) {
		if ctx.Err() != nil {
			return
		}
		c.mu.RLock()
		d, diverged := c.diverged[src.ID()]
		c.mu.RUnlock()
		if diverged {
			if err := c.reportDiverged(ctx, cs, src, d); err != nil {
				if errors.Is(err, errConnChanged) {
					return
				}
				log.Printf("[agent] divergence of %s not reported: %v", shortID(src.ID()), err)
			}
			continue
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
					c.lostOwnership(src.ID(), epoch, cursor, err)
					logRejected(src.ID(), err)
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
				c.lostOwnership(src.ID(), epoch, cursor, err)
				logRejected(src.ID(), err)
				continue
			}
			if errors.Is(err, errConnChanged) {
				return
			}
			log.Printf("[agent] sync %s failed: %v", shortID(src.ID()), err)
		}
	}
	if !backlog {
		// Caught up: the running sessions report their state after the transcript (ADR 0039).
		c.report(cs)
	}
}

// lostOwnership handles a refused append: a session deleted in Hoplet, or one another device owns, is not synced
// again; one this device owned is first reported as diverged when it wrote past the cursor.
func (c *Client) lostOwnership(id string, epoch uint64, cursor int64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ownershipEpoch[id] != epoch {
		return
	}
	if !errors.Is(err, errDeleted) && c.mine[id] {
		c.diverged[id] = divergence{cursor: cursor, epoch: c.ownerEpochs[id]}
	} else {
		c.suppressed[id] = true
	}
	delete(c.mine, id)
}

// sendBacklog names the transcript bytes each session has still to send, once per connection; the core shows
// the catch-up from them.
func (c *Client) sendBacklog(ctx context.Context, cs *connState, sources []provider.Source) {
	msg := DeviceBacklog{V: ProtocolVersion, Type: TypeDeviceBacklog, Sessions: []DeviceBacklogSession{}}
	for _, src := range sources {
		if len(msg.Sessions) >= MaxBacklogSessions || ctx.Err() != nil {
			break
		}
		c.mu.RLock()
		skip := c.suppressed[src.ID()] || c.rewritten[src.ID()]
		_, diverged := c.diverged[src.ID()]
		cursor := c.cursors[src.ID()]
		c.mu.RUnlock()
		if skip || diverged {
			continue
		}
		if size, err := src.Size(ctx); err == nil && size > cursor {
			msg.Sessions = append(msg.Sessions, DeviceBacklogSession{SessionID: src.ID(), Bytes: size - cursor})
		}
	}
	if len(msg.Sessions) == 0 {
		return
	}
	if err := c.writeMsg(cs, msg); err != nil {
		log.Printf("[agent] backlog not sent: %v", err)
	}
}

// maxDivergedScan is how many bytes past the cursor are read to count the diverged lines.
const maxDivergedScan = 64 << 20

// reportDiverged sends session.diverged for a session this device wrote to after it stopped being its owner. The
// lines stay in the file; the session is not synced from this device.
func (c *Client) reportDiverged(ctx context.Context, cs *connState, src provider.Source, d divergence) error {
	size, err := src.Size(ctx)
	if err != nil {
		return err
	}
	var lines int64
	for at := d.cursor; at < size && at-d.cursor < maxDivergedScan; {
		chunk, err := src.Read(ctx, at, maxBatchBytes)
		if err != nil || len(chunk) == 0 {
			break
		}
		lines += int64(bytes.Count(chunk, []byte{'\n'}))
		at += int64(len(chunk))
	}
	if size > d.cursor {
		if c.cs.Load() != cs {
			return errConnChanged
		}
		msg := SessionDiverged{V: ProtocolVersion, Type: TypeSessionDiverged, SessionID: src.ID(), OwnerEpoch: d.epoch, Bytes: size - d.cursor, Lines: lines}
		if err := c.writeMsg(cs, msg); err != nil {
			return errConnChanged
		}
		log.Printf("[agent] session %s: %d lines written after another device took it stay on this machine only", shortID(src.ID()), lines)
	}
	c.mu.Lock()
	delete(c.diverged, src.ID())
	c.suppressed[src.ID()] = true
	c.mu.Unlock()
	return nil
}

var (
	errRejected    = errors.New("rejected")
	errDeleted     = fmt.Errorf("%w: deleted", errRejected)
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
			Provider:   src.Provider(),
			ProjectDir: src.Dir(),
			FilePath:   "",
			ByteStart:  cursor,
			Encoding:   EncodingGzipBase64,
			Lines:      base64.StdEncoding.EncodeToString(gzBuf.Bytes()),
			Title:      provider.PeekTitle(ctx, src),

			Configuration: c.sessionConfiguration(src),
			OwnerEpoch:    c.ownerEpoch(src.ID()),
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
			return false, rejection(rep.reason)
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
		Provider:   src.Provider(),
		ProjectDir: src.Dir(),
		FilePath:   "",
		ByteStart:  cursor,
		Encoding:   EncodingGzipBase64,
		Lines:      base64.StdEncoding.EncodeToString(gzBuf.Bytes()),
		Title:      title,

		Configuration: c.sessionConfiguration(src),
		OwnerEpoch:    c.ownerEpoch(src.ID()),
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
		return rejection(rep.reason)
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

// rejection is the error of a session.rejected: errDeleted when the session was deleted in Hoplet, errRejected otherwise.
func rejection(reason string) error {
	if reason == ReasonDeleted {
		return errDeleted
	}
	return errRejected
}

// logRejected says why the core refused a session.
func logRejected(id string, err error) {
	if errors.Is(err, errDeleted) {
		log.Printf("[agent] session %s deleted in Hoplet; not syncing", shortID(id))
		return
	}
	log.Printf("[agent] session %s belongs to another device; skipping", shortID(id))
}

// sessionConfiguration is the configuration to announce with the session's appends: empty for the vendor's own.
func (c *Client) sessionConfiguration(src provider.Source) string {
	if c.hooks.SessionConfiguration == nil {
		return ""
	}
	id := c.hooks.SessionConfiguration(src.ID())
	if id == src.Provider() {
		return ""
	}
	return id
}

// ownerEpoch is the core's owner epoch of the session as this agent last heard it.
func (c *Client) ownerEpoch(id string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ownerEpochs[id]
}
