package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func script(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateRefusesWhatIsNotCode(t *testing.T) {
	good := script(t, 0o700, "")
	if err := Validate([]Entry{{ID: "ok", Command: good, Events: []string{"*"}}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, e := range map[string]Entry{
		"relative":       {ID: "a", Command: "hook.sh", Events: []string{"*"}},
		"directory":      {ID: "a", Command: dir, Events: []string{"*"}},
		"not executable": {ID: "a", Command: script(t, 0o600, ""), Events: []string{"*"}},
		"group writable": {ID: "a", Command: script(t, 0o770, ""), Events: []string{"*"}},
		"world writable": {ID: "a", Command: script(t, 0o707, ""), Events: []string{"*"}},
		"star and type":  {ID: "a", Command: good, Events: []string{"*", "session.ended"}},
	} {
		if err := Validate([]Entry{e}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateDefaultsTheTimeout(t *testing.T) {
	entries := []Entry{{ID: "a", Command: script(t, 0o755, ""), Events: []string{"session.ended"}}}
	if err := Validate(entries); err != nil || entries[0].TimeoutMS != DefaultTimeoutMS {
		t.Fatalf("%+v %v", entries[0], err)
	}
	entries[0].TimeoutMS = MaxTimeoutMS + 1
	if Validate(entries) == nil {
		t.Fatal("a timeout above the maximum was accepted")
	}
}
