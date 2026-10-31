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
}

// shellOnly are variables that describe the shell process itself, not the
// user's environment.
var shellOnly = []string{"PWD", "OLDPWD", "SHLVL", "_"}

// IsCredential reports whether a variable name looks like it holds a
// secret.
func IsCredential(name string) bool {
	up := strings.ToUpper(name)
	for _, p := range credentialParts {
		if strings.Contains(up, p) {
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
