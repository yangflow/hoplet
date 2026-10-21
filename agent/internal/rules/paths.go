package rules

import (
	"os"
	"path/filepath"
	"strings"
)

// physical returns the absolute path raw names after the file system has resolved it: symlinks
// are followed, ".." is applied to the resolved parent, and the part that does not exist yet is
// appended after its nearest existing parent.
func physical(raw string) string {
	queue := strings.Split(raw, "/")
	cur := "/"
	hops := 0
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		if fi, err := os.Lstat(next); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			hops++
			if hops > 255 {
				return filepath.Clean(raw)
			}
			t, err := os.Readlink(next)
			if err != nil {
				cur = next
				continue
			}
			if strings.HasPrefix(t, "/") {
				cur = "/"
			}
			queue = append(strings.Split(t, "/"), queue...)
			continue
		}
		cur = next
	}
	return cur
}

// ResolvePath makes p absolute against cwd (a leading ~ is the home directory) and returns the
// cleaned path as written and the path with symlinks resolved.
func ResolvePath(p, cwd, home string) (written, resolved string) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home != "" {
			p = home + p[1:]
		}
	}
	raw := p
	if !strings.HasPrefix(p, "/") {
		raw = cwd + "/" + p
		if !strings.HasPrefix(raw, "/") {
			raw = "/" + raw
		}
	}
	return filepath.Clean(raw), physical(raw)
}

func splitSegs(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}

// globMatch matches whole path segments: `*` inside a segment, `**` for any number of segments
// (also none, so `/a/**` matches `/a`).
func globMatch(pat, path []string, fold bool) bool {
	for len(pat) > 0 && pat[0] == "**" {
		for len(pat) > 1 && pat[1] == "**" {
			pat = pat[1:]
		}
		if len(pat) == 1 {
			return true
		}
		for k := 0; k <= len(path); k++ {
			if globMatch(pat[1:], path[k:], fold) {
				return true
			}
		}
		return false
	}
	if len(pat) == 0 {
		return len(path) == 0
	}
	if len(path) == 0 || !wildcard(pat[0], path[0], fold) {
		return false
	}
	return globMatch(pat[1:], path[1:], fold)
}

// wildcard matches s against pat where `*` is any run of characters.
func wildcard(pat, s string, fold bool) bool {
	if fold {
		pat, s = strings.ToLower(pat), strings.ToLower(s)
	}
	p, i := 0, 0
	star, mark := -1, 0
	for i < len(s) {
		switch {
		case p < len(pat) && pat[p] == '*':
			star, mark = p, i
			p++
		case p < len(pat) && pat[p] == s[i]:
			p++
			i++
		case star >= 0:
			p = star + 1
			mark++
			i = mark
		default:
			return false
		}
	}
	for p < len(pat) && pat[p] == '*' {
		p++
	}
	return p == len(pat)
}
