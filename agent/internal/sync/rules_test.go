package sync

import (
	"encoding/json"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/rules"
)

const rulesPush = `{"v":13,"type":"device.rules","revision":4,` +
	`"global":[{"id":"default.force-push","tool":"run","match":{"command":"git push","anyArg":["--force*"]},"action":"ask","scope":"global","note":"Force push","enabled":false,"builtin":true}],` +
	`"projects":[{"projectPath":"/work/app","rule":{"id":"p1","tool":"run","match":{"command":"pnpm test*"},"action":"allow","scope":"project","note":"","enabled":true,"builtin":false}}],` +
	`"sessions":[{"sessionId":"s1","rule":{"id":"s1r","tool":"fetch","match":{"domains":["example.com"]},"action":"deny","scope":"session","note":"","enabled":true,"builtin":false}}]}`

func newStore(t *testing.T) (*rules.Store, string) {
	dir := t.TempDir()
	return rules.OpenStore(rules.NewEnv("/home/u", dir), dir, nil), dir
}

func TestHandleRulesAppliesAndAnswersTheRevision(t *testing.T) {
	c, cs, read := dialTestConn(t)
	st, _ := newStore(t)
	c.hooks.Rules = st.Apply
	c.handleRules(cs, []byte(rulesPush))
	msg := read()
	if msg["type"] != "device.rules.applied" || msg["revision"] != float64(4) || msg["error"] != nil || msg["v"] != float64(ProtocolVersion) {
		t.Fatalf("got %#v", msg)
	}
	if st.Revision() != 4 {
		t.Fatalf("revision %d", st.Revision())
	}
}

func TestHandleRulesRefusesAPushWithAnUnknownRuleFieldAndKeepsTheSet(t *testing.T) {
	c, cs, read := dialTestConn(t)
	st, _ := newStore(t)
	c.hooks.Rules = st.Apply
	c.handleRules(cs, []byte(rulesPush))
	read()
	bad := `{"v":13,"type":"device.rules","revision":5,"global":[{"id":"x","tool":"run","match":{},"action":"allow","scope":"global","note":"","enabled":true,"builtin":false,"deviceId":"d"}],"projects":[],"sessions":[]}`
	c.handleRules(cs, []byte(bad))
	msg := read()
	if msg["type"] != "device.rules.applied" || msg["revision"] != float64(5) {
		t.Fatalf("got %#v", msg)
	}
	if e, _ := msg["error"].(string); e == "" {
		t.Fatalf("no error in %#v", msg)
	}
	if st.Revision() != 4 {
		t.Fatalf("the previous set was replaced: revision %d", st.Revision())
	}
}

func TestHandleRulesWithoutAHookIsRefused(t *testing.T) {
	c, cs, read := dialTestConn(t)
	c.handleRules(cs, []byte(rulesPush))
	msg := read()
	if e, _ := msg["error"].(string); e == "" || msg["revision"] != float64(4) {
		t.Fatalf("got %#v", msg)
	}
}

func TestDeviceRulesWireShape(t *testing.T) {
	raw, err := json.Marshal(DeviceRules{
		V: ProtocolVersion, Type: TypeDeviceRules, Revision: 2,
		Global:   []rules.Rule{{ID: "g", Tool: "run", Action: rules.Ask, Scope: rules.ScopeGlobal, Enabled: true}},
		Projects: []rules.ProjectRule{{ProjectPath: "/p", Rule: rules.Rule{ID: "p", Tool: "run", Action: rules.Allow, Scope: rules.ScopeProject}}},
		Sessions: []rules.SessionRule{{SessionID: "s", Rule: rules.Rule{ID: "s", Tool: "run", Action: rules.Deny, Scope: rules.ScopeSession}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":13,"type":"device.rules","revision":2,` +
		`"global":[{"id":"g","tool":"run","match":{},"action":"ask","scope":"global","note":"","enabled":true,"builtin":false}],` +
		`"projects":[{"projectPath":"/p","rule":{"id":"p","tool":"run","match":{},"action":"allow","scope":"project","note":"","enabled":false,"builtin":false}}],` +
		`"sessions":[{"sessionId":"s","rule":{"id":"s","tool":"run","match":{},"action":"deny","scope":"session","note":"","enabled":false,"builtin":false}}]}`
	if string(raw) != want {
		t.Fatalf("got %s", raw)
	}
}
