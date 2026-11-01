// Package scrub replaces personal paths in recorded harness sessions so
// they can be committed as test fixtures.
package scrub

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// New returns the function that replaces dir and home in a line. For
// each it covers the given and the symlink-resolved path (macOS temporary
// directories live under /var, a link to /private/var) and the dash slug
// claude derives project directory names from. Longer paths go first, as
// dir may be inside home.
func New(dir, home string) func(string) string {
	type pair struct{ from, to string }
	var pairs []pair
	add := func(p, to string) {
		if p == "" {
			return
		}
		forms := []string{p}
		if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
			forms = append(forms, r)
		}
		for _, f := range forms {
			pairs = append(pairs, pair{f, to})
			for _, slug := range []func(string) string{
				func(p string) string { return strings.ReplaceAll(p, "/", "-") },
				func(p string) string { // claude's project directory names
					return strings.Map(func(r rune) rune {
						if IsAlnum(r) {
							return r
						}
						return '-'
					}, p)
				},
			} {
				if from := slug(f); from != to && !slices.ContainsFunc(pairs, func(p pair) bool { return p.from == from }) {
					pairs = append(pairs, pair{from, slug(to)})
				}
			}
		}
	}
	add(dir, "/work")
	add(home, "/Users/me")
	slices.SortStableFunc(pairs, func(a, b pair) int { return len(b.from) - len(a.from) })
	return func(s string) string {
		for _, p := range pairs {
			s = ReplacePath(s, p.from, p.to)
		}
		return s
	}
}

// ReplacePath replaces the occurrences of the path from in s, except those
// followed by an ASCII letter or digit, which continue into a longer name:
// from "/Users/ann" leaves "/Users/anna/x" alone. Anything else after
// it (a slash, a quote, a period, "-", "_", the end) ends the path; erring
// toward scrubbing too much is intended.
func ReplacePath(s, from, to string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, from)
		if i < 0 {
			break
		}
		end := i + len(from)
		b.WriteString(s[:i])
		if end < len(s) && IsAlnum(rune(s[end])) {
			b.WriteString(from) // part of a longer name
		} else {
			b.WriteString(to)
		}
		s = s[end:]
	}
	b.WriteString(s)
	return b.String()
}

// IsAlnum reports whether c is an ASCII letter or digit.
func IsAlnum(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// StripKey removes key from every object inside v, in place.
func StripKey(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if k == key {
				delete(x, k)
				continue
			}
			x[k] = StripKey(e, key)
		}
	case []any:
		for i, e := range x {
			x[i] = StripKey(e, key)
		}
	}
	return v
}

// Word returns the function that replaces word, wherever it stands as a
// whole word, with to: not part of a longer run of letters, digits or
// underscores. It also matches right after a JSON escape (`\n`, `\t`, `\"`, `\u201c`),
// as recorded lines are JSON text. A word shorter than three characters
// would also match unrelated text, so it replaces nothing.
func Word(word, to string) func(string) string {
	if len(word) < 3 {
		return func(s string) string { return s }
	}
	re := regexp.MustCompile(`(^|[^A-Za-z0-9_]|\\[nrtbf"/\\]|\\u[0-9a-fA-F]{4})` + regexp.QuoteMeta(word) + `\b`)
	return func(s string) string { return re.ReplaceAllString(s, "${1}"+strings.ReplaceAll(to, "$", "$$")) }
}
