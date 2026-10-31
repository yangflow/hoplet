// Package env builds the environment harness processes run with: the
// user's login-shell environment without credentials or Hoplet internals,
// plus the proxy variables of the configured or system proxy.
package env

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"time"
)

const (
	// loginTimeout bounds how long the login shell may take to start.
	loginTimeout = 15 * time.Second
)

// sessionVars are the variables a macOS GUI session gives the daemon that
// startup files neither set nor can do without; the login shell starts with
// them (when the daemon has them) so its startup files see them and may
// override them. LC_* and XDG_* match by prefix.
var sessionVars = []string{"LANG", "TMPDIR", "SSH_AUTH_SOCK", "__CF_USER_TEXT_ENCODING"}

func isSessionVar(name string) bool {
	return slices.Contains(sessionVars, name) || strings.HasPrefix(name, "LC_") || strings.HasPrefix(name, "XDG_")
}

// parseLogin extracts the NUL-separated environment between the markers.
func parseLogin(out []byte, begin, end string) (map[string]string, error) {
	i := bytes.Index(out, []byte(begin+"\n"))
	j := bytes.LastIndex(out, []byte(end))
	if i < 0 || j < i {
		return nil, errors.New("env: the login shell printed no environment")
	}
	vars := map[string]string{}
	for _, kv := range bytes.Split(out[i+len(begin)+1:j], []byte{0}) {
		k, v, ok := strings.Cut(string(kv), "=")
		if ok && k != "" {
			vars[k] = v
		}
	}
	return vars, nil
}

// lastLine returns the last line of the shell's stderr for an error
// message. A line that mentions a credential-looking variable (startup
// files run with xtrace print their assignments) is replaced by a neutral
// message, because the error reaches the log and the CLI.
func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	line := lines[len(lines)-1]
	for _, tok := range strings.Fields(line) {
		name, _, ok := strings.Cut(tok, "=")
		if ok && IsCredential(strings.TrimLeft(name, "+-'\"$`(<>{}[],;:")) {
			return "(the shell's error message mentions a credential and is hidden)"
		}
	}
	return line
}
