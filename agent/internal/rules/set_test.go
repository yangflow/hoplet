package rules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func setOf(rev int, g []Rule, p []ProjectRule, s []SessionRule) []byte {
	b, _ := json.Marshal(Set{Revision: rev, Global: g, Projects: p, Sessions: s})
	return b
}

func ids(rs []Rule) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func TestSetJSONShape(t *testing.T) {
	raw := `{"revision":3,"global":[{"id":"g","tool":"run","match":{"command":"ls"},"action":"allow","scope":"global","note":"n","enabled":true,"builtin":false}],
"projects":[{"projectPath":"/p","rule":{"id":"p","tool":"run","match":{},"action":"ask","scope":"project","note":"","enabled":true,"builtin":false}}],
"sessions":[{"sessionId":"s1","rule":{"id":"s","tool":"run","match":{},"action":"deny","scope":"session","note":"","enabled":false,"builtin":false}}]}`
	s, err := ParseSet([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if s.Revision != 3 || s.Global[0].ID != "g" || s.Projects[0].ProjectPath != "/p" || s.Sessions[0].SessionID != "s1" || s.Sessions[0].Rule.Enabled {
		t.Fatalf("decoded wrong: %+v", s)
	}
}

func TestParseSetRefusesUnknownFields(t *testing.T) {
	good := `{"id":"g","tool":"run","match":{},"action":"allow","scope":"global","note":"","enabled":true,"builtin":false}`
	for name, raw := range map[string]string{
		"top level":  `{"revision":1,"global":[],"projects":[],"sessions":[],"extra":1}`,
		"rule field": `{"revision":1,"global":[{"id":"g","tool":"run","match":{},"action":"allow","scope":"global","note":"","enabled":true,"builtin":false,"deviceId":"d"}],"projects":[],"sessions":[]}`,
		"match":      `{"revision":1,"global":[{"id":"g","tool":"run","match":{"regex":"x"},"action":"allow","scope":"global","note":"","enabled":true,"builtin":false}],"projects":[],"sessions":[]}`,
		"trailing":   `{"revision":1,"global":[` + good + `],"projects":[],"sessions":[]} {}`,
		"negative":   `{"revision":-1,"global":[],"projects":[],"sessions":[]}`,
	} {
		if _, err := ParseSet([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestStoreRefusesInvalidSets(t *testing.T) {
	st := OpenStore(testEnv(), t.TempDir(), nil)
	g := func(id string) Rule { return rule(id, "run", Ask, ScopeGlobal, Match{Command: "x"}) }
	for name, raw := range map[string][]byte{
		"duplicate id":      setOf(1, []Rule{g("a"), g("a")}, nil, nil),
		"agent scope":       setOf(1, []Rule{rule("a", "run", Deny, ScopeAgent, Match{})}, nil, nil),
		"wrong list":        setOf(1, []Rule{rule("a", "run", Ask, ScopeProject, Match{})}, nil, nil),
		"relative project":  setOf(1, nil, []ProjectRule{{"p", rule("a", "run", Ask, ScopeProject, Match{})}}, nil),
		"session no id":     setOf(1, nil, nil, []SessionRule{{"", rule("a", "run", Ask, ScopeSession, Match{})}}),
		"invalid match":     setOf(1, []Rule{rule("a", "run", Ask, ScopeGlobal, Match{Paths: []string{"/x"}})}, nil, nil),
		"not json":          []byte("nope"),
		"builtin collision": setOf(1, []Rule{{ID: Floor().ID, Tool: "run", Action: Allow, Scope: ScopeGlobal}}, nil, nil),
	} {
		if _, err := st.Apply(raw); err == nil {
			t.Errorf("%s: applied", name)
		}
	}
	if st.Revision() != 0 {
		t.Fatalf("revision moved to %d", st.Revision())
	}
}

func TestStoreKeepsThePreviousSetWhenAPushIsRefused(t *testing.T) {
	dir := t.TempDir()
	st := OpenStore(testEnv(), dir, nil)
	deny := rule("no-x", "run", Deny, ScopeGlobal, Match{Command: "x"})
	if rev, err := st.Apply(setOf(5, []Rule{deny}, nil, nil)); err != nil || rev != 5 {
		t.Fatalf("apply: %d %v", rev, err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, SetFile))
	bad := `{"revision":6,"global":[{"id":"y","tool":"run","match":{},"action":"allow","scope":"global","note":"","enabled":true,"builtin":false,"surprise":1}],"projects":[],"sessions":[]}`
	if _, err := st.Apply([]byte(bad)); err == nil {
		t.Fatal("unknown field accepted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, SetFile))
	if string(before) != string(after) || st.Revision() != 5 {
		t.Fatalf("previous set lost: revision %d", st.Revision())
	}
	e, err := st.Engine("s", "/ws")
	if err != nil {
		t.Fatal(err)
	}
	if d := e.Decide(run("x")); d.Action != Deny || d.RuleID != "no-x" {
		t.Fatalf("decision %+v", d)
	}
}

func TestStorePersistsAndLoadsAtStart(t *testing.T) {
	dir := t.TempDir()
	st := OpenStore(testEnv(), dir, nil)
	if st.Revision() != 0 {
		t.Fatal("fresh store has a revision")
	}
	// No file yet: the built-in defaults decide.
	e, _ := st.Engine("s", "/ws")
	if d := e.Decide(run("git push --force")); d.Action != Ask || d.RuleID != "default.force-push" {
		t.Fatalf("defaults: %+v", d)
	}
	off := Defaults()
	for i := range off {
		if off[i].ID == "default.force-push" {
			off[i].Enabled = false
		}
	}
	if _, err := st.Apply(setOf(7, off, nil, nil)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, SetFile))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left: %v", entries)
	}

	again := OpenStore(testEnv(), dir, nil)
	if again.Revision() != 7 {
		t.Fatalf("revision after restart: %d", again.Revision())
	}
	e, _ = again.Engine("s", "/ws")
	if d := e.Decide(run("git push --force")); d.Action == Ask {
		t.Fatalf("the switched-off built-in still asks: %+v", d)
	}
}

func TestStoreFallsBackToDefaultsOnABadFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SetFile), []byte(`{"revision":2,"bogus":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st := OpenStore(testEnv(), dir, nil)
	if st.Revision() != 0 {
		t.Fatal("bad file used")
	}
	e, _ := st.Engine("s", "/ws")
	if d := e.Decide(run("git push -f")); d.RuleID != "default.force-push" {
		t.Fatalf("defaults not in force: %+v", d)
	}
}

func TestForSessionSelectsByProjectAndSession(t *testing.T) {
	r := func(id string, s Scope) Rule { return rule(id, "run", Ask, s, Match{Command: "x"}) }
	s := Set{
		Global: []Rule{r("g", ScopeGlobal)},
		Projects: []ProjectRule{
			{"/work/a", r("pa", ScopeProject)},
			{"/work/ab", r("pab", ScopeProject)},
			{"/work/b", r("pb", ScopeProject)},
		},
		Sessions: []SessionRule{{"s1", r("s1r", ScopeSession)}, {"s2", r("s2r", ScopeSession)}},
	}
	for _, c := range []struct {
		session, dir string
		want         []string
	}{
		{"s1", "/work/a", []string{"g", "pa", "s1r"}},
		{"s2", "/work/a/sub/dir", []string{"g", "pa", "s2r"}},
		{"s3", "/work/ab", []string{"g", "pab"}},
		{"s1", "/work/b/", []string{"g", "pb", "s1r"}},
		{"s1", "/elsewhere", []string{"g", "s1r"}},
	} {
		got := ids(s.ForSession(c.session, c.dir))
		if len(got) != len(c.want) {
			t.Errorf("%s %s: %v, want %v", c.session, c.dir, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s %s: %v, want %v", c.session, c.dir, got, c.want)
			}
		}
	}
}

func TestStoreEngineHonoursScopePrecedenceFloorAndLocalDeny(t *testing.T) {
	local := []Rule{{ID: "local.no-curl", Tool: "run", Match: Match{Command: "curl"}, Action: Deny, Note: "no curl"}}
	st := OpenStore(testEnv(), t.TempDir(), local)
	x := func(id string, a Action, s Scope) Rule { return rule(id, "run", a, s, Match{Command: "tool"}) }
	raw := setOf(1,
		[]Rule{x("g-ask", Ask, ScopeGlobal), rule("g-allow-curl", "run", Allow, ScopeGlobal, Match{Command: "curl"})},
		[]ProjectRule{{"/ws", x("p-allow", Allow, ScopeProject)}},
		[]SessionRule{{"mine", x("s-ask", Ask, ScopeSession)},
			{"mine", rule("s-allow-conf", "write", Allow, ScopeSession, Match{Paths: []string{"/home/u/.hoplet/**"}})}},
	)
	if _, err := st.Apply(raw); err != nil {
		t.Fatal(err)
	}
	mine, _ := st.Engine("mine", "/ws")
	other, _ := st.Engine("other", "/ws")
	away, _ := st.Engine("other", "/elsewhere")
	if d := mine.Decide(run("tool")); d.RuleID != "s-ask" {
		t.Errorf("session scope should be narrowest: %+v", d)
	}
	if d := other.Decide(run("tool")); d.RuleID != "p-allow" {
		t.Errorf("project allow over global ask: %+v", d)
	}
	if d := away.Decide(run("tool")); d.RuleID != "g-ask" {
		t.Errorf("outside the project only global applies: %+v", d)
	}
	if d := mine.Decide(run("curl https://x")); d.Action != Deny || d.RuleID != "local.no-curl" {
		t.Errorf("local deny must beat a pushed allow: %+v", d)
	}
	if d := mine.Decide(write("/home/u/.hoplet/x")); d.Action != Deny {
		t.Errorf("the floor must beat a pushed allow: %+v", d)
	}
}
