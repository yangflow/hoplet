package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The tools of a parent session for its children (ADR 0035). The core owns every fact and every check; these are
// projections of the routes under /api/sessions/:id/children/.

// childrenPath is the route of the session's own children, always under the caller's own ID.
func childrenPath(self, rest string) string {
	return fmt.Sprintf("/api/sessions/%s/children/%s", url.PathEscape(self), rest)
}

// waitChildren holds the call until the children are settled or the wait is over. The state is the core's: this only
// asks again, so a restart of the daemon or the core loses nothing.
func (c *Core) waitChildren(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		SessionIDs     []string `json:"sessionIds"`
		Until          string   `json:"until"`
		TimeoutSeconds *int     `json:"timeoutSeconds"`
	}
	if len(args) > 0 && json.Unmarshal(args, &a) != nil {
		return errReply("invalid arguments: sessionIds, until and timeoutSeconds")
	}
	if a.Until == "" {
		a.Until = "all"
	}
	if (a.Until != "all" && a.Until != "any") || len(a.SessionIDs) > maxWaitIDs ||
		(a.TimeoutSeconds != nil && (*a.TimeoutSeconds < 1 || *a.TimeoutSeconds > maxWaitSeconds)) {
		return errReply("invalid arguments: until is all or any, sessionIds has at most 16 entries and timeoutSeconds is 1 to " + strconv.Itoa(maxWaitSeconds))
	}
	for _, id := range a.SessionIDs {
		if !sessionIDPattern.MatchString(id) {
			return errReply("invalid arguments: sessionIds are session IDs of children")
		}
	}
	path := childrenPath(sessionID, "status")
	if len(a.SessionIDs) > 0 {
		path += "?ids=" + url.QueryEscape(strings.Join(a.SessionIDs, ","))
	}
	limit := c.waitMax
	if a.TimeoutSeconds != nil && time.Duration(*a.TimeoutSeconds)*time.Second < limit {
		limit = time.Duration(*a.TimeoutSeconds) * time.Second
	}
	deadline := time.Now().Add(limit)
	for {
		body, rep, ok := c.get(ctx, sessionID, path)
		if !ok {
			return rep
		}
		var st struct {
			Children []struct {
				State string `json:"state"`
			} `json:"children"`
		}
		if json.Unmarshal(body, &st) != nil {
			return errReply("the core's answer is not valid")
		}
		running, settled := 0, 0
		for _, ch := range st.Children {
			if ch.State == "running" {
				running++
			} else {
				settled++
			}
		}
		done := running == 0 || (a.Until == "any" && settled > 0)
		if done || !time.Now().Add(c.pollEvery).Before(deadline) {
			var out map[string]json.RawMessage
			if json.Unmarshal(body, &out) != nil {
				return errReply("the core's answer is not valid")
			}
			out["pending"], _ = json.Marshal(!done)
			return finish(out)
		}
		select {
		case <-ctx.Done():
			return errReply("the call was cancelled")
		case <-time.After(c.pollEvery):
		}
	}
}

func (c *Core) collectChild(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		SessionID string `json:"sessionId"`
		Release   *bool  `json:"release"`
	}
	if json.Unmarshal(args, &a) != nil || !sessionIDPattern.MatchString(a.SessionID) {
		return errReply("invalid arguments: sessionId is a child of this session")
	}
	path := childrenPath(sessionID, url.PathEscape(a.SessionID)+"/collect")
	if a.Release != nil && !*a.Release {
		path += "?release=false"
	}
	body, rep, ok := c.post(ctx, sessionID, path, struct{}{})
	if !ok {
		return rep
	}
	var out json.RawMessage
	if json.Unmarshal(body, &out) != nil {
		return errReply("the core's answer is not valid")
	}
	return finish(out)
}

func (c *Core) readChildDiff(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		SessionID string `json:"sessionId"`
		Path      string `json:"path"`
		Cursor    *int64 `json:"cursor"`
	}
	if json.Unmarshal(args, &a) != nil || !sessionIDPattern.MatchString(a.SessionID) ||
		strings.TrimSpace(a.Path) == "" || utf8.RuneCountInString(a.Path) > maxPathChars ||
		(a.Cursor != nil && *a.Cursor < 0) {
		return errReply("invalid arguments: sessionId is a child of this session and path is a file from collect_child")
	}
	q := url.Values{"path": {a.Path}, "limit": {strconv.Itoa(diffPageBytes)}}
	if a.Cursor != nil {
		q.Set("cursor", strconv.FormatInt(*a.Cursor, 10))
	}
	body, rep, ok := c.get(ctx, sessionID, childrenPath(sessionID, url.PathEscape(a.SessionID)+"/diff?"+q.Encode()))
	if !ok {
		return rep
	}
	var out json.RawMessage
	if json.Unmarshal(body, &out) != nil {
		return errReply("the core's answer is not valid")
	}
	return finish(out)
}

func (c *Core) cancelChild(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		SessionID string `json:"sessionId"`
		Subtree   bool   `json:"subtree"`
	}
	if json.Unmarshal(args, &a) != nil || !sessionIDPattern.MatchString(a.SessionID) {
		return errReply("invalid arguments: sessionId is a session this session started")
	}
	if a.SessionID == sessionID {
		return errReply("Hoplet refused the call: self (a session cannot cancel itself)")
	}
	path := childrenPath(sessionID, url.PathEscape(a.SessionID)+"/cancel")
	if a.Subtree {
		path += "?subtree=true"
	}
	body, rep, ok := c.post(ctx, sessionID, path, struct{}{})
	if !ok {
		return rep
	}
	var out struct {
		Cancelled []string `json:"cancelled"`
	}
	if json.Unmarshal(body, &out) != nil {
		return errReply("the core's answer is not valid")
	}
	return Reply{Text: scrub("Cancelled: " + strings.Join(out.Cancelled, ", ") + ".")}
}
