package configfiles

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func str(s string) *string { return &s }

func newWriter(t *testing.T) (Writer, string) {
	t.Helper()
	return Writer{Home: t.TempDir()}, t.TempDir()
}

func change(objects ...Object) Change {
	return Change{Provider: "claude", Tested: true, Objects: objects}
}

func read(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func missing(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists: %v", p, err)
	}
}

// applyObject is an apply of content over the file that has base now.
func applyObject(kind, name, base string, content *string) Object {
	o := Object{Kind: kind, Name: name, Content: content, BaseHash: base}
	if content != nil {
		o.NewHash = hashOf(*content)
	}
	return o
}

func TestPreviewOfANewMemoryFile(t *testing.T) {
	w, cwd := newWriter(t)
	got, err := w.Preview(cwd, change(Object{Kind: KindMemory, Name: MemoryName, Content: str("Use pnpm.\n")}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].CurrentHash != "" || got[0].Previous != nil || got[0].NewDirectories != nil {
		t.Fatalf("preview %+v", got)
	}
	if want := "--- /dev/null\n+++ b/CLAUDE.md\n@@ -0,0 +1,1 @@\n+Use pnpm.\n"; got[0].Diff != want {
		t.Errorf("diff %q", got[0].Diff)
	}
	missing(t, filepath.Join(cwd, "CLAUDE.md"))
}

func TestPreviewDiffsTheCurrentFile(t *testing.T) {
	w, cwd := newWriter(t)
	before := "# Rules\n\nOne\nTwo\nThree\nFour\nFive\nSix\nSeven\nEight\n"
	write(t, filepath.Join(cwd, "CLAUDE.md"), before)
	after := strings.Replace(before, "Six\n", "Six and a half\n", 1)
	got, err := w.Preview(cwd, change(Object{Kind: KindMemory, Name: MemoryName, Content: &after}))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CurrentHash != hashOf(before) || got[0].Previous == nil || *got[0].Previous != before {
		t.Fatalf("preview %+v", got[0])
	}
	want := "--- a/CLAUDE.md\n+++ b/CLAUDE.md\n@@ -5,6 +5,6 @@\n Three\n Four\n Five\n-Six\n+Six and a half\n Seven\n Eight\n"
	if got[0].Diff != want {
		t.Errorf("diff\n%s\nwant\n%s", got[0].Diff, want)
	}
}

func TestDiffWithoutTrailingNewline(t *testing.T) {
	got := unifiedDiff("CLAUDE.md", str("a"), str("b"))
	want := "--- a/CLAUDE.md\n+++ b/CLAUDE.md\n@@ -1,1 +1,1 @@\n-a\n\\ No newline at end of file\n+b\n\\ No newline at end of file\n"
	if got != want {
		t.Errorf("diff %q", got)
	}
	if unifiedDiff("x", str("same"), str("same")) != "" {
		t.Error("equal texts have a diff")
	}
}

func TestApplyWritesAtomicallyAndKeepsTheMode(t *testing.T) {
	w, cwd := newWriter(t)
	p := filepath.Join(cwd, "CLAUDE.md")
	write(t, p, "old\n")
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := w.Apply(cwd, change(applyObject(KindMemory, MemoryName, hashOf("old\n"), str("new\n"))))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Hash != hashOf("new\n") {
		t.Fatalf("applied %+v", got)
	}
	if read(t, p) != "new\n" {
		t.Errorf("content %q", read(t, p))
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode())
	}
	entries, _ := os.ReadDir(cwd)
	if len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestApplyRefusesAStaleFile(t *testing.T) {
	w, cwd := newWriter(t)
	p := filepath.Join(cwd, "CLAUDE.md")
	write(t, p, "edited by hand\n")
	_, err := w.Apply(cwd, change(applyObject(KindMemory, MemoryName, hashOf("old\n"), str("new\n"))))
	if code(err) != CodeStale {
		t.Fatalf("err %v", err)
	}
	if read(t, p) != "edited by hand\n" {
		t.Error("a stale file was written")
	}
	// A file that appeared since the preview of a new one is stale too.
	_, err = w.Apply(cwd, change(applyObject(KindCommand, "deploy", "", str("Deploy.\n"))))
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.Apply(cwd, change(applyObject(KindCommand, "deploy", "", str("Again.\n"))))
	if code(err) != CodeStale {
		t.Fatalf("err %v", err)
	}
}

func TestApplyRefusesANewHashThatIsNotTheContent(t *testing.T) {
	w, cwd := newWriter(t)
	o := applyObject(KindMemory, MemoryName, "", str("new\n"))
	o.NewHash = hashOf("other\n")
	if _, err := w.Apply(cwd, change(o)); code(err) != CodeInvalid {
		t.Fatalf("err %v", err)
	}
	missing(t, filepath.Join(cwd, "CLAUDE.md"))
}

func TestApplyIsAllOrNothing(t *testing.T) {
	w, cwd := newWriter(t)
	memory := filepath.Join(cwd, "CLAUDE.md")
	write(t, memory, "old\n")
	if err := os.Chmod(memory, 0o640); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(cwd, ".claude", "skills", "kept", "SKILL.md")
	write(t, skill, "Kept skill.\n")
	w.fail = func(rel string) error {
		if rel == ".claude/commands/deploy.md" {
			return errors.New("disk full")
		}
		return nil
	}
	_, err := w.Apply(cwd, change(
		applyObject(KindMemory, MemoryName, hashOf("old\n"), str("new\n")),
		applyObject(KindSkill, "kept", hashOf("Kept skill.\n"), nil),
		applyObject(KindSkill, "fresh", "", str("A new skill.\n")),
		applyObject(KindCommand, "deploy", "", str("Deploy.\n")),
	))
	if code(err) != CodeIO {
		t.Fatalf("err %v", err)
	}
	if read(t, memory) != "old\n" {
		t.Errorf("memory %q", read(t, memory))
	}
	if info, _ := os.Stat(memory); info.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", info.Mode())
	}
	if read(t, skill) != "Kept skill.\n" {
		t.Error("the deleted skill was not put back")
	}
	missing(t, filepath.Join(cwd, ".claude", "skills", "fresh"))
	missing(t, filepath.Join(cwd, ".claude", "commands"))
	entries, _ := os.ReadDir(cwd)
	if len(entries) != 2 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestANewSkillNamesTheDirectoriesItCreates(t *testing.T) {
	w, cwd := newWriter(t)
	o := Object{Kind: KindSkill, Name: "review", Content: str("---\nname: review\ndescription: Reviews.\n---\nReview it.\n")}
	got, err := w.Preview(cwd, change(o))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude", ".claude/skills", ".claude/skills/review"}
	if !slices.Equal(got[0].NewDirectories, want) {
		t.Fatalf("new directories %v", got[0].NewDirectories)
	}
	write(t, filepath.Join(cwd, ".claude", "commands", "x.md"), "x\n")
	got, _ = w.Preview(cwd, change(o))
	if !slices.Equal(got[0].NewDirectories, want[1:]) {
		t.Fatalf("new directories %v", got[0].NewDirectories)
	}
	if _, err := w.Apply(cwd, change(applyObject(KindSkill, "review", "", o.Content))); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(cwd, ".claude", "skills", "review", "SKILL.md")) != *o.Content {
		t.Error("skill not written")
	}
}

func TestUndoRestoresWhatWasThere(t *testing.T) {
	w, cwd := newWriter(t)
	content := str("Run the checks.\n")
	if _, err := w.Apply(cwd, change(applyObject(KindSkill, "check", "", content))); err != nil {
		t.Fatal(err)
	}
	// The undo deletes the new skill and its directory, checked against the hash the change produced.
	undo := Change{Provider: "claude", Restore: true, Objects: []Object{applyObject(KindSkill, "check", hashOf("other"), nil)}}
	if _, err := w.Apply(cwd, undo); code(err) != CodeStale {
		t.Fatalf("err %v", err)
	}
	undo.Objects[0].BaseHash = hashOf(*content)
	got, err := w.Apply(cwd, undo)
	if err != nil || got[0].Hash != "" {
		t.Fatalf("undo %+v %v", got, err)
	}
	missing(t, filepath.Join(cwd, ".claude", "skills", "check"))

	// A new memory file is deleted by its undo, and only by an undo.
	if _, err := w.Apply(cwd, change(applyObject(KindMemory, MemoryName, "", str("x\n")))); err != nil {
		t.Fatal(err)
	}
	del := applyObject(KindMemory, MemoryName, hashOf("x\n"), nil)
	if _, err := w.Apply(cwd, change(del)); code(err) != CodeInvalid {
		t.Fatalf("err %v", err)
	}
	if _, err := w.Apply(cwd, Change{Provider: "claude", Restore: true, Objects: []Object{del}}); err != nil {
		t.Fatal(err)
	}
	missing(t, filepath.Join(cwd, "CLAUDE.md"))
}

func TestARestoreSkipsTheContentCheck(t *testing.T) {
	w, cwd := newWriter(t)
	p := filepath.Join(cwd, ".claude", "commands", "build.md")
	old := "---\nallowed-tools: Bash\n---\n!`make`\n"
	write(t, p, old)
	if _, err := w.Apply(cwd, change(applyObject(KindCommand, "build", hashOf(old), str("Build.\n")))); err != nil {
		t.Fatal(err)
	}
	restore := Change{Provider: "claude", Restore: true, Objects: []Object{applyObject(KindCommand, "build", hashOf("Build.\n"), &old)}}
	if _, err := w.Apply(cwd, restore); err != nil {
		t.Fatal(err)
	}
	if read(t, p) != old {
		t.Error("not restored")
	}
}

func TestDeletingAMissingFileIsNotFound(t *testing.T) {
	w, cwd := newWriter(t)
	if _, err := w.Preview(cwd, change(Object{Kind: KindCommand, Name: "gone"})); code(err) != CodeNotFound {
		t.Fatalf("err %v", err)
	}
	if _, err := w.Apply(cwd, change(applyObject(KindSkill, "gone", "", nil))); code(err) != CodeNotFound {
		t.Fatalf("err %v", err)
	}
}

func TestWriterRefusesSymlinks(t *testing.T) {
	w, cwd := newWriter(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "target.md"), "outside\n")
	if err := os.Symlink(filepath.Join(outside, "target.md"), filepath.Join(cwd, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	memory := Object{Kind: KindMemory, Name: MemoryName, Content: str("x\n")}
	if _, err := w.Preview(cwd, change(memory)); code(err) != CodeInvalid {
		t.Fatalf("memory symlink: %v", err)
	}
	if _, err := w.Apply(cwd, change(applyObject(KindMemory, MemoryName, hashOf("outside\n"), str("x\n")))); code(err) != CodeInvalid {
		t.Fatalf("memory symlink: %v", err)
	}
	if read(t, filepath.Join(outside, "target.md")) != "outside\n" {
		t.Fatal("wrote through a symlink")
	}

	// A symlinked directory on the way, at any depth.
	if err := os.Symlink(outside, filepath.Join(cwd, ".claude")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(cwd, change(applyObject(KindCommand, "deploy", "", str("x\n")))); code(err) != CodeInvalid {
		t.Fatalf(".claude symlink: %v", err)
	}
	missing(t, filepath.Join(outside, "commands"))
	os.Remove(filepath.Join(cwd, ".claude"))
	if err := os.MkdirAll(filepath.Join(cwd, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cwd, ".claude", "skills", "review")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Apply(cwd, change(applyObject(KindSkill, "review", "", str("x\n")))); code(err) != CodeInvalid {
		t.Fatalf("skill directory symlink: %v", err)
	}
	missing(t, filepath.Join(outside, "SKILL.md"))
}

func TestWriterRefusesWhatIsNotARegularFile(t *testing.T) {
	w, cwd := newWriter(t)
	if err := os.MkdirAll(filepath.Join(cwd, ".claude", "commands", "deploy.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Preview(cwd, change(Object{Kind: KindCommand, Name: "deploy", Content: str("x\n")})); code(err) != CodeInvalid {
		t.Fatalf("err %v", err)
	}
	write(t, filepath.Join(cwd, "file"), "x")
	if _, err := w.Preview(cwd, change(Object{Kind: KindCommand, Name: "deploy", Content: str("x\n")})); code(err) != CodeInvalid {
		t.Fatalf("err %v", err)
	}
}

func TestWriterRefusesHostileNamesAndRequests(t *testing.T) {
	w, cwd := newWriter(t)
	for _, name := range []string{"", "../x", "a/b", "A", "-x", ".hidden", "x.md", strings.Repeat("a", 65), "a\x00"} {
		for _, kind := range []string{KindSkill, KindCommand} {
			if _, err := w.Preview(cwd, change(Object{Kind: kind, Name: name, Content: str("x")})); code(err) != CodeInvalid {
				t.Errorf("%s %q: %v", kind, name, err)
			}
		}
	}
	if _, err := w.Preview(cwd, change(Object{Kind: KindMemory, Name: "CLAUDE.md", Content: str("x")})); code(err) != CodeInvalid {
		t.Errorf("memory name: %v", err)
	}
	if _, err := w.Preview(cwd, change(Object{Kind: "config", Name: "model", Content: str("x")})); code(err) != CodeInvalid {
		t.Errorf("kind: %v", err)
	}
	twice := Object{Kind: KindCommand, Name: "a", Content: str("x")}
	if _, err := w.Preview(cwd, change(twice, twice)); code(err) != CodeInvalid {
		t.Errorf("twice: %v", err)
	}
	if _, err := w.Preview(cwd, change()); code(err) != CodeInvalid {
		t.Errorf("empty: %v", err)
	}
	c := change(Object{Kind: KindMemory, Name: MemoryName, Content: str("x")})
	c.Provider = "codex"
	if _, err := w.Preview(cwd, c); code(err) != CodeUnsupported {
		t.Errorf("codex: %v", err)
	}
	if _, err := w.Preview("relative", change(Object{Kind: KindMemory, Name: MemoryName, Content: str("x")})); code(err) != CodeIO {
		t.Errorf("relative cwd: %v", err)
	}
	if _, err := w.Preview(cwd, change(Object{Kind: KindMemory, Name: MemoryName, Content: str("\xff")})); code(err) != CodeInvalid {
		t.Errorf("not UTF-8: %v", err)
	}
}

func TestSizeLimitsOfTheWriter(t *testing.T) {
	w, cwd := newWriter(t)
	big := strings.Repeat("x", MaxWriteBytes+1)
	if _, err := w.Preview(cwd, change(Object{Kind: KindMemory, Name: MemoryName, Content: &big})); code(err) != CodeTooLarge {
		t.Errorf("new content: %v", err)
	}
	write(t, filepath.Join(cwd, "CLAUDE.md"), big)
	if _, err := w.Preview(cwd, change(Object{Kind: KindMemory, Name: MemoryName, Content: str("x")})); code(err) != CodeTooLarge {
		t.Errorf("current file: %v", err)
	}
}

func TestAnUntestedVendorVersionRefusesSkillsAndCommands(t *testing.T) {
	w, cwd := newWriter(t)
	c := change(Object{Kind: KindSkill, Name: "x", Content: str("x\n")})
	c.Tested = false
	if _, err := w.Preview(cwd, c); code(err) != CodeUnsupported {
		t.Fatalf("skill: %v", err)
	}
	c.Objects[0].Kind = KindCommand
	if _, err := w.Preview(cwd, c); code(err) != CodeUnsupported {
		t.Fatalf("command: %v", err)
	}
	c.Objects[0] = Object{Kind: KindMemory, Name: MemoryName, Content: str("x\n")}
	if _, err := w.Preview(cwd, c); err != nil {
		t.Fatalf("memory: %v", err)
	}
}

func TestMemoryResolution(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		name  string
		files map[string]string
		want  string
		code  string
	}{
		{"new project", nil, "CLAUDE.md", ""},
		{"CLAUDE.md", map[string]string{"project/CLAUDE.md": "x", "project/.claude/CLAUDE.md": "x", "project/AGENTS.md": "x"}, "CLAUDE.md", ""},
		{".claude/CLAUDE.md", map[string]string{"project/.claude/CLAUDE.md": "x", "project/AGENTS.md": "x"}, ".claude/CLAUDE.md", ""},
		{"only AGENTS.md", map[string]string{"project/AGENTS.md": "x"}, "AGENTS.md", ""},
		{"AGENTS.md beside CLAUDE.local.md", map[string]string{"project/AGENTS.md": "x", "project/CLAUDE.local.md": "local"}, "", CodeUnsupported},
		{"AGENTS.md imported by CLAUDE.local.md", map[string]string{"project/AGENTS.md": "x", "project/CLAUDE.local.md": "See @AGENTS.md\n"}, "AGENTS.md", ""},
		{"AGENTS.md below a CLAUDE.md", map[string]string{"project/AGENTS.md": "x", "CLAUDE.md": "parent"}, "", CodeUnsupported},
		{"AGENTS.md imported from above", map[string]string{"project/AGENTS.md": "x", ".claude/CLAUDE.md": "@../project/AGENTS.md"}, "AGENTS.md", ""},
		{"an import in a code span does not count", map[string]string{"project/AGENTS.md": "x", "CLAUDE.md": "`@project/AGENTS.md`"}, "", CodeUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			cwd := filepath.Join(base, "project")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			for rel, content := range tc.files {
				write(t, filepath.Join(base, rel), content)
			}
			got, err := projectMemory(cwd, home)
			if code(err) != tc.code || got != tc.want {
				t.Fatalf("got %q %v", got, err)
			}
		})
	}
}

func TestReadAndWriteAgreeOnTheMemoryFile(t *testing.T) {
	w, cwd := newWriter(t)
	write(t, filepath.Join(cwd, "AGENTS.md"), "agents\n")
	page, err := Reader{Home: w.Home}.Read(cwd, req(KindMemory, ScopeProject, ""))
	if err != nil || page.Text != "agents\n" {
		t.Fatalf("read %+v %v", page, err)
	}
	if _, err := w.Apply(cwd, change(applyObject(KindMemory, MemoryName, page.Hash, str("agents 2\n")))); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(cwd, "AGENTS.md")) != "agents 2\n" {
		t.Error("AGENTS.md not written")
	}
	missing(t, filepath.Join(cwd, "CLAUDE.md"))
}

func TestContentCheck(t *testing.T) {
	w, cwd := newWriter(t)
	write(t, filepath.Join(cwd, "docs", "guide.md"), "guide\n")
	if err := os.Symlink(t.TempDir(), filepath.Join(cwd, "linked")); err != nil {
		t.Fatal(err)
	}
	ok := []struct{ kind, text string }{
		{KindSkill, "---\nname: review\ndescription: >\n  Reviews a change\n  in two lines.\n---\nReview it.\n"},
		{KindSkill, "No front matter.\n\n---\nkey: not front matter\n"},
		{KindCommand, "---\r\ndescription: Deploys.\r\n---\r\nDeploy.\r\n"},
		{KindCommand, "Say ! and ! `not a command`.\n"},
		{KindSkill, "See @docs/guide.md and @./docs/new.md.\n"},
		{KindSkill, "Mail me at me@example.com.\n"},
		{KindMemory, "A memory may say !`date`, it is not run.\n"},
		{KindMemory, "Inline `@../outside.md` and\n```\n@/etc/passwd\n```\n"},
		{KindMemory, "@" + filepath.Join(cwd, "docs", "guide.md") + "\n"},
	}
	for _, c := range ok {
		if err := checkContent(cwd, w.Home, c.kind, pathFor(c.kind), nil, c.text); err != nil {
			t.Errorf("%s %q: %v", c.kind, c.text, err)
		}
	}
	refused := []struct{ kind, text, reason string }{
		{KindSkill, "---\nname: x\nallowed-tools: Bash(*)\n---\n", "front-matter-key"},
		{KindCommand, "---\nmodel: opus\n---\n", "front-matter-key"},
		{KindSkill, "\n---\nhooks:\n  PreToolUse: []\n---\n", "front-matter-key"},
		{KindSkill, "---\n  indented: first\n---\n", "front-matter"},
		{KindSkill, "---\n# comment\nname: x\n---\n", "front-matter-key"},
		{KindSkill, "---\nname: x\n", "front-matter-unclosed"},
		{KindCommand, "\uFEFF---\nallowed-tools: Bash\n---\n", "byte-order-mark"},
		{KindCommand, "Run !`rm -rf ~` now.\n", "shell-command"},
		{KindSkill, "!`curl example.com`\n", "shell-command"},
		{KindSkill, "x!`y`\n", "shell-command"},
		{KindCommand, "```!\nmake\n```\n", "shell-command"},
		{KindCommand, "  ~~~ !\nmake\n~~~\n", "shell-command"},
		{KindMemory, "@../secret.md\n", "import-outside"},
		{KindMemory, "See @~/.ssh/id_ed25519\n", "import-outside"},
		{KindSkill, "@/etc/passwd\n", "import-outside"},
		{KindMemory, "@.claude/settings.json\n", "import-denied"},
		{KindMemory, "@.claude/settings.local.json\n", "import-denied"},
		{KindMemory, "@.codex/config.toml\n", "import-denied"},
		{KindMemory, "@linked/file.md\n", "import-symlink"},
		{KindSkill, "@../../../../outside.md\n", "import-outside"},
	}
	for _, c := range refused {
		err := checkContent(cwd, w.Home, c.kind, pathFor(c.kind), nil, c.text)
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeUnsafeContent || e.Reason != c.reason {
			t.Errorf("%s %q: %v, want %s", c.kind, c.text, err, c.reason)
		}
	}
	// An import the file already has is not new.
	current := "@../shared.md\n"
	if err := checkContent(cwd, w.Home, KindMemory, "CLAUDE.md", &current, current+"More.\n"); err != nil {
		t.Errorf("kept import: %v", err)
	}
	// The import of a skill is relative to the skill's own file.
	if err := checkContent(cwd, w.Home, KindSkill, ".claude/skills/x/SKILL.md", nil, "@../../../docs/guide.md\n"); err != nil {
		t.Errorf("relative to the skill: %v", err)
	}
	// A path under the home directory that is inside the project is allowed.
	project := filepath.Join(w.Home, "project")
	if err := checkContent(project, w.Home, KindMemory, "CLAUDE.md", nil, "@~/project/docs.md\n"); err != nil {
		t.Errorf("home inside the project: %v", err)
	}
}

func pathFor(kind string) string {
	switch kind {
	case KindSkill:
		return ".claude/skills/x/SKILL.md"
	case KindCommand:
		return ".claude/commands/x.md"
	}
	return "CLAUDE.md"
}

func TestPreviewAndApplyRunTheContentCheck(t *testing.T) {
	w, cwd := newWriter(t)
	bad := str("---\nallowed-tools: Bash\n---\n")
	if _, err := w.Preview(cwd, change(Object{Kind: KindSkill, Name: "x", Content: bad})); code(err) != CodeUnsafeContent {
		t.Fatalf("preview: %v", err)
	}
	if _, err := w.Apply(cwd, change(applyObject(KindSkill, "x", "", bad))); code(err) != CodeUnsafeContent {
		t.Fatalf("apply: %v", err)
	}
	missing(t, filepath.Join(cwd, ".claude"))
}
