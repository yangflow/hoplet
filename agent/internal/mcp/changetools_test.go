package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// changeCore answers every route with the status and body it is told to and records each request with its body.
func changeCore(t *testing.T, status int, body string) (*Core, *[]string, *[]string) {
	t.Helper()
	f := newFakeCore(t)
	var seen, bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewCore(srv.URL, f.tokens), &seen, &bodies
}

const hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func pendingChange(objects string) string {
	return `{"changeId":"c1","sessionId":"s1","state":"pending","reason":"r","objects":[` + objects + `],"createdAt":1,"expiresAt":2,"decidedAt":null,"appliedAt":null,"undoneAt":null,"undoable":false}`
}

func TestTheWriteToolsValidateBeforeAnyRequest(t *testing.T) {
	c, seen, _ := changeCore(t, 201, pendingChange(""))
	big := strings.Repeat("a", maxChangeBytes+1)
	half := strings.Repeat("a", maxChangeBytes/2+1)
	bad := map[string][]string{
		ToolProposeConfigChange: {``, `{}`, `{"reason":"r"}`, `{"model":"opus"}`, `{"model":"opus","reason":"  "}`,
			`{"model":"","reason":"r"}`, `{"effort":"` + strings.Repeat("a", 129) + `","reason":"r"}`,
			`{"model":"opus","reason":"` + strings.Repeat("r", 501) + `"}`},
		ToolProposeMemoryChange: {``, `{"reason":"r"}`, `{"content":"x"}`, `{"content":"x","reason":"r","scope":"user"}`,
			`{"content":"x","reason":"r","baseHash":"abc"}`, `{"content":"` + big + `","reason":"r"}`},
		ToolProposeSkillChange: {``, `{"reason":"r"}`, `{"changes":[],"reason":"r"}`,
			`{"changes":[{"name":"a","content":"x"}]}`,
			`{"changes":[{"name":"../a","content":"x"}],"reason":"r"}`,
			`{"changes":[{"name":"a","kind":"memory","content":"x"}],"reason":"r"}`,
			`{"changes":[{"name":"a","scope":"user","content":"x"}],"reason":"r"}`,
			`{"changes":[{"name":"a"}],"reason":"r"}`,
			`{"changes":[{"name":"a","content":"x","delete":true}],"reason":"r"}`,
			`{"changes":[{"name":"a","content":"x","baseHash":"zz"}],"reason":"r"}`,
			`{"changes":[{"name":"a","content":"x"},{"name":"a","kind":"skill","content":"y"}],"reason":"r"}`,
			`{"changes":[{"name":"a","content":"` + half + `"},{"name":"b","content":"` + half + `"}],"reason":"r"}`,
			`{"changes":[{"name":"a","delete":true},{"name":"b","delete":true},{"name":"c","delete":true},{"name":"d","delete":true},{"name":"e","delete":true},{"name":"f","delete":true}],"reason":"r"}`},
		ToolGetChange: {`{"changeId":"../x"}`, `[]`},
	}
	for tool, cases := range bad {
		for _, args := range cases {
			if rep := c.Call(context.Background(), "s1", tool, json.RawMessage(args)); !rep.IsError {
				t.Fatalf("%s %.80q must be refused: %+v", tool, args, rep)
			}
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("a refused call reached the core: %v", *seen)
	}
}

func TestProposeConfigChangeSendsConfigObjectsAndSaysTheNextTurn(t *testing.T) {
	c, seen, bodies := changeCore(t, 201, pendingChange(`{"kind":"config","name":"model","takesEffect":"next-turn","content":"opus","diff":"-a\n+b"}`))
	rep := c.Call(context.Background(), "s1", ToolProposeConfigChange, json.RawMessage(`{"model":" opus ","reason":" faster "}`))
	if rep.IsError || !strings.Contains(rep.Text, "Change c1 is waiting for the user's answer") ||
		!strings.Contains(rep.Text, "The model takes effect from this session's next turn once applied") || !strings.Contains(rep.Text, `"takesEffect":"next-turn"`) {
		t.Fatalf("propose_config_change = %+v", rep)
	}
	if strings.Contains(rep.Text, "-a") {
		t.Fatalf("the diff must not be echoed: %q", rep.Text)
	}
	if (*seen)[0] != "POST /api/sessions/s1/config-changes" {
		t.Fatalf("requests = %v", *seen)
	}
	var sent struct {
		Reason  string           `json:"reason"`
		Objects []map[string]any `json:"objects"`
	}
	if err := json.Unmarshal([]byte((*bodies)[0]), &sent); err != nil || sent.Reason != "faster" || len(sent.Objects) != 1 ||
		sent.Objects[0]["kind"] != "config" || sent.Objects[0]["name"] != "model" || sent.Objects[0]["content"] != "opus" {
		t.Fatalf("body = %s", (*bodies)[0])
	}
	if _, has := sent.Objects[0]["baseHash"]; has {
		t.Fatalf("a config object has no base: %s", (*bodies)[0])
	}
}

func TestProposeMemoryChangeSaysTheConversationKeepsTheOldInstructions(t *testing.T) {
	c, _, bodies := changeCore(t, 201, pendingChange(`{"kind":"memory","name":"project","takesEffect":"next-start"}`))
	rep := c.Call(context.Background(), "s1", ToolProposeMemoryChange, json.RawMessage(`{"content":"# Rules\n","baseHash":"`+hashA+`","reason":"r"}`))
	if rep.IsError || !strings.Contains(rep.Text, "The memory file takes effect when this session next starts; the current conversation keeps the old instructions") {
		t.Fatalf("propose_memory_change = %+v", rep)
	}
	if !strings.Contains((*bodies)[0], `{"kind":"memory","name":"project","baseHash":"`+hashA+`","content":"# Rules\n"}`) {
		t.Fatalf("body = %s", (*bodies)[0])
	}
}

func TestProposeSkillChangeSendsEachChangeAndDeletes(t *testing.T) {
	c, _, bodies := changeCore(t, 201, pendingChange(`{"kind":"skill","name":"review","takesEffect":"next-start"},{"kind":"command","name":"fix","takesEffect":"now"}`))
	rep := c.Call(context.Background(), "s1", ToolProposeSkillChange, json.RawMessage(
		`{"changes":[{"name":"review","content":"---\nname: review\n---\n"},{"name":"fix","kind":"command","delete":true,"baseHash":"`+hashA+`"}],"reason":"r"}`))
	if rep.IsError || !strings.Contains(rep.Text, "The skill review takes effect when this session next starts; do not assume this session sees it before") ||
		!strings.Contains(rep.Text, "The command fix takes effect in this session once applied") {
		t.Fatalf("propose_skill_change = %+v", rep)
	}
	body := (*bodies)[0]
	if !strings.Contains(body, `{"kind":"skill","name":"review","baseHash":"","content":"---\nname: review\n---\n"}`) ||
		!strings.Contains(body, `{"kind":"command","name":"fix","baseHash":"`+hashA+`","content":null}`) {
		t.Fatalf("body = %s", body)
	}
}

func TestAProposalRefusalNamesTheCodeAndTheReason(t *testing.T) {
	c, _, _ := changeCore(t, 403, `{"code":"forbidden","reason":"not-allowed"}`)
	rep := c.Call(context.Background(), "s1", ToolProposeConfigChange, json.RawMessage(`{"effort":"high","reason":"r"}`))
	if !rep.IsError || rep.Text != "Hoplet refused the call: forbidden (reason: not-allowed)" {
		t.Fatalf("refusal = %+v", rep)
	}
}

// A Codex session has no command files: its agent refuses the preview, and the tool relays the code and reason.
func TestACodexCommandProposalRelaysUnsupported(t *testing.T) {
	c, _, bodies := changeCore(t, 501, `{"code":"unsupported","reason":"Codex has no command files"}`)
	rep := c.Call(context.Background(), "s1", ToolProposeSkillChange, json.RawMessage(
		`{"changes":[{"name":"ship","kind":"command","content":"Ship.\n"}],"reason":"r"}`))
	if !rep.IsError || rep.Text != "Hoplet refused the call: unsupported (reason: Codex has no command files)" {
		t.Fatalf("refusal = %+v", rep)
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], `"kind":"command"`) {
		t.Fatalf("bodies = %v", *bodies)
	}
}

func TestTheConfigToolsNameTheCodexFiles(t *testing.T) {
	desc := map[string]string{}
	for _, tool := range tools() {
		desc[tool.Name] = tool.Description
	}
	if !strings.Contains(desc[ToolReadMemory], "AGENTS.md for Codex") {
		t.Errorf("read_memory: %s", desc[ToolReadMemory])
	}
	for _, name := range []string{ToolListSkills, ToolReadSkill, ToolProposeSkillChange} {
		if !strings.Contains(desc[name], "Codex") {
			t.Errorf("%s does not say what Codex has: %s", name, desc[name])
		}
	}
}

func TestGetChangeReadsTheNewestOrOneAndLeavesTheContentOut(t *testing.T) {
	applied := `{"changeId":"c2","sessionId":"s1","state":"applied","reason":"r","objects":[{"kind":"memory","name":"project","content":"SECRET-CONTENT","diff":"+x","takesEffect":"next-start"}],"createdAt":1,"expiresAt":2,"decidedAt":3,"appliedAt":4,"undoneAt":null,"undoable":true}`
	c, seen, _ := changeCore(t, 200, `{"changes":[`+applied+`,`+pendingChange("")+`]}`)
	rep := c.Call(context.Background(), "s1", ToolGetChange, nil)
	if rep.IsError || !strings.HasPrefix(rep.Text, "Change c2 was applied. The memory file takes effect when this session next starts") ||
		!strings.Contains(rep.Text, `"takesEffect":"next-start"`) || strings.Contains(rep.Text, "SECRET-CONTENT") {
		t.Fatalf("get_change = %+v", rep)
	}
	if (*seen)[0] != "GET /api/sessions/s1/config-changes" {
		t.Fatalf("requests = %v", *seen)
	}

	one, seen, _ := changeCore(t, 200, pendingChange(`{"kind":"config","name":"model","takesEffect":"next-turn"}`))
	rep = one.Call(context.Background(), "s1", ToolGetChange, json.RawMessage(`{"changeId":"c1"}`))
	if rep.IsError || !strings.HasPrefix(rep.Text, "Change c1 is waiting for the user's answer in Hoplet.") || (*seen)[0] != "GET /api/config-changes/c1" {
		t.Fatalf("get_change c1 = %+v %v", rep, *seen)
	}

	none, _, _ := changeCore(t, 200, `{"changes":[]}`)
	if rep := none.Call(context.Background(), "s1", ToolGetChange, json.RawMessage(`{}`)); rep.IsError || rep.Text != "This session has proposed no change." {
		t.Fatalf("no change = %+v", rep)
	}
}
