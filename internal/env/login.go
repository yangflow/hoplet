package env

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"time"
)

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
