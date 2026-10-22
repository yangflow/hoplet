package mcp

import "regexp"

// mask replaces a credential. The shapes mirror core/src/scrub: the agent applies them to a tool result before it
// leaves, so a token that reached a conversation never reaches another one (ADR 0034).
const mask = "***"

var scrubRules = []struct {
	re *regexp.Regexp
	to string
}{
	{regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`), "${1}" + mask + "@"},
	{regexp.MustCompile(`(?i)(\bBearer\s+)[^\s"'\\]+`), "${1}" + mask},
	{regexp.MustCompile(`(?i)([?&](?:token|ticket|key|secret|password|sig|signature|access_token|api_key)=)[^&\s"'\\]+`), "${1}" + mask},
	{regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}`), mask},
	{regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{16,}`), mask},
	{regexp.MustCompile(`\bhpt_[A-Za-z0-9_-]{16,}`), mask},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), mask},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), mask},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*`), mask},
}

// scrub masks the credential shapes in s.
func scrub(s string) string {
	for _, r := range scrubRules {
		s = r.re.ReplaceAllString(s, r.to)
	}
	return s
}
