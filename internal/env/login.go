// Package env builds the environment harness processes run with: the
// user's login-shell environment without credentials or Hoplet internals,
// plus the proxy variables of the configured or system proxy.
package env

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strings"
	"syscall"
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

// login starts shell as an interactive login shell in home and returns the
// environment its startup files build. The shell starts from a fixed base
// (HOME, SHELL, PATH, USER, LOGNAME, TERM=dumb) plus the session variables
// the calling process has (LANG, LC_*, TMPDIR, SSH_AUTH_SOCK, XDG_*,
// __CF_USER_TEXT_ENCODING), so nothing else of the caller's leaks in;
// output the startup files print around the environment is ignored.
//
// The shell runs with -l -i -c, which bash, zsh, fish and dash-compatible
// shells accept; tcsh, nushell and shells without -l or -i are not
// supported, and the error then shows the shell's last stderr line. The
// process group is killed only on timeout: agents that startup files start
// (ssh-agent, gpg-agent) survive a normal exit, as they would for a
// terminal.
func login(ctx context.Context, shell, home string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	nonce := rand.Text()
	begin, end := "__HOPLET_ENV_BEGIN_"+nonce+"__", "__HOPLET_ENV_END_"+nonce+"__"
	script := "printf '%s\\n' " + begin + "; /usr/bin/env -0; printf '%s' " + end
	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", script)
	base := []string{"HOME=" + home, "SHELL=" + shell, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}
	if u, err := user.Current(); err == nil {
		base = append(base, "USER="+u.Username, "LOGNAME="+u.Username)
	}
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); isSessionVar(k) {
			base = append(base, kv)
		}
	}
	cmd.Env = base
	cmd.Dir = home
	// Its own session: no controlling terminal to read from, and the whole
	// group can be killed if a startup file hangs.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	vars, perr := parseLogin(out, begin, end)
	if perr == nil {
		// Startup files that end with an error still built the environment.
		return vars, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("env: %s did not finish starting within %v", shell, loginTimeout)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("env: run %s: %w: %s", shell, err, lastLine(ee.Stderr))
		}
		return nil, fmt.Errorf("env: run %s: %w", shell, err)
	}
	return nil, perr
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

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return lines[len(lines)-1]
}
