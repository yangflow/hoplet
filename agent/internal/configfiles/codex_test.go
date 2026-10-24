package configfiles

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The Codex layout: AGENTS.md, skills in .agents/skills, no commands.

func codexReq(kind, scope, name string) Request {
	return Request{Provider: "codex", Kind: kind, Scope: scope, Name: name}
}

func codexChange(objects ...Object) Change {
	return Change{Provider: "codex", Tested: true, Objects: objects}
}

const codexSkill = "---\nname: review\ndescription: Reviews a change.\n---\nReview it.\n"

func TestCodexReadsAgentsMdOfBothScopes(t *testing.T) {
	r, cwd, home := newReader(t)
	write(t, filepath.Join(cwd, "AGENTS.md"), "project agents\n")
	write(t, filepath.Join(cwd, "CLAUDE.md"), "not for codex\n")
	write(t, filepath.Join(home, ".codex", "AGENTS.md"), "user agents\n")

	page, err := r.Read(cwd, codexReq(KindMemory, ScopeProject, ""))
	if err != nil || page.Text != "project agents\n" {
		t.Fatalf("project memory: %+v %v", page, err)
	}
	page, err = r.Read(cwd, codexReq(KindMemory, ScopeUser, ""))
	if err != nil || page.Text != "user agents\n" {
		t.Fatalf("user memory: %+v %v", page, err)
	}
	// In ~/.codex the override is read first.
	write(t, filepath.Join(home, ".codex", "AGENTS.override.md"), "user override\n")
	page, err = r.Read(cwd, codexReq(KindMemory, ScopeUser, ""))
	if err != nil || page.Text != "user override\n" {
		t.Fatalf("user override: %+v %v", page, err)
	}
	// A project with an override shows it: that is the file Codex may read instead.
	write(t, filepath.Join(cwd, "AGENTS.override.md"), "project override\n")
	page, err = r.Read(cwd, codexReq(KindMemory, ScopeProject, ""))
	if err != nil || page.Text != "project override\n" {
		t.Fatalf("project override: %+v %v", page, err)
	}
}

func TestCodexMissingMemoryIsNotAnError(t *testing.T) {
	r, cwd, _ := newReader(t)
	for _, scope := range []string{ScopeProject, ScopeUser} {
		page, err := r.Read(cwd, codexReq(KindMemory, scope, ""))
		if err != nil || page.Exists {
			t.Errorf("%s: %+v %v", scope, page, err)
		}
	}
}

func TestCodexReadsAndListsSkillsAndHasNoCommands(t *testing.T) {
	r, cwd, home := newReader(t)
	write(t, filepath.Join(cwd, ".agents", "skills", "review", "SKILL.md"), codexSkill)
	write(t, filepath.Join(cwd, ".claude", "skills", "claude-only", "SKILL.md"), "---\ndescription: Claude's\n---\n")
	write(t, filepath.Join(cwd, ".claude", "commands", "ship.md"), "# Ship\n")
	write(t, filepath.Join(home, ".agents", "skills", "mine", "SKILL.md"), "---\nname: mine\ndescription: Mine.\n---\n")

	page, err := r.Read(cwd, codexReq(KindSkill, ScopeProject, "review"))
	if err != nil || page.Text != codexSkill {
		t.Fatalf("project skill: %+v %v", page, err)
	}
	page, err = r.Read(cwd, codexReq(KindSkill, ScopeUser, "mine"))
	if err != nil || !page.Exists {
		t.Fatalf("user skill: %+v %v", page, err)
	}
	page, err = r.Read(cwd, codexReq(KindList, ScopeProject, ""))
	if err != nil || len(page.Entries) != 1 || page.Entries[0] != (Entry{Kind: KindSkill, Name: "review", Description: "Reviews a change."}) {
		t.Fatalf("project list: %+v %v", page, err)
	}
	page, err = r.Read(cwd, codexReq(KindList, ScopeUser, ""))
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Name != "mine" {
		t.Fatalf("user list: %+v %v", page, err)
	}
	for _, scope := range []string{ScopeProject, ScopeUser} {
		if _, err := r.Read(cwd, codexReq(KindCommand, scope, "ship")); code(err) != CodeUnsupported {
			t.Errorf("%s command: %v", scope, err)
		}
	}
}

func TestCodexSkillSymlinkIsRefused(t *testing.T) {
	r, cwd, _ := newReader(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "skills", "x", "SKILL.md"), codexSkill)
	if err := os.MkdirAll(filepath.Join(cwd, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "skills"), filepath.Join(cwd, ".agents", "skills")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(cwd, codexReq(KindSkill, ScopeProject, "x")); code(err) != CodeInvalid {
		t.Errorf("read: %v", err)
	}
	w := Writer{Home: t.TempDir()}
	if _, err := w.Preview(cwd, codexChange(Object{Kind: KindSkill, Name: "x", Content: str(codexSkill)})); code(err) != CodeInvalid {
		t.Errorf("preview: %v", err)
	}
}

func TestCodexWritesAgentsMd(t *testing.T) {
	w, cwd := newWriter(t)
	write(t, filepath.Join(cwd, "CLAUDE.md"), "claude\n")
	got, err := w.Preview(cwd, codexChange(Object{Kind: KindMemory, Name: MemoryName, Content: str("Use pnpm.\n")}))
	if err != nil || len(got) != 1 || got[0].CurrentHash != "" || !strings.Contains(got[0].Diff, "+++ b/AGENTS.md") {
		t.Fatalf("preview %+v %v", got, err)
	}
	if _, err := w.Apply(cwd, codexChange(applyObject(KindMemory, MemoryName, "", str("Use pnpm.\n")))); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(cwd, "AGENTS.md")) != "Use pnpm.\n" || read(t, filepath.Join(cwd, "CLAUDE.md")) != "claude\n" {
		t.Fatal("AGENTS.md not written, or CLAUDE.md touched")
	}
	page, err := Reader{Home: w.Home}.Read(cwd, codexReq(KindMemory, ScopeProject, ""))
	if err != nil || page.Text != "Use pnpm.\n" || page.Hash != hashOf("Use pnpm.\n") {
		t.Fatalf("read back %+v %v", page, err)
	}
}

func TestCodexMemoryRefusals(t *testing.T) {
	w, cwd := newWriter(t)
	big := strings.Repeat("x", codexMemoryBytes+1)
	if _, err := w.Preview(cwd, codexChange(Object{Kind: KindMemory, Name: MemoryName, Content: &big})); code(err) != CodeTooLarge {
		t.Errorf("past what Codex reads: %v", err)
	}
	// An undo puts back what was there, whatever its size.
	write(t, filepath.Join(cwd, "AGENTS.md"), "small\n")
	undo := codexChange(applyObject(KindMemory, MemoryName, hashOf("small\n"), &big))
	undo.Restore = true
	if _, err := w.Apply(cwd, undo); err != nil {
		t.Errorf("restore: %v", err)
	}
	write(t, filepath.Join(cwd, "AGENTS.override.md"), "override\n")
	if _, err := w.Preview(cwd, codexChange(Object{Kind: KindMemory, Name: MemoryName, Content: str("x\n")})); code(err) != CodeUnsupported {
		t.Errorf("beside an override: %v", err)
	}
}

func TestCodexWritesSkillsInAgentsSkills(t *testing.T) {
	w, cwd := newWriter(t)
	got, err := w.Preview(cwd, codexChange(Object{Kind: KindSkill, Name: "review", Content: str(codexSkill)}))
	if err != nil || len(got) != 1 {
		t.Fatalf("preview %+v %v", got, err)
	}
	if want := []string{".agents", ".agents/skills", ".agents/skills/review"}; !slices.Equal(got[0].NewDirectories, want) {
		t.Errorf("new directories %v, want %v", got[0].NewDirectories, want)
	}
	if _, err := w.Apply(cwd, codexChange(applyObject(KindSkill, "review", "", str(codexSkill)))); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(cwd, ".agents", "skills", "review", "SKILL.md")) != codexSkill {
		t.Fatal("skill not written")
	}
	missing(t, filepath.Join(cwd, ".claude"))
	if _, err := w.Apply(cwd, codexChange(applyObject(KindSkill, "review", hashOf(codexSkill), nil))); err != nil {
		t.Fatal(err)
	}
	missing(t, filepath.Join(cwd, ".agents", "skills", "review"))
}

func TestCodexSkillAndCommandRefusals(t *testing.T) {
	w, cwd := newWriter(t)
	refused := []struct {
		name string
		obj  Object
		code string
	}{
		{"command", Object{Kind: KindCommand, Name: "ship", Content: str("Ship.\n")}, CodeUnsupported},
		{"no front matter", Object{Kind: KindSkill, Name: "x", Content: str("Review it.\n")}, CodeInvalid},
		{"no description", Object{Kind: KindSkill, Name: "x", Content: str("---\nname: x\n---\n")}, CodeInvalid},
		{"allowed-tools", Object{Kind: KindSkill, Name: "x", Content: str("---\nname: x\ndescription: d\nallowed-tools: Bash\n---\n")}, CodeUnsafeContent},
		{"shell line", Object{Kind: KindSkill, Name: "x", Content: str(codexSkill + "!`date`\n")}, CodeUnsafeContent},
	}
	for _, tc := range refused {
		if _, err := w.Preview(cwd, codexChange(tc.obj)); code(err) != tc.code {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.code)
		}
	}
	untested := codexChange(Object{Kind: KindSkill, Name: "x", Content: str(codexSkill)})
	untested.Tested = false
	if _, err := w.Preview(cwd, untested); code(err) != CodeUnsupported {
		t.Errorf("untested version: %v", err)
	}
	missing(t, filepath.Join(cwd, ".agents"))
}
