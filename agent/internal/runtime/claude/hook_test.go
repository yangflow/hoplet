package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/rules"
	"github.com/yangflow/hoplet/agent/internal/runtime"
)

// hookScript stands in for claude: it records the initialize line, sends each given hook_callback request and
// records the answers, one per line.
func hookScript(t *testing.T, requests []string) (bin, initFile, replyFile string) {
	t.Helper()
	dir := t.TempDir()
	initFile, replyFile = filepath.Join(dir, "init"), filepath.Join(dir, "replies")
	var sb strings.Builder
	sb.WriteString("read -r init\nprintf '%s\\n' \"$init\" > " + initFile + "\n")
	sb.WriteString(`echo '{"type":"control_response","response":{"subtype":"success","request_id":"hoplet-init","response":{}}}'` + "\n")
	for _, r := range requests {
		sb.WriteString("printf '%s\\n' '" + r + "'\nread -r answer\nprintf '%s\\n' \"$answer\" >> " + replyFile + "\n")
	}
	return script(t, sb.String()), initFile, replyFile
}

func hookRequest(id, callback, tool, input string) string {
	return `{"type":"control_request","request_id":"` + id + `","request":{"subtype":"hook_callback","callback_id":"` + callback +
		`","input":{"hook_event_name":"PreToolUse","tool_name":"` + tool + `","tool_use_id":"toolu_` + id + `","tool_input":` + input + `}}}`
}

// runHooks starts a process under opts and spec, lets it send requests and returns the answers by request ID.
func runHooks(t *testing.T, opts Options, spec runtime.StartSpec, requests []string) (initLine string, answers map[string]map[string]any) {
	t.Helper()
	initLine, answers, _ = runHooksWithEvents(t, opts, spec, requests)
	return initLine, answers
}

// runHooksWithEvents is runHooks that also returns the events the process emitted.
func runHooksWithEvents(t *testing.T, opts Options, spec runtime.StartSpec, requests []string) (initLine string, answers map[string]map[string]any, events []runtime.ProcessEvent) {
	t.Helper()
	bin, initFile, replyFile := hookScript(t, requests)
	opts.Bin = bin
	if spec.Dir == "" {
		spec.Dir = t.TempDir()
	}
	spec.SessionID = "s"
	p, err := New(opts).Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	events = collect(t, p)
	raw, _ := os.ReadFile(initFile)
	initLine = string(raw)
	answers = map[string]map[string]any{}
	data, _ := os.ReadFile(replyFile)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var m struct {
			Response struct {
				Subtype   string         `json:"subtype"`
				RequestID string         `json:"request_id"`
				Response  map[string]any `json:"response"`
				Error     string         `json:"error"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("answer %q: %v", line, err)
		}
		out := m.Response.Response
		if m.Response.Subtype != "success" {
			out = map[string]any{"error": m.Response.Error}
		}
		answers[m.Response.RequestID] = out
	}
	return initLine, answers, events
}

func decisionOf(t *testing.T, out map[string]any) (string, string) {
	t.Helper()
	h, ok := out["hookSpecificOutput"].(map[string]any)
	if !ok {
		return "", ""
	}
	if h["hookEventName"] != "PreToolUse" {
		t.Fatalf("output = %v", out)
	}
	d, _ := h["permissionDecision"].(string)
	r, _ := h["permissionDecisionReason"].(string)
	return d, r
}

func allowStatus() Options {
	return Options{Rules: func(runtime.StartSpec) (*rules.Engine, error) {
		return rules.New(rules.NewEnv("/home/u", "/home/u/.hoplet"), nil, append(rules.Defaults(), rules.Rule{
			ID: "user.status", Tool: "run", Match: rules.Match{Command: "git status"}, Action: rules.Allow,
			Scope: rules.ScopeGlobal, Note: "git status", Enabled: true,
		}))
	}}
}

func TestInitializeRegistersThePreToolUseHook(t *testing.T) {
	line, _ := runHooks(t, Options{}, runtime.StartSpec{}, nil)
	var m struct {
		Request struct {
			Subtype string `json:"subtype"`
			Hooks   struct {
				PreToolUse []struct {
					Matcher         string   `json:"matcher"`
					HookCallbackIDs []string `json:"hookCallbackIds"`
					Timeout         int      `json:"timeout"`
				} `json:"PreToolUse"`
			} `json:"hooks"`
		} `json:"request"`
	}
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("initialize = %q: %v", line, err)
	}
	h := m.Request.Hooks.PreToolUse
	if m.Request.Subtype != "initialize" || len(h) != 1 || h[0].Matcher != "" ||
		len(h[0].HookCallbackIDs) != 1 || h[0].HookCallbackIDs[0] != hookCallbackID || h[0].Timeout < 600 {
		t.Fatalf("initialize = %s", line)
	}
}

func TestHookCallbacksAreDecidedByTheRules(t *testing.T) {
	dir := t.TempDir()
	reqs := []string{
		hookRequest("h1", hookCallbackID, "Read", `{"file_path":"`+dir+`/.env"}`),
		hookRequest("h2", hookCallbackID, "Bash", `{"command":"git push --force origin main"}`),
		hookRequest("h3", hookCallbackID, "Bash", `{"command":"git status"}`),
		hookRequest("h4", hookCallbackID, "Read", `{"file_path":"`+dir+`/main.go"}`),
		hookRequest("h5", hookCallbackID, "Bash", `{"command":"git status; rm -rf x"}`),
		hookRequest("h6", hookCallbackID, "Write", `{"file_path":"/etc/hosts"}`),
	}
	_, got := runHooks(t, allowStatus(), runtime.StartSpec{Dir: dir}, reqs)
	// h5 has shell control: no allow, and the recursive rm asks.
	for id, want := range map[string]string{"h1": "deny", "h2": "ask", "h3": "allow", "h4": "", "h5": "ask", "h6": "deny"} {
		d, reason := decisionOf(t, got[id])
		if d != want {
			t.Fatalf("%s: decision %q (%v), want %q", id, d, got[id], want)
		}
		if want != "" && reason == "" {
			t.Fatalf("%s: no reason", id)
		}
	}
	if _, reason := decisionOf(t, got["h1"]); !strings.Contains(reason, "Secret files") {
		t.Fatalf("the deny names the rule: %q", reason)
	}
	if len(got["h4"]) != 0 {
		t.Fatalf("no match is no decision: %v", got["h4"])
	}
}

func TestHookAllowIsWithheldInPlanMode(t *testing.T) {
	reqs := []string{hookRequest("h1", hookCallbackID, "Bash", `{"command":"git status"}`)}
	_, got := runHooks(t, allowStatus(), runtime.StartSpec{Config: map[string]any{"mode": "plan"}}, reqs)
	if len(got["h1"]) != 0 {
		t.Fatalf("plan mode: %v", got["h1"])
	}
}

func TestHookDenyHoldsInBypassMode(t *testing.T) {
	dir := t.TempDir()
	reqs := []string{hookRequest("h1", hookCallbackID, "Edit", `{"file_path":"`+dir+`/.claude/settings.json"}`)}
	_, got := runHooks(t, Options{}, runtime.StartSpec{Dir: dir, Config: map[string]any{"mode": "bypassPermissions"}}, reqs)
	if d, _ := decisionOf(t, got["h1"]); d != "deny" {
		t.Fatalf("answer = %v", got["h1"])
	}
}

func TestHookEdgeCasesFailClosed(t *testing.T) {
	reqs := []string{
		hookRequest("h1", "someone-elses", "Bash", `{"command":"ls"}`),
		hookRequest("h2", hookCallbackID, "Bash", `"not an object"`),
		`{"type":"control_request","request_id":"h3","request":{"subtype":"hook_callback","callback_id":"` + hookCallbackID + `","input":"junk"}}`,
		`{"type":"control_request","request_id":"h4","request":{"subtype":"hook_callback","callback_id":"` + hookCallbackID + `"}}`,
		hookRequest("h5", hookCallbackID, "FutureTool", `{"x":1}`),
	}
	_, got := runHooks(t, Options{}, runtime.StartSpec{}, reqs)
	if e, _ := got["h1"]["error"].(string); !strings.Contains(e, "unsupported") {
		t.Fatalf("an unknown callback id gets an error: %v", got["h1"])
	}
	for _, id := range []string{"h2", "h3", "h4"} {
		if d, _ := decisionOf(t, got[id]); d != "deny" {
			t.Fatalf("%s: unreadable input must deny: %v", id, got[id])
		}
	}
	// A tool the mapper does not know is of kind other: no rule but `*` matches, so no decision.
	if d, _ := decisionOf(t, got["h5"]); d != "" {
		t.Fatalf("unknown tool: %v", got["h5"])
	}
}

// A rule set pushed while a process runs holds for its next tool call, and the source is given the session.
func TestEachCallIsDecidedWithTheRulesInForceNow(t *testing.T) {
	env := rules.NewEnv("/home/u", "/home/u/.hoplet")
	deny := rules.Rule{ID: "user.no-ls", Tool: "run", Match: rules.Match{Command: "ls"}, Action: rules.Deny,
		Scope: rules.ScopeSession, Note: "no ls", Enabled: true}
	var calls int
	var seen []runtime.StartSpec
	opts := Options{Rules: func(spec runtime.StartSpec) (*rules.Engine, error) {
		calls++
		seen = append(seen, spec)
		if calls <= 1 { // the engine of the start
			return rules.New(env, nil, rules.Defaults())
		}
		return rules.New(env, nil, append(rules.Defaults(), deny))
	}}
	dir := t.TempDir()
	_, got := runHooks(t, opts, runtime.StartSpec{Dir: dir}, []string{
		hookRequest("h1", hookCallbackID, "Bash", `{"command":"ls"}`),
	})
	if d, _ := decisionOf(t, got["h1"]); d != "deny" {
		t.Fatalf("the rule pushed after the start did not hold: %v", got["h1"])
	}
	for _, s := range seen {
		if s.SessionID != "s" || s.Dir != dir {
			t.Fatalf("the source got %+v", s)
		}
	}
}

func TestStartFailsWhenTheRulesCannotBeBuilt(t *testing.T) {
	d := New(Options{Bin: script(t, "exit 0\n"), Rules: func(runtime.StartSpec) (*rules.Engine, error) { return nil, os.ErrInvalid }})
	if _, err := d.Start(context.Background(), runtime.StartSpec{SessionID: "s", Dir: t.TempDir()}); err == nil {
		t.Fatal("a session started without its rules")
	}
}

func ruleDecisions(events []runtime.ProcessEvent) map[string]runtime.RuleDecided {
	out := map[string]runtime.RuleDecided{}
	for _, ev := range events {
		if d, ok := ev.(runtime.RuleDecided); ok {
			out[d.ToolUseID] = d
		}
	}
	return out
}

// A decision a rule made is reported with the rule, its scope and the revision of the set; a call no rule decided,
// a plan-mode call and an unreadable input are not.
func TestHookReportsTheCallsARuleDecided(t *testing.T) {
	dir := t.TempDir()
	reqs := []string{
		hookRequest("h1", hookCallbackID, "Read", `{"file_path":"`+dir+`/.env"}`),
		hookRequest("h2", hookCallbackID, "Bash", `{"command":"git status"}`),
		hookRequest("h3", hookCallbackID, "Read", `{"file_path":"`+dir+`/main.go"}`),
		hookRequest("h4", hookCallbackID, "Bash", `{"command":"git push --force origin main"}`),
		hookRequest("h5", hookCallbackID, "Bash", `"not an object"`),
	}
	opts := Options{Rules: func(runtime.StartSpec) (*rules.Engine, error) {
		return rules.New(rules.NewEnv("/home/u", "/home/u/.hoplet"), nil, append(rules.Defaults(), rules.Rule{
			ID: "user.status", Tool: "run", Match: rules.Match{Command: "git status"}, Action: rules.Allow,
			Scope: rules.ScopeProject, Note: "git status", Enabled: true,
		}))
	}}
	_, _, events := runHooksWithEvents(t, opts, runtime.StartSpec{Dir: dir}, reqs)
	got := ruleDecisions(events)
	if len(got) != 3 {
		t.Fatalf("decisions = %+v", got)
	}
	if d := got["toolu_h1"]; d.Decision != "deny" || d.RuleID == "" || d.Scope == "" {
		t.Fatalf("h1 = %+v", d)
	}
	if d := got["toolu_h2"]; d != (runtime.RuleDecided{ToolUseID: "toolu_h2", Decision: "allow", RuleID: "user.status", Scope: "project"}) {
		t.Fatalf("h2 = %+v", d)
	}
	if d := got["toolu_h4"]; d.Decision != "ask" {
		t.Fatalf("h4 = %+v", d)
	}
	for _, id := range []string{"toolu_h3", "toolu_h5"} {
		if _, ok := got[id]; ok {
			t.Fatalf("%s: no rule decided, yet an event was emitted", id)
		}
	}
}

func TestHookReportsNothingWhenThePlanModeDecides(t *testing.T) {
	reqs := []string{hookRequest("h1", hookCallbackID, "Bash", `{"command":"git status"}`)}
	_, _, events := runHooksWithEvents(t, allowStatus(), runtime.StartSpec{Config: map[string]any{"mode": "plan"}}, reqs)
	if got := ruleDecisions(events); len(got) != 0 {
		t.Fatalf("decisions = %+v", got)
	}
}
