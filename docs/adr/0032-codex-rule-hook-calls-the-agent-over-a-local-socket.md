# 0032 The Codex rule hook calls the agent over a local socket

- Status: accepted
- Date: 2026-10-21
- Related: ADR 0008, ADR 0012, ADR 0029, docs/architecture.md#wire-protocol, docs/hosting.md#data, docs/threat-model.md, issue #98

## Context

ADR 0029 decides that Codex tool calls are checked by a `PreToolUse` command hook, and that the agent holds an
`ask` open itself and returns `allow` or `deny`. It does not say what the command is or how the process Codex starts
reaches the agent's decision. Codex runs the hook as a separate process for each tool call; the rules, the session
state and the permission flow to the browser live in the running daemon. The hook needs a path to the daemon, and
that path is a new trust boundary: a local caller that can ask the daemon questions.

The generated app-server schema (`codex app-server generate-json-schema --experimental`, codex 0.163.0) types the
`config` of `thread/start` as a free object, and the hook's own stdin and stdout are not in it: the payload a hook
command reads and writes is not part of the app-server protocol.

## Decision

**The hook command is the agent itself.** For each Codex process (`codex app-server`) it starts, the agent puts the
hook in the `config` of `thread/start` and of a resume, with `bypass_hook_trust: true` (ADR 0029):
`hooks.PreToolUse=[{matcher, hooks=[{type="command", command, timeout}]}]`. The `command` is the absolute path of the
running `hoplet-agent` binary followed by `hook codex`; `timeout` is 900 seconds, the value of the Claude Code hook.
`hoplet-agent hook codex` reads the hook input on stdin, asks the daemon, and writes the hook output
(`hookSpecificOutput.permissionDecision`, ADR 0029) on stdout. It does not read the rules, the configuration or the
network.

**The hook reaches the daemon over a Unix socket.** The daemon listens on `<hoplet_home>/hook.sock`; the directory
is mode 0700 and the socket 0600. It is a local file, not a port. The daemon checks the peer credentials of each
connection and accepts only the user ID the agent runs as.

**A nonce names the session; the hook is not believed.** The agent creates a random nonce (at least 128 bits) for
each Codex process it starts and puts it in that process's environment; Codex passes the environment to the hook.
The hook sends the nonce with the hook input. The daemon maps the nonce to the session, its working directory and its
permission mode, and ignores any session ID, `cwd` or mode in the hook input. The nonce is dropped when the process
ends. An unknown nonce is a deny.

**The exchange is small and bounded.** One request and one reply per connection, a request of at most 1 MiB
(larger is a deny). An `ask` is held open by the daemon while the user decides in the web UI through the existing
permission flow; the hook waits on the socket and the reply is `allow` or `deny`, never `ask`.

**Fail closed.** The hook returns `deny`, with a reason that names the failure and not the input, when the socket is
missing or refuses the connection, the reply is malformed, the reply does not come within the timeout, or the nonce is
unknown. A hook that cannot run at all is caught by the start check of ADR 0029 (no `hook/started` on the first tool
call) and the session shows that rules are not enforced.

**The payload is verified before code relies on it.** The field names of the hook's stdin and stdout are not in the
schema. The code change verifies them against the installed `codex` first, as ADR 0029 did for Claude Code, and
records the result in this record if it differs.

**Windows has no rule hook yet.** Named pipes are not supported. On Windows the driver declares
`rules: prompts-only` (ADR 0029): rules act only on the approval requests Codex sends, Full access runs without
them, and the session says so in a visible note.

**The limit.** The socket stops other users and processes without the nonce from getting an answer or from raising
permission cards. It does not stop a malicious process running as the same user: it can read the nonce from the Codex
process environment or open the socket itself. That is outside the threat model, the same as writing the provider's own
configuration files.

## Verified against codex 0.163.0

Three short app-server turns registered a probe hook in the `thread/start` config the way this record describes.

1. The environment reaches the hook: `HOPLET_HOOK_SOCK` and `HOPLET_HOOK_NONCE` arrive. The matcher `.*` fires for
   the shell tool (`tool_name` `Bash`, `tool_input.command`) and for `apply_patch` (`tool_input.command` is the patch
   text, with absolute file paths). `bypass_hook_trust` works on `thread/start`; the hook's source is reported as
   `sessionFlags`. `tool_use_id` equals the id of the `commandExecution` or `fileChange` item.
2. `permissionDecision: "deny"` with a reason blocks the call: the hook run is `blocked` and the command does not
   run.
3. `permissionDecision: "allow"` is not supported: the hook run is `failed` ("unsupported permissionDecision:allow")
   and the call then runs under Codex's own policy.
4. Codex fails open on a hook failure. A hook that outlived its timeout was reported `failed` and the command ran.
   Any failure of the hook process (a crash, unsupported output, a process that cannot start) lets the call through.

Consequences:

- An `allow` by a rule, and a decision the user approved in the web UI, print nothing and exit 0, so Codex's approval
  policy applies. The daemon remembers the approved `tool_use_id` for ten minutes; when Codex then sends its own
  approval request for the same item, the driver accepts it without asking the user again. A call a rule denies never
  reaches that point.
- Fail closed holds only while the hook process runs and answers in time. The hook prints its deny at 870 s and the
  daemon resolves an `ask` that nobody answered as a deny ("no answer in time") at 860 s, both before the 900 s timeout
  of the hook in the config, because past that timeout the call runs.
- A hook that cannot start or is killed lets the call through. The agent cannot prevent that; the start check logs a
  session in which no call ever reached the hook.

## Cost

A new subcommand and a local listener in the agent, a nonce per process, and a process start per tool call
(`hoplet-agent hook codex` is short-lived and does nothing but forward). Every Codex tool call waits on the daemon;
if the daemon is gone, the call is denied rather than run. The same-user limit means the rules protect against model
mistakes, not against a hostile local program.

## Alternatives

- *TCP loopback with a token.* Works on Windows, but opens a port that any local user can reach, needs a port choice
  and a token in the command line or environment, and is a larger surface than a 0600 socket with peer credentials.
  Rejected.
- *The hook reads the rules from disk itself.* No IPC, but it cannot hold an `ask` for a person, would duplicate the
  engine in a second process, and would read a rule file the daemon may be writing. Rejected.
- *Stdin and stdout through the app-server only.* Codex runs hook commands as processes; the app-server protocol has
  no request that carries a hook decision, so there is no such path today.

## Revisit triggers

A Codex hook type that calls back over the app-server connection; named pipe support for Windows; a hook payload
that carries a session ID Codex vouches for.
