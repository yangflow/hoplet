package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The write tools of ADR 0036: a session proposes a change to its own configuration and the user approves or
// declines it in Hoplet. They are projections of POST /api/sessions/:id/config-changes, which holds the checks
// (the capability, the setting, the declared options, the staleness and the content checks); nothing is written
// before the user approves. get_change reads the state of a proposal.

const (
	// maxChangeObjects, maxChangeBytes and maxChangeValueChars are the limits of shared/src/rest/config-changes.ts.
	maxChangeObjects    = 5
	maxChangeBytes      = 64 << 10
	maxChangeValueChars = 128
	changeLimits        = "one open request for this session at a time, at most 5 objects and 64 KiB of content, and it expires after 24 hours"
)

var (
	baseHashPattern = regexp.MustCompile(`^([0-9a-f]{64})?$`)
	changeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// changeObject is one object of a proposal as the core takes it; Content nil deletes the file.
type changeObject struct {
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	BaseHash *string `json:"baseHash,omitempty"`
	Content  *string `json:"content"`
}

// reasonOf trims and checks the reason every proposal carries.
func reasonOf(s string) (string, bool) {
	r := strings.TrimSpace(s)
	return r, r != "" && utf8.RuneCountInString(r) <= maxReasonChars
}

func (c *Core) proposeConfigChange(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		Model  *string `json:"model"`
		Effort *string `json:"effort"`
		Reason string  `json:"reason"`
	}
	const usage = "invalid arguments: give model, effort or both as a value get_config lists, and a reason of 1 to 500 characters"
	if json.Unmarshal(args, &a) != nil || (a.Model == nil && a.Effort == nil) {
		return errReply(usage)
	}
	reason, ok := reasonOf(a.Reason)
	if !ok {
		return errReply(usage)
	}
	var objects []changeObject
	for _, o := range []struct {
		name  string
		value *string
	}{{"model", a.Model}, {"effort", a.Effort}} {
		if o.value == nil {
			continue
		}
		v := strings.TrimSpace(*o.value)
		if v == "" || utf8.RuneCountInString(v) > maxChangeValueChars {
			return errReply(usage)
		}
		objects = append(objects, changeObject{Kind: "config", Name: o.name, Content: &v})
	}
	return c.propose(ctx, sessionID, reason, objects)
}

func (c *Core) proposeMemoryChange(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		Content  *string `json:"content"`
		BaseHash string  `json:"baseHash"`
		Reason   string  `json:"reason"`
		Scope    string  `json:"scope"`
	}
	if json.Unmarshal(args, &a) != nil || a.Content == nil {
		return errReply("invalid arguments: content is the whole new memory file and reason is 1 to 500 characters")
	}
	if a.Scope != "" && a.Scope != "project" {
		return errReply("invalid arguments: scope is project; only the project's memory file can be proposed, the user changes their own files themselves")
	}
	if !baseHashPattern.MatchString(a.BaseHash) {
		return errReply("invalid arguments: baseHash is the hash read_memory returned, or empty for a file that does not exist")
	}
	reason, ok := reasonOf(a.Reason)
	if !ok {
		return errReply("invalid arguments: reason is 1 to 500 characters")
	}
	if len(*a.Content) > maxChangeBytes {
		return errReply("Hoplet refused the call: too-large (the content is at most 64 KiB)")
	}
	base := a.BaseHash
	return c.propose(ctx, sessionID, reason, []changeObject{{Kind: "memory", Name: "project", BaseHash: &base, Content: a.Content}})
}

func (c *Core) proposeSkillChange(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		Changes []struct {
			Name     string  `json:"name"`
			Kind     string  `json:"kind"`
			Content  *string `json:"content"`
			Delete   bool    `json:"delete"`
			BaseHash string  `json:"baseHash"`
			Scope    string  `json:"scope"`
		} `json:"changes"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(args, &a) != nil || len(a.Changes) == 0 || len(a.Changes) > maxChangeObjects {
		return errReply("invalid arguments: changes lists 1 to 5 skills or commands and reason is 1 to 500 characters")
	}
	reason, ok := reasonOf(a.Reason)
	if !ok {
		return errReply("invalid arguments: reason is 1 to 500 characters")
	}
	seen := map[string]bool{}
	total := 0
	objects := make([]changeObject, 0, len(a.Changes))
	for _, ch := range a.Changes {
		kind := ch.Kind
		if kind == "" {
			kind = "skill"
		}
		if kind != "skill" && kind != "command" {
			return errReply("invalid arguments: kind is skill or command")
		}
		if ch.Scope != "" && ch.Scope != "project" {
			return errReply("invalid arguments: scope is project; only the project's skills and commands can be proposed, the user changes their own files themselves")
		}
		if !configNamePattern.MatchString(ch.Name) {
			return errReply("invalid arguments: name is lower-case letters, digits and hyphens, at most 64 characters")
		}
		if seen[kind+":"+ch.Name] {
			return errReply("invalid arguments: " + kind + " " + ch.Name + " appears twice")
		}
		seen[kind+":"+ch.Name] = true
		if !baseHashPattern.MatchString(ch.BaseHash) {
			return errReply("invalid arguments: baseHash is the hash read_skill returned, or empty for a new one")
		}
		if ch.Delete == (ch.Content != nil) {
			return errReply("invalid arguments: each change has either content (the whole new file) or delete true")
		}
		if ch.Content != nil {
			total += len(*ch.Content)
		}
		base := ch.BaseHash
		objects = append(objects, changeObject{Kind: kind, Name: ch.Name, BaseHash: &base, Content: ch.Content})
	}
	if total > maxChangeBytes {
		return errReply("Hoplet refused the call: too-large (the content is at most 64 KiB in all)")
	}
	return c.propose(ctx, sessionID, reason, objects)
}

// changeView is what a tool says about a proposal: never its content or its diff, which the session wrote itself.
type changeView struct {
	ChangeID    string             `json:"changeId"`
	State       string             `json:"state"`
	Reason      string             `json:"reason"`
	Objects     []changeObjectView `json:"objects"`
	CreatedAt   int64              `json:"createdAt"`
	ExpiresAt   int64              `json:"expiresAt"`
	DecidedAt   *int64             `json:"decidedAt"`
	AppliedAt   *int64             `json:"appliedAt"`
	UndoneAt    *int64             `json:"undoneAt"`
	Error       string             `json:"error,omitempty"`
	ErrorReason string             `json:"errorReason,omitempty"`
}

type changeObjectView struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	TakesEffect string `json:"takesEffect"`
}

// takesEffectText says when an applied object reaches this session.
func takesEffectText(o changeObjectView) string {
	what := "The " + o.Kind + " " + o.Name
	switch {
	case o.Kind == "memory":
		what = "The memory file"
	case o.Kind == "config":
		what = "The " + o.Name
	}
	switch o.TakesEffect {
	case "now":
		return what + " takes effect in this session once applied."
	case "next-turn":
		return what + " takes effect from this session's next turn once applied."
	}
	if o.Kind == "memory" {
		return what + " takes effect when this session next starts; the current conversation keeps the old instructions."
	}
	return what + " takes effect when this session next starts; do not assume this session sees it before."
}

func (c *Core) propose(ctx context.Context, sessionID, reason string, objects []changeObject) Reply {
	path := "/api/sessions/" + url.PathEscape(sessionID) + "/config-changes"
	body, rep, ok := c.post(ctx, sessionID, path, map[string]any{"reason": reason, "objects": objects})
	if !ok {
		return rep
	}
	var out struct {
		ChangeID string             `json:"changeId"`
		State    string             `json:"state"`
		Objects  []changeObjectView `json:"objects"`
	}
	if json.Unmarshal(body, &out) != nil || !changeIDPattern.MatchString(out.ChangeID) {
		return errReply("the core's answer is not valid")
	}
	text := "Change " + out.ChangeID + " is waiting for the user's answer in Hoplet; nothing is written until they approve it, and it expires in 24 hours. Use get_change for its state and do not propose it again."
	for _, o := range out.Objects {
		text += " " + takesEffectText(o)
	}
	return finishWith(scrub(text), out)
}

func (c *Core) getChange(ctx context.Context, sessionID string, args json.RawMessage) Reply {
	var a struct {
		ChangeID string `json:"changeId"`
	}
	if len(args) > 0 && json.Unmarshal(args, &a) != nil || (a.ChangeID != "" && !changeIDPattern.MatchString(a.ChangeID)) {
		return errReply("invalid arguments: changeId is the ID a propose tool returned")
	}
	var raw []byte
	if a.ChangeID != "" {
		body, rep, ok := c.get(ctx, sessionID, "/api/config-changes/"+url.PathEscape(a.ChangeID))
		if !ok {
			return rep
		}
		raw = body
	} else {
		body, rep, ok := c.get(ctx, sessionID, "/api/sessions/"+url.PathEscape(sessionID)+"/config-changes")
		if !ok {
			return rep
		}
		var list struct {
			Changes []json.RawMessage `json:"changes"`
		}
		if json.Unmarshal(body, &list) != nil {
			return errReply("the core's answer is not valid")
		}
		if len(list.Changes) == 0 {
			return Reply{Text: "This session has proposed no change."}
		}
		raw = list.Changes[0]
	}
	var view changeView
	if json.Unmarshal(raw, &view) != nil || view.ChangeID == "" {
		return errReply("the core's answer is not valid")
	}
	return finishWith(changeStateText(view), view)
}

// changeStateText heads get_change's result: what the state means for this session.
func changeStateText(v changeView) string {
	switch v.State {
	case "pending":
		return "Change " + v.ChangeID + " is waiting for the user's answer in Hoplet."
	case "approved":
		return "Change " + v.ChangeID + " was approved and is being written."
	case "applied":
		text := "Change " + v.ChangeID + " was applied."
		for _, o := range v.Objects {
			text += " " + takesEffectText(o)
		}
		return text
	case "declined":
		return "Change " + v.ChangeID + " was declined by the user; do not propose it again unless they ask."
	case "expired":
		return "Change " + v.ChangeID + " expired without an answer."
	case "failed":
		return "Change " + v.ChangeID + " failed; error and errorReason name why."
	case "undone":
		return "Change " + v.ChangeID + " was applied and then undone by the user."
	}
	return "Change " + v.ChangeID + " is " + v.State + "."
}
