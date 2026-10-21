# 0031 The session lifecycle events and the two hook kinds

- Status: proposed
- Date: 2026-10-21
- Related: ADR 0002, ADR 0005, ADR 0008, ADR 0012, ADR 0021, ADR 0026, ADR 0028, ADR 0029, docs/architecture.md#principles, docs/architecture.md#wire-protocol, docs/threat-model.md, docs/hosting.md, docs/spec.md#lifecycle-hooks, issues #75, #77, #80, #97, #115

## Context

A session that runs unattended (the point of v0.9) needs to tell other things when it changes: a chat message when a
turn is done, a ticket when a session fails, a script that pulls a branch or turns on a light. Notifications
(ADR 0021) tell a person. They choose events by the user's attention (presets, quiet hours, "only when no tab has the
session open"), carry a redacted title for a human, and a webhook there is a channel of a notification, not an
interface for programs. What a program needs is a stable set of events, an envelope it can version, an idempotency
key, a signature it can verify with a clock, and a record of what was delivered.

What the code does today, which decides how the events are made:

- **The moments exist, the events do not all.** `control.onEvent` (`core/src/control/types.ts:416`) delivers a
  `ControlEvent` of three kinds only (`types.ts:343`): `permission` (emitted at `control/runtime.ts:401`, when
  `runtime.permission` arrives), `turn-done` (`runtime.ts:379`, a `runtime.state` of `ready` after `running` or
  `waiting-permission`) and `failure` with `source` `start` or `handoff` (`control/start.ts:138`; the handoff
  failure path at `control/handoff.ts:565`). Everything else is a publish to the browsers and is not an event: a
  session started (the `runtime.started` answer after `start.ts:119`, and the plan approved into a new session,
  `control/fresh.ts:117`), a process that stopped or failed (`runtime.state` with `idle` or `failed` and its `error`,
  `runtime.ts:367`; the stop the core forwards, `runtime.ts:300`), a permission answered (`answerPermission`,
  `runtime.ts:257`, where the first answer wins and the request is deleted; an unanswered one is denied by the
  agent after `PERMISSION_OPEN_MS`, `runtime.ts:52`, and `dropPermissions` forgets requests when a turn is over,
  `runtime.ts:156`), a session deleted (`control/deletion.ts:125`) and the two ends of a handoff
  (`handoff.ts:547` and `handoff.ts:559`).
- **Notifications are built (#77).** `core/src/notify/` turns `ControlEvent`s into Web Push and webhook messages. Its
  outbound HTTP is `post` in `notify/deliver.ts:11`: a 5 second timeout (`notify/index.ts:31`), `redirect: 'manual'`
  so a redirect does not carry the body to a host the user did not name (`deliver.ts:21`), the result classified as
  `gone` (404, 410), `http-<status>`, `timeout` or `error`, and one retry after 1 second (`withRetry`,
  `deliver.ts:43`; `notify/index.ts:32`). The webhook signature is `X-Hoplet-Signature: sha256=<hex HMAC of the body>`
  (`notify/webhook.ts:4`): no timestamp, so a captured request replays for ever. **There is no address check
  anywhere in the core**: a webhook URL may name loopback, a private address or a cloud metadata address, which
  ADR 0021 accepts as the owner's own choice. Secrets are in `notify-secrets.json`, mode 0600, outside `hoplet.db`
  (`core/src/store/secrets.ts`, the webhook secrets keyed by webhook ID); `notify/index.ts:96` registers each with
  the log redactor. The text of a summary is redacted and cut by `notify/redact.ts` (`SUMMARY_MAX` 80).
- **The agent configuration is JSON read once at start.** `agent/internal/config/config.go:38` (`Load`) reads
  `~/.hoplet/config.json` or the `--config` file; it has no list of any kind today and no hot reload. The agent
  starts external processes of these kinds: the providers (`runtime/claude/driver.go:190`,
  `runtime/codex/driver.go:346`), `git` (`workspace/git.go:31`, `workspace/cloneinto.go:528`), the operator's shell
  on a PTY when `terminal` is on (`terminal/terminal.go:217`, ADR 0026) and fixed read-only probes (`ps`, port,
  resource and toolchain reads). Each is a fixed argv or a command the operator's own file names (ADR 0028).
  Nothing runs a path that arrives from the core.
- **Two lanes.** Control work is never queued behind bulk work (docs/architecture.md#principles). Events for hooks
  are neither prompts nor answers.

## Decision

**Hooks observe.** A hook receives an event after it happened. What a hook does or fails to do never changes a
session, a permission request, a budget or a handoff. Synchronous interception (a hook that answers a permission
request) is out of scope: it would put a program in the path of control work and is the question ADR 0029 answers
with rules decided on the agent, so it would extend that ADR with a record of its own and a separate confirmation by
the maintainer.

**The core makes the events, once.** A new `core/src/hooks/` module subscribes to `control.onEvent`. `ControlEvent`
gains the kinds `started`, `ended`, `resolved`, `handoff-started` and `handoff-done`; `notify` ignores kinds it does
not use. Each event gets an `id` and a time when it is made, and the same envelope goes to every hook that matches.
The emit sites are the lines above; none of them awaits a hook. A delivery or a forward to an agent starts apart
from the code that caused it, as notification sends do (docs/spec.md#notifications).

**The envelope, version 1.**

```json
{
  "v": 1,
  "id": "evt_01JAB...",
  "type": "turn.completed",
  "at": "2026-10-21T08:30:00.123Z",
  "session": { "id": "<session id>", "project": { "name": "hoplet" }, "machine": { "id": "<device id>", "name": "studio" } },
  "actor": { "kind": "user" },
  "data": {}
}
```

- `id` is the idempotency key: a time-sortable unique ID made by the core, the same in every attempt and for every
  hook. A receiver that sees an `id` twice has seen one event. Delivery is at least once and not ordered: a retry
  can arrive after a later event, so a receiver orders by `at`.
- `type` is one of the eight below. `session.id` is absent only for a `session.error` of a start that failed before
  a session existed. `project` is the directory's name, as notifications carry it (`projectName` in
  `notify/index.ts`), never a path, for a webhook; a local script also gets `path`, its working directory on its own
  machine.
- `actor` says who caused the event where Hoplet knows: `{ "kind": "user" }` (the web UI or the host CLI, with the
  host token), `{ "kind": "action-link" }` (a signed notification link), `{ "kind": "rule", "id": ... }` (a
  permission rule, once ADR 0029 is built) or `{ "kind": "system" }` (a timeout, an idle stop, a failure). It never
  names a person: Hoplet has one operator. `answerPermission` gets the actor as a parameter from its callers.
- **Compatibility.** Within `v: 1` a new event type, a new field in `data` and a new `actor.kind` are additions;
  receivers ignore what they do not know, and a hook's `events` list is an allow list, so a new type reaches no
  existing hook. A removed or renamed type or field, or a changed meaning, raises `v`; a hook is created for one
  `v` and keeps it, and the core sends a new `v` to a hook only when the user changes it. The envelope and the types
  are Valibot schemas in `shared/` and part of the generated API schema (`pnpm schema`); that is the one definition.

**The event set.**

| `type` | When (code) | `data` by default |
|---|---|---|
| `session.started` | a session Hoplet made exists: New session or a plan approved into a new session (`start.ts:119`, `fresh.ts:117`) | `provider`, `configuration`, `origin` (`new`, `plan`) |
| `turn.completed` | a turn ended, `ready` after `running` or `waiting-permission` (`runtime.ts:379`); also after an interrupt | `turn` (the session's turn count), `durationMs` |
| `session.ended` | the process stopped without failing (`idle` after the user's stop or the idle timer, `runtime.ts:367`), or the session was deleted (`deletion.ts:125`) | `reason` (`stopped`, `idle`, `deleted`) |
| `permission.requested` | a request is open (`runtime.ts:401`) | `requestId`, `tool`, `plan` (true for a plan exit), `expiresAt` (absent for a plan) |
| `permission.resolved` | the request is no longer open (`runtime.ts:257`, the expiry, `dropPermissions`) | `requestId`, `resolution` (`allow`, `deny`, `expired`, `abandoned` when the turn ended first) |
| `session.error` | a runtime `failed`, a session that failed to start, or a handoff that failed (`runtime.ts:367`, `start.ts:138`, `handoff.ts:565`) | `stage` (`start`, `runtime`, `handoff`), `code` (the core's error code, never a message from a tool or a file) |
| `handoff.started` | the handoff is accepted (`handoff.ts:547`); `session.machine` is the source | `handoffId`, `from`, `to` (machine `id` and `name`) |
| `handoff.completed` | the commit step succeeded (`handoff.ts:559`); `session.machine` is the new owner | `handoffId`, `from`, `to` |

A refused or cancelled handoff is not an error, as in notifications (docs/spec.md#notifications). A handoff that
fails is a `session.error` with `stage: "handoff"`; there is no `handoff.failed`, so the set stays the eight of #97.
A test event has `type: "hook.test"` and `data.test: true`; it is sent only when the user asks and is never in a
hook's `events` list.

**Payloads carry metadata by default.** Session ID, machine, project name, type, time and the fields above: names,
codes, counts and times. Never a prompt, a message, a tool's input or output, a file, a diff, a path (webhook), a
token or a secret. Each hook has `summary: false` as its default; turned on, events gain these fields in `data` and
nothing else: `turn.completed` gets `title` (the session title cut to 80 characters) and `usage` (input, output and
cache tokens and the cost of the turn, as the usage records of #75 have them); `permission.requested` gets `summary`
(the tool's command or path through `notify/redact.ts`, cut to 80 characters, as a notification has it);
`session.error` gets `reason`, the error text redacted and cut to 200 characters. The opt-in is the hook's own, so a
user can give one trusted receiver a title and keep another on metadata. A hook never gets more than these.

**Webhook hooks: the core sends.**

- **Configuration.** URL, a label, `events`, a scope (every project, or one machine and project), `summary`,
  `enabled`. Created and edited with the host token over REST and stored in `hoplet.db`.
- **Signature.** `Hoplet-Signature: t=<unix seconds>,v1=<hex>`, where `v1` is the HMAC-SHA-256 under the hook's
  secret of `<t>.<body>`, the body being the exact bytes sent. Each attempt is signed with its own `t`. A receiver
  recomputes it over the raw body and refuses a `t` more than 5 minutes from its clock. Other headers:
  `Hoplet-Event` (the type), `Content-Type: application/json`, `User-Agent: Hoplet-Hook/1`. During a rotation the
  header carries two `v1=` values (below). The notification webhook's `X-Hoplet-Signature` of ADR 0021 stays as it
  is; the two schemes differ on purpose, and the documents tell receivers of anything new to use the hook one.
- **Secret.** The core generates 32 random bytes (`whsec_` and base64url) and returns it once, in the response that
  creates the hook or rotates its secret. It is stored in `notify-secrets.json` (mode 0600, outside `hoplet.db`)
  next to the webhook secrets of ADR 0021, in a `hooks` map by hook ID, and registered with the log redactor. It is
  never returned again, logged or put in an event; the API lists `hasSecret`. Rotation makes a new secret and keeps
  the old one valid for 24 hours: the header then carries both, so a receiver can switch without a gap. Deleting
  the file voids every secret (docs/hosting.md#data); each hook then shows `needs-secret` and sends nothing until
  the user rotates it.
- **Transport.** `https` only. Plain `http` is allowed only when the operator has set `HOPLET_HOOK_ALLOW_PRIVATE`
  and the address is private or loopback (a receiver on the same host or LAN). A request has a 5 second timeout,
  does not follow redirects (the `post` of `notify/deliver.ts` is reused for this: manual redirect, timeout,
  classified result) and reads at most 1 KiB of the response, which it never stores.
- **SSRF.** The core resolves the host itself and refuses the request if any address is loopback, private
  (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`), shared address space (`100.64/10`), unspecified, multicast or
  link-local (`169.254/16`, `fe80::/10`, which holds the cloud metadata address), after unwrapping IPv4-mapped IPv6.
  It connects to the address it checked, so a second lookup cannot change it. Loopback, private and shared
  addresses are allowed when `HOPLET_HOOK_ALLOW_PRIVATE` is set; link-local never is. The URL is checked when the
  hook is saved and again at every attempt. This is stricter than the notification webhook, which has no check
  (above); closing that gap for #77 is a separate change and a revisit trigger here.
- **Retries.** An attempt that ends in a timeout, a network error, `408`, `429` or a `5xx` is retried with
  exponential backoff and jitter: after 30 s, 2 min, 10 min, 30 min, then every 2 hours, until 24 hours after the
  event; then the delivery is `failed`. Any other `4xx` (`404` and `410` included) fails at once without a retry,
  and a hook is never disabled for answering `410`, as a push subscription is. A `2xx` is delivered. The state lives
  in `hoplet.db`, so a restart goes on. A hook keeps at most 1000 deliveries waiting; beyond that the oldest waiting
  one is `dropped`, and counted.
- **Isolation.** The sender runs apart from every control handler, takes events from a queue per hook and has at
  most 4 requests in flight overall. A slow or dead receiver delays only deliveries; the control lane and the agents'
  lanes never wait on it.
- **History.** One row per delivery: event ID and type, state (`pending`, `delivered`, `failed`, `dropped`), attempts,
  the last status or error class (`http-503`, `timeout`, `blocked-address`), times, and the body, so that a retry
  sends the same bytes. Rows are kept 14 days after they settle, then deleted, at most 500 per hook. The response
  body is not kept. The web UI shows them.

**Script hooks: the agent runs.**

- **Declared on the machine, only there.** A script hook is an entry of `hooks` in the agent's `config.json`
  (docs/hosting.md#lifecycle-hooks): `id` (a slug, unique on the machine), `command` (an absolute path to an
  executable), `events` (a list of types, or `*`), optional `projects` (absolute paths it applies to), `timeout_ms`
  (default 10000, at most 60000) and `env` (names of variables to pass through). The agent reads it at start, like
  every other setting of that file, so a change takes a restart. The agent refuses at load an entry whose `command`
  is relative, not a regular file, not executable, or writable by group or others, and logs why. This follows
  ADR 0028: what is code is written on the machine.
- **The core and the web may list, enable and disable; never add, edit or point.** The agent announces its hooks in
  `device.hello` (`id`, `command`, `events`, `projects`, `timeout_ms`, the names in `env`, counters; the path is not
  secret) and the core stores an `enabled` switch per machine and hook ID, on by default. The core sends an event to
  a script hook only when it is enabled, and the agent checks again that the ID is one of its own. No message carries
  a command, an argument, an environment variable or a path to the agent, so the core cannot name anything the
  file does not. A script cannot be started from a session, from the web UI or from another event: the only trigger
  is an event of a type the entry lists. (Send test event, below, runs the same entry with a fixed event.)
- **How it runs.** `exec` of the path with no shell and no argument; nothing from an event reaches argv. The event
  JSON goes to standard input, which is then closed. Working directory: the session's project directory when the
  machine watches it, else `hoplet_home`. Environment: only `PATH`, `HOME`, `USER`, `LANG`, `TZ` and `TMPDIR` from the
  agent's, the names the entry lists in `env`, and `HOPLET_HOOK_ID`, `HOPLET_EVENT_ID` and `HOPLET_EVENT_TYPE`; every
  other variable, the agent's `HOPLET_*` ones included, is removed. It runs as the agent's user in a process group
  of its own. After `timeout_ms` the group gets `SIGTERM` and, 2 seconds later, `SIGKILL`; a start-up journal kills
  what a crashed agent left, as for the terminal (ADR 0026). Standard output is read and thrown away, at most
  64 KiB; standard error is kept to its first 1 KiB.
- **Queueing.** One run at a time per hook. At most 16 events wait; beyond that an event is dropped and counted
  (`dropped`), never delayed behind a growing queue. Events for a machine that is offline are not queued by the
  core: a script's events matter now, and the delivery history records them as `dropped` with `offline`. An event
  goes to the machine named by `session.machine`: the owner for most events, the source for `handoff.started` and
  the target for `handoff.completed`.
- **What comes back.** The agent reports `hook.result` per run: hook ID, event ID, `ok`, the exit code or `timeout`
  or `spawn-failed`, the duration, and the 1 KiB of standard error cut at a character boundary. The core keeps these
  in the same delivery history (a run is a delivery with one attempt). Standard error is the script's own text;
  Hoplet does not redact it, and the web UI shows it as text to the host token's holder. Nothing from a result
  changes a session.

**The test event.** The web UI's Send test event sends a `hook.test` to one hook: a webhook gets it signed like any
other, a script hook runs once with it. A test does not let the web choose anything the file did not already allow:
the event is fixed, and the script and its environment are the entry's.

**The audit log.** Creating, editing, enabling, disabling, rotating the secret of and deleting a hook, and sending a
test, are audit events in the log of #80 with the hook ID, the machine for a script hook and the host-token actor,
never a URL's query, a secret or a payload. Until that log exists they are core log lines with the same fields.
Deliveries are not audit events: their history is the hook's own.

**The wire.** Three messages on the bulk lane, because none is control work: `hook.event` (core to agent: hook ID and
the envelope, a test included), `hook.result` (agent to core) and the `hooks` list in `device.hello`. They are
mirrored in `shared/src/wire/` and `agent/internal/sync/protocol.go`, with `PROTOCOL_VERSION` raised in the pull
request that adds them (docs/architecture.md#wire-protocol). The REST routes (hooks CRUD, secret rotation, enable
and disable of a script hook, deliveries, test) join docs/api.md with the code.

**Web UI.** Settings gets a Hooks section: the two kinds in one list, each with its enabled switch, events, scope,
the state of its last delivery and the recent deliveries, a Send test event button, and for a script hook the path
and the line "Declared on <machine>; edit it in that machine's `config.json`". No screen exists yet: the mockup in
`design/` comes first and the maintainer confirms its direction before UI code. The Mac client (ADR 0022) and the
host CLI use the same routes.

## Cost

A `hooks` module in the core with a queue, a sender and a store (a table of hooks, a table of deliveries, secrets in
the file that exists); an address check and a pinned connection in front of `fetch`; `ControlEvent` grows five kinds
and `answerPermission` an actor; the agent gains a runner (process group, environment, timeout, queue), a section of
`config.json` and a place in `device.hello`; three wire messages; a Settings section. Three documents change with
it: `docs/hosting.md` (outbound connections, the new variable, the agent file, the data), `docs/threat-model.md` (an
outbound request that is checked, a program the agent runs, a payload that leaves) and `docs/spec.md` (the section).

An event is a contract: the eight types and their fields are what a user's automation will depend on, so a change
after release is a version, not an edit. Events carry no session content, so they sit outside the byte cursor
(ADR 0002). A receiver that is down for a day costs the core up to 1000 stored bodies of metadata per hook; the
retention bounds it. A script is code the operator wrote and the agent runs as the operator with a clean
environment; it can do whatever that user can.

A session running on the same machine as the same user can write `config.json` and the script it names. That is not
new (it can already write any file the user can), and it is why the built-in rules of ADR 0029 deny a session
writing the Hoplet home. The boundary this ADR draws is the one against the host token and the core: whoever holds
them gains no script and no argument.

## Alternatives

- *Extend the notification webhooks (#77).* They exist, but they are a channel of a notification: events are chosen
  by attention rules (a turn that ends while a tab is open is not sent), the payload is a human message and the
  signature has no timestamp. Putting program events behind presets and quiet hours would make a hook miss events by
  design. The two share the secrets file, the redactor, the `post` helper and the control event stream, and stay
  separate features.
- *Provider-native hooks (Claude Code `PreToolUse`, Codex hooks).* They live inside one vendor and see its tool
  calls, not Hoplet's moments (a handoff, a session start from the web), and ADR 0012 keeps the surface above
  providers. A user can still use them; they are not Hoplet's hooks and run whatever the permission mode is.
- *A plugin system.* Code loaded into the core or the agent is a larger supply-chain and trust surface than a program
  at the other end of a pipe. Rejected.
- *Message-queue integrations (NATS, MQTT, a broker).* A new client and credentials in the core for every broker. A
  webhook reaches any of them through a receiver; revisit if asked for.
- *The web UI adds scripts.* The host token would run code on every machine, which ADR 0028 refuses for provider
  commands. Rejected.
- *A shell line as the command.* A string through `sh -c` invites quoting mistakes. A path with no argument is
  enough: the script reads standard input.
- *Block private addresses with no override.* It breaks the usual self-hosted receiver (Home Assistant, n8n on the
  LAN). The override is the operator's environment variable, not a per-hook switch the host token could set.

## Revisit triggers

A user needs `handoff.failed` or finer types than `session.error`'s `stage`; a receiver needs redelivery of a
`failed` delivery; the notification webhooks of ADR 0021 get the same address check and a timestamped signature;
synchronous hooks (an extension of ADR 0029); hot reload of the agent's hook list; a second core per data directory;
an event rate that makes the 1000-row bound too small; budgets (#75) need to stop a loop in which a hook starts
sessions.
