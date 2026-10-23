# 0035 Child sessions work on parts of a task in parallel

- Status: proposed
- Date: 2026-10-23
- Related: ADR 0002, ADR 0007, ADR 0012, ADR 0021, ADR 0023, ADR 0024, ADR 0029, ADR 0030, ADR 0031, ADR 0033, ADR 0034, docs/api.md, docs/architecture.md, docs/threat-model.md, docs/hosting.md, docs/spec.md, issues #9, #67, #68, #70, #72, #75, #80, #81

Not yet decided. The maintainer confirms or changes each decision below; the recommendation is marked in each table.

## Context

Issue #81 asks that a session can split a task into parts that other sessions work on in parallel, on other
machines and in their own worktrees, then wait for them and collect what they did without pulling their whole
conversations into its own context. Its done-when is this record: the tool interface, how the result summary is
made, the default limits for depth and concurrency, and the permission-inheritance rules.

ADR 0034 is built and is the base. This record adds a lifecycle on top of it and does not repeat it. What exists
today, read from the code and the documents:

- **A child already exists.** `start_session` starts a session whose provenance (`session_provenance`, the chain of
  session IDs that led to it) is recorded by the core from the session token. The child gets the sender's
  permission mode or the closest below it, never a `risk: "high"` option, and never a mode more permissive than the
  sender's. `start_session` takes `machine`, `projectPath`, `permissionMode` and `worktree` (a boolean: a new
  worktree of the project, `hoplet/<8 hex>`, made by the agent before the provider starts; docs/spec.md, "In a new
  worktree"). The core refuses with `forbidden`, `loop-limit`, `rate-limited` or `invalid-request`.
- **Limits that exist.** A chain of at most 3 sessions (`MAX_SESSION_CHAIN`), 5 starts an hour per session and 10 live
  children per session (`DEFAULT_SESSION_LIMITS` in `core/src/control/session-mcp.ts`, first values to try). A
  child counts as live in the states `starting`, `ready`, `running` and `waiting-permission`, so a child that has
  finished its turn and waits idle still holds a slot until its process is stopped.
- **Reading is bounded.** `read_session` returns a page of at most 32 KiB, never a whole session (ADR 0007). Visibility
  (`project`, `machine`, `all`) and the `send` and `create` switches are per-session settings that only the user
  sets (ADR 0034).
- **Waiting exists only in the SDK.** ADR 0033 infers "the turn finished" from the runtime states (`ready` after a
  start, `failed`, `idle`, `offline`, `external`, `waiting-permission` as blocked). No MCP tool waits.
- **Changes of a worktree can be read, but not listed.** The agent can return one file's diff
  (`GET /api/sessions/:id/workspace/diff`) and a directory listing, and it reports a branch name. No route returns
  "the files this session changed". The change list of a handoff snapshot exists only for a handoff.
- **Usage and budgets exist per session and per scope.** The usage table records tokens and an estimated cost by
  session, project, machine and model (docs/spec.md, "Usage"). A budget (#75) has a scope of everything, one project
  or one machine, and a `warn` or `pause` action. There is no budget for a tree of sessions.
- **The UI knows no parent.** Provenance is stored and labels a message in the target, but the session list and the
  session API do not carry a parent.
- **One rule is documented but not found in code.** docs/spec.md and the threat model say a child obeys its
  parent's `deny` and `ask` rules and gets none of its `allow` rules. A search of `core/` and `agent/` finds only the
  mode ceiling. The implementation must deliver the rule, or the documents must change; decision 4 asks the
  maintainer to keep it.

What is new is therefore: a relation the user and the parent can see and act on (children of X), the wait, the
result and the change list, limits for a tree of work, a way to cancel a child or a subtree, and the UI.

## Decisions

1. Tool interface. Recommended: keep `start_session` as the only way to create a child; add `wait_children`,
   `collect_child`, `read_child_diff` and `cancel_child`. Wait is a long poll of at most 55 seconds, called again
   until done.
2. The result. Recommended: the summary is the last assistant message of the child's last turn, which the child
   was told to write as a report; the change list is a `git` listing of the child's worktree taken by the agent.
3. Defaults. Recommended: depth 2 below the root, 4 live children per parent, 8 live in one tree, 12 starts an
   hour per session, a 30-minute limit per child and a token cap per tree; a `pause` budget (#75) also stops a
   spawn. The numbers sit under ADR 0034's ceilings and are first values.
4. Permission inheritance. Recommended: everything a child has is the intersection with its parent's; it never
   gets `request-handoff`; it gets `create` only when the spawn asks for `nested`; a new worktree is the default.
5. Lifecycle. Recommended: an interrupted or failed parent leaves its children running; stopping or deleting a
   parent asks about its children; a finished child is stopped when its result is collected; cancelling is by
   child or by subtree.
6. UI. Recommended: a tree in the session list and a children panel in the session, after a mockup the maintainer
   confirms. This record states what the UI needs and nothing about how it looks.

## Decision

### The tool interface (decision 1)

**Recommended: a child is a session started with `start_session`; four new tools complete the loop.**

- A child is not a new kind of session. It is a session whose provenance chain ends in the parent. "Children of
  X" is what `store.sessionChildren` already computes from that chain, so no table is added for the relation; a
  `parentId` is derived on read. This keeps ADR 0034's checks (token, mode ceiling, `forbidden`, `loop-limit`,
  `rate-limited`, audit) as the only path to create anything.
- `start_session` gains `worktree` defaulting to `true` for a project that is a git repository (decision 4), and an
  optional `title` the list shows. It returns `{ sessionId, worktree?: { path, branch } }`. It does not wait.
- `wait_children({ sessionIds?, until: "all" | "any", timeoutSeconds? })` waits on the caller's own children (all of
  them when `sessionIds` is absent; an ID that is not its child is `session-not-found`). It returns each child's
  `state` (`running`, `done`, `failed`, `blocked`, `ended`, `timed-out`, `cancelled`) and the elapsed time. `done`
  is ADR 0033's "finished" (the runtime reached `ready` after a start). `blocked` is a child waiting for a
  permission, which the parent cannot answer (`answer` is never available to a session) and so reports to the
  user. It returns at once when the condition already holds.
- `collect_child({ sessionId })` returns the result (decision 2): `{ state, summary, changes, usage }`, at most 8 KiB
  in total. `summary` is cut with a marker; `changes` lists at most 200 files and says if it was cut.
- `read_child_diff({ sessionId, path, cursor? })` returns one file's diff by the existing diff path, in pages of at
  most 32 KiB. This is how the parent looks at a change; no tool returns the whole diff.
- `cancel_child({ sessionId, subtree? })`: decision 5.
- `read_session` stays as it is. A parent that wants a child's conversation reads it in pages, as for any session,
  under the same visibility. Because a child's machine is limited to the parent's visibility (decision 4), a child
  is always in scope.

**Why a wait that returns after 55 seconds.** An MCP tool call that does not return holds the parent's turn, and
the vendor's tool timeout applies (Codex's default is believed to be 60 seconds; Claude Code's is far longer).
Neither is verified here: a spike on both installed binaries confirms the limits before code relies on them, as
ADRs 0029 and 0032 did, and the result goes into this record if it differs. The tool returns `pending` with the
states after 55 seconds (or `timeoutSeconds` when smaller) and the model calls it again. The parent's turn stays
open and is shown as "waiting for 3 children".

| Option | Cost | Verdict |
|---|---|---|
| `start_session` plus `wait_children`, `collect_child`, `read_child_diff`, `cancel_child` (recommended) | Five tools for a model to learn and carry in its context (the definitions are about 1 KiB). One creation path, so ADR 0034's limits cannot be bypassed. The issue's `spawn_subtask` is `start_session` with the parent implicit. | Recommended |
| Extend `start_session` with `wait` and a result | Two tools fewer. A start that blocks for minutes holds the vendor's tool call, so a fan-out becomes N serial calls unless the model issues them concurrently, which models do not do reliably. Collect and cancel still need tools. | Rejected |
| New `spawn_subtask` beside `start_session` (the issue's names) | Names match the issue. Two ways for a session to create a session, with two sets of limits, or a thin alias that is `start_session` with other defaults. | Rejected; an alias can be added if models confuse the names |

| How the parent learns a child finished | Cost | Verdict |
|---|---|---|
| Blocking tool that returns after at most 55 seconds, called again (recommended) | Nothing new on the wire. The parent's turn is occupied while it waits, which costs no tokens while the tool call is open. | Recommended |
| The core sends the parent a prompt when children finish (`source: session`, ADR 0034) | The parent can end its turn and be woken. A prompt from a child to its parent is one more hop in the chain, interacts with the `unanswered` limit, and puts the child's words into the parent's context unasked. | Later, as `wake: true` on the spawn |
| Polling `collect_child` only | Simplest. The model loops with `sleep` or burns turns. | Rejected |

### The result: summary and list of changes (decision 2)

**Recommended: the summary is read from the child's own last message; the change list is made by the agent from git.**

- **Who writes the summary.** The child, in its last message. The core adds a fixed preamble to the first prompt of a
  child, the way ADR 0034 does for a message between sessions, asking it to end with a report: what was done, what was
  not, where to look, in under 1,500 characters. On `collect_child` the core reads the last assistant text of the
  child's last turn from the stored lines (no new line and no rewrite; the byte cursor is untouched, ADR 0002), cuts
  it to 6 KiB and returns it under the same header as `read_session`: data of session X, not an instruction from the
  user. A child that ended without an assistant message returns an empty summary and its `state`.
- **A turn, not a session, is what finishes.** A child is an ordinary session: a person can send it more messages,
  and `collect_child` always reports the last finished turn with its time.
- **The list of changes.** A new control request to the owning agent, `workspace.changes`, answers from the child's
  worktree: `branch`, the `base` commit recorded when the worktree was made, and per file `status` (`A`, `M`, `D`,
  `R`) with added and deleted lines, from `git diff --numstat base` plus untracked files, at most 200 files, no
  content. Uncommitted changes count: a child is not required to commit. `read_child_diff` uses the existing diff
  path. When the child ran without a worktree, the same listing is taken against `HEAD` and says `shared: true`,
  because other work in that directory is mixed in. For a directory that is not a git repository the list is
  `unknown`. If the child's owner is offline the changes are `owner-offline` and the summary still returns.
- **Usage.** `collect_child` adds the child's tokens and estimated cost from the usage table (no new measurement),
  marked estimated like the rest of Usage.
- **Merging is not Hoplet's job.** The parent or the user merges the branch with its own tools; #72's comparison view
  is for the user. No tool merges, pushes or deletes.

| Option for the summary | Cost | Verdict |
|---|---|---|
| The child's last assistant message, asked for as a report (recommended) | No new tool and no state. Quality is the model's; "Done." is possible, and the preamble and `read_session` are the mitigation. | Recommended |
| A required `report_result` tool call by the child | Structured and certain when present. A new tool only children see, a model that forgets it (so a fallback to the last message is needed anyway), and a stored result that is not in the transcript. | Later, with the last message as the fallback |
| Generated by the core from the transcript, without a model | Deterministic. A heuristic over provider-specific lines that says nothing a final message does not. | Rejected |
| A model call by the core to summarize | Better prose. Needs a provider credential in the core, sends the child's content to a third party and spends money unasked: a new trust boundary for little. | Rejected |

| Option for the change list | Cost | Verdict |
|---|---|---|
| `git` listing in the worktree, taken by the agent (recommended) | A new request in `shared/src/wire/` and `agent/internal/sync/protocol.go`; reuses the worktree marker and the diff path; sees changes made by shell commands. Needs the machine online and git. | Recommended |
| Files touched, from the child's Edit and Write tool calls | Core only, works offline, no wire change. Misses what a shell command wrote and gives neither line counts nor a branch. | Rejected as the source; may label files in the UI |
| Both | More to keep in step; the two answers can disagree. | Rejected |

### Defaults and what is measured (decision 3)

**Recommended: keep ADR 0034's limits as ceilings and add limits for a tree of work.**

What Hoplet can measure today: how many sessions are live (runtime states), the wall-clock time since a session
started (runtime states with timestamps), tokens and estimated cost per session (the usage table, written as the
session syncs, so it lags a running turn by a message), and a count of prompts. It cannot measure a "turn count" that
means work: a child that runs one long turn is one turn. So the limits are time, tokens and counts of sessions.

| Limit | Default | Relation to ADR 0034 |
|---|---|---|
| Depth below the root | 2 (root, child, grandchild) | ADR 0034's chain of 3 stays the ceiling for every agent hop, children included; the child depth is stricter and refused as `depth-limit` |
| Live children per parent | 4 | Below ADR 0034's 10, which stays the ceiling; refused as `children-limit` |
| Live sessions in one tree | 8 | New. A `ready` child counts as live until it is stopped (decision 5) |
| Starts an hour per session | 12 | Raised from 5 (a fan-out of five would leave no room to re-run one). Applies to every `start_session` |
| Time per child | 30 minutes since it started; at most 2 hours if the spawn asks | New. Past it the core interrupts the child, then stops it; its state is `timed-out`. The partial work stays in the worktree |
| Tokens per tree | 3,000,000 input plus output (cache reads excluded), checked at each spawn and every 30 seconds | New. Past it the core interrupts the live descendants and refuses spawns with `budget-exceeded` |
| Budgets (#75) | as set | A spawn is refused with `budget-exceeded` while a `pause` budget whose scope holds the project or machine is at its limit. Child usage already counts in a budget, because a child is a session of that project and machine |

- **Refusals.** The new codes are `depth-limit`, `children-limit` and `budget-exceeded`; `rate-limited` keeps the
  rate limits. A refusal names its reason, the tool description says not to retry one that is not `rate-limited`,
  and each is audited as `token.denied`.
- **Where they are set.** In the core's configuration, as ADR 0034's numbers are. The issue asks for them per
  project; ADR 0034's project defaults can carry the same fields. The recommendation is the core configuration
  first and a per-project override when a user needs it.
- **Tree.** A tree is all descendants of a root, found from provenance. The root is the earliest session of the chain
  that a user or a script started.

| Option | Cost | Verdict |
|---|---|---|
| Time and tokens per tree, counts of sessions, #75 for money (recommended) | A watcher in the core, which already receives usage and states; three new error codes. Honest about what is measured; tokens lag a message. | Recommended |
| Counts only (depth and concurrency) | Nothing to build beyond ADR 0034. A runaway tree is bounded in width, not in cost or time. | Rejected |
| A cost cap per tree in dollars | The unit a user thinks in. Unpriced models report no cost, so the cap would be silent for them. | Possible later, beside tokens |
| Turn limits | Easy to count. A turn is not a unit of work here. | Rejected |

### Permission inheritance (decision 4)

**Recommended: a child's power is the intersection with its parent's, taken at spawn and re-checked at each call.**

| What | Rule | State |
|---|---|---|
| Permission mode | Not more permissive than the parent's; defaults to the parent's; never a `risk: "high"` option | Built (ADR 0034) |
| Allow, ask and deny rules | The child obeys the parent's `deny` and `ask` rules and gets none of its `allow` rules (docs/spec.md, ADR 0029). The deliverable is that the parent's session-scoped rules reach the child's agent with its start | Documented, not found in code; to implement |
| Session MCP switches | `read` and `send` are inherited when the parent has them; `create` only when the spawn asks `nested: true` and depth remains; `request-handoff` and auto-approve are never inherited. Only the user changes a child's switches, as for any session | New |
| Visibility | At most the parent's. A `machine` or `all` parent lets a child be placed on those machines; a `project` parent only on its own machine and project | New check on `machine` |
| Token | A `session` token as ever (ADR 0034); no child gets the `answer` capability | Built |
| Machine and project | The parent names `machine` and `projectPath` inside its visibility; the project must be one the target machine watches. Choosing by capability (#70, tools and GPU from the inventory) is a `requires` field added later; at first the parent picks from `list_machines` | Machine choice built; `requires` later |
| Worktree | A new worktree by default for a git project, the parent's directory otherwise; `worktree: false` is allowed but marks the changes `shared` | Worktree built; default new |
| Uncommitted changes | A new worktree starts from the project's `HEAD` and does not carry uncommitted changes (docs/spec.md). The parent commits first or puts what the child needs in the prompt; the tool description says so | Built, stated |
| A child asking for a permission | The card is on the child and notifies the user; the parent sees `blocked` and cannot answer | New state in `wait_children` |

| Option for `create` in a child | Cost | Verdict |
|---|---|---|
| Not inherited; the spawn asks `nested: true` (recommended) | One more field. The default fan-out is flat, and a hijacked child cannot spawn by default. | Recommended |
| Inherited whenever the parent has it | No field. Every child is a possible spawner, bounded only by depth and counts. | Rejected |
| Never; depth is always 1 | Simplest. Rules out a planner that delegates to sub-planners. | Rejected; the depth limit is the bound |

### Lifecycle (decision 5)

**Recommended: children are sessions with their own life; only an explicit stop or cancel from the user or the parent ends one.**

| Event | What happens to the children |
|---|---|
| The parent's turn is interrupted or ends | Nothing. A child is a separate session, possibly on another machine. A parent that ended its turn without collecting leaves the children to finish; the user sees them in the tree and tells the parent. An interrupted `wait_children` simply returns |
| The parent fails, goes offline or its agent disconnects | Nothing. The parent's token is revoked after the reconnection window (ADR 0034); a child keeps its own. The tree shows the parent as failed |
| The parent is stopped from the UI | A dialog asks "Also stop N running children", default yes; the route takes `cascade: true` for the same |
| The parent is deleted (ADR 0023) | The dialog lists live children and offers to stop them. The sessions stay and show "started by a deleted session"; their provenance is kept. Worktrees stay, since deletion never removes what the user did not agree to |
| The parent is handed off (ADR 0024) | The relation is by session ID and is unchanged. The new owner starts the parent with a new token and `wait_children` works as before. Children are not moved |
| A child is handed off | The relation is unchanged; its changes are read from its new owner and its worktree there |
| A child finishes its turn | It is `ready` and holds a slot. When the parent collects its result with `release: true` (the default), the core stops the process and keeps the session and worktree; the user may still open it and send a message, which starts it again as for any stopped session |
| `cancel_child` by the parent | Interrupts the child's turn, then stops it. Needs `create` and a descendant; `subtree: true` includes its descendants. A narrow addition to ADR 0033's rule that stop is host-only: descendants only, audited |
| Cancel from the UI | Per child or per subtree, with the host token, on the stop route with `cascade` |
| The time or token limit is reached | Interrupt, then stop, state `timed-out`; `wait_children` tells the parent |

- **Orphans.** There is no orphan collector. A session without a live parent is a normal session; the tree keeps
  showing the relation from provenance so a person can find and finish it. Nothing is deleted automatically.
- **Idempotence.** A repeated `start_session` with the same `promptId` returns the first result (the existing rule),
  so a parent that retries after a timeout does not start a second child.
- **Audit.** Spawn (`session.created` with actor `session`), `child.collected`, `child.cancelled` and `child.limit`
  (a time or token stop). They are added to ADR 0030's list.

| Option | Cost | Verdict |
|---|---|---|
| Children keep running unless a user or the parent stops them; stopping a parent asks (recommended) | A dialog and `cascade`. Never loses work silently. | Recommended |
| Children die with the parent | No orphans. A parent that fails after four hours of its children's work loses it, and interrupting a turn kills the team. | Rejected |
| Children are never stopped automatically, finished ones included | Simplest. Finished children hold slots, so a tree stalls after 8 children. | Rejected |

### The UI (decision 6)

**Recommended: state the needs now; draw two or three directions in `design/screens/` before any web code.** This
record does not claim a confirmation the maintainer did not give.

- The session list shows a session with children as a tree: a parent row with a count and a rollup state (running,
  waiting for a permission, done, failed), children below it, indented and collapsible, each with its machine and
  worktree branch. A child that is a parent shows its own children. A session whose parent was deleted says so.
- A parent has a children panel: each child with its state, elapsed time, tokens, the summary when done and the file
  list of its changes; a click opens the child like any session. The user can view, message and hand off any child.
- Cancel per child and per subtree, with a confirmation that says what stops and what stays (worktrees stay).
- A child waiting for a permission is visible from the parent and in the list, and notifies as a permission request
  does (ADR 0021).
- The session API carries `parentId` and `childCount` (derived), the list can be fetched as a tree, and the browser
  WebSocket sends a child's state changes to the parent's subscribers. Strings go into
  `web/src/i18n/zh-Hans.json`.

## Cost

Four tools in the agent's MCP server, a `workspace.changes` request on the wire (`shared/src/wire/` and
`agent/internal/sync/protocol.go` in one change), a core component that watches a tree for time and tokens, a
`cascade` on the stop and delete routes, a `parentId` on the session API, three new error codes (`depth-limit`,
`children-limit`, `budget-exceeded`), four audit events, a tree in the session list, a children panel and strings.
The parent's turn is held while it waits, which a user sees as a long-running parent. A fan-out multiplies cost by the
number of children, bounded by the limits and not removed. A child's summary is text written by a model that may have
read hostile content; it is marked as data and is a way for prompt injection to reach the parent, bounded by the mode
ceiling, the rules and the limits as in ADR 0034.

## Alternatives

- *Use only #67's tools on sessions the user created.* Needs no code; there is no parent, no lifecycle and no summary,
  which is what the issue asks to fix.
- *The vendors' own subagents.* One process, one machine, one directory; not visible or controllable in Hoplet, no
  separate worktree. They remain available and are not in the way.
- *An orchestrator in the core that holds the plan.* A different product (ADR 0034 rejected it).
- *A `children` table separate from provenance.* A second source of truth for one fact; the chain already answers it.

## Documents that change with the implementation

None changes in the pull request that adds this record. The implementation pull requests update `docs/api.md`
(`parentId`, `cascade`, the changes response, the new codes), `docs/spec.md` (a section for child sessions, the limits,
and the rules sentence if decision 4 changes it), `docs/architecture.md` (the child relation, the tree watcher, where
the changes request is served), `docs/threat-model.md` (below), `docs/hosting.md` (the new limits in the core's
configuration; ports and data only if a table is added), ADR 0030's event list, ADR 0034 (the `worktree` default of
`start_session` and its raised hourly limit), `shared/src/wire/` with `agent/internal/sync/protocol.go`, and
`web/src/i18n/zh-Hans.json`. Implementation order, one concern each:

1. The core: `parentId` and tree queries on the session API, the new limits and codes in `guardStart`, the time and
   token watcher, `cascade` on stop, with tests over the route table. No agent, no UI.
2. The parent's rules for a child (decision 4) in core and agent, with tests that a child cannot be given an `allow`
   rule or a wider mode.
3. The agent and wire: `workspace.changes` and the diff paging for a child.
4. The MCP tools: `wait_children`, `collect_child`, `read_child_diff`, `cancel_child`, the report preamble and the
   `start_session` defaults; verified with the fake Claude, and with the Codex driver after the spike on tool timeouts.
5. The mockups in `design/screens/` (two or three directions), then the web UI once the maintainer has confirmed one.
6. The threat-model entries, the audit events and an e2e flow across two fake machines.

## Trust boundary changes

No new network surface and no new credential kind. A session can now stop sessions (its descendants), which ADR 0033
and 0034 reserved for the host token; this is new authority, limited to descendants, audited and bounded by the
`create` capability. A parent now receives text that a child wrote, which makes a child a channel of prompt injection
into its parent: the summary and diffs are marked as data and bounded in size, and the parent's own mode and rules
still decide what it does next. The core now acts without a request from a session (it stops a child at a time or token
limit). The threat-model entries to add: a session cancels sessions beyond its subtree; a hostile child steers its
parent through its summary or diff; a fan-out exhausts money or time; a child gets power its parent lacks (the
intersection rule); a child's worktree is read by a session that is not its parent. They are verified by tests over the
route table with a session principal and by the tree-limit tests.

## Revisit triggers

A user needs the parent woken when children finish (`wake`); the final message proves too poor a summary and the
`report_result` tool is wanted; the vendors' tool timeouts differ from the assumption; a per-project limit is asked for;
a cost cap in dollars is wanted; choosing a machine by capability (#70) is wanted at spawn; users or roles arrive
(ADR 0005).
