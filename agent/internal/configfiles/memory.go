package configfiles

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The project memory file is the one the vendor reads, not a fixed name (ADR 0036, spike): Claude Code reads
// CLAUDE.md or .claude/CLAUDE.md, and AGENTS.md in a project that has neither.

const agentsFile = "AGENTS.md"

// claudeShadows are the files of a directory that Claude Code reads as memory beside or instead of AGENTS.md.
var claudeShadows = []string{"CLAUDE.md", ".claude/CLAUDE.md", "CLAUDE.local.md"}

// projectMemory is the memory file of the project in cwd, relative to cwd: an existing CLAUDE.md, else an
// existing .claude/CLAUDE.md, else an existing AGENTS.md, else a new CLAUDE.md. AGENTS.md is refused
// ("unsupported") when a CLAUDE.local.md here or a memory file of a directory above would be read and none of
// them imports it: writing it would have no effect. home's own .claude/CLAUDE.md is the user's memory and is
// not a project's.
func projectMemory(cwd, home string) (string, error) {
	if !filepath.IsAbs(cwd) {
		return "", refuse(CodeIO, "the session has no working directory")
	}
	for _, name := range []string{"CLAUDE.md", ".claude/CLAUDE.md"} {
		if present(filepath.Join(cwd, name)) {
			return name, nil
		}
	}
	if !present(filepath.Join(cwd, agentsFile)) {
		return "CLAUDE.md", nil
	}
	agents := filepath.Join(cwd, agentsFile)
	var shadows []string
	if p := filepath.Join(cwd, "CLAUDE.local.md"); present(p) {
		shadows = append(shadows, p)
	}
	for dir := filepath.Dir(cwd); ; dir = filepath.Dir(dir) {
		for _, name := range claudeShadows {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if home != "" && p == filepath.Join(home, ".claude", "CLAUDE.md") {
				continue
			}
			if present(p) {
				shadows = append(shadows, p)
			}
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	if len(shadows) == 0 {
		return agentsFile, nil
	}
	for _, p := range shadows {
		if importsFile(p, home, agents) {
			return agentsFile, nil
		}
	}
	return "", refuse(CodeUnsupported, "the project has a CLAUDE.md that does not import AGENTS.md")
}

// present reports whether something is at p, a symlink included.
func present(p string) bool {
	_, err := os.Lstat(p)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// importsFile reports whether the regular file p imports target.
func importsFile(p, home, target string) bool {
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return false
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxFileBytes))
	if err != nil {
		return false
	}
	dir := filepath.Dir(p)
	for _, imp := range imports(string(raw)) {
		var abs string
		switch {
		case imp == "~" || strings.HasPrefix(imp, "~/"):
			if home == "" {
				continue
			}
			abs = filepath.Join(home, strings.TrimPrefix(imp, "~"))
		case filepath.IsAbs(imp):
			abs = filepath.Clean(imp)
		default:
			abs = filepath.Join(dir, imp)
		}
		if abs == target {
			return true
		}
	}
	return false
}
