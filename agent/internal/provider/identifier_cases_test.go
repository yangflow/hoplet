package provider

import (
	"encoding/json"
	"os"
	"testing"
)

// shared/session-id-cases.json holds the cases the TypeScript rule (shared/src/ids.ts) is tested with;
// scripts/test/contract.test.ts runs both.
func TestSessionIDCases(t *testing.T) {
	raw, err := os.ReadFile("../../../shared/session-id-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Valid   []string `json:"valid"`
		Invalid []string `json:"invalid"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Valid) == 0 || len(cases.Invalid) == 0 {
		t.Fatal("the cases file has no cases")
	}
	for _, id := range cases.Valid {
		if !ValidSessionID(id) {
			t.Errorf("ValidSessionID(%q) = false, want true", id)
		}
	}
	for _, id := range cases.Invalid {
		if ValidSessionID(id) {
			t.Errorf("ValidSessionID(%q) = true, want false", id)
		}
	}
}
