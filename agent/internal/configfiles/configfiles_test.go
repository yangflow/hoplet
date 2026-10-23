package configfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func newReader(t *testing.T) (Reader, string, string) {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	return Reader{Home: home}, cwd, home
}

func req(kind, scope, name string) Request {
	return Request{Provider: "claude", Kind: kind, Scope: scope, Name: name}
}

func TestReadsMemoryOfBothScopes(t *testing.T) {
	r, cwd, home := newReader(t)
	write(t, filepath.Join(cwd, "CLAUDE.md"), "project rules\n")
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), "user rules\n")

	page, err := r.Read(cwd, req(KindMemory, ScopeProject, ""))
	if err != nil || !page.Exists || page.Text != "project rules\n" || page.Size != 14 || page.NextOffset != nil {
		t.Fatalf("project memory: %+v %v", page, err)
	}
	sum := sha256.Sum256([]byte("project rules\n"))
	if page.Hash != hex.EncodeToString(sum[:]) {
		t.Errorf("hash %s", page.Hash)
	}
	page, err = r.Read(cwd, req(KindMemory, ScopeUser, ""))
	if err != nil || page.Text != "user rules\n" {
		t.Fatalf("user memory: %+v %v", page, err)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	r, cwd, _ := newReader(t)
	for _, q := range []Request{
		req(KindMemory, ScopeProject, ""),
		req(KindSkill, ScopeProject, "review"),
		req(KindCommand, ScopeUser, "ship"),
	} {
		page, err := r.Read(cwd, q)
		if err != nil || page.Exists || page.Text != "" || page.Hash != "" {
			t.Errorf("%+v: %+v %v", q, page, err)
		}
	}
	page, err := r.Read(filepath.Join(cwd, "gone"), req(KindMemory, ScopeProject, ""))
	if err != nil || page.Exists {
		t.Errorf("missing directory: %+v %v", page, err)
	}
}

func TestReadsSkillAndCommand(t *testing.T) {
	r, cwd, _ := newReader(t)
	write(t, filepath.Join(cwd, ".claude", "skills", "review", "SKILL.md"), "---\nname: review\n---\nbody")
	write(t, filepath.Join(cwd, ".claude", "commands", "ship.md"), "Ship it")
	page, err := r.Read(cwd, req(KindSkill, ScopeProject, "review"))
	if err != nil || !strings.HasSuffix(page.Text, "body") {
		t.Fatalf("skill: %+v %v", page, err)
	}
	page, err = r.Read(cwd, req(KindCommand, ScopeProject, "ship"))
	if err != nil || page.Text != "Ship it" {
		t.Fatalf("command: %+v %v", page, err)
	}
}

func TestHostileNamesAreRefused(t *testing.T) {
	r, cwd, _ := newReader(t)
	write(t, filepath.Join(cwd, "secret.md"), "secret")
	for _, name := range []string{"", "..", "../secret", "a/b", "a\\b", "A", "-a", "a.b", "a b", "a\x00b", strings.Repeat("a", 65), "../../etc/passwd", "%2e%2e"} {
		for _, kind := range []string{KindSkill, KindCommand} {
			if _, err := r.Read(cwd, req(kind, ScopeProject, name)); code(err) != CodeInvalid {
				t.Errorf("%s %q: %v", kind, name, err)
			}
		}
	}
	if _, err := r.Read(cwd, req(KindMemory, ScopeProject, "x")); code(err) != CodeInvalid {
		t.Errorf("memory with a name: %v", err)
	}
	if _, err := r.Read(cwd, req("config", ScopeProject, "")); code(err) != CodeInvalid {
		t.Errorf("unknown kind: %v", err)
	}
	if _, err := r.Read(cwd, req(KindMemory, "global", "")); code(err) != CodeInvalid {
		t.Errorf("unknown scope: %v", err)
	}
	if _, err := r.Read(cwd, Request{Provider: "claude", Kind: KindMemory, Scope: ScopeProject, Offset: -1}); code(err) != CodeInvalid {
		t.Errorf("negative offset: %v", err)
	}
	if _, err := r.Read("relative/dir", req(KindMemory, ScopeProject, "")); code(err) != CodeIO {
		t.Errorf("relative cwd: %v", err)
	}
}

func TestUnsupportedProvider(t *testing.T) {
	r, cwd, _ := newReader(t)
	_, err := r.Read(cwd, Request{Provider: "codex", Kind: KindMemory, Scope: ScopeProject})
	if code(err) != CodeUnsupported {
		t.Errorf("codex: %v", err)
	}
}

func TestSymlinksAreRefused(t *testing.T) {
	r, cwd, _ := newReader(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), "outside")

	// the file itself, pointing outside and pointing inside
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(cwd, "CLAUDE.md")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if _, err := r.Read(cwd, req(KindMemory, ScopeProject, "")); code(err) != CodeInvalid {
		t.Errorf("memory symlink out: %v", err)
	}
	write(t, filepath.Join(cwd, "real.md"), "inside")
	_ = os.Remove(filepath.Join(cwd, "CLAUDE.md"))
	if err := os.Symlink(filepath.Join(cwd, "real.md"), filepath.Join(cwd, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(cwd, req(KindMemory, ScopeProject, "")); code(err) != CodeInvalid {
		t.Errorf("memory symlink in: %v", err)
	}

	// the provider directory is a symlink out of the project
	write(t, filepath.Join(outside, "commands", "ship.md"), "outside")
	if err := os.Symlink(outside, filepath.Join(cwd, ".claude")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(cwd, req(KindCommand, ScopeProject, "ship")); code(err) != CodeInvalid {
		t.Errorf("provider directory symlink: %v", err)
	}
	page, err := r.Read(cwd, req(KindList, ScopeProject, ""))
	if err != nil || len(page.Entries) != 0 {
		t.Errorf("list through a symlinked provider directory: %+v %v", page, err)
	}
}

func TestSkillDirectorySymlinkIsRefusedAndNotListed(t *testing.T) {
	r, cwd, _ := newReader(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "SKILL.md"), "outside")
	write(t, filepath.Join(cwd, ".claude", "skills", "real", "SKILL.md"), "real")
	if err := os.Symlink(outside, filepath.Join(cwd, ".claude", "skills", "linked")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if _, err := r.Read(cwd, req(KindSkill, ScopeProject, "linked")); code(err) != CodeInvalid {
		t.Errorf("linked skill: %v", err)
	}
	page, err := r.Read(cwd, req(KindList, ScopeProject, ""))
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Name != "real" {
		t.Errorf("list: %+v %v", page, err)
	}
}

func TestNotARegularFile(t *testing.T) {
	r, cwd, _ := newReader(t)
	if err := os.MkdirAll(filepath.Join(cwd, "CLAUDE.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(cwd, req(KindMemory, ScopeProject, "")); code(err) != CodeInvalid {
		t.Errorf("directory as memory: %v", err)
	}
}

func TestSizeBounds(t *testing.T) {
	r, cwd, _ := newReader(t)

	big := strings.Repeat("a", MaxFileBytes+1)
	write(t, filepath.Join(cwd, "CLAUDE.md"), big)
	if _, err := r.Read(cwd, req(KindMemory, ScopeProject, "")); code(err) != CodeInvalid {
		t.Errorf("over the file bound: %v", err)
	}

	// a file of several pages is read page by page and reassembles
	text := strings.Repeat("0123456789", 10_000) // 100,000 bytes
	write(t, filepath.Join(cwd, "CLAUDE.md"), text)
	var got strings.Builder
	var offset int64
	pages := 0
	for {
		q := req(KindMemory, ScopeProject, "")
		q.Offset = offset
		page, err := r.Read(cwd, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Text) > PageBytes {
			t.Fatalf("page of %d bytes", len(page.Text))
		}
		if page.Offset != offset || page.Size != int64(len(text)) {
			t.Fatalf("page %+v", page)
		}
		got.WriteString(page.Text)
		pages++
		if page.NextOffset == nil {
			break
		}
		offset = *page.NextOffset
	}
	if got.String() != text || pages != 4 {
		t.Errorf("reassembled %d bytes in %d pages", got.Len(), pages)
	}

	// an offset past the end is an empty last page
	q := req(KindMemory, ScopeProject, "")
	q.Offset = 1 << 40
	page, err := r.Read(cwd, q)
	if err != nil || page.Text != "" || page.NextOffset != nil || page.Offset != int64(len(text)) {
		t.Errorf("offset past the end: %+v %v", page, err)
	}
}

func TestPagesBreakOnCharacters(t *testing.T) {
	r, cwd, _ := newReader(t)
	// three-byte characters, so a page boundary at 32 KiB falls inside one
	text := strings.Repeat("字", 20_000)
	write(t, filepath.Join(cwd, "CLAUDE.md"), text)
	var got strings.Builder
	var offset int64
	for {
		q := req(KindMemory, ScopeProject, "")
		q.Offset = offset
		page, err := r.Read(cwd, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Text) > PageBytes || strings.ContainsRune(page.Text, '�') {
			t.Fatalf("page of %d bytes: broken character", len(page.Text))
		}
		got.WriteString(page.Text)
		if page.NextOffset == nil {
			break
		}
		offset = *page.NextOffset
	}
	if got.String() != text {
		t.Errorf("reassembled %d of %d bytes", got.Len(), len(text))
	}
	// an offset inside a character moves to the next one
	q := req(KindMemory, ScopeProject, "")
	q.Offset = 1
	page, err := r.Read(cwd, q)
	if err != nil || page.Offset != 3 || !strings.HasPrefix(page.Text, "字") {
		t.Errorf("offset inside a character: offset %d %v", page.Offset, err)
	}
}

func TestBinaryFilesAreRefused(t *testing.T) {
	r, cwd, _ := newReader(t)
	write(t, filepath.Join(cwd, "CLAUDE.md"), "a\x00b")
	if _, err := r.Read(cwd, req(KindMemory, ScopeProject, "")); code(err) != CodeInvalid {
		t.Errorf("NUL: %v", err)
	}
	write(t, filepath.Join(cwd, "CLAUDE.md"), "a\xffb")
	if _, err := r.Read(cwd, req(KindMemory, ScopeProject, "")); code(err) != CodeInvalid {
		t.Errorf("invalid UTF-8: %v", err)
	}
}

func TestListNamesSkillsAndCommands(t *testing.T) {
	r, cwd, home := newReader(t)
	write(t, filepath.Join(cwd, ".claude", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: \"Review a diff\"\n---\nbody")
	write(t, filepath.Join(cwd, ".claude", "skills", "empty", "other.md"), "no skill file")
	write(t, filepath.Join(cwd, ".claude", "skills", "Bad Name", "SKILL.md"), "hostile")
	write(t, filepath.Join(cwd, ".claude", "skills", "stray.md"), "not a directory")
	write(t, filepath.Join(cwd, ".claude", "commands", "ship.md"), "# Ship it\nmore")
	write(t, filepath.Join(cwd, ".claude", "commands", "Odd.md"), "hostile")
	write(t, filepath.Join(cwd, ".claude", "commands", "notes.txt"), "not a command")
	write(t, filepath.Join(home, ".claude", "commands", "mine.md"), "user command")

	page, err := r.Read(cwd, req(KindList, ScopeProject, ""))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range page.Entries {
		names = append(names, e.Kind+":"+e.Name+":"+e.Description)
	}
	want := []string{"skill:review:Review a diff", "command:ship:Ship it"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("project list %v, want %v", names, want)
	}
	page, err = r.Read(cwd, req(KindList, ScopeUser, ""))
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Name != "mine" {
		t.Errorf("user list: %+v %v", page, err)
	}
}

func TestListBoundsEntriesAndDescriptions(t *testing.T) {
	r, cwd, _ := newReader(t)
	long := strings.Repeat("字", 400)
	write(t, filepath.Join(cwd, ".claude", "commands", "long.md"), "---\ndescription: "+long+"\n---\n")
	write(t, filepath.Join(cwd, ".claude", "commands", "ctl.md"), "---\ndescription: a\x1b[31m\x07b\n---\n")
	page, err := r.Read(cwd, req(KindList, ScopeProject, ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range page.Entries {
		if len(e.Description) > MaxDescriptionBytes || strings.ContainsAny(e.Description, "\x1b\x07\n") {
			t.Errorf("%s: %q", e.Name, e.Description)
		}
	}

	for i := 0; i < MaxEntries+50; i++ {
		write(t, filepath.Join(cwd, ".claude", "commands", "c"+strings.Repeat("a", 3)+string(rune('a'+i%26))+strings.Repeat("b", i/26)+".md"), "x")
	}
	page, err = r.Read(cwd, req(KindList, ScopeProject, ""))
	if err != nil || len(page.Entries) > MaxEntries {
		t.Errorf("%d entries: %v", len(page.Entries), err)
	}
}

func TestListWithoutDirectoriesIsEmpty(t *testing.T) {
	r, cwd, _ := newReader(t)
	page, err := r.Read(cwd, req(KindList, ScopeProject, ""))
	if err != nil || page.Entries == nil || len(page.Entries) != 0 {
		t.Errorf("%+v %v", page, err)
	}
}

func TestNoHomeRefusesTheUserScope(t *testing.T) {
	cwd := t.TempDir()
	if _, err := (Reader{}).Read(cwd, req(KindMemory, ScopeUser, "")); code(err) != CodeIO {
		t.Errorf("%v", err)
	}
}
