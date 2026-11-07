package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

func TestCompanionCommands(t *testing.T) {
	h, _ := testHome(t)
	fakeBin, err := fake.BuildBinary(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	script, err := fake.WriteScript(h, fake.Script{Session: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	startDaemonProcess(t, h, "HOPLET_TEST_FAKE="+fakeBin, "HOPLET_TEST_SCRIPT="+script)

	var made proto.CompanionCreated
	if err := json.Unmarshal([]byte(mustCLI(t, "companion", "new", "--harness", "fake", "--name", "Ada", "--json")), &made); err != nil {
		t.Fatal(err)
	}
	id := string(made.Companion.ID)
	if id == "" || made.Companion.Name != "Ada" || made.Session.Mode != proto.ModePersonal {
		t.Fatalf("new: %+v", made)
	}
	if b, err := os.ReadFile(filepath.Join(made.Companion.Dir, "persona.md")); err != nil || len(b) != 0 {
		t.Fatalf("persona.md %q %v", b, err)
	}
	if out := mustCLI(t, "companion", "show", id[:6]); !strings.Contains(out, "name          Ada") || !strings.Contains(out, string(made.Session.ID)) {
		t.Fatalf("show: %q", out)
	}

	persona := "Start every answer with: OK, partner.\n"
	var out, errb bytes.Buffer
	a := testApp(&out, &errb)
	a.stdin = strings.NewReader(persona)
	if code := a.run([]string{"companion", "persona", id, "--set", "-"}); code != 0 {
		t.Fatalf("persona --set: exit %d: %s", code, errb.String())
	}
	if out.String() != "Persona saved. The main session uses it from its next start.\n" {
		t.Fatalf("persona --set: %q", out.String())
	}
	if got := mustCLI(t, "companion", "persona", id); got != persona {
		t.Fatalf("persona: %q", got)
	}
	file := filepath.Join(h, "persona.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, "companion", "persona", id, "--set", file)
	if got := mustCLI(t, "companion", "persona", id); got != "x" {
		t.Fatalf("persona from a file: %q", got)
	}

	if out := mustCLI(t, "companion", "list"); !strings.Contains(out, id) || !strings.Contains(out, "Ada") {
		t.Fatalf("list: %q", out)
	}
	mustCLI(t, "session", "archive", string(made.Session.ID))
	if out := mustCLI(t, "companion", "list"); strings.Contains(out, id) {
		t.Fatalf("list after archiving: %q", out)
	}
	if out := mustCLI(t, "companion", "list", "--all"); !strings.Contains(out, string(made.Session.ID)+" (archived)") {
		t.Fatalf("list --all: %q", out)
	}

	for _, args := range [][]string{{"new"}, {"show"}, {"persona"}, {"list", "x"}, {"nope"}} {
		if code, _, _ := cli(t, append([]string{"companion"}, args...)...); code != 2 {
			t.Errorf("companion %v: exit %d, want 2", args, code)
		}
	}
	if code, _, _ := cli(t, "companion", "show", "nope"); code == 0 {
		t.Error("show of an unknown companion succeeded")
	}
}
