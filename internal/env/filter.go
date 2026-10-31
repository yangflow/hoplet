package env

import (
	"net/url"
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
var credentialSuffixes = []string{"_KEY", "_PAT", "_DSN", "_AUTH", "_PWD"}

// credentialPrefixes are name beginnings of variables that hold secrets
// (the 1Password CLI names its session token OP_SESSION_<account>).
var credentialPrefixes = []string{"OP_SESSION_", "BW_SESSION"}

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
	for _, p := range credentialPrefixes {
		if strings.HasPrefix(up, p) {
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

// hasUserinfo reports whether a proxy or no-proxy value may carry a
// password: any "@" counts, however the URL is written, because a password
// may hold unencoded "/", "?" or "#". A stray "@" only costs one extra read
// of the shell.
func hasUserinfo(v string) bool { return strings.Contains(v, "@") }

// urlPassword reports whether v is a URL that carries a password
// (postgres://user:pw@host/db). A value with "://" and "@" that does not
// parse counts, since a password may hold characters a URL cannot.
func urlPassword(v string) bool {
	if !strings.Contains(v, "://") || !strings.Contains(v, "@") {
		return false
	}
	u, err := url.Parse(v)
	if err != nil {
		return true
	}
	_, has := u.User.Password()
	return has
}

// stripSecrets removes the variables that are secrets by their value from
// vars and reports whether it removed any: proxy and no-proxy variables
// that carry user information, and any variable whose value is a URL with
// a password. Such a value is used from memory and never written to disk.
func stripSecrets(vars map[string]string) bool {
	removed := false
	for k, v := range vars {
		if (slices.Contains(proxyVars, k) || slices.Contains(noProxyVars, k)) && hasUserinfo(v) || urlPassword(v) {
			delete(vars, k)
			removed = true
		}
	}
	return removed
}
