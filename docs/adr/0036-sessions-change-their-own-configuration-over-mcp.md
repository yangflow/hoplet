# 0036 Sessions change their own configuration over MCP

- Status: proposed
- Date: 2026-10-23
- Related: ADR 0002, ADR 0009, ADR 0012, ADR 0018, ADR 0021, ADR 0024, ADR 0028, ADR 0029, ADR 0030, ADR 0032, ADR 0033, ADR 0034, docs/api.md, docs/architecture.md, docs/threat-model.md, docs/hosting.md, docs/spec.md, issues #9, #12, #18, #67, #69, #81

This record is a proposal. The maintainer has not confirmed any decision below; each has its options, their cost and a
recommendation. It builds on ADR 0034, whose session MCP server, session tokens and request-and-confirm pattern
(`request_handoff`) it reuses.

## Context

Issue #69 asks that a running session can read and, once enabled, change its own configuration, skills and prompts
(`CLAUDE.md`) through the Hoplet MCP server of #67, so that an agent can tune itself in conversation. Its
done-when is this record: the tool scope, whether a write needs a confirmation each time, and which settings a
session may never change itself. The issue proposes reading first, writes as a separate switch, a diff and a
confirmation per write, undo, a write made by the machine's agent and never by the web UI, and a permission mode
that a session may tighten but not loosen.

What the code and the documents say today, which decides the design:

- **ADR 0034 is in place.** `hoplet-agent mcp` is a stdio server whose daemon holds a per-process `session` token;
  a route is reachable only with a capability (`read`, `send`, `create`, `request-handoff`) that the user sets per
  session and the core reads live. `request_handoff` is the pattern for an action that is a request, a card in
  the web UI and a decision by the user, with an optional auto-approve. ADR 0034 states the invariant "no session
  can change rules, modes, tokens, devices or the audit log"; it left #69 open.
- **A session can already edit these files with its own file tools.** `CLAUDE.md`, `.claude/skills/` and
  `.claude/commands/` are ordinary files in the project, and Claude Code edits them as it edits any file, under the
  session's permission mode: in Default mode the user is asked per edit, in Accept edits and Bypass permissions not.
  The compiled-in floor of ADR 0029 (`default.hoplet-and-provider-config`) denies reads and writes only of the
  agent's `<hoplet_home>/**`, `~/.claude/settings*.json`, `.claude/settings*.json`, `~/.codex/config.toml` and
  `.codex/config.toml`. So today a session can change its own prompt and skills, with no diff, no record and no undo,
  and cannot change the provider settings or the hooks where the rule hook sits.
- **The agent already reads some of it.** The runtime reports the slash commands and skills its Claude Code
  process offers, each with a source (project, user, plugin, built-in), for the composer
  (`agent/internal/runtime/claude/commands.go`, #12). Nothing reads `CLAUDE.md` yet; the web UI's Memory section is
  #18 and "a later section".
- **A session's settings are config options** (ADR 0018): the driver declares them, `POST /api/sessions/:id/config`
  sets them on the live process, and the route is host-only. A `risk: "high"` option (Bypass permissions, Codex
  full access) is never offered to a scoped or session token (ADR 0033).
- **The agent writes files only in narrow cases** (its own state, the worktrees it creates, the snapshot of a
  handoff, ADR 0009). The web UI never writes files (docs/spec.md). A write by the agent into a project at a model's
  request is a new kind of act, and the floor, which is a deny list for the vendor's tools, does not constrain the
  agent itself.
- **Files in the project travel with the work.** A project file changed in a worktree is part of the uncommitted
  workspace that a handoff snapshots (ADR 0009); a user-level file (`~/.claude/...`) stays on its machine.

The risk is persistence. A prompt injection that reaches a session today lives as long as that conversation. If it
can write `CLAUDE.md` or a skill, it lives in every later session of the project, and in a user-level file in every
session of the machine, long after the page that carried it is forgotten. A skill or a command is also code in
disguise: Claude Code lets their front matter grant tools without asking (`allowed-tools`) and lets a command body
run shell lines (to be verified, decision 3). A tool that writes them is therefore a path to widening the session's
own permissions, which the threat model says no session can do. The design has to make this path no weaker than
the file tools it replaces, and should make it stricter.

## Decisions

1. Tool scope: which objects, read and write, and for which scope. Recommended: read the session's configuration,
   skills, slash commands and `CLAUDE.md`/`AGENTS.md` of the project and the user; write only those of the session's
   project, never user-level; `model` and `effort` as the only settings.
2. Confirmation. Recommended: every write is a change request the user confirms, shown as a diff, one request
   holding up to five objects; no auto-approve; undo restores the stored previous version.
3. What a session may never change. Recommended: a closed list of object kinds enforced by the agent and the core,
   a content check on skills and commands; the floor stays and is not the mechanism.
4. Where the write happens and when it takes effect. Recommended: the owner's agent writes after the core records
   the approval, with a hash check; the vendor reads the file at its next start; pending requests end at a handoff.
5. Threat model, documents and order of work (below).

## Decision

### Tool scope (decision 1)

**Recommended: read broadly, write only the session's project files, and tools named `propose_*` because a
write is a proposal.**

Objects and what each tool does. Names follow the tools of ADR 0034 (snake case, `mcp:hoplet/<tool>` for rules).

| Object | Read | Write | Why |
|---|---|---|---|
| Session settings (model, effort, mode and the other config options of the driver, with current value and choices) | `get_config` | `propose_config_change`: `model` and `effort` only | #9 and ADR 0018; a model is a cost decision the user confirms. The mode is excluded (decision 3). |
| `CLAUDE.md` / `AGENTS.md` of the session's working directory | `read_memory` (`scope: project`) | `propose_memory_change` (`scope: project`) | The issue's core case: the file the vendor loads for this project. |
| `CLAUDE.md` / `AGENTS.md` of the user (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`) | `read_memory` (`scope: user`) | none | Applies to every session of the machine, so one injected sentence reaches all of them. |
| Skills (`.claude/skills/<name>/SKILL.md`) of the project and of the user, and slash commands (`.claude/commands/<name>.md`) | `list_skills`, `read_skill` | `propose_skill_change`, project only | Same data as the composer's list (#12). |
| MCP server configuration, hooks, subagent definitions, permission rules, provider settings, Hoplet's own settings | none | none | Each one grants power or is the control itself (decision 3). |

- `get_config` and the reads return page-sized results: at most 32 KiB a call, text elided past that with a
  cursor (ADR 0007, as `read_session` in ADR 0034). The reads go through the same agent request the web UI uses
  for #12 and #18, so there is one reading logic. If it does not exist when this lands, step 2 builds it for all
  three.
- The tools are `get_config`, `list_skills`, `read_skill`, `read_memory`, `propose_config_change`,
  `propose_memory_change`, `propose_skill_change` and `get_change` (the state of one change: `pending`, `applied`,
  `declined`, ...). The issue's names `update_config`, `write_skill` and `write_memory` are not used: a tool that
  returns "waiting for the user" and does not write must not be named as if it wrote.
- **Capabilities.** Two new ones on the session token, set per session by the user like the others of ADR 0034:
  `read-config` (the four read tools; on by default wherever `read` is, since the file tools already read the
  same files) and `write-config` (the three `propose_*` tools; off by default, and a session that has it still
  needs a confirmation for every change, decision 2). `answer` stays unavailable to a session token.
- **Vendors.** The tools are one set; the provider behind them (ADR 0012) maps an object kind to the vendor's
  files: Claude Code's `CLAUDE.md`, `.claude/skills` and `.claude/commands`, Codex's `AGENTS.md` and its skills
  directory. A driver declares a capability `selfConfig` listing the kinds it supports, and a tool for a kind it
  lacks answers `unsupported`. The exact paths and front-matter keys of each vendor are verified against the
  installed binaries before code relies on them, as ADRs 0029 and 0032 did; the result goes into this record.
- **Which project.** The session's working directory (its worktree if it has one), never a parent or another
  project. The paths are fixed by object kind; `propose_memory_change` cannot name a path, and a subdirectory's
  `CLAUDE.md` is out. A skill or command is addressed by its name (`[a-z0-9][a-z0-9-]{0,63}`).

| Option | Cost | Verdict |
|---|---|---|
| Read all of the above, write only the session's project files, `model` and `effort` (recommended) | Three write tools, one writer in the agent, a content check. A change reaches only this project, and git shows and reverts it. A session can read user-level memory but not improve it; the user does that in Settings (#18). | Recommended |
| Read and write project and user level, plus commands and subagents | What the issue lists in full. A user-level write reaches every session of the machine, outside the project's git and outside a handoff snapshot, so it is neither visible in a diff of the work nor undone by a checkout. | Rejected for now; user-level write is a switch to add after the project one has run |
| Read-only (the issue's first step, no write tools) | Smallest change: four read tools, no new write path. The model still edits `CLAUDE.md` with its file tools and gets no diff, record or undo, so Hoplet-visible self-management is not met. | Fallback; it is steps 1 and 2 of the order below |
| No tools; document the file tools (the issue's alternative) | Nothing to build. No confirmation in Accept edits or Bypass permissions, no record, no undo, and nothing reaches `model` or `effort`. | Rejected as the answer; it stays what happens when `write-config` is off |

### Confirmation, diff and undo (decision 2)

**Recommended: every write is a change request the user confirms, with a diff and an undo; no auto-approve.**

- **A change request is a row in `config_changes`**, created by a `propose_*` tool through the session's token
  (capability `write-config`), `pending` at once, and the tool returns at once. It holds the session, the object
  kind and key (`memory:project`, `skill:<name>`, `config:model`), the base hash (SHA-256 of the file as the
  session last read it, empty for a new file), the new content, the diff, a reason of at most 500 characters and
  the times. One request holds up to five objects and 64 KiB in total, approved or declined as a whole, so that a
  skill and the line in `CLAUDE.md` that mentions it cannot be split by accident.
- **The diff is computed by the agent** from the file it reads at proposal time, so the card shows the real file
  and not what the model claims it was. If the base hash the model supplied differs from the file, the proposal is
  refused with `stale` and the model reads again. A new file is shown whole, a deletion as removed lines. Content is
  shown as text, never rendered as Markdown or HTML; control and bidirectional characters are made visible.
- **The card** appears on the session, with a notification of the existing kinds (ADR 0021) linking to it. Approve,
  decline and undo are host-token actions, not notification action links (as the hand-off). A request not decided
  in 24 hours is `expired`. The permission mode has no part: Bypass permissions, Accept edits and an `allow` rule
  for `mcp:hoplet/propose_*` still wait for the user. That is the difference from the file tools, which can be
  silent in a permissive mode.
- **Undo.** The core stores the previous content of every object it applies (at most 64 KiB each) with the change,
  so the user can undo an applied change from any machine, also after a handoff. Undo is itself a change: the
  owner's agent applies it only if the file still has the hash the change produced; otherwise it answers `stale`
  and the card shows the conflict. Previous versions are kept 30 days and then pruned; the change record (what,
  when, by which session) stays as long as the audit log. Because content is stored, a proposal that contains a
  shape the scrubber knows (`hpt_` tokens, API keys, private keys) is refused with `unsafe-content`: a secret never
  enters the table, the diff or a notification.
- **Record.** The audit log (ADR 0030) gets `config.change.requested`, `config.change.decided`,
  `config.change.applied`, `config.change.failed` and `config.change.undone`, actor kind `session` for the
  request and `user` for the decision. The core relays `session.change` (ids only) to the browsers subscribed to
  the session, and the conversation shows a line at the tool call, matched by the vendor's tool-use ID when the
  driver can supply it and by time otherwise. The session file is not touched (ADR 0002).

| Option | Cost | Verdict |
|---|---|---|
| A request per change set, always confirmed, no auto-approve (recommended) | A table, a card with a diff, a notification and a stored previous version. One more tap than the file tools in Accept edits, which is the point. | Recommended |
| A batched proposal: the session collects changes and offers them once at the end of a turn | Fewer cards for a session that edits ten things. The user decides late on changes the session may already assume, and the session needs a "submit" step it can forget. The recommended request already holds up to five objects. | Later, if the cards prove noisy |
| Auto-approve per object kind or path, as the hand-off has it for machines | Fewest taps, for example for appends to project `CLAUDE.md`. In the hand-off the user names a destination from a short list; here the risk is the content, which the user cannot name in advance, and an injected sentence is exactly a small append. | Rejected for now; the `autoApproveMachines` shape is the model if it comes back |
| Apply at once, record, and let the user undo | Matches the issue's "can be undone". The persistence risk lands before anyone looks. | Rejected |

### What a session may never change (decision 3)

**Recommended: a closed list of object kinds, enforced where the write happens and again where it is requested.**

A session can propose a change only to an object kind in the table of decision 1: the project `CLAUDE.md` /
`AGENTS.md`, project skills and commands, and `model` and `effort`. This is an allow list on purpose. The floor of
ADR 0029 is a deny list for the vendor's tools; the agent that writes here is not subject to it, so the writer
names what it may touch and everything else does not exist for it. Never changeable by a session, through these
tools or any route its token reaches:

- **Permission rules, permission modes, the `deny` and `ask` lists** (ADR 0029 and ADR 0034's invariant). The issue
  allows tightening a mode; this record does not (options below).
- **Hooks and the provider's settings files** (`.claude/settings*.json`, `~/.claude/settings*.json`,
  `.codex/config.toml`, `~/.codex/config.toml`), which carry the rule hook, and **MCP server configuration**
  (`.mcp.json`, the user's MCP entries, `<hoplet_home>/mcp/`), because a server is code that runs with the user's
  rights and one of them is Hoplet's own.
- **Subagent definitions** (`.claude/agents/`), which carry tool lists and modes, and plugins.
- **Tokens, devices, the audit log, webhooks and notification targets, provider configurations (ADR 0028), the
  agent's `config.json`, `<hoplet_home>/**`**, and everything the host-only routes of ADR 0033 cover.
- **The session's own `session_mcp` settings**: its capabilities, visibility, hand-off auto-approve and
  `write-config` itself. A session that could switch on its own `send` or `request-handoff` would take the grant
  ADR 0034 reserved for the user.
- **Hoplet's own settings and any `risk: "high"` option.**

Enforcement, three layers, each tested on its own:

1. **Core.** The object kind is an enum in `shared/`; the router gives the `propose_*` routes only to a session
   token with `write-config`; the routes for the session's settings, rules, modes, tokens and devices stay host-only
   as ADR 0033 made them. `propose_config_change` accepts the option IDs `model` and `effort` of the driver's
   `capabilities.config` and refuses any option the driver marks `risk: "high"` or whose ID is a mode.
2. **Agent.** The writer resolves an object kind to a path from the session's working directory and nothing else.
   It refuses a path through a symbolic link, one whose real path leaves the working directory or the directory of
   the kind, a skill name that does not match the pattern, and a target that is not a regular file. It takes the
   working directory from the one announced at the session's start, so the model cannot move the root.
3. **Content check on skills and commands.** Front matter keys other than `name` and `description` are refused
   (`allowed-tools`, `model`, `hooks`, anything the vendor adds), and so are lines that run a shell command when
   the command or skill is invoked (Claude Code's `!`-prefixed lines). A new `@path` import into `CLAUDE.md` or a
   skill that points outside the working directory or at a path the floor denies is refused. The key and syntax
   lists are checked against the installed vendor versions before implementation (the spike rule of ADR 0032); a
   vendor whose format the check does not know gets the kind marked unsupported rather than unchecked.

What the content check cannot do is judge prose: a `CLAUDE.md` that says "always run `curl ... | sh`" is plain
text. The defence for that is the confirmation and the diff the user reads, the project scope, and the permission
rules that still decide every tool call the changed prompt later causes.

| Option | Cost | Verdict |
|---|---|---|
| Closed allow list of kinds, enforced in the core and the agent, plus a content check (recommended) | Three layers and a spike per vendor format. A new object kind needs an amendment of this record. | Recommended |
| Deny list: everything except the floor and a few more | Less to write. An object nobody thought of, such as a new vendor file, is writable by default. | Rejected |
| Reuse the floor: the agent writes through the permission engine as if it were the vendor's Write tool | One mechanism. The floor and the rules decide on a model's tool call; here the agent is the actor, and the floor does not list `.mcp.json`, `.claude/agents/` or skill front matter. The floor can be extended, but the writer would still need its own allow list. | Rejected as the mechanism, kept as a second check on the paths |
| Let a session tighten its permission mode (the issue) | Tightening is safe in effect. It breaks ADR 0034's invariant, adds a mode ordering the session must obey, and gives a hijacked session a way to stop the user's work (plan mode). | Rejected for the first version; the same request flow could carry it later |

### Where it is written and when it takes effect (decision 4)

**Recommended: the owner's agent writes after the core records approval; the vendor reads the file when it next
starts; settings apply to the live process.**

- **The flow.** The tool calls `POST /api/sessions/:id/config-changes` with the session's token. The core checks
  the capability and the kinds, sends `runtime.config.preview` to the owner's agent (it reads the files, computes
  the diff and hashes, runs the content check) and stores the result as `pending`. On the user's approval the core
  sends `runtime.config.apply` (`changeId`, `sessionId`, the objects with base and new hashes); the agent re-reads,
  compares the base hash, writes each file through a temporary file and a rename in the same directory, keeps the
  file's mode, and answers `runtime.config.applied` with the new hashes or `runtime.config.failed` with a reason
  (`stale`, `unsafe-content`, `not-found`, `io`). A change set is applied all or nothing: on a failure the files
  already written are restored from what the agent just read. The web UI never writes a file; the core holds the
  text and the decision. Wire messages are added to `shared/src/wire/` and `agent/internal/sync/protocol.go` in
  the same pull request.
- **The session is told.** The tool result of a proposal is `pending`; `get_change` returns the state. The session
  is not interrupted when the change is applied, and the new text is not injected into a running conversation:
  that would be a message the user did not write.
- **When it takes effect.** A `CLAUDE.md`, skill or command is read by the vendor when its process starts and,
  for skills and commands, possibly when it rescans its directories. Hoplet does not restart a process to apply a
  change. The card says "takes effect when the session next starts" and, where the driver reports a rescan
  (`selfConfig.reload`), "takes effect now"; a Restart button on the card uses the existing resume path and is
  offered only while the session is `ready`. `model` and `effort` go through the existing live config route (ADR
  0018) and take effect on the running process at once. Which vendors rescan is verified in the spike before the
  card promises it.
- **Running sessions.** A change can be proposed and applied while the session runs: it touches project files, not
  the session's byte stream (ADR 0002).
- **Handoff.** A pending request ends as `expired` when a handoff commits or the session stops (ADR 0034 does the
  same for hand-off requests); the new owner's session can propose again. Applied project files are part of the
  working tree, so they travel in the workspace snapshot or the commit like any other change (ADR 0009). The undo of
  an applied change after a handoff is a request to the new owner, checked against the hash in the new clone; if
  the work moved on, it answers `stale`. An approved change whose agent is offline is sent on reconnect for up to
  24 hours, then `expired`.
- **Child sessions (#81).** A child has these tools only if the user gave it the capability; it does not inherit
  its parent's, and it can propose changes only to its own working directory.

| Option | Cost | Verdict |
|---|---|---|
| Agent writes after approval, the vendor reads at its next start (recommended) | Two message pairs (preview, apply), hash checks, an all-or-nothing writer. The card is honest about timing. | Recommended |
| Restart the vendor process after every applied change | The change is live when the user expects it. A restart kills a running turn or waits for it, and the process, its MCP spec and `Release` (ADR 0034) change under the session. | A button on the card, not automatic |
| Inject the new content into the conversation as a message | Takes effect now. Puts into the conversation something the user did not type, which the byte cursor then stores as a user line. | Rejected |
| The core stores the file and the agent reads it before each start (Hoplet becomes the source of the file) | Easy history and undo. The file is no longer the one in the project and git, and a hand edit is overwritten. | Rejected |

### Documents and order of work (decision 5)

The trust-boundary changes, the documents that change with the implementation and the order are given below. None
of them is part of the pull request that adds this record.

## Cost

Two capabilities and eight tools in the session MCP server, a `config_changes` table with stored previous versions
and a retention job, two message pairs in `shared/src/wire/` and `agent/internal/sync/protocol.go`, a writer, a diff
and a content check in the agent per vendor format, a driver capability `selfConfig`, a card with a diff and an undo
in the web UI with strings in `web/src/i18n/zh-Hans.json` and a mockup in `design/screens/` (two or three
directions, confirmed by the maintainer before code; this record claims no confirmation), five audit events and the
error codes `stale`, `unsafe-content` and `unsupported`. The route-table test of ADR 0033 covers the new routes with
the session token. The persistence risk is bounded by the project scope, the closed list, the content check, the
diff the user reads and the undo, not removed: a user who approves a harmful sentence has approved it.

## Alternatives

- *Do nothing; the file tools already work.* True, and they stay what happens without `write-config`. They give a
  session in a permissive mode a silent, unrecorded, non-undoable way to rewrite the prompt of every later session
  of the project.
- *Deny `CLAUDE.md` and skill writes to the vendor's tools through the floor.* Stops the silent path and
  self-management with it, and ties the compiled-in floor to file names the vendor owns. Possible as an operator
  choice in `permission_deny`, which exists already.
- *Let the web UI write the file.* The issue rules it out; the agent is on the machine and the web UI never writes
  files.

## Documents that change with the implementation

None changes in the pull request that adds this record. The implementation pull requests update `docs/spec.md`
(a section on sessions changing their own configuration beside the Memory section of #18: the card, undo and the
audit events), `docs/api.md` (the routes under `/api/sessions/:id/config-changes`, the host-only decide and undo
actions, the `read-config` and `write-config` capabilities, the error codes), `docs/threat-model.md` (below),
`docs/hosting.md` (the `config_changes` table, the 30-day retention of previous versions, and that the agent writes
into the working directory of a session), `docs/architecture.md` (the writer, the driver capability and where a
request becomes a file), ADR 0029 (the floor is unchanged and is not the mechanism for these objects), ADR 0034 (the
two capabilities and the tool list; the invariant keeps its words), ADR 0030 (the five events),
`shared/src/wire/` together with `agent/internal/sync/protocol.go`, and `web/src/i18n/zh-Hans.json`. Order, one
concern each:

1. The core: the capabilities, the `config_changes` table, the routes with the closed object kinds, the decide and
   undo actions, the retention job, with tests over the route table (no agent, no UI).
2. The read tools (`get_config`, `list_skills`, `read_skill`, `read_memory`) and the shared reading request that #12
   and #18 use: the wire, the agent, the tools. Verified with the fake Claude, not a real session. This is the
   issue's read-only first step and can ship without the rest.
3. The vendor spike: where each vendor loads these files, what its front matter and import syntax allow, and
   whether it rescans at runtime. The result goes into this record.
4. The agent writer: preview, apply, hash check, all-or-nothing, the content check, with tests on a temporary
   directory including symbolic links and hostile names.
5. The web UI: the card with the diff, approve, decline, undo, and the `write-config` switch in the session's
   settings, once the mockup is confirmed.
6. The write tools and `get_change`, with `model` and `effort` through the existing config route.
7. The Codex driver.

## Trust boundary changes

One write path is added and one boundary changes meaning. **Session model to the project's files via the agent**:
until now the model wrote its project files only through the vendor's tools, under a permission mode the user
chose; now it can also ask the agent, which acts with the machine user's rights, to write them. The path is
narrower than the file tools (a closed list of kinds, a content check, the session's working directory only) and
stricter (a user confirmation in every mode, a diff, a stored previous version), and it is the only place where the
agent writes into a project at a model's request. **Persistent prompt injection**: a hostile page could mislead
one session; through this path it could ask for a sentence that outlives the session. It is bounded by the user
reading the diff, the project scope and undo, and it is not prevented.

Threat-model entries to add: a session writes outside its allowed objects (path, symbolic link, name, a new object
kind); a skill or command is used to grant itself tools or run a command (front matter and shell lines); a
proposal differs from what the user looked at (the diff is the agent's own and the apply checks the hash); a
persistent injection into `CLAUDE.md` or a skill; a secret enters a change record or a notification; a session
switches on its own `write-config`. The existing row "A session widens or removes its own rules" gains the
sentence that a skill or command cannot carry `allowed-tools`. No new route is open to the network, no session can
change rules, modes, tokens, devices, its own capabilities or the audit log, and the sync cursor and the stored
lines are unchanged.

## Revisit triggers

User-level files become wanted for writes (a separate switch and a second warning on the card); the cards prove
noisy and a batched proposal or a narrow auto-approve is wanted; a session should tighten its mode; a vendor reloads
`CLAUDE.md` into a live conversation; a vendor's skill format gains a field that grants power the check does not
know; users or roles arrive (ADR 0005).
