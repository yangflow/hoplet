package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

// detachedLen is how much of a detached HEAD's commit Branch reports.
const detachedLen = 7

// Branch returns the git branch checked out in the repository that contains dir, read from its HEAD file
// without running git: the branch name, the first 7 hex digits of the commit when HEAD is detached, or ""
// when dir is in no repository or HEAD cannot be read. A linked worktree's `.git` file points at its own HEAD.
func Branch(dir string) string {
	gitDir := findGitDir(dir)
	if gitDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(raw))
	if ref, ok := strings.CutPrefix(head, "ref:"); ok {
		ref = strings.TrimSpace(ref)
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			return name
		}
		return ref
	}
	if len(head) >= detachedLen && isHex(head) {
		return head[:detachedLen]
	}
	return ""
}

// findGitDir walks up from dir to the first `.git`: a directory is the git directory; a file names it with
// `gitdir: <path>`, relative to the file's directory when not absolute.
func findGitDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		dot := filepath.Join(abs, ".git")
		if info, err := os.Stat(dot); err == nil {
			if info.IsDir() {
				return dot
			}
			raw, err := os.ReadFile(dot)
			if err != nil {
				return ""
			}
			target, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
			if !ok {
				return ""
			}
			target = strings.TrimSpace(target)
			if !filepath.IsAbs(target) {
				target = filepath.Join(abs, target)
			}
			return target
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
