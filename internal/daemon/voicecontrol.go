package daemon

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/internal/tools"
	"github.com/yangflow/hoplet/internal/voice"
	"github.com/yangflow/hoplet/proto"
)

const (
	voiceHarness       = "codex" // the one voice provider so far
	voiceRecordTimeout = 5 * time.Second
)

// voiceController gives each call a hidden session: the record that carries
// its tool token and transcript.
type voiceController struct {
	store   *store.Store
	command []string // the MCP server's command line
	log     *slog.Logger
}

var _ voice.Controller = (*voiceController)(nil)

// Begin implements voice.Controller.
func (c *voiceController) Begin(ctx context.Context) (voice.Control, error) {
	// A crash leaves the last call's token behind.
	if err := c.store.ClearVoiceTokens(ctx); err != nil {
		return nil, err
	}
	now := time.Now()
	sess, err := c.store.CreateSession(ctx, proto.Session{
		ID: proto.SessionID(rand.Text()), Harness: proto.HarnessRef{Kind: voiceHarness}, Mode: proto.ModeVoice,
		Owner: proto.OwnerMe, Origin: proto.OriginMe, Status: proto.StatusIdle, Permission: proto.ModeConfirm,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return nil, fmt.Errorf("daemon: voice session: %w", err)
	}
	token := rand.Text()
	if err := c.store.SetSessionToken(ctx, sess.ID, store.TokenHash(token), now); err != nil {
		return nil, fmt.Errorf("daemon: voice token: %w", err)
	}
	return &voiceControl{
		c: c, id: sess.ID,
		tools: []harness.MCPServer{{Name: "hoplet", Command: slices.Clone(c.command), Env: []string{tools.TokenEnv + "=" + token}}},
	}, nil
}

// voiceControl is the hidden session of one call.
type voiceControl struct {
	c      *voiceController
	id     proto.SessionID
	tools  []harness.MCPServer
	spoken atomic.Bool
}

// Tools implements voice.Control.
func (v *voiceControl) Tools() []harness.MCPServer { return v.tools }

// Transcript implements voice.Control. It logs no text.
func (v *voiceControl) Transcript(role proto.Role, id, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), voiceRecordTimeout)
	defer cancel()
	if _, err := v.c.store.Append(ctx, store.AppendRequest{
		Session: v.id, Turn: 1,
		Entries: []store.Entry{{Payload: proto.Message{ID: id, Role: role, Text: text}}},
	}); err != nil {
		v.c.log.Warn("voice transcript not recorded", "session", v.id, "err", err)
		return
	}
	v.spoken.Store(true)
}

// End implements voice.Control: the token dies with the call, and a call in
// which nothing was said leaves no record.
func (v *voiceControl) End() {
	ctx, cancel := context.WithTimeout(context.Background(), voiceRecordTimeout)
	defer cancel()
	var err error
	if v.spoken.Load() {
		err = v.c.store.ClearVoiceTokens(ctx)
	} else {
		err = v.c.store.ArchiveSession(ctx, v.id, time.Now())
	}
	if err != nil {
		v.c.log.Warn("voice session not closed", "session", v.id, "err", err)
	}
}
