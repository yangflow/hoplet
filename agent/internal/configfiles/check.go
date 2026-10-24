package configfiles

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// The content check of ADR 0036, run on the new text of every object before a preview and again before an
// apply. A refusal is "unsafe-content" with the rule that failed as the reason.

// frontMatterKeys are the only keys a skill or a command may set: anything else (allowed-tools, model, hooks,
// disable-model-invocation, ...) could give the file a power the user does not see in its text.
var frontMatterKeys = map[string]bool{"name": true, "description": true}

var (
	// frontMatterKey is a top-level key line of a YAML block.
	frontMatterKey = regexp.MustCompile(`^([A-Za-z0-9_-]+)\s*:`)
	// shellFence opens a fenced block whose content the vendor runs (```! or ~~~!).
	shellFence = regexp.MustCompile("^\\s*(```|~~~)\\s*!")
	// fence opens or closes a fenced code block.
	fence = regexp.MustCompile("^\\s*(```|~~~)")
	// codeSpan is an inline code span; imports inside one are not imports.
	codeSpan = regexp.MustCompile("`[^`\n]*`")
	// importToken is an `@path` import: at the start of a line or after white space.
	importToken = regexp.MustCompile(`(?:^|\s)@([^\s` + "`" + `]+)`)
)

// floorFiles are the files the compiled-in floor keeps away from sessions (ADR 0029), by their path below the
// working directory; an import may not point at them either.
var floorFiles = []string{".codex/config.toml"}

func floorDenied(rel string) bool {
	for _, f := range floorFiles {
		if rel == f {
			return true
		}
	}
	dir, base := path.Split(rel)
	return dir == ".claude/" && strings.HasPrefix(base, "settings") && strings.HasSuffix(base, ".json")
}

func unsafe(reason string) error { return refuse(CodeUnsafeContent, reason) }

// checkFrontMatter refuses a skill or a command whose front matter sets a key other than name and description,
// or that the vendor might read differently from this check: an unclosed block, a byte-order mark before it.
func checkFrontMatter(text string) error {
	if strings.HasPrefix(text, "\uFEFF") {
		return unsafe("byte-order-mark")
	}
	lines := strings.Split(text, "\n")
	first := 0
	for first < len(lines) && strings.TrimSpace(lines[first]) == "" {
		first++
	}
	if first == len(lines) || strings.TrimSpace(lines[first]) != "---" {
		return nil
	}
	keyed := false
	for _, line := range lines[first+1:] {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "---":
			return nil
		case trimmed == "":
			continue
		case line[0] == ' ' || line[0] == '\t':
			// A continuation of the value above.
			if !keyed {
				return unsafe("front-matter")
			}
		default:
			m := frontMatterKey.FindStringSubmatch(line)
			if m == nil || !frontMatterKeys[m[1]] {
				return unsafe("front-matter-key")
			}
			keyed = true
		}
	}
	return unsafe("front-matter-unclosed")
}

// checkShell refuses the forms the vendor runs as a command when it loads a skill or a command: !`cmd` and a
// fenced block opened with ```!. Any !` is refused, not only one at the start of a line or after a space.
func checkShell(text string) error {
	if strings.Contains(text, "!`") {
		return unsafe("shell-command")
	}
	for _, line := range strings.Split(text, "\n") {
		if shellFence.MatchString(line) {
			return unsafe("shell-command")
		}
	}
	return nil
}

// imports lists the `@path` imports of text, outside fenced blocks and code spans.
func imports(text string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if fence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		line = codeSpan.ReplaceAllString(line, " ")
		for _, m := range importToken.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// checkImports refuses an import the new text adds (one the current text does not have) that points outside
// the working directory, through a symlink, or at a file of the floor. rel is the file's path below cwd.
func checkImports(cwd, home, rel string, current *string, text string) error {
	known := map[string]bool{}
	if current != nil {
		for _, imp := range imports(*current) {
			known[imp] = true
		}
	}
	for _, imp := range imports(text) {
		if known[imp] {
			continue
		}
		target, ok := importTarget(cwd, home, rel, imp)
		if !ok {
			return unsafe("import-outside")
		}
		if floorDenied(target) {
			return unsafe("import-denied")
		}
		if linked(cwd, target) {
			return unsafe("import-symlink")
		}
	}
	return nil
}

// importTarget resolves an import the way the vendor does (relative to the importing file, `~` to the home
// directory) and returns it relative to cwd; false when it is not inside cwd.
func importTarget(cwd, home, rel, imp string) (string, bool) {
	var abs string
	switch {
	case imp == "~" || strings.HasPrefix(imp, "~/"):
		if home == "" {
			return "", false
		}
		abs = filepath.Join(home, strings.TrimPrefix(imp, "~"))
	case filepath.IsAbs(imp):
		abs = filepath.Clean(imp)
	default:
		abs = filepath.Join(cwd, filepath.Dir(filepath.FromSlash(rel)), imp)
	}
	inside, err := filepath.Rel(cwd, abs)
	if err != nil || inside == "." || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(inside), true
}

// linked reports whether a component of rel below cwd that exists is a symlink or cannot be looked at.
func linked(cwd, rel string) bool {
	parts := strings.Split(rel, "/")
	for i := range parts {
		info, err := os.Lstat(filepath.Join(cwd, filepath.FromSlash(path.Join(parts[:i+1]...))))
		if errors.Is(err, fs.ErrNotExist) {
			return false
		}
		if err != nil || info.Mode()&fs.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// checkContent runs the rules of the object's kind on its new text.
func checkContent(cwd, home, kind, rel string, current *string, text string) error {
	if kind == KindSkill || kind == KindCommand {
		if err := checkFrontMatter(text); err != nil {
			return err
		}
		if err := checkShell(text); err != nil {
			return err
		}
	}
	return checkImports(cwd, home, rel, current, text)
}
