package memory

import "regexp"

var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{20,}|ghp_[A-Za-z0-9]{20,}|gho_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{30,}|xox[abp]-[A-Za-z0-9-]{10,})`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)(?:password|passwd|token|secret|api[_-]?key|密码|口令|密钥)\s*(?::|=|：|是|为)\s*\S{6,}`),
}

// hasCredential reports whether text looks like it holds a credential.
func hasCredential(text string) bool {
	for _, p := range credentialPatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}
