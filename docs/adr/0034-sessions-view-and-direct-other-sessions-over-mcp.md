# 0034 Sessions view and direct other sessions over MCP

- Status: accepted
- Date: 2026-10-22
- Related: ADR 0002, ADR 0005, ADR 0007, ADR 0012, ADR 0018, ADR 0021, ADR 0024, ADR 0029, ADR 0030, ADR 0031, ADR 0032, ADR 0033, docs/api.md, docs/architecture.md, docs/threat-model.md, docs/hosting.md, docs/spec.md, issues #67, #69, #81

The maintainer accepted the recommendation of each decision below; the options and their cost stay for the record.

## Context

Issue #67 asks that a running session can see and direct the other sessions of the same user: list them, read
one, send it a prompt, later start or stop one, and ask to be moved to another machine, so that an agent can
organize its own work. The issue proposes an MCP server that wraps what the core already offers, registered into
the session by the agent, with a per-session token weaker than the host token. Its done-when is this record:
where the MCP server lives and what the token may do. A session changing its own rules, prompt or permission
mode (#69) is not part of it.

What the code and the documents say today, which decides the design:

- **ADR 0033 built the credential.** `api_tokens` holds hashed `hpt_` tokens with a `kind` (`scoped` is issued;
  `session` is reserved), a grant of `capabilities` (`read`, `send`, `create`, `answer`) and an `allow` list of
  machines and project paths, an expiry, revocation on the next request, a per-token rate limit, the actor kind
  `token` in the audit log, and a router in which a route is host-only unless it declares a capability. A session
  or machine out of scope is `session-not-found`. A `create` token cannot ask for a `risk: "high"` option
  (Bypass permissions, Codex full access). Handoff, `.../stop`, `.../config`, deletion and the token routes are
  host-only. The scrubber knows the `hpt_` shape.
- **The agent has no REST client.** It holds a device token and one WebSocket to the core (`/ws/agent`, ADR
  0005). It knows `server_url` and `token` from its own configuration, but nothing in it calls `/api/*`, and a
  device token is refused there.
- **The agent starts every process and can put things in it.** The Claude driver builds the command line
  (`-p`, `--input-format stream-json`, `--permission-prompt-tool stdio`, `--permission-mode`, `--settings`) and
  the environment (`agent/internal/runtime/claude/driver.go`); the Codex driver passes a free `config` object in
  `thread/start` (ADR 0032). Neither passes an MCP server today. Both vendors list and call MCP tools, which
  ADR 0029 matches as `mcp:<server>/<tool>`.
- **ADR 0032 is the pattern for a local helper.** `hoplet-agent hook codex` is a process the vendor starts; it
  reaches the daemon over `<hoplet_home>/hook.sock` (directory 0700, socket 0600, peer credentials) with a
  per-process nonce that the daemon maps to the session. The helper is not believed about who it is.
- **ADR 0029 already binds Hoplet's own MCP tools.** They go through the permission rules as `mcp:hoplet/<tool>`,
  and no MCP tool and no session token can create, change or delete a rule or change a permission mode.
- **Reads and handoff exist.** Reads are pages and blocks (ADR 0007). A prompt carries a client-chosen
  `promptId`. A handoff is host-only, refuses a session that is running (`busy`), and moves ownership; the new
  owner starts the process again (ADR 0024). The audit log already names the actor kind `session` (ADR 0030).

The risks are the point. A session's model decides what it calls, and what it reads can be written by a
stranger (a web page, an issue, a file), so prompt injection can now travel from one session to another and each
hop can have different permissions. A session in Default mode that may send to a session in Bypass permissions
has, in effect, Bypass permissions. Two sessions that answer each other can loop and spend money. A credential
that sits in the session's environment is one `env` away from the transcript.

## Decisions

1. Where the MCP server lives. Chosen: `hoplet-agent mcp`, a stdio process started by the vendor, that
   reaches the daemon over a local socket; the daemon calls the core's REST API with the session's token.
2. The session credential. Chosen: ADR 0033's `session` kind, minted by the core for each process start,
   held in the daemon's memory only, revoked when the process ends.
3. Capabilities and who grants them. Chosen: `read` by default; `send`, `create` and `request-handoff`
   are per-session switches the user sets, live; `answer` never.
4. Which sessions a session sees and may drive. Chosen: its own project by default, widened per session to
   its machine or all machines; a send target may not be more permissive than the sender.
5. Loop protection. Chosen: a provenance record and a fixed preamble the target's agent adds, a chain with
   no repeats, depth 3, and rate limits.
6. `request_handoff`. Chosen: always a request the user confirms, even in permissive modes; an
   auto-approve switch per session with a list of target machines; it runs after the turn ends.
7. Vendors. Chosen: Claude Code through `--mcp-config` with a file, Codex through `mcp_servers` in the
   `thread/start` config, both behind a driver capability `mcp`; no `--strict-mcp-config`.
8. Whether it is on. Chosen: off until the machine's operator turns it on in the agent's `config.json`
   (`session_mcp`), then on for every new session of that machine with `read` only.

## Decision

### Where it lives (decision 1)

**Chosen: a stdio MCP server that is a subcommand of the agent, and a daemon that holds the credential.**

- The vendor starts `hoplet-agent mcp` for the session (decision 7). It speaks MCP over standard input and output
  and implements the tools below. It holds no token: it connects to `<hoplet_home>/mcp.sock` (the same
  directory, modes and peer-credential check as ADR 0032), presents the per-process nonce from its environment,
  and sends each tool call as one request. The daemon maps the nonce to the session and its credential,
  ignores any session ID the caller states, and answers with the result.
- The daemon performs the call as REST requests to `server_url` with the session's `hpt_` token, so every check
  ADR 0033 built (capability, allow list, `session-not-found`, rate limit, audit) applies unchanged. The MCP
  tools are a thin projection of existing routes, not new authority.
- The tool set: `list_machines`, `list_sessions`, `read_session` (a page of the conversation by cursor, blocks
  elided past a size, one call returns at most 32 KiB, never a whole session, ADR 0007), `send_prompt` (the
  normal prompt path with a `promptId` the server generates; it returns when the core accepted the prompt, not
  when the turn ends, and the sender reads the answer with `read_session`), then `start_session`,
  `interrupt_session` and `request_handoff`. There is no tool that answers a permission, edits a rule, a mode, a
  hook or a token, or deletes anything.

| Option | Cost | Verdict |
|---|---|---|
| Stdio server in the agent, daemon holds the token (chosen) | A new socket and a new subcommand, as ADR 0032 added for Codex. No wire change for the calls, no new route, the credential never enters the vendor process. The machine must reach the core's REST API over HTTP, not only the agent WebSocket. | Chosen |
| Stdio server in the agent, token in its environment | Simplest: no socket. The token is in the environment of a process the model can ask a shell to print (`env`, `/proc`), so it can reach the transcript and any command the session runs. | Rejected |
| Stdio server in the agent, calls proxied over the agent's WebSocket (new wire messages) | The agent needs no REST path and no token: the core authorizes by device and session. Needs messages in `shared/src/wire/` and `agent/internal/sync/protocol.go`, a second authorization path beside ADR 0033's, and every tool becomes a wire message to keep in step with the REST API. | Fallback if a machine cannot reach the REST API |
| An MCP endpoint in the core (streamable HTTP), the vendor connects to it remotely | One implementation for every vendor; no agent work. A new exposed route, a bearer in the vendor's MCP configuration on disk, and the development machine must reach the core directly (the core is usually behind the user's proxy and may be unreachable from it). | Rejected for now; the shape to revisit for a client with no agent |
| Document the `hoplet` CLI for sessions (the issue's alternative) | Nothing to build. The model needs a credential in its shell, and the host token opens every machine. A scoped token (ADR 0033) fixes the power but not the leak into the transcript. | Rejected for sessions; fine for scripts |

### The session credential (decision 2)

**Chosen: the `session` kind of `api_tokens`, minted per process, never given to the vendor process.**

- **Kind and issuer.** The core mints it, not the user, and delivers it over the authenticated agent WebSocket
  as a request and a reply on the control lane: the daemon sends `runtime.mcp.token.request` (`requestId`,
  `sessionId`) the first time a tool call of that session needs a token, and the core answers
  `runtime.mcp.token` (`requestId`, `sessionId`, then `token` and `expiresAt`, or a `reason`: `unknown-session`,
  `not-owner` or `not-allowed`). The core answers only a device that announced `capabilities.sessionMcp` and owns
  the session. The delivery is lazy because the session ID of a new session exists only after `runtime.started`,
  and the `runtime.start` message is built before it; a session that never calls a tool never gets a token. The
  row records the session ID. It is not a token the user lists or revokes in Settings; the session's settings
  show that it exists.
- **The grant is looked up, not frozen.** For a `session` token the router reads the capabilities, visibility and
  limits from the session's current settings at each request (decisions 3 and 4), so a switch the user turns off
  takes effect on the next call, with no new token. The `allow` entries are derived from the visibility
  setting and are never wider than what the user chose.
- **Lifetime.** It lives as long as the process: revoked when the runtime ends (`idle`, `failed`, `offline`), the
  session is deleted, a handoff commits (the new owner gets its own) or the agent stays disconnected past the
  reconnection window; at most 24 hours, after which the daemon asks the core for a new one (a token that has
  expired, or that the core refused with 401, is requested again) without restarting the process. It is not the 90 days of a scoped token; a leaked session token is useless within a day.
- **Storage.** In the daemon's memory, keyed by the nonce. It is never written to disk, never placed in the
  environment, arguments or MCP configuration of the vendor process, and never returned by a tool. The
  nonce is the only secret the vendor process holds; like ADR 0032's, it protects against other users and
  against processes without it, not against a process of the same user.
- **Never in a transcript.** Tool results and errors carry only IDs, titles, times and conversation content; an
  error names a code, not the request. The scrubber (`core/src/scrub/`) already masks `hpt_`; the agent applies the
  same shapes to a tool result before it leaves, and a test feeds a token through every tool path.
- **Audit.** The actor kind is `session` with the session's ID (ADR 0030 already names it). Events: the existing
  `prompt.sent` and `session.created` with that actor, plus `handoff.requested` and `handoff.decided`, and
  `token.denied` for a refused call (once a minute per token and route).

| Option | Cost | Verdict |
|---|---|---|
| `session` kind in `api_tokens`, minted per start (chosen) | One start-message field; the table, the lookup, the router and the audit already exist. A live grant means the row is only an identity. | Chosen |
| A `scoped` token the user issues and pastes per session | No core mint. The user manages a token per session and it outlives the session. | Rejected |
| A new credential kind (an HMAC of the session ID under a core key, no row) | Nothing stored and no start-message change. Revocation needs a deny list, and it is a second authorization path. | Rejected |
| One long-lived token per machine for all its sessions | Least work. The grant is the same for every session and the audit log cannot name the session. | Rejected |

### Capabilities and who grants them (decision 3)

**Chosen: `read` by default, three switches for the rest, set by the user, live.**

| Capability | Lets the session | Default |
|---|---|---|
| `read` | `list_machines`, `list_sessions`, `read_session` (the four configuration reads of ADR 0036 need `read-config`) | on (when the machine has MCP on, decision 8) |
| `send` | `send_prompt`, `interrupt_session` to a session it can see | off |
| `create` | `start_session`; implies `send` | off |
| `request-handoff` | `request_handoff` for itself, never for another session | off |

- `request-handoff` is new, and only a session token can hold it. It is not a handoff: it creates a request
  (decision 6). `answer` is not available to a session token at all, and the core refuses a grant that holds it.
- `start_session` cannot ask for a `risk: "high"` option (as ADR 0033), and the child's permission mode may not be
  more permissive than its parent's; the child obeys the `deny` and `ask` rules of its parent (ADR 0029). Stopping
  a session stays host-only; a session can `interrupt` one it may `send` to, which is under `send`.
- **Who grants them.** The user, in the session's settings in the web UI (a section "Other sessions" with the
  switches, the visibility choice and the handoff switch), with project and global defaults in Settings. The
  initial values of a new session are the project's default, else the global default, else `read`. Only the host
  token (and so the web UI) changes them; a session token can neither read nor write them, as with permission
  modes. The UI needs a mockup in `design/screens/`, with two or three directions, that the maintainer confirms
  before any web code; this record does not claim that confirmation.
- **Permission modes still apply to the call itself.** Calling `mcp__hoplet__send_prompt` is a tool call of the
  session; in Default mode the user is asked as for any other MCP tool, and `mcp:hoplet/*` rules (ADR 0029)
  apply. The agent adds a built-in `allow` for the read tools only while `read` is on, so reading does not
  raise a card per call; every other tool asks unless the user wrote a rule.

| Option | Cost | Verdict |
|---|---|---|
| Read only, no way to grant more | Safest, and useless for the issue's point (directing sessions). | Rejected |
| Switches, off by default, set per session with project and global defaults (chosen) | A settings section, a table of per-session settings and a live lookup per request. The user decides the power per task. | Chosen |
| A global switch only | Smallest UI. Power for all sessions at once, which is where a loop or an injection does the most damage. | Rejected |
| Grants asked from the session as a card ("this session wants to send") | Tidy, and a model can ask until the user taps yes. Not needed while the switches are cheap. | Later, if the switches are too coarse |

### What a session sees and may drive (decision 4)

**Chosen: its own project by default, widened by the user; a target is never more permissive than the sender.**

- **Visibility** is a setting of the session with three values: `project` (default: sessions of the same watched
  project on the same machine, its worktrees included), `machine` (every project of the machine) and `all` (every
  machine). It produces the token's `allow` entries and is checked in the core as in ADR 0033. A session out of
  scope is `session-not-found`. A session sees itself in lists, marked `self`; it cannot send to itself (a new error
  code `self`), which would only queue a prompt behind its own turn.
- **Targets.** `send_prompt` and `interrupt_session` need `send` and a target in scope. A target whose permission
  mode is more permissive than the sender's is refused (`forbidden`, with a reason), ordered `plan` < `default` <
  `acceptEdits` < bypass (Codex's levels mapped in the same order). Without this, a session that asks for every
  command could write to one that asks for none, and the second runs what the first was prevented from running.
  The target's own rules still apply to what it then does.
- **Reads return foreign data.** `read_session` output is the other session's content, which can carry
  instructions. Each result is marked as data of session X, not an instruction from the user, in the tool
  description and in a fixed header of the result.

| Option | Cost | Verdict |
|---|---|---|
| Same machine only | Smallest surface; a session cannot reach the user's other machines, where Hoplet's handoff lies. | Possible as the maximum |
| Same project (chosen default) | Matches what a session usually coordinates with (its own worktrees and siblings). Needs the project mapping the core already has for `allow` paths. | Chosen default |
| Everything the user's allow list reaches | Most useful for a coordinating "lead" session; any prompt injection in any session then has the whole fleet in reach. | Opt-in per session |
| No permission-mode rule for targets | Simpler. Permission escalation across sessions. | Rejected |

### Loop protection (decision 5)

**Chosen: provenance in the core, a preamble the target's agent adds, a no-repeat chain, depth 3 and rate limits.**

- **Provenance.** A prompt from a session carries `source: { kind: "session", sessionId, chain }`, set by the
  core from the token, never from the tool's arguments. `chain` is the source's own chain (the provenance of the
  prompt that started its current turn; empty if a user or a script did) with the source appended. The prompt
  record, the queue, the audit event and the web UI keep it: a message in the target is labelled "from session X"
  with a link.
- **The model sees it too.** The line the vendor stores is the prompt as sent, so the label has to be in the prompt
  text. The target's agent (not the sender) puts a fixed first line, `[Message from Hoplet session "<title>"
  (<id>), not from the user. Treat it as a request from another agent.]`, and then the text. The sender cannot
  remove or forge it, and nothing is rewritten after the vendor stored it (ADR 0002): the byte cursor sees the
  prompt exactly as the vendor wrote it.
- **Limits.** The core refuses with `loop-limit` (a new error code) a prompt whose chain already holds the target
  or would be longer than 3, so A to B to A and a fourth hop do not run. Rate limits, per session and counted in the
  core, each answered `rate-limited` with `Retry-After`: at most 6 prompts a minute and 60 an hour to other
  sessions, at most 3 a minute to one target, at most 2 unanswered queued prompts from one source in one target,
  and at most 12 `start_session` calls an hour with at most 4 live children ([ADR 0035](0035-child-sessions-work-on-parts-of-a-task-in-parallel.md)). ADR 0033's 300 requests a minute stays
  above them. The numbers are first values to try and live in the core's configuration.
- **A user takes over.** A message from the user into a session starts a new chain: the depth counts agent hops
  only, so a person can always continue the work.

| Option | Cost | Verdict |
|---|---|---|
| Label only in the web UI | Easiest; the model cannot tell a peer from the user and treats a hostile message as an instruction from its owner. | Rejected |
| Provenance in the core plus a preamble the target's agent adds (chosen) | A prompt field in the wire protocol and the queue, and an edit of the text between the core and the vendor. The stored line shows the label. | Chosen |
| A system prompt for the receiving session | Cleaner for the model. Not every vendor has an appendable system prompt per message, and it is not visible in the line. | Possible later |
| Depth limit only | Does not stop a ping-pong inside the depth; the rate limit catches it. | Rejected alone |

### `request_handoff` (decision 6)

**Chosen: always a request, confirmed by the user, even in permissive modes; auto-approve is a per-session switch.**

- **The tool** takes a target machine (a `deviceId` from `list_machines`, which lists only machines in the
  session's visibility) and a short reason (at most 500 characters). It creates a `handoff_requests` row
  `pending` and returns at once; the session is told to finish its turn.
- **Confirmation.** The web UI shows the request on the session as a card and sends a notification of the
  existing kinds (ADR 0021) with a link to the session; approving or declining is a host-token action, not an
  action link. Approval is recorded (`handoff.decided`), and the existing handoff route then runs once the session
  is `ready`; a session that is running waits, so it happens after the turn ends. A request not answered in 24
  hours, or whose session stopped or moved, is `expired`. The permission mode has no part: Bypass permissions,
  Full access and an `allow` rule for the tool all still need the user, because moving the work to another machine
  is not a tool call that a mode can approve.
- **Auto-approve.** A per-session switch, off by default, set by the user in the same section, with a list of
  target machines it applies to (empty means none). When on, a request for a listed target is approved by the
  core with the decision recorded as `auto`; it still waits for the end of the turn; at most 3 auto-approved
  handoffs an hour per session. A request for any other machine still needs the user. The switch and the list are
  the settings `autoApproveHandoff` and `autoApproveMachines`, part of the session's settings and of the defaults
  (global and per project); they only exist with `requestHandoff`, and the machines must be known to Hoplet. The
  settings are stored and shown in Settings and in the session's inspector; step 5 acts on them.
- **Afterwards.** The new owner starts the process with a new session token (decision 2) and the same settings,
  which belong to the session; the old token is revoked at commit. If the target lacks the project, the existing
  handoff errors (`no-clone`, `clone-failed`) are recorded on the request and shown.

| Option | Cost | Verdict |
|---|---|---|
| Never allow a session to ask | Nothing to build; the issue's main example is lost. | Rejected |
| Always confirm, auto-approve per session with a target list (chosen) | A request table, a card and a notification. Auto-approve is one more way a hijacked session moves itself, limited to machines the user named. | Chosen |
| Confirm unless the session's mode is permissive | Fewer taps. Makes Bypass permissions also mean "may move itself anywhere". | Rejected |
| Execute at once, interrupting the turn | Handoff refuses a running session (`busy`) for a reason: the byte cursor and the process. | Rejected |

### Vendors (decision 7)

**Chosen: the runtime driver declares an `mcp` capability and takes an MCP spec; each vendor maps it.**

- The runtime driver interface (ADR 0012) gains `StartSpec.MCP`: a command (the running `hoplet-agent`), its
  arguments (`mcp`), the environment (`HOPLET_MCP_SOCK=<hoplet_home>/mcp.sock` and `HOPLET_MCP_NONCE=<nonce>`,
  and nothing else), the nonce, the flag whether the read tools are allowed without a prompt, and a `Release`
  that the driver calls when the process ends or fails to start. A driver without the
  `mcp` capability gets none, and the session shows the tools are not available. Nothing outside a driver
  knows the vendor's flag.
- **Claude Code**: the agent writes `<hoplet_home>/mcp/<nonce>.json` (`{"mcpServers":{"hoplet":{...}}}`, mode 0600,
  removed when the process ends) and starts the process with `--mcp-config <path>`; inline JSON would put the
  nonce in the process list. It does not pass `--strict-mcp-config`, so the user's own MCP servers keep
  working. The tools are `mcp__hoplet__<tool>`. The built-in allow of the read tools (`list_machines`,
  `list_sessions`, `read_session`) lives in the Claude hook, not in the rule defaults: it applies only to a
  process that has the server and only when no rule decided, so a `deny` or `ask` rule wins, and in plan mode the
  hook still returns no decision.
- **Codex**: `mcp_servers.hoplet = { command, args, env }` in the `config` of `thread/start` and of a resume,
  beside the hook of ADR 0032. Both are verified against the installed binaries before code relies on them
  (flag names and the generated schema), as ADRs 0029 and 0032 did; the result goes into this record if it differs.
- **Other vendors** implement the capability or leave it out. The tools, the socket and the core are the same.
- **Rules.** Calls match as `mcp:hoplet/<tool>` for both vendors (ADR 0029).

| Option | Cost | Verdict |
|---|---|---|
| Driver capability and spec (chosen) | An interface change in `agent/internal/runtime/` and a driver change per vendor, each with a spike on its MCP flag. | Chosen |
| Write the user's global Claude Code or Codex MCP settings once | No per-process work. Changes the maintainer's saved settings and applies to sessions Hoplet did not start. | Rejected |
| Only Claude Code at first | Fewer spikes. The interface is shaped by one vendor. | Acceptable as the first pull request, behind the capability |

### Whether it is on (decision 8)

**Chosen: a machine setting the operator owns, like the `terminal` switch.**

`session_mcp` in the agent's `config.json` (default `false`) decides whether the agent registers the server and
opens `mcp.sock`; the core cannot turn it on. With it on, every new session of the machine gets the `read` tools and
the session's own switches, so the model's context pays for the tool definitions only where the operator chose
that. The issue says registered automatically; this is automatic per machine, not for every install.

| Option | Cost | Verdict |
|---|---|---|
| On everywhere | One less setting. Tool definitions in every session's context and a new boundary on machines whose operator did not ask. | Rejected |
| Operator switch on the machine (chosen) | A config field, and a capability announced in `device.hello` so the UI shows the settings only when it is on. | Chosen |
| A per-session choice only | Finer control. The user must remember to turn it on for each session, so it will not be used. | Possible in addition |

## Cost

A subcommand and a socket in the agent, a driver capability and an MCP spec per vendor, a field in the start
message and in the prompt, a per-session settings table and a live grant lookup in the router, the `session` token
kind finally used, a `handoff_requests` table with a card and a notification, provenance in the prompt and the
queue, the rate limits, two audit events, two new error codes (`self`, `loop-limit`), and a settings section in the
web UI with strings in `web/src/i18n/zh-Hans.json` and a mockup in `design/screens/` first. Every tool is a route
that must have the right access and filter; the test of ADR 0033 that walks the route table covers the session token
as one more principal. A model that reads a hostile page can now try to act on the user's other sessions, bounded by
the project scope, the switches, the permission-mode rule and the limits, not removed.

## Alternatives

- *Wait for the vendors' own subagents.* They cover one process on one machine; Hoplet's value is across
  machines and sessions that already exist.
- *An orchestrator inside the core that decides, with no tool for the model.* A different product: Hoplet would
  hold the plan, not the session. Rejected.
- *One MCP endpoint for scripts as well.* The SDK serves scripts (ADR 0033); an MCP endpoint in the core can come
  later on the same routes.

## Documents that change with the implementation

None changes in the pull request that adds this record. The implementation pull requests update
`docs/api.md` (the `session` token kind, session settings, handoff requests, the new errors), `docs/spec.md` (the
section for sessions that direct sessions, the settings, the audit events), `docs/threat-model.md` (below),
`docs/hosting.md` (the agent's `session_mcp` field, the socket `mcp.sock`, the agent's need to reach the core's
REST API, the new tables), `docs/architecture.md` (the driver capability, the MCP subcommand and where a session token
is resolved), ADR 0029 (the name `mcp:hoplet/<tool>` and the built-in read allow), ADR 0030 (the `session`
actor's events), ADR 0033 (the `session` kind in use), `shared/src/wire/` with `agent/internal/sync/protocol.go`,
and `web/src/i18n/zh-Hans.json`. Implementation order, one concern each:

1. The core: the session grant lookup, the per-session settings, provenance, the limits and the mode rule, with
   tests over the route table (no agent, no UI).
2. The wire and the agent: the token request and reply, `mcp.sock`, `hoplet-agent mcp` with the read tools,
   the Claude Code driver and the `session_mcp` setting. Verified with the fake Claude, not a real session.
3. The web UI: the settings section, once the mockup is confirmed.
4. `send_prompt` and `interrupt_session`, with the preamble and the labelled message in the target.
5. `request_handoff`: the request table, the card, the notification and auto-approve.
6. `start_session`.
7. The Codex driver.

## Trust boundary changes

Two boundaries are added and one changes meaning. **Session model to the agent's MCP socket**: the model of a
session, which reads untrusted content, now drives calls that the agent turns into REST requests; the nonce names
the session, the daemon ignores anything the caller claims, and the call is checked in the core against the
session's live grant. **Session token to core**: a credential that exists only in the agent's memory, with a
grant that the user sets per session and that the core reads at each request. **Prompt injection between
sessions**: previously a hostile page could mislead one session, bounded by that session's permissions; it can now
ask another session to act, bounded by the visibility setting, by the rule that a target is not more permissive than
the sender, by the target's own deny rules, by the limits, and by the label the target sees and the web UI shows.
It is not prevented: a model can still be talked into sending a harmful but permitted prompt. The threat-model
entries to add: a session token used beyond its grant; a hostile session drives another; a loop between sessions; a
session moves itself; the nonce read by a process of the same user (an accepted risk, the same as ADR 0032). No new
route is open to the network, no session can change rules, modes, tokens, devices or the audit log, and the sync
cursor and the stored lines are unchanged.

## Revisit triggers

Users or roles arrive (ADR 0005); a client with no agent needs the tools, which is the core-hosted endpoint; a
machine cannot reach the REST API from the agent, which is the wire proxy; the switches prove too coarse and a
card per request is wanted; a vendor offers per-message system prompts; the limits are wrong in practice; #69
needs a session to change itself.
