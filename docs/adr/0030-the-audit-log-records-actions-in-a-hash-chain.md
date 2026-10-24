# 0030 The audit log records actions in a hash chain

- Status: accepted
- Date: 2026-10-21
- Related: ADR 0002, ADR 0005, ADR 0007, ADR 0012, ADR 0021, ADR 0023, ADR 0026, ADR 0029, docs/architecture.md#principles, docs/hosting.md#data, docs/threat-model.md, docs/spec.md#audit-log, issues #9, #67, #71, #79, #80, #81, #97, #98

## Context

A session that runs without someone watching leaves one record of what happened: the vendor's session file, which
Hoplet stores verbatim and does not own. It holds the tool calls, but not who answered a permission, who switched a
session to Bypass permissions, who minted a device token or changed a budget, or that a session was handed off or
deleted (deleting a session removes its lines, ADR 0023). The core log has some of those events, as IDs and codes
on standard output, with no query, no retention of its own and no way to tell whether a line was removed
(`core/src/log/index.ts`). The unattended-sessions theme (permission rules #98, lifecycle hooks #97, auto
verification #79) adds actors that are not the user, which makes "who did this" the first question after
something goes wrong.

What the code does today, which decides the design:

- **One operator, no user identity.** The host token authorizes every `/api/*` route (`auth: 'host'` in
  `core/src/api/routes.ts`; ADR 0005); an agent has a device token and reaches `/ws/agent` only
  (`core/src/ingest/index.ts`, `deviceIdForToken`). A request made with the host token does not say which
  browser, person or CLI made it, and the core keeps no list of host-token clients. An action link (ADR 0021) is a
  third way to answer a permission, anonymous but bound to one request (`core/src/notify/index.ts`, `decide`).
  The audit actor of an operator action is therefore "the host token" plus the channel; it is not a person.
- **Where the actions happen in the core.** A permission is raised by `runtime.permission` and answered in
  `control/runtime.ts` (`answerPermission`, which logs `permission answered`); a setting changes in `config` (same
  file), reached by `POST /api/sessions/:id/config`; a stop in `stop`; a handoff in `control/handoff.ts` (`run`);
  a session starts in `control/start.ts`; a deletion in `control/deletion.ts` and `store/deletion.ts`
  (`purgeSession`, one transaction); a device token is minted by `mintDevice` in `store/devices.ts`, which replaces
  the token when the `device_id` exists, so there is no revoke route and minting again is the revoke; a budget, a
  label, a provider default, a notification preference and a price override each have a store module and a route;
  the browser's terminals are in `control/terminals.ts`. No schedule exists in the core yet, and neither does
  session-to-session control or auto verification.
- **Tool calls are in the lines, not in the core's tables.** The core already derives from appended lines without
  changing them: `store/chunks.ts` calls `extractUsage` (`store/usage-lines.ts`, one extractor per vendor) in the
  transaction that appends, and the web projection maps `tool_use` blocks and Codex items per vendor
  (`api/projection/claude.ts`, `codex.ts`). Nothing extracts a command or a changed path into a table.
- **Credential scrubbing exists twice.** The log redacts registered secrets, `Bearer` values, `token=` values and
  sensitive field names (`log/index.ts`, `redact`); notifications mask URL userinfo, authorization headers,
  `NAME=value` pairs whose name says credential, query-string credentials and known key shapes
  (`notify/redact.ts`, `redactText`). Neither is shared.
- **No retention exists** for anything but action-link tokens (`store/notify.ts`) and attachments; a session stays
  until the user deletes it.
- **Reads are pages** (docs/api.md): list routes take `limit` with a ceiling, and no client receives a whole session
  (ADR 0007).
- **ADR 0029 (#98)** has the agent report each decision a permission rule makes; those decisions need a home in
  the core.

The risks are the point. An audit log that stores the command a session ran stores whatever credential was typed in
it. A log kept in a database the operator can edit proves nothing to anyone who is not the operator. And a log whose
derivation costs time on the append path slows the thing Hoplet exists to do.

## Decision

**One append-only table, `audit_events`, in the core's database.** It is separate from session content (nothing
here is a session line) and shares the database file, so the backup, the restore and the data directory are those of
docs/hosting.md#data; there is no second store to run. A record has:

- `seq`: the position in the one global chain, increasing, never reused.
- `at`: when the action happened (Unix ms; for something an agent reported, the agent's time) and `recorded_at`:
  when the core wrote it.
- `type`: from the list below.
- `session_id`: the session, or none. Kept after the session is deleted.
- `device_id`: the machine the action concerned (the session's owner when it happened), or none. A handoff does not
  change the records already written.
- `actor_kind` and `actor_id`: who did it (below). `channel`: how it reached the core (`rest`, `link`, `agent`,
  `core`).
- `ref`: an ID inside the session the record points to (the tool use ID, the request ID, the prompt ID), indexed
  with `session_id`.
- `summary`: canonical JSON of the event's fields (below), at most 2 KiB.
- `prev_hash` and `hash` (below).

Nothing in the core updates or deletes a row except retention (below). No REST route and no agent message writes a
row directly: the core writes records from its own code, and an agent can only report facts of a defined type about
a session it owns (an auto decision of #98, a result of #79), which the core validates, stamps with `channel:
agent` and the agent's device ID, and writes.

**Actors.** `operator` (the host token; `actor_id` empty, `channel` `rest`, or `link` for an action link, whose
record also carries the link's request ID); `token` (a scoped API token, ADR 0033; `actor_id` is the token's ID,
`channel` `rest`, so the log names which token acted, though still not who holds it); `device` (an agent acting on its own, such as an auto decision, with its
device ID); `session` (a session acting through Hoplet's own tools, #67 and #81, with its session ID); `schedule`
(a schedule, with its ID); `core` (Hoplet itself, with a reason: `retention`, `startup`, `permission-timeout`,
`budget`). `session` and `schedule` are in the schema from the start and no code produces them until their features
exist. The record does not name a person, or a device that answered through the host token, because the core cannot
know it: an answer made in the web UI, with the CLI or by a script look the same. This is the single-operator model
of ADR 0005, stated in the log rather than hidden by it; roles would change it (Revisit triggers).

**Events recorded.** `summary` holds only what is listed, as IDs, names, codes and short scrubbed text.

| Type | When | `summary` holds |
|---|---|---|
| `tool.call` | derived from a session line that holds a tool call | tool name, `command` for a shell tool (scrubbed, at most 500 characters), `paths` the call changes (from `Edit`, `MultiEdit`, `Write`, `NotebookEdit`, Codex `fileChange`; at most 20, and their count), the line number |
| `permission.requested` | `runtime.permission` arrives | tool name, the same `command` or `paths` summary, whether it is a plan exit |
| `permission.answered` | an answer, or the ten-minute denial | decision, `answered_by` (`operator`, `link`, `timeout`, `session-end`), the mode a plan exit chose |
| `permission.auto` | an agent reports a decision by a rule (#98) | decision, rule ID, scope, rule revision |
| `mode.changed` | a session's permission mode or another config option changes, by a request or by the provider (a plan exit) | option, previous and new value |
| `session.created`, `session.stopped`, `session.interrupted`, `session.deleted` | a session starts, is stopped, interrupted or deleted | machine, project path, provider and configuration, the options chosen; for a deletion what was removed (ADR 0023); for a stop by a cascade the session it started from (`cascade`). The actor is whoever caused it: the parent session for a child it cancels or releases, the core (channel `core`) for a child past a limit |
| `session.handoff` | a handoff ends | source and target machine, outcome and failure code; `session.auto_transfer` when an automatic move (not built) does it |
| `prompt.sent` | a prompt or a steer is accepted | prompt ID, length in characters, number of images; never the text |
| `child.limit` | the core stops a session past a limit of its tree (#81, [ADR 0035](0035-child-sessions-work-on-parts-of-a-task-in-parallel.md)) | the session's ID, the root of its tree, the limit (`time` or `tokens`) and its value; actor `core` |
| `child.collected`, `child.cancelled` | a parent session collects a child (ADR 0035), or cancels a child or a descendant | the child's ID, the parent, and for a collect the state, whether it was released, the number of files and the bytes of the report; never the report, the files' content or a diff; actor `session` |
| `session.op` | one session views or directs another (#67) | the other session's ID, the operation |
| `config.changed` | a setting of the core changes: budgets, notification preferences and webhooks, price overrides, device labels and watched projects, provider defaults, permission rules (#98) | the area, the object ID, the names of the fields changed and, for a field that is not a secret, its previous and new value; a secret's value is never recorded, only that it changed |
| `verify.result` | an automatic verification finishes (#79) | the check's name, outcome, duration |
| `device.token.issued` | `mintDevice` | device ID, name, whether it replaced a token (`replaced`: the old token stopped working) |
| `token.issued`, `token.revoked` | the host issues or revokes a scoped API token (ADR 0033) | token ID, name, the grant (abilities and allow-list) and the expiry; never the secret |
| `token.denied` | a known token asks for something its grant does not cover | token ID, method and route, reason `forbidden`, or `depth-limit`, `children-limit` or `budget-exceeded` for a refused session start; at most one record a minute per token and route. A token that is unknown, expired or revoked is not recorded |
| `terminal.opened`, `terminal.closed` | a shell is opened or closed (ADR 0026) | terminal ID, reason, duration, byte counts; never input or output |
| `audit.started`, `audit.checkpoint`, `audit.exported` | the first start with this feature; retention; an export | for a checkpoint see Retention; for an export the format, the filters and the number of records |

Out, on purpose: prompts, assistant text, tool output and results, file contents, diffs, images, titles,
environment variables, and the content of any command beyond the scrubbed `command` of a tool call and of a
permission request. A tool's exit status is not recorded either; the session holds it. Connections and
disconnections of agents and every `GET` (reads of the log included) are not recorded; the core log has them as
counts. A session the user runs in a terminal outside Hoplet has its tool calls derived like any other, because they
come from its file; what Hoplet did not do (a permission answered in that terminal) is not in the log.

**Tool-call events are derived from lines already stored, and never on the append path.** The sync cursor stays
sacred (ADR 0002): no line is filtered or rewritten for the log. After a chunk is committed, a background deriver
reads the session's lines from its own cursor (`audit_cursor`: session ID and the last line number derived), maps
them through the provider (one extractor per vendor beside `extractUsage`, so a vendor's line shapes stay in its
mapping, ADR 0012), scrubs, and writes the events and the new cursor in one transaction. It runs as bulk work, one
session at a time in batches, yielding between batches. A crash repeats at most one batch and no event is written
twice, since the cursor and the events commit together. Control work is never queued behind it, and a tool-call
event lags its line by seconds under load. Only lines that arrive after this feature starts are derived; an existing
session's earlier history is not back-filled, and `audit.started` says from when the log is complete. A tool input
too large for the summary is cut; the deriver never needs the whole block.

All other events are written by the core's own code, in the same transaction as the change they describe where there
is one (a token minted, a budget changed, a session deleted); an action with no transaction (a permission answer
sent to an agent) writes its record once the core accepted it and before it answers the caller. When a record cannot
be written the action it describes fails with `audit-unavailable`: an action the log cannot record does not
happen. The deriver is the exception; it retries.

**Credentials are scrubbed before they are stored.** The two scrubbers become one module, `core/src/scrub/`
(the notifications' `redactText`, extended; the log keeps its field-name redaction and calls it for text), covering
what they cover today: URL userinfo, `Authorization`, `Bearer` and `Basic` values, `NAME=value` and `--name value`
whose name says token, secret, password, key or credential, query-string credentials, the key formats of the major
services, JWTs and long unbroken runs of key characters with a digit. It runs on every string that enters a
`summary`, before the hash is computed, so the chain covers the scrubbed text and a stored record never holds the
original. A test feeds known secrets through every event type. The residual risk is stated, not solved: a secret with
no recognizable name or shape (`curl -d 'hunter2'`) is stored as written. This is why commands are the only free
text in the log, why they are cut to 500 characters, and why the log is readable only with the host token. The
session file already holds the same command verbatim; the log adds a second copy of the same sensitivity in the same
database.

**The chain: SHA-256 over a canonical record and the previous hash.** `hash` is the SHA-256, in hex, of the previous
record's `hash` (64 zero characters for the first) followed by the canonical JSON (keys sorted, no whitespace,
UTF-8, integers only, strings as stored) of the record's other fields except `hash`: `seq`, `at`, `recorded_at`,
`type`, `session_id`, `device_id`, `actor_kind`, `actor_id`, `channel`, `ref`, `summary`. The chain is global, not
per session: removing every record of one session leaves a gap that verification finds, which a per-session chain
would not. A record is written in the transaction that takes the next `seq` (`BEGIN IMMEDIATE`; the core is one
process and `node:sqlite` is synchronous, so chain order is write order). `hoplet audit verify` (the host CLI, over
`GET /api/audit/verify`) walks the chain from the oldest record, recomputes every hash, checks `prev_hash` and that
`seq` has no gap, and prints the first break with its `seq`, the head's `seq` and hash and the number of records
checked. The walk reads in batches and yields between them. It can also verify an export from a file with no core
running (`hoplet audit verify --file`): the JSON Lines export carries `prev_hash`, `hash` and the canonical
`summary`, so a check does not depend on the code under suspicion.

**What the chain proves, and what it cannot.** It detects a record that was edited, removed from the middle or
inserted. It does not prevent any of that: Hoplet is self-hosted, the database and the code are the operator's, and
someone with write access to the host can rewrite records and recompute every hash after the change, or replace the
verifier. The chain also cannot detect that the newest records were deleted, or that the whole log was replaced by a
consistent forgery. What closes that is a copy of the head the attacker cannot reach: `hoplet audit head` prints
`seq` and `hash`, and `hoplet audit verify --head <seq>:<hash>` checks that the stored record at that `seq` still has
that hash. The operator writes the head somewhere that is not the host (a note, a ticket, a mail, a git commit) as
often as the stakes ask; Hoplet sends it nowhere (nothing leaves the host, docs/hosting.md#network). The documents
say plainly: this is tamper evidence against mistakes and against a session or a stolen token, and no defence against
an attacker who owns the host.

**Retention.** `HOPLET_AUDIT_RETENTION_DAYS`, default `180`, an integer of at least 1, or `0` for no limit. It is an
environment variable and not a setting in the web UI, so that a stolen host token cannot shorten it and erase
evidence; Settings shows the value read-only. Pruning is the only deletion: the core removes records older than the
limit at start and once a day, from the oldest, at most 10,000 per transaction. In the same transaction it writes an
`audit.checkpoint` at the head of the chain with the `seq` and `hash` of the last record it removed. Verification
starts at the oldest remaining record and accepts its `prev_hash` when a checkpoint names exactly that `seq` and hash;
a chain that begins without a matching checkpoint is reported as "records before seq N were removed outside
retention". Deleting a session keeps its records until retention removes them; the session's ID in them stays
readable (it is an ID, not content).

**Reads: search, pages and export.** All with the host token; a session's own tools and a device token cannot read
the log. `GET /api/audit/events` takes `session`, `device`, `actor` (a kind, or `kind:id`), `type` (a type or a
prefix such as `permission.`), `from` and `to` (Unix ms), `q` (a plain substring of `summary`, not indexed, applied
after the other filters), `after` (the `seq` cursor; newest first, `order=asc` available) and `limit` (default 100,
at most 500); the answer carries the next cursor. A session's Audit tab in the inspector and the global view in
Settings are this route with `session` set or not. `GET /api/audit/export?format=jsonl|csv` takes the same filters
and streams the same records in pages of 1,000 by `seq` cursor, with backpressure, as bulk work, at most two at a
time (`rate-limited` beyond that); the core holds one page, never the log. JSON Lines is one record per line with
every column; CSV has the columns of the table with `summary` as one JSON cell, and a cell that starts with `=`,
`+`, `-` or `@` is prefixed with `'` so a spreadsheet does not run it. An export is itself an `audit.exported`
event. The CLI (#71) gets `hoplet audit list|export|verify|head` over these routes.

**Relation to ADR 0029.** The audit log is the single home of permission facts. #98's auto decisions
(`runtime.permission.auto`) and the user's answers become `permission.auto` and `permission.answered` records, keyed
by `session_id` and `ref` (the tool use ID), and rule changes become `config.changed` records with the area
`permission-rules`; there is no separate table of decisions. The "Allowed by rule" line in a conversation is read from the log by the
same key, so it shows for as long as the record exists (retention).

## Cost

A table and a deriver in the core, a vendor extractor per provider, a scrubber that two modules now share, a hash on
every write, and a background pass that reads stored lines. The database grows: tool calls are most of it, at about
0.5 KiB per record with indexes, so a machine that makes a few thousand tool calls a day adds a few MiB a day, and
the default 180 days is a few hundred MiB at the heavy end (estimates; the code pull request measures them with the
fixtures and corrects docs/hosting.md). An action fails when its record cannot be written, which makes the log a
dependency of control. The log cannot say who among several people used the host token. Scrubbing is pattern-based
and will miss an unnamed secret. An operator who wants evidence that survives a compromised host has to anchor the
head themselves.

## Alternatives

- *The server log only.* Nothing to build, but it is one stream with no query, rotates with the container runtime,
  has no tool-call detail and cannot show removal. Rejected; it stays the operational log.
- *Query the session content live.* No new storage for tool calls, but a search across hundreds of megabytes per
  question, nothing for events that are not in lines (answers, switches, tokens), and gone when a session is
  deleted. Rejected.
- *A hash chain per session.* Verification is local and a deleted session takes its chain along, but removing a
  session's records leaves no trace, and config and device events have no session. Rejected for a global chain.
- *An external append-only store* (a managed log service, syslog, a transparency log). Real tamper resistance, but
  a dependency and data sent off the host, against "no outside service". Left to the operator through the head hash
  and the export.
- *Sign records with a key in the data directory.* Whoever can write the database can read the key; it adds key
  handling and no protection the chain does not give. Rejected.
- *Derive tool calls inside the append transaction, as usage is.* Always current, but scrubbing and mapping run on
  the path of every append and the chain head serializes all appends. Rejected for a deriver.
- *Retention in the web UI.* Convenient, and a stolen host token could then erase the log. Rejected.

## Revisit triggers

Hoplet gains users or roles (actors name a person); tail truncation matters enough to anchor automatically (a
configured destination for a signed head); the scrubber misses a class of secret in practice; the table outgrows one
SQLite file, or search by `q` needs an index; a provider whose lines give no tool calls; a rule that fixes the
retention.
