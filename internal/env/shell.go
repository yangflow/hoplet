package env

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/user"
	"runtime"
	"strings"
)

// UserShell returns the login shell to read the environment from: the user's
// shell from the user database (the directory service on macOS, the
// passwd file elsewhere), else $SHELL, else /bin/zsh. The daemon runs under
// launchd or systemd, whose $SHELL need not be the user's shell. Only an
// absolute path to an executable file is accepted from the sources. Linux
// reads /etc/passwd only, so accounts that come from NSS or LDAP fall back
// to $SHELL.
func UserShell(ctx context.Context) string {
	if u, err := user.Current(); err == nil {
		var s string
		if runtime.GOOS == "darwin" {
			s = dsclShell(ctx, u.Username)
		} else {
			s = passwdShell(u.Username)
		}
		if usableShell(s) {
			return s
		}
	}
	if s := os.Getenv("SHELL"); usableShell(s) {
		return s
	}
	return "/bin/zsh"
}

func passwdShell(username string) string {
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	return parsePasswd(b, username)
}

// parseDscl reads the shell from `dscl . -read /Users/<name> UserShell`
// output ("UserShell: /bin/zsh", the value possibly on the next line).
func parseDscl(out []byte) string {
	_, rest, ok := strings.Cut(string(out), "UserShell:")
	if !ok {
		return ""
	}
	if f := strings.Fields(rest); len(f) > 0 {
		return f[0]
	}
	return ""
}

// parsePasswd returns the shell, the seventh field, of username's line in
// passwd file content.
func parsePasswd(b []byte, username string) string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) >= 7 && f[0] == username {
			return strings.TrimSpace(f[6])
		}
	}
	return ""
}
