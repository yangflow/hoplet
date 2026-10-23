package rules

import (
	"encoding/json"
	"testing"
)

func inheritedSet(rev int, mine []SessionRule, inherited []SessionRule) []byte {
	b, _ := json.Marshal(Set{Revision: rev, Global: []Rule{}, Projects: []ProjectRule{}, Sessions: mine, Inherited: inherited})
	return b
}

func TestInheritedRulesAreDenyAndAskOfTheSessionScopeOnly(t *testing.T) {
	st := OpenStore(testEnv(), t.TempDir(), nil)
	ok := rule("p-deny", "run", Deny, ScopeSession, Match{Command: "tool"})
	for name, raw := range map[string][]byte{
		"allow":      inheritedSet(1, nil, []SessionRule{{"c", rule("a", "run", Allow, ScopeSession, Match{})}}),
		"project":    inheritedSet(1, nil, []SessionRule{{"c", rule("a", "run", Deny, ScopeProject, Match{})}}),
		"no session": inheritedSet(1, nil, []SessionRule{{"", ok}}),
	} {
		if _, err := st.Apply(raw); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := st.Apply(inheritedSet(2, nil, []SessionRule{{"c", ok}, {"d", ok}})); err != nil {
		t.Fatalf("a deny rule shared by two children: %v", err)
	}
	if _, err := ParseSet([]byte(`{"revision":1,"global":[],"projects":[],"sessions":[]}`)); err != nil {
		t.Fatalf("a set without inherited rules: %v", err)
	}
}

func TestAChildObeysTheInheritedRulesAndCannotLoosenThem(t *testing.T) {
	st := OpenStore(testEnv(), t.TempDir(), nil)
	deny := rule("p-deny", "run", Deny, ScopeSession, Match{Command: "tool"})
	ask := rule("p-ask", "run", Ask, ScopeSession, Match{Command: "other"})
	raw := inheritedSet(1,
		[]SessionRule{
			{"child", rule("c-allow", "run", Allow, ScopeSession, Match{Command: "tool"})},
			{"child", rule("c-allow2", "run", Allow, ScopeSession, Match{Command: "other"})},
		},
		[]SessionRule{{"child", deny}, {"child", ask}},
	)
	if _, err := st.Apply(raw); err != nil {
		t.Fatal(err)
	}
	child, _ := st.Engine("child", "/ws")
	stranger, _ := st.Engine("stranger", "/ws")
	if d := child.Decide(run("tool")); d.Action != Deny || d.RuleID != "p-deny" {
		t.Errorf("the parent's deny: %+v", d)
	}
	if d := child.Decide(run("other")); d.Action != Ask || d.RuleID != "p-ask" {
		t.Errorf("the parent's ask beats the child's own allow: %+v", d)
	}
	if d := stranger.Decide(run("tool")); d.Action == Deny {
		t.Errorf("another session is not bound: %+v", d)
	}
}

func TestACarryBindsToTheSessionAndEndsWithThePushedSet(t *testing.T) {
	st := OpenStore(testEnv(), t.TempDir(), nil)
	deny := rule("p-deny", "run", Deny, ScopeSession, Match{Command: "tool"})
	if _, err := st.Inherit("/ws", []Rule{rule("a", "run", Allow, ScopeSession, Match{})}); err == nil {
		t.Fatal("an allow rule was carried")
	}
	c, err := st.Inherit("/ws/", []Rule{deny})
	if err != nil {
		t.Fatal(err)
	}
	// Before the session's ID is known the rules hold for the directory.
	early, _ := st.Engine("not-yet-known", "/ws")
	if d := early.Decide(run("tool")); d.Action != Deny {
		t.Errorf("pending carry: %+v", d)
	}
	elsewhere, _ := st.Engine("x", "/other")
	if d := elsewhere.Decide(run("tool")); d.Action == Deny {
		t.Errorf("a carry holds for its directory only: %+v", d)
	}
	c.Bind("child")
	c.Drop() // after Bind it does nothing
	child, _ := st.Engine("child", "/ws")
	if d := child.Decide(run("tool")); d.Action != Deny {
		t.Errorf("bound carry: %+v", d)
	}
	sibling, _ := st.Engine("sibling", "/ws")
	if d := sibling.Decide(run("tool")); d.Action == Deny {
		t.Errorf("a bound carry is for its session: %+v", d)
	}
	// The pushed set carries the rule: the carry is gone and the rule still holds.
	if _, err := st.Apply(inheritedSet(1, nil, []SessionRule{{"child", deny}})); err != nil {
		t.Fatal(err)
	}
	if len(st.bound) != 0 {
		t.Errorf("bound carry kept: %v", st.bound)
	}
	child, err = st.Engine("child", "/ws")
	if err != nil {
		t.Fatal(err)
	}
	if d := child.Decide(run("tool")); d.Action != Deny {
		t.Errorf("pushed set: %+v", d)
	}
}

func TestADroppedCarryHoldsNothing(t *testing.T) {
	st := OpenStore(testEnv(), t.TempDir(), nil)
	c, err := st.Inherit("/ws", []Rule{rule("p-deny", "run", Deny, ScopeSession, Match{Command: "tool"})})
	if err != nil {
		t.Fatal(err)
	}
	c.Drop()
	c.Bind("late")
	e, _ := st.Engine("late", "/ws")
	if d := e.Decide(run("tool")); d.Action == Deny {
		t.Errorf("dropped carry: %+v", d)
	}
}
