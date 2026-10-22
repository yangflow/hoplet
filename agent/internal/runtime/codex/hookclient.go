package codex

import (
	"encoding/json"
	"io"
	"net"
	"time"
)

// hookDialTimeout bounds the connection to the daemon: it is a local socket, so a daemon that does not accept
// at once is not running.
const hookDialTimeout = 5 * time.Second

// RunHook is `hoplet-agent hook codex`: it reads the hook input from stdin, asks the daemon at the socket named
// by the environment, and writes the hook output to stdout. It reads no rules, configuration or network. Every
// failure is a deny whose reason names the failure and never the input, so the caller always exits 0 with an
// answer on stdout.
func RunHook(stdin io.Reader, stdout io.Writer, getenv func(string) string, timeout time.Duration) {
	reply := askDaemon(stdin, getenv, timeout)
	if reply.Decision == hookAllow {
		// Codex does not support an allow from a hook: it reports the output as a failure and then decides
		// itself. Printing nothing leaves the call to its approval policy.
		return
	}
	_ = json.NewEncoder(stdout).Encode(hookOutput(reply))
}

func askDaemon(stdin io.Reader, getenv func(string) string, timeout time.Duration) hookReply {
	sock, nonce := getenv(HookSockEnv), getenv(HookNonceEnv)
	if sock == "" || nonce == "" {
		return denyReply("Hoplet cannot reach its permission rules: the hook has no socket or nonce in its environment")
	}
	// The envelope around the input is small; the cap on the input keeps the whole request within the daemon's.
	const envelope = 256
	input, err := io.ReadAll(io.LimitReader(stdin, MaxHookRequest-envelope+1))
	if err != nil {
		return denyReply("Hoplet could not read the hook input")
	}
	if len(input) > MaxHookRequest-envelope {
		return denyReply("The hook input is larger than Hoplet accepts")
	}
	if !json.Valid(input) {
		return denyReply("Hoplet could not read the hook input")
	}
	req, err := json.Marshal(hookRequest{Nonce: nonce, Input: input})
	if err != nil {
		return denyReply("Hoplet could not read the hook input")
	}
	c, err := net.DialTimeout("unix", sock, hookDialTimeout)
	if err != nil {
		return denyReply("Hoplet's permission rules are not reachable: the agent is not listening on the hook socket")
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(timeout))
	if _, err := c.Write(req); err != nil {
		return denyReply("Hoplet's permission rules are not reachable: the request was not sent")
	}
	if uc, ok := c.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	raw, err := io.ReadAll(io.LimitReader(c, maxHookReply+1))
	if err != nil || len(raw) == 0 || len(raw) > maxHookReply {
		return denyReply("Hoplet's permission rules did not answer in time or answered unreadably")
	}
	var r hookReply
	if err := json.Unmarshal(raw, &r); err != nil {
		return denyReply("Hoplet's permission rules answered unreadably")
	}
	switch r.Decision {
	case hookAllow, hookPass:
		return r
	case hookDeny:
		if r.Reason == "" {
			r.Reason = "Denied by Hoplet"
		}
		return r
	}
	return denyReply("Hoplet's permission rules answered unreadably")
}

// hookOutput is the PreToolUse output Codex reads. Codex supports only deny: a person's decision is made in the
// daemon and an approval arrives here as allow, which prints nothing. A pass prints an empty object, which leaves
// the call to Codex.
func hookOutput(r hookReply) map[string]any {
	if r.Decision == hookPass {
		return map[string]any{}
	}
	out := map[string]any{"hookEventName": "PreToolUse", "permissionDecision": r.Decision}
	if r.Reason != "" {
		out["permissionDecisionReason"] = r.Reason
	}
	return map[string]any{"hookSpecificOutput": out}
}
