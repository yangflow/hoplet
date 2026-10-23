package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
	case ToolSendPrompt:
		return c.sendPrompt(ctx, sessionID, args)
	case ToolInterruptSession:
		return c.interrupt(ctx, sessionID, args)
	case ToolStartSession:
		return c.startSession(ctx, sessionID, args)
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

// refusal turns the core's error answer into text the calling model can act on: the code, the reason and, for a
// rate limit, the seconds to wait.
func refusal(status int, header http.Header, body []byte) string {
	var e struct {
		Code   string `json:"code"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &e)
	if e.Code == "" {
		e.Code = fmt.Sprintf("http-%d", status)
	}
	text := "Hoplet refused the call: " + scrub(e.Code)
	if e.Reason != "" {
		text += " (reason: " + scrub(e.Reason) + ")"
	}
	if ra := header.Get("Retry-After"); ra != "" {
		if n, err := strconv.Atoi(ra); err == nil && n >= 0 {
			text += fmt.Sprintf(" (retry after %d seconds)", n)
		}
	}
	return text
}

// post performs one POST with the session's token; a 401 drops the token and asks for another once. A refusal of
// the core comes back as an error reply that names its code.
func (c *Core) post(ctx context.Context, sessionID, path string, payload any) ([]byte, Reply, bool) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errReply("invalid request"), false
	}
	for attempt := 0; ; attempt++ {
		tok, err := c.tokens.Get(ctx, sessionID)
		if err != nil {
			return nil, errReply(err.Error()), false
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(data))
		if err != nil {
			return nil, errReply("invalid request"), false
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, errReply("the core is not reachable"), false
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			c.tokens.Forget(sessionID)
			continue
		}
		if rerr != nil {
			return nil, errReply("the core's answer could not be read"), false
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			if resp.StatusCode == http.StatusUnauthorized {
				c.tokens.Forget(sessionID)
			}
			return nil, errReply(refusal(resp.StatusCode, resp.Header, body)), false
		}
		return body, Reply{}, true
	}
}

func (c *Core) sendPrompt(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		SessionID string `json:"sessionId"`
		Text      string `json:"text"`
	}
	if json.Unmarshal(args, &a) != nil || !sessionIDPattern.MatchString(a.SessionID) ||
		strings.TrimSpace(a.Text) == "" || utf8.RuneCountInString(a.Text) > maxPromptChars {
		return errReply("invalid arguments: sessionId is a session ID from list_sessions and text is 1 to 8000 characters")
	}
	if a.SessionID == sessionID {
		return errReply("Hoplet refused the call: self (a session cannot send to itself)")
	}
	id, err := queueItemID()
	if err != nil {
		return errReply("the request could not be built")
	}
	path := fmt.Sprintf("/api/sessions/%s/queue", url.PathEscape(a.SessionID))
	if _, rep, ok := c.post(ctx, sessionID, path, map[string]any{"id": id, "text": a.Text}); !ok {
		return rep
	}
	return Reply{Text: "Queued for session " + a.SessionID + "; it runs when that session's current turn ends."}
}

func (c *Core) interrupt(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(args, &a) != nil || !sessionIDPattern.MatchString(a.SessionID) {
		return errReply("invalid arguments: sessionId is a session ID from list_sessions")
	}
	if a.SessionID == sessionID {
		return errReply("Hoplet refused the call: self (a session cannot interrupt itself)")
	}
	path := fmt.Sprintf("/api/sessions/%s/interrupt", url.PathEscape(a.SessionID))
	if _, rep, ok := c.post(ctx, sessionID, path, struct{}{}); !ok {
		return rep
	}
	return Reply{Text: "Interrupted session " + a.SessionID + "."}
}

// queueItemID is the opaque ID of a queued message, which the core takes from the caller.
func queueItemID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mcp-" + hex.EncodeToString(b), nil
}

// startSession starts a session as a child of the caller (ADR 0034): the core checks the grant, the chain, the
// limits and the permission mode; this only turns the tool's arguments into the request of POST /api/sessions.
func (c *Core) startSession(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		Prompt         string `json:"prompt"`
		Machine        string `json:"machine"`
		ProjectPath    string `json:"projectPath"`
		PermissionMode string `json:"permissionMode"`
		Worktree       bool   `json:"worktree"`
		Branch         string `json:"branch"`
	}
	if json.Unmarshal(args, &a) != nil || strings.TrimSpace(a.Prompt) == "" ||
		utf8.RuneCountInString(a.Prompt) > maxPromptChars || utf8.RuneCountInString(a.ProjectPath) > maxPathChars ||
		utf8.RuneCountInString(a.Branch) > maxBranchChars || (a.Branch != "" && !a.Worktree) {
		return errReply("invalid arguments: prompt is 1 to 8000 characters, branch needs worktree")
	}

	body, rep, ok := c.get(ctx, sessionID, "/api/sessions/"+url.PathEscape(sessionID))
	if !ok {
		return rep
	}
	var self struct {
		Provider      string `json:"provider"`
		ProjectPath   string `json:"projectPath"`
		OwnerDeviceID string `json:"ownerDeviceId"`
	}
	if json.Unmarshal(body, &self) != nil || self.Provider == "" || self.OwnerDeviceID == "" {
		return errReply("the core's answer is not valid")
	}

	deviceID, projectPath := self.OwnerDeviceID, self.ProjectPath
	if a.ProjectPath != "" {
		projectPath = a.ProjectPath
	}
	if a.Machine != "" || a.PermissionMode != "" {
		body, rep, ok := c.get(ctx, sessionID, "/api/devices")
		if !ok {
			return rep
		}
		var devs struct {
			Devices []struct {
				DeviceID  string `json:"deviceId"`
				Name      string `json:"name"`
				Providers []struct {
					Provider     string `json:"provider"`
					Capabilities struct {
						Config []struct {
							ID       string `json:"id"`
							Category string `json:"category"`
							Type     string `json:"type"`
							Options  []struct {
								Value string `json:"value"`
							} `json:"options"`
						} `json:"config"`
					} `json:"capabilities"`
				} `json:"providers"`
			} `json:"devices"`
		}
		if json.Unmarshal(body, &devs) != nil {
			return errReply("the core's answer is not valid")
		}
		if a.Machine != "" {
			deviceID = ""
			for _, d := range devs.Devices {
				if d.DeviceID == a.Machine || d.Name == a.Machine {
					deviceID = d.DeviceID
					break
				}
			}
			if deviceID == "" {
				return errReply("Hoplet refused the call: invalid-request (reason: machine; use an ID or name from list_machines)")
			}
			if deviceID != self.OwnerDeviceID && a.ProjectPath == "" {
				return errReply("invalid arguments: projectPath is needed for another machine")
			}
		}
		if a.PermissionMode != "" {
			modeID := ""
			for _, d := range devs.Devices {
				if d.DeviceID != deviceID {
					continue
				}
				for _, p := range d.Providers {
					if p.Provider != self.Provider {
						continue
					}
					for _, o := range p.Capabilities.Config {
						if o.Category != "mode" || o.Type != "select" {
							continue
						}
						for _, v := range o.Options {
							if v.Value == a.PermissionMode {
								modeID = o.ID
							}
						}
					}
				}
			}
			if modeID == "" {
				return errReply("Hoplet refused the call: invalid-request (reason: permissionMode is not offered by that provider)")
			}
			return c.postStart(ctx, sessionID, deviceID, projectPath, self.Provider, a.Prompt, a.Worktree, a.Branch,
				map[string]any{modeID: a.PermissionMode})
		}
	}
	return c.postStart(ctx, sessionID, deviceID, projectPath, self.Provider, a.Prompt, a.Worktree, a.Branch, nil)
}

func (c *Core) postStart(ctx context.Context, sessionID, deviceID, projectPath, provider, prompt string,
	worktree bool, branch string, config map[string]any) Reply {
	id, err := queueItemID()
	if err != nil {
		return errReply("the request could not be built")
	}
	req := map[string]any{
		"deviceId": deviceID, "projectPath": projectPath, "provider": provider, "text": prompt, "promptId": id,
	}
	if config != nil {
		req["config"] = config
	}
	if worktree {
		wt := map[string]any{}
		if branch != "" {
			wt["branch"] = branch
		}
		req["worktree"] = wt
	}
	body, rep, ok := c.post(ctx, sessionID, "/api/sessions", req)
	if !ok {
		return rep
	}
	var out struct {
		SessionID string `json:"sessionId"`
		Worktree  *struct {
			Path   string `json:"path"`
			Branch string `json:"branch"`
		} `json:"worktree"`
	}
	if json.Unmarshal(body, &out) != nil || !sessionIDPattern.MatchString(out.SessionID) {
		return errReply("the core's answer is not valid")
	}
	text := "Started session " + out.SessionID + "; its first prompt is running."
	if out.Worktree != nil {
		text += " Worktree: " + out.Worktree.Path + " (branch " + out.Worktree.Branch + ")."
	}
	return Reply{Text: scrub(text)}
}
