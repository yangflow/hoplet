package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	fence = "---"
	null  = "null"
)

var (
	plainPattern  = regexp.MustCompile(`^[A-Za-z0-9_:./@-]+$`)
	statusPattern = regexp.MustCompile(`^(\S+)(?:\s+#.*)?$`)
)

// Encode renders a valid entry as its Markdown file.
func Encode(e Entry) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line(fence)
	line("id: %s", e.ID)
	line("version: %d", e.Version)
	line("category: %s", e.Category)
	line("status: %s", e.Status)
	line("scope: { kind: %s, value: %s }", e.Scope.Kind, token(e.Scope.Value))
	if len(e.Evidence) == 0 {
		line("evidence: []")
	} else {
		line("evidence:")
		for _, ev := range e.Evidence {
			line("  - { kind: %s, ref: %s }", ev.Kind, token(ev.Ref))
		}
	}
	if e.ValidUntil == nil {
		line("valid_until: %s", null)
	} else {
		line("valid_until: %s", e.ValidUntil.Format(time.RFC3339Nano))
	}
	if e.ReplacedBy == "" {
		line("replaced_by: %s", null)
	} else {
		line("replaced_by: %s", e.ReplacedBy)
	}
	line("updated_at: %s", e.UpdatedAt.Format(time.RFC3339Nano))
	line(fence)
	line("%s", e.Body)
	return b.Bytes(), nil
}

// token renders a scalar: plain when it can be, otherwise a JSON string.
func token(s string) string {
	if plainPattern.MatchString(s) {
		return s
	}
	b, err := json.Marshal(s)
	if err != nil { // a string always marshals
		return `""`
	}
	return string(b)
}

// readToken parses one scalar from the start of s and returns the rest.
func readToken(s string) (string, string, error) {
	if strings.HasPrefix(s, `"`) {
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '"':
				var v string
				if err := json.Unmarshal([]byte(s[:i+1]), &v); err != nil {
					return "", "", invalid("bad quoted string %q", s[:i+1])
				}
				return v, s[i+1:], nil
			}
		}
		return "", "", invalid("unterminated string %q", s)
	}
	end := strings.IndexAny(s, ", ")
	if end < 0 {
		end = len(s)
	}
	if !plainPattern.MatchString(s[:end]) {
		return "", "", invalid("bad value %q", s)
	}
	return s[:end], s[end:], nil
}

// readPair parses `{ kind: K, <name>: V }`.
func readPair(s, name string) (string, string, error) {
	rest, ok := strings.CutPrefix(s, "{ kind: ")
	if !ok {
		return "", "", invalid("bad mapping %q", s)
	}
	k, rest, err := readToken(rest)
	if err != nil {
		return "", "", err
	}
	if rest, ok = strings.CutPrefix(rest, ", "+name+": "); !ok {
		return "", "", invalid("bad mapping %q", s)
	}
	v, rest, err := readToken(rest)
	if err != nil {
		return "", "", err
	}
	if rest != " }" {
		return "", "", invalid("bad mapping %q", s)
	}
	return k, v, nil
}

func readTime(field, s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || !strings.HasSuffix(s, "Z") {
		return time.Time{}, invalid("%s %q is not an RFC 3339 UTC time", field, s)
	}
	return t.UTC(), nil
}

// fields lists every frontmatter key; each must appear exactly once.
var fields = []string{"id", "version", "category", "status", "scope", "evidence", "valid_until", "replaced_by", "updated_at"}

// Decode parses and validates an entry file. Anything but the exact fields
// the format defines is an error.
func Decode(data []byte) (Entry, error) {
	rest, ok := strings.CutPrefix(string(data), fence+"\n")
	if !ok {
		return Entry{}, invalid("no frontmatter")
	}
	lines := strings.Split(rest, "\n")
	end := -1
	for i, l := range lines {
		if l == fence {
			end = i
			break
		}
	}
	if end < 0 {
		return Entry{}, invalid("frontmatter is not closed")
	}
	var e Entry
	seen := make(map[string]bool, len(fields))
	for i := 0; i < end; i++ {
		key, val, ok := strings.Cut(lines[i], ":")
		if !ok || key == "" || key != strings.TrimSpace(key) {
			return Entry{}, invalid("bad line %q", lines[i])
		}
		if seen[key] {
			return Entry{}, invalid("duplicate field %q", key)
		}
		seen[key] = true
		if key == "evidence" && val == "" {
			for i+1 < end && strings.HasPrefix(lines[i+1], "  - ") {
				i++
				k, r, err := readPair(strings.TrimPrefix(lines[i], "  - "), "ref")
				if err != nil {
					return Entry{}, err
				}
				e.Evidence = append(e.Evidence, Evidence{EvidenceKind(k), r})
			}
			if len(e.Evidence) == 0 {
				return Entry{}, invalid("evidence has no items")
			}
			continue
		}
		val, ok = strings.CutPrefix(val, " ")
		if !ok {
			return Entry{}, invalid("bad line %q", lines[i])
		}
		if err := setField(&e, key, val); err != nil {
			return Entry{}, err
		}
	}
	for _, f := range fields {
		if !seen[f] {
			return Entry{}, invalid("missing field %q", f)
		}
	}
	e.Body = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	if err := e.Validate(); err != nil {
		return Entry{}, err
	}
	return e, nil
}

func setField(e *Entry, key, val string) error {
	switch key {
	case "id":
		e.ID = val
	case "version":
		n, err := strconv.Atoi(val)
		if err != nil {
			return invalid("version %q", val)
		}
		e.Version = n
	case "category":
		e.Category = Category(val)
	case "status":
		m := statusPattern.FindStringSubmatch(val)
		if m == nil {
			return invalid("status %q", val)
		}
		e.Status = Status(m[1])
	case "scope":
		k, v, err := readPair(val, "value")
		if err != nil {
			return err
		}
		e.Scope = Scope{ScopeKind(k), v}
	case "evidence":
		if val != "[]" {
			return invalid("evidence %q", val)
		}
	case "valid_until":
		if val != null {
			t, err := readTime(key, val)
			if err != nil {
				return err
			}
			e.ValidUntil = &t
		}
	case "replaced_by":
		if val != null {
			e.ReplacedBy = val
		}
	case "updated_at":
		t, err := readTime(key, val)
		if err != nil {
			return err
		}
		e.UpdatedAt = t
	default:
		return invalid("unknown field %q", key)
	}
	return nil
}
