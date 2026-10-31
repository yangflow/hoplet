// Package env builds the environment harness processes run with: the
// user's login-shell environment without credentials or Hoplet internals,
// plus the proxy variables of the configured or system proxy.
package env

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"time"
)

const (
	beginMarker = "__HOPLET_ENV_BEGIN__"
	endMarker   = "__HOPLET_ENV_END__"
	// loginTimeout bounds how long the login shell may take to start.
	loginTimeout = 15 * time.Second
)

// DefaultShell returns the login shell to read the environment from:
// $SHELL, else /bin/zsh.
func DefaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}

// login starts shell as an interactive login shell in home and returns the
// environment its startup files build. The shell starts from a minimal
// environment, so nothing of the caller's leaks in; output the startup
// files print around the environment is ignored.
func login(ctx context.Context, shell, home string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	script := "printf '%s\\n' " + beginMarker + "; /usr/bin/env -0; printf '%s' " + endMarker
	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", script)
	base := []string{"HOME=" + home, "SHELL=" + shell, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb"}
	if u, err := user.Current(); err == nil {
		base = append(base, "USER="+u.Username, "LOGNAME="+u.Username)
	}
	cmd.Env = base
	cmd.Dir = home
	// Its own session: no controlling terminal to read from, and the whole
	// group can be killed if a startup file hangs.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	vars, perr := parseLogin(out)
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
func parseLogin(out []byte) (map[string]string, error) {
	i := bytes.Index(out, []byte(beginMarker+"\n"))
	j := bytes.LastIndex(out, []byte(endMarker))
	if i < 0 || j < i {
		return nil, errors.New("env: the login shell printed no environment")
	}
	vars := map[string]string{}
	for _, kv := range bytes.Split(out[i+len(beginMarker)+1:j], []byte{0}) {
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
