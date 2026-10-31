package env

import (
	"slices"
	"strings"
)

// credentialParts are name fragments of variables that hold secrets; such
// variables are withheld from harnesses unless allowed. Harnesses keep their
// own credentials in their own stores.
var credentialParts = []string{
	"TOKEN", "SECRET", "PASSWORD", "PASSWD", "PASSPHRASE",
	"API_KEY", "APIKEY", "ACCESS_KEY", "PRIVATE_KEY", "CREDENTIAL",
	"COOKIE",
}

// credentialSuffixes are name endings of variables that hold secrets.
var credentialSuffixes = []string{"_KEY", "_PAT", "_DSN", "_AUTH"}

// shellOnly are variables that describe the shell process itself, not the
// user's environment. TERM is one of them: the login shell has no terminal,
// so whoever owns the harness's pty sets it.
var shellOnly = []string{"PWD", "OLDPWD", "SHLVL", "_", "TERM"}

// IsCredential reports whether a variable name looks like it holds a
// secret. It is a heuristic and may withhold harmless variables (for
// example TOKENIZERS_PARALLELISM); the allow list in config.toml passes
// such a variable, and matches exact names, case-sensitively.
func IsCredential(name string) bool {
	up := strings.ToUpper(name)
	for _, p := range credentialParts {
		if strings.Contains(up, p) {
			return true
		}
	}
	for _, s := range credentialSuffixes {
		if strings.HasSuffix(up, s) {
			return true
		}
	}
	return false
}

// filter drops shell-process variables, Hoplet's own variables and
// credentials not in allow, returning what is kept and the names of the
// credentials dropped, sorted.
func filter(vars map[string]string, allow []string) (kept map[string]string, dropped []string) {
	kept = map[string]string{}
	for k, v := range vars {
		switch {
		case slices.Contains(shellOnly, k), strings.HasPrefix(k, "HOPLET_"):
		case IsCredential(k) && !slices.Contains(allow, k):
			dropped = append(dropped, k)
		default:
			kept[k] = v
		}
	}
	slices.Sort(dropped)
	return kept, dropped
}

// hasUserinfo reports whether v is, or lists, a URL carrying user
// information (user:password@host).
func hasUserinfo(v string) bool {
	if _, rest, ok := strings.Cut(v, "://"); ok {
		v = rest
	}
	authority, _, _ := strings.Cut(v, "/")
	return strings.Contains(authority, "@")
}

// stripSecrets removes the proxy and no-proxy variables whose value carries
// user information from vars and reports whether it removed any. Such a
// value is a secret: it is used from memory and never written to disk.
func stripSecrets(vars map[string]string) bool {
	removed := false
	for _, k := range append(slices.Clone(proxyVars), noProxyVars...) {
		if v, ok := vars[k]; ok && hasUserinfo(v) {
			delete(vars, k)
			removed = true
		}
	}
	return removed
}
