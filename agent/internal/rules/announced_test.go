package rules

import (
	"fmt"
	"testing"
)

func TestAnnouncedIsWhatTheEngineHolds(t *testing.T) {
	if Announced(nil) != nil {
		t.Fatal("no rules must announce nil")
	}
	in := []Rule{{ID: "local.x", Tool: "run", Match: Match{Command: "curl"}, Action: Deny, Scope: ScopeGlobal, Builtin: true}}
	out := Announced(in)
	if len(out) != 1 || out[0].Scope != ScopeAgent || !out[0].Enabled || out[0].Builtin {
		t.Fatalf("announced = %#v", out)
	}
	if in[0].Scope != ScopeGlobal {
		t.Fatal("the configured rules were changed")
	}
}

func TestValidateLocalRefusesMoreThanMaxLocal(t *testing.T) {
	var many []Rule
	for i := 0; i <= MaxLocal; i++ {
		many = append(many, Rule{ID: fmt.Sprintf("local.%d", i), Tool: "run", Match: Match{Command: "curl"}, Action: Deny})
	}
	if err := ValidateLocal(many); err == nil {
		t.Fatal("more than MaxLocal rules were accepted")
	}
	if err := ValidateLocal(many[:MaxLocal]); err != nil {
		t.Fatal(err)
	}
}
