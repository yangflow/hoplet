package rules

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The core serves the built-in rules from shared/src/permission-rule-defaults.json; this keeps that file equal to Defaults().
func TestDefaultsMatchSharedFile(t *testing.T) {
	raw, err := os.ReadFile("../../../shared/src/permission-rule-defaults.json")
	if err != nil {
		t.Fatal(err)
	}
	var shared []Rule
	if err := json.Unmarshal(raw, &shared); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(shared, Defaults()) {
		t.Fatalf("shared/src/permission-rule-defaults.json differs from Defaults()")
	}
}
