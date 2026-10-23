package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TokenReply is the core's answer to a token request (`runtime.mcp.token`): a token, or the reason there is none.
type TokenReply struct {
	RequestID string
	SessionID string
	Token     string
	ExpiresAt int64 // epoch milliseconds
	Reason    string
}

// Tokens holds the session tokens of the processes that run, in memory only. The daemon asks the core for one the
// first time a session calls a tool and again when it nears its expiry; the core mints it for the session's owner.
type Tokens struct {
	send func(requestID, sessionID string) error
	now  func() time.Time
	wait time.Duration

	fetch   sync.Mutex // one request at a time
	mu      sync.Mutex
	tokens  map[string]held
	waiting map[string]chan TokenReply
}

type held struct {
	token   string
	expires time.Time
}

// refreshBefore is how long before its expiry a token is asked for again.
const refreshBefore = 5 * time.Minute

// NewTokens builds the holder; send writes a token request to the core and must not block.
func NewTokens(send func(requestID, sessionID string) error) *Tokens {
	return &Tokens{send: send, now: time.Now, wait: 15 * time.Second, tokens: map[string]held{}, waiting: map[string]chan TokenReply{}}
}

// Get is the session's token, asked of the core when there is none or it is about to expire.
func (t *Tokens) Get(ctx context.Context, sessionID string) (string, error) {
	if tok, ok := t.cached(sessionID); ok {
		return tok, nil
	}
	t.fetch.Lock()
	defer t.fetch.Unlock()
	if tok, ok := t.cached(sessionID); ok {
		return tok, nil
	}
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	ch := make(chan TokenReply, 1)
	t.mu.Lock()
	t.waiting[id] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.waiting, id)
		t.mu.Unlock()
	}()
	if err := t.send(id, sessionID); err != nil {
		return "", errors.New("the core is not connected")
	}
	timer := time.NewTimer(t.wait)
	defer timer.Stop()
	select {
	case rep := <-ch:
		if rep.Token == "" {
			reason := rep.Reason
			if reason == "" {
				reason = "refused"
			}
			return "", fmt.Errorf("the core gave this session no token (%s)", reason)
		}
		t.mu.Lock()
		t.tokens[sessionID] = held{token: rep.Token, expires: time.UnixMilli(rep.ExpiresAt)}
		t.mu.Unlock()
		return rep.Token, nil
	case <-timer.C:
		return "", errors.New("the core did not answer the token request")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (t *Tokens) cached(sessionID string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h, ok := t.tokens[sessionID]
	if !ok || t.now().Add(refreshBefore).After(h.expires) {
		return "", false
	}
	return h.token, true
}

// Deliver hands the core's answer to the call that waits for it; an answer nobody waits for is dropped.
func (t *Tokens) Deliver(rep TokenReply) {
	t.mu.Lock()
	ch := t.waiting[rep.RequestID]
	t.mu.Unlock()
	if ch != nil {
		select {
		case ch <- rep:
		default:
		}
	}
}

// Forget drops the session's token, when its process ended or the core refused it.
func (t *Tokens) Forget(sessionID string) {
	t.mu.Lock()
	delete(t.tokens, sessionID)
	t.mu.Unlock()
}
