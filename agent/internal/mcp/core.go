package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Core calls the core's REST API for the sessions of this machine with the session's own token (ADR 0034). Every
// check of ADR 0033 (the grant, the allow list, the rate limit, the audit) happens in the core; the tools here are
// a projection of its routes.
type Core struct {
	base   string
	http   *http.Client
	tokens *Tokens
}

// NewCore builds the client for the core at serverURL.
func NewCore(serverURL string, tokens *Tokens) *Core {
	return &Core{base: strings.TrimRight(serverURL, "/"), http: &http.Client{Timeout: 30 * time.Second}, tokens: tokens}
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Call runs one tool for the session that owns the nonce. It never returns a request or a token in an error.
func (c *Core) Call(ctx context.Context, sessionID, tool string, args json.RawMessage) Reply {
	switch tool {
	case ToolListMachines:
		return c.listMachines(ctx, sessionID)
	case ToolListSessions:
		return c.listSessions(ctx, sessionID, args)
	case ToolReadSession:
		return c.readSession(ctx, sessionID, args)
	}
	return errReply("unknown tool")
}

// get performs one GET with the session's token; a 401 drops the token and asks for another once.
func (c *Core) get(ctx context.Context, sessionID, path string) ([]byte, Reply, bool) {
	for attempt := 0; ; attempt++ {
		tok, err := c.tokens.Get(ctx, sessionID)
		if err != nil {
			return nil, errReply(err.Error()), false
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
		if err != nil {
			return nil, errReply("invalid request"), false
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, errReply("the core is not reachable"), false
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			c.tokens.Forget(sessionID)
			continue
		}
		if rerr != nil {
			return nil, errReply("the core's answer could not be read"), false
		}
		if resp.StatusCode != http.StatusOK {
			var e struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(body, &e)
			if e.Code == "" {
				e.Code = fmt.Sprintf("http-%d", resp.StatusCode)
			}
			if resp.StatusCode == http.StatusUnauthorized {
				c.tokens.Forget(sessionID)
			}
			return nil, errReply("the core refused the call: " + scrub(e.Code)), false
		}
		return body, Reply{}, true
	}
}

func finish(v any) Reply {
	b, err := json.Marshal(v)
	if err != nil {
		return errReply("the result could not be built")
	}
	text := dataNotice + "\n" + scrub(string(b))
	if len(text) > MaxResultBytes {
		text = cut(text, MaxResultBytes)
	}
	return Reply{Text: text}
}

// cut shortens s to at most n bytes on a rune boundary.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (c *Core) listMachines(ctx context.Context, sessionID string) Reply {
	body, rep, ok := c.get(ctx, sessionID, "/api/devices")
	if !ok {
		return rep
	}
	var in struct {
		Devices []struct {
			DeviceID  string `json:"deviceId"`
			Name      string `json:"name"`
			Connected bool   `json:"connected"`
			Providers []struct {
				Provider string `json:"provider"`
			} `json:"providers"`
		} `json:"devices"`
	}
	if json.Unmarshal(body, &in) != nil {
		return errReply("the core's answer is not valid")
	}
	type machine struct {
		DeviceID  string   `json:"deviceId"`
		Name      string   `json:"name"`
		Connected bool     `json:"connected"`
		Providers []string `json:"providers"`
	}
	out := make([]machine, 0, len(in.Devices))
	for _, d := range in.Devices {
		m := machine{DeviceID: d.DeviceID, Name: d.Name, Connected: d.Connected, Providers: []string{}}
		for _, p := range d.Providers {
			m.Providers = append(m.Providers, p.Provider)
		}
		out = append(out, m)
	}
	return finish(map[string]any{"machines": out})
}

func (c *Core) listSessions(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		Limit int `json:"limit"`
	}
	if len(args) > 0 && json.Unmarshal(args, &a) != nil {
		return errReply("invalid arguments")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	body, rep, ok := c.get(ctx, sessionID, "/api/sessions")
	if !ok {
		return rep
	}
	var in struct {
		Sessions []struct {
			SessionID      string `json:"sessionId"`
			Title          string `json:"title"`
			Provider       string `json:"provider"`
			ProjectPath    string `json:"projectPath"`
			DeviceName     string `json:"deviceName"`
			OwnerDeviceID  string `json:"ownerDeviceId"`
			GitBranch      string `json:"gitBranch"`
			UpdatedAt      int64  `json:"updatedAt"`
			OwnerConnected bool   `json:"ownerConnected"`
			Self           bool   `json:"self"`
			Runtime        struct {
				State string `json:"state"`
			} `json:"runtime"`
		} `json:"sessions"`
	}
	if json.Unmarshal(body, &in) != nil {
		return errReply("the core's answer is not valid")
	}
	type item struct {
		SessionID   string `json:"sessionId"`
		Title       string `json:"title"`
		Provider    string `json:"provider"`
		ProjectPath string `json:"projectPath"`
		Machine     string `json:"machine"`
		DeviceID    string `json:"deviceId,omitempty"`
		Branch      string `json:"branch,omitempty"`
		State       string `json:"state"`
		UpdatedAt   int64  `json:"updatedAt"`
		Self        bool   `json:"self,omitempty"`
	}
	out := []item{}
	for _, s := range in.Sessions {
		if len(out) == limit {
			break
		}
		out = append(out, item{
			SessionID: s.SessionID, Title: s.Title, Provider: s.Provider, ProjectPath: s.ProjectPath,
			Machine: s.DeviceName, DeviceID: s.OwnerDeviceID, Branch: s.GitBranch, State: s.Runtime.State,
			UpdatedAt: s.UpdatedAt, Self: s.Self,
		})
	}
	return finish(map[string]any{"sessions": out, "total": len(in.Sessions)})
}

func (c *Core) readSession(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		SessionID string `json:"sessionId"`
		Start     *int   `json:"start"`
		Limit     int    `json:"limit"`
	}
	if json.Unmarshal(args, &a) != nil || !sessionIDPattern.MatchString(a.SessionID) {
		return errReply("invalid arguments: sessionId is a session ID from list_sessions")
	}
	start := -20
	if a.Start != nil {
		start = *a.Start
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > maxPageLines {
		limit = maxPageLines
	}
	path := fmt.Sprintf("/api/sessions/%s/lines?%s", url.PathEscape(a.SessionID),
		url.Values{"start": {fmt.Sprint(start)}, "limit": {fmt.Sprint(limit)}}.Encode())
	body, rep, ok := c.get(ctx, sessionID, path)
	if !ok {
		return rep
	}
	var page struct {
		SessionID string            `json:"sessionId"`
		Start     int               `json:"start"`
		End       int               `json:"end"`
		Total     int               `json:"total"`
		Entries   []json.RawMessage `json:"entries"`
	}
	if json.Unmarshal(body, &page) != nil {
		return errReply("the core's answer is not valid")
	}
	return finish(fitPage(page.SessionID, page.Start, page.End, page.Total, page.Entries))
}

// pageBudget leaves room for the notice and the page's own fields inside MaxResultBytes.
const pageBudget = MaxResultBytes - 1024

// fitPage keeps the entries that fit in one result. When the first one alone is too large its text is cut and
// marked. `next` is the line to continue from, absent at the end of the session.
func fitPage(id string, start, end, total int, entries []json.RawMessage) map[string]any {
	kept := make([]json.RawMessage, 0, len(entries))
	used := 0
	next := end
	truncated := false
	for i, e := range entries {
		e = json.RawMessage(scrub(string(e)))
		if used+len(e)+1 > pageBudget {
			if i == 0 {
				var head struct {
					LineNo int `json:"lineNo"`
				}
				_ = json.Unmarshal(e, &head)
				cutText, _ := json.Marshal(cut(string(e), pageBudget/2))
				e, _ = json.Marshal(map[string]any{"lineNo": head.LineNo, "truncated": true, "text": json.RawMessage(cutText)})
				kept, used, truncated = append(kept, e), used+len(e)+1, true
				next = head.LineNo + 1
				continue
			}
			var head struct {
				LineNo int `json:"lineNo"`
			}
			_ = json.Unmarshal(entries[i], &head)
			next, truncated = head.LineNo, true
			break
		}
		kept = append(kept, e)
		used += len(e) + 1
	}
	out := map[string]any{"sessionId": id, "start": start, "end": end, "total": total, "entries": kept}
	if truncated {
		out["cut"] = true
	}
	if next < total {
		out["next"] = next
	}
	return out
}
