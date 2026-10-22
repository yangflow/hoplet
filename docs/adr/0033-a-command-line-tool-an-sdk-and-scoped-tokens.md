# 0033 A command-line tool, an SDK and scoped tokens

- Status: accepted
- Date: 2026-10-22
- Related: ADR 0002, ADR 0005, ADR 0007, ADR 0011, ADR 0012, ADR 0018, ADR 0021, ADR 0022, ADR 0029, ADR 0030, ADR 0031, docs/api.md, docs/architecture.md, docs/threat-model.md, docs/hosting.md, docs/spec.md, issues #67, #71, #115

The maintainer accepted the recommendation of each decision below; the options and their cost stay for the record.

## Context

Issue #71 asks for a command-line tool that does what the web UI does for one session (start, list, attach, send,
wait), a TypeScript SDK the tool is built on, and tokens weaker than the host token, so that a script, a CI job or
another program can drive Hoplet without holding the credential that opens a shell on every machine.

What the code does today, which decides the design:

- **A host CLI exists and is small.** `scripts/hoplet/cli.ts` implements `prompt`, `interrupt`, `handoff`,
  `devices`, `devices add` and the `audit` commands over `HostClient` (`scripts/hoplet/client.ts`), which validates
  responses with the schemas of `shared/`. It runs with `pnpm hoplet` from a checkout, as TypeScript on the
  repository's Node version, so it cannot be installed anywhere else. It cannot start a session, follow output or
  wait. Exit code 3 already means "the audit chain is broken".
- **One credential opens every route.** `createRouter` answers `unauthorized` to every `/api/*` request unless the
  host token matches, apart from the two `token` routes of action links (`core/src/api/router.ts`, `auth` is
  `'host' | 'token'`). A device token reaches `/ws/agent` only (ADR 0005). A script cannot be given less than
  everything, including Bypass permissions, a terminal, a handoff, a deletion and the minting of device tokens.
- **The browser WebSocket authorizes once.** `POST /api/ws-tickets` mints a 30 second single-use ticket
  (`core/src/api/tickets.ts`); `redeem` answers a boolean and remembers nothing about who minted it. The connection
  then receives `session.appended`, `session.updated`, `device.presence`, `session.runtime` and `session.permission`
  for every session; only `session.output` and the terminal messages go to subscribers.
- **Hashed tokens exist for devices.** The core stores the SHA-256 of a device token and shows the plaintext once
  (ADR 0005). Nothing has an expiry, a last-used time or a revoke route.
- **Most of what a CLI needs is there.** Prompt and new-session requests take a client-chosen `promptId` that makes
  a retry safe; reads are pages and blocks (ADR 0007); the runtime state of a session is `offline`, `external`,
  `idle`, `starting`, `ready`, `running`, `waiting-permission` or `failed` with an `error` string on
  `session.runtime`, and the control events `turn-done` and `failure` already exist (ADR 0031). Errors are
  `{ code, reason? }` with stable codes.
- **What is missing.** `NewSessionRequest` has no worktree field, so `--worktree` is a wire and agent change, not a
  CLI one. There is no turn ID: a turn is "the state went through `running` and came back to `ready`".
- **The workspace is three private packages.** `pnpm-workspace.yaml` lists `core`, `shared` and `web`; `shared`
  exports raw `.ts`; `scripts/check-version.sh` and `release-please-config.json` list the manifests that carry the
  version; the image copies `core`, `shared` and `web`; `scripts/boundaries.ts` knows `core`, `web`, `shared` and
  `agent`. Nothing is published to a registry; a release is a GitHub Release made when the maintainer merges the
  release-please pull request.
- **ADR 0030 and #67 point here.** The audit log names the actor kinds `operator`, `device`, `session`, `schedule`
  and `core`, and #67 gives a running session its own token for Hoplet's MCP tools.

The risks are the point. A scoped token is a new credential kept in a CI secret store or a shell history, so its
power, life and revocation have to be small by default. A scope checked in each handler is a scope one forgotten
handler defeats. A public package makes the REST contract a promise to people the maintainer cannot see.

## Decisions

1. Package location and what is published. Chosen: `sdk/` (`@hoplet/sdk`) and `cli/` (`@hoplet/cli`) as
   workspaces, the schemas of `shared/` bundled into the SDK.
2. Publishing method. Chosen: npm, by a workflow that waits for the maintainer's approval, with
   provenance; no CLI in the image in this version.
3. Token capabilities. Chosen: four (`read`, `send`, `create`, `answer`) with presets and `answer` as a
   separate switch.
4. Restrictions. Chosen: one allow-list of machines and projects; a session out of scope is
   `session-not-found`.
5. Expiry. Chosen: required, 90 days by default, 365 at most.
6. Whether `hoplet new --worktree` belongs here. Chosen: no; its own issue, because it needs a wire and
   agent change.
7. How `wait` finds the end of a turn. Chosen: infer it from runtime states; no new protocol field now.
8. Where the CLI keeps credentials. Chosen: nowhere; environment variables and a token file only.

## Decision

### Packages (decision 1)

**Two new workspaces: `sdk/` and `cli/`.**

- `sdk/` is `@hoplet/sdk`, a typed client for the REST API and the browser WebSocket. It imports `shared/` for
  schemas and types and nothing else of the repository, and uses `fetch` and the global `WebSocket` only (no
  `node:` imports), so it runs in Node and in a browser.
- `cli/` is `@hoplet/cli`, the `hoplet` command (`bin`). It imports `sdk/` and Node's standard library, not
  `shared/` and not `core/`. `scripts/hoplet/` and its tests move to `cli/`; `pnpm hoplet` runs the new package;
  `scripts/` keeps the repository checks, fixtures and benchmarks.
- `shared/` stays private. The SDK build inlines the schemas it uses, emits ESM JavaScript and declarations, and
  has `valibot` as its one dependency. The agent wire protocol is not in the published package: the SDK exports the
  REST and browser message schemas only.
- The runtime floor is a maintained Node LTS (22 and later, which has global `fetch` and `WebSocket`), lower than
  the repository's own, because the package is installed on machines Hoplet does not control.

**Boundaries** (`scripts/boundaries.ts`): `sdk/` may import `shared/` and its own files, not `node:*`, `core/`,
`web/`, `agent/` or `cli/`; `cli/` may import `sdk/` and `node:*`, not `shared/`, `core/` or `web/`; `shared/`,
`core/` and `web/` import neither. A CLI that can only use the SDK keeps "the CLI is built on the SDK" true.

**Versions.** Both packages carry the product version: `sdk/package.json` and `cli/package.json` join
`release-please-config.json` `extra-files` and `scripts/check-version.sh`, and the CLI depends on the SDK at the
same exact version. The SDK compares `protocolVersion` with the one it was built from on first use and refuses to
continue on a mismatch, naming both. Before 1.0 the SDK's API may change in a minor version, as everything else
does.

| Option | Cost | Verdict |
|---|---|---|
| Both inside `shared/` | No new manifest. `shared/` is the contract between the parts and depends on nothing; a client and a command line are not a contract. Publishing it would publish the agent protocol. | Rejected |
| One package, `@hoplet/sdk`, with the `bin` | One manifest and one publish. A program that wants the client installs a tool, and "no `node:` in the SDK" has to be enforced per file. | Possible |
| `sdk/` and `cli/` (chosen) | Two manifests, two version-check entries, a bundling step for `shared/`. A clean rule that the tool uses only the public client. | Chosen |
| Publish `@hoplet/shared` too, SDK stays thin | No bundling. The private contract of core, agent and web becomes a public one with its own compatibility promise. | Fallback if bundling declarations proves brittle |

### Publishing (decision 2)

**Chosen: npm, `@hoplet/sdk` and `@hoplet/cli`, public, published by a workflow with provenance and no
stored publishing token.**

- The workflow runs when a release is created and waits on a protected GitHub environment that only the maintainer
  approves, so merging the release pull request is not enough: each publish is its own approval, as the red line
  requires. It uses npm trusted publishing (OIDC), so no long-lived npm token sits in the repository. It is added
  in the implementation pull request and never run by an agent.
- Claiming the `@hoplet` scope on npm, the first publish and any change of the workflow's permissions are the
  maintainer's, done by hand.
- `npx @hoplet/cli sessions` and `npm i -g @hoplet/cli` work from any machine with Node; a script imports the SDK.

| Option | Cost | Verdict |
|---|---|---|
| npm, workflow behind an approval (chosen) | An npm account and scope, a workflow, a public-API promise. Releases are otherwise unchanged. | Chosen |
| npm, published by hand from the maintainer's machine | No workflow, no OIDC; no provenance, and a credential on a laptop. | Acceptable for the first release only |
| Single-file executable (Node SEA, Bun or Deno compile) on the GitHub Release | No Node needed; one file per platform, signing and notarization on macOS, a build matrix. A bigger release process than the feature. | Later, if users without Node ask |
| The CLI inside the image (`docker exec`) | The host token is already there and nothing is published. It couples the CLI to the server release and grows the image. | Left out of this version; cheap to add after |
| Not published; a checkout is required | Today's state, which the issue says is wrong. | Rejected |

### Scoped tokens (decisions 3 to 5)

**One new kind of credential: an API token.** It is minted with the host token only (Settings, or
`hoplet tokens create`), shown once, and sent as `Authorization: Bearer` like the host token.

- **Shape.** `hpt_` followed by 32 random bytes in base64url. The prefix lets the scrubber (`core/src/scrub/`) and a
  secret scanner recognize it; the scrubber gains the shape in the implementation pull request. It travels in a
  header, never in a URL.
- **Storage.** A table `api_tokens`: `id` (random, public, shown in lists and the audit log), `kind`, `name`,
  `token_hash` (SHA-256, unique, as device tokens), `grant` (JSON), `created_at`, `expires_at`, `revoked_at`,
  `last_used_at`. The plaintext is returned once by `POST /api/tokens`; lists show the id, name, grant, times and a
  state (`active`, `expired`, `revoked`), never the hash. `last_used_at` is written at most once a minute per token.
- **Lookup.** Each request hashes the presented token and reads one indexed row; there is no cache, so a revoke or
  an expiry takes effect on the next request. An unknown, expired or revoked token is `unauthorized` with no
  distinction in the answer.
- **Kinds.** `scoped` is the only kind built here. `session` is the kind #67 mints for a running session (the agent asks for it with `runtime.mcp.token.request`; it stays in the daemon's memory): the same
  table, lookup and `grant` type, a different issuer (the core), a short life, revoked when the session ends. That
  is the shared permission model; #67 decides what its grants hold.

**The grant** is `{ capabilities, allow? }`, defined in `shared/` so the core, the web UI and the SDK read one type.

*Capabilities (decision 3).* Four, each a statement about routes:

| Capability | Lets the holder | Routes |
|---|---|---|
| `read` | See sessions, pages, blocks and search; see machines and their projects; receive the session stream | `GET /api/health`, `GET /api/sessions`, `GET /api/sessions/:id` and its `lines`, `blocks` and `search`, `GET /api/search`, `GET /api/devices`, `GET /api/devices/:id/projects`, `POST /api/ws-tickets` and the stream it opens |
| `send` | Add to an existing session | `POST .../prompt`, the queue routes, `.../steer`, `.../interrupt`, image upload to a machine in scope |
| `create` | Start sessions | `POST /api/sessions`, `GET /api/templates`, `POST /api/templates/:id/start` |
| `answer` | Answer permission requests | `POST .../permission`, `POST .../permission/fresh` |

Presets: **Read only** (`read`), **May send** (`read`, `send`), **May create** (`read`, `send`, `create`), each with
a separate switch **May answer permission requests** (adds `answer`), off by default and worded as the powerful
thing it is: answering a permission decides what a tool may do on that machine.

Everything else is host-only and stays so: `PATCH`, `DELETE`, `.../config` (so no token can switch a session to
Bypass permissions), `.../stop`, terminals and ports, handoff, deletion, workspace reads, snapshots, device
administration, notifications, webhooks, hooks, budgets, usage, permission rules, template edits, the audit log and
the token routes. A route is host-only unless its definition says otherwise, so a route added later is closed to
tokens by default.

A `create` token cannot start a session with a config option the driver marks `risk: "high"` (ADR 0018: Bypass
permissions, Codex full access); `POST /api/sessions` answers `forbidden`.

| Option | Cost | Verdict |
|---|---|---|
| Three levels (read, send, create), as the issue sketches | Smallest settings UI. Answering a permission has to live in a level: either `send` can approve what its own prompt asks for, or nothing can answer. | Rejected |
| Capabilities with presets (chosen) | A grant is a list, and #67 can use a subset. Four names to document. | Chosen |
| Per-route or per-method scopes (`sessions:write`, OAuth style) | Finest control, a long list to maintain and explain; nobody can say what a token may do without reading it. | Rejected for a single operator |

*Restrictions (decision 4).* `allow` is a list of entries `{ deviceId, path? }`; absent means everything. An entry
without `path` is a whole machine; with one, a project of that machine. A session is in scope when its owner
machine is listed and, for an entry with a path, its working directory is that path or below it.

- List routes return only sessions and machines in scope; `GET /api/devices/:id/projects` returns only allowed
  projects; `GET /api/search` runs over sessions in scope only.
- A route on a session out of scope answers `session-not-found` (not `forbidden`), so a token cannot learn that
  the session exists. A request that targets a machine or project out of scope (new session, upload) is `forbidden`.
- A session handed off to a machine out of scope leaves the token's view when ownership moves.

Restrictions are for machines and projects, not single sessions; #67's per-session tokens add a session list to
`allow` and nothing else changes.

*Expiry (decision 5).* `expires_at` is required. Settings offers 1, 7, 30 and 90 days, defaults to 90, and the core
refuses more than 365. Options: optional expiry (a forgotten token lives forever; rejected); required with a
maximum (chosen); required and short such as 24 hours (safest, but a CI secret then needs a renewal job, which
defeats it). A token cannot be edited: another grant or a longer life is a new token and the old one is revoked.

**Revocation.** `DELETE /api/tokens/:id` sets `revoked_at`; it is immediate (see Lookup) and closes the browser
WebSockets that token opened. The row stays so the list and the audit log make sense; Settings can delete an
expired or revoked row. At most 100 tokens are active at once.

**Token routes.** `GET /api/tokens`, `POST /api/tokens` (name, grant, expiry; returns the token once) and
`DELETE /api/tokens/:id`, all host-only. A scoped token cannot list, mint or revoke tokens, so it cannot grant
itself more than it has.

### Enforcement

**One place decides, and the default is closed.** The route definition replaces `auth: 'host' | 'token'` with
`access`: `host` (default), `action-token` (the two existing routes) or a capability. `createRouter` resolves the
bearer to a principal (the host, or a token with its grant) and refuses before the handler runs: a host-only route
answers `forbidden` to a token, and `unauthorized` to an unknown credential as today. The principal reaches the
handler in the call object, with two helpers that apply restrictions: `visibleSessions` for list routes and
`requireSession` for routes with a session ID (`session-not-found` out of scope); machine and path parameters use
`requireDevice` and `requireProject` (`forbidden`). A test walks the route table: every route declares its access,
and for each preset the status of every route is compared with an expectation table in the test, so a route added
with the wrong capability or without the filter fails `pnpm check`. `docs/api.md` and the generated schema carry
each route's access.

**New error code `forbidden` (HTTP 403)**: the token is valid but does not allow this request. A scoped token is
rate limited per token (300 requests a minute on the control lane, using the limiter in
`core/src/api/ratelimit.ts`); the host token stays unlimited as the threat model accepts, because a leaked scoped
token is exposed to scripts in a way the host token, kept behind the user's proxy, is not.

**WebSocket tickets inherit the grant.** `Tickets.mint` records the principal and `redeem` returns it, so the
connection carries a grant. A token needs `read` to mint a ticket. The connection then filters wherever the core
publishes: broadcasts to "every connection" go only to connections whose grant sees that session or machine;
`subscribe` for a session out of scope is answered as a session that does not exist; `terminal.*` is refused for
any token; the connection is closed when its token expires or is revoked. A ticket is still 30 seconds and single
use, and tokens never travel in the WebSocket URL.

This is the largest change in the core, because every publish site learns who may receive it. Option: REST only for
tokens, with `attach` and `waitForTurn` polling `GET /api/sessions/:id` every second. Rejected as the main path
(live output is the point of `attach`; polling costs a read per second per waiter), kept as the SDK's fallback when
the WebSocket cannot connect.

### Audit

ADR 0030 gets an actor kind `token` (`actor_id` is the token's `id`, `channel` stays `rest`), so every event a token
causes (`prompt.sent`, `session.created`, `session.interrupted`, `permission.answered`) names the token, not "the
host token". The call sites that write `operator` take the actor from the principal. New events: `token.issued`
(id, name, grant, expiry; never the secret), `token.revoked` (id) and `token.denied` (id, method and path, reason
`forbidden`; at most one record per token and route per minute, so a probing token cannot flood the log). An
unknown, expired or revoked token is not recorded, because failed authentication is a count in the core log, as
connections are in ADR 0030. The host's actor stays `operator`.

### `wait` and "the turn finished" (decision 7)

There is no turn ID. The SDK defines a turn from the runtime states the core already publishes, the same states
behind the `turn-done` and `failure` events (ADR 0031):

- **Started**: after the accepted send (`202` with its `promptId`), the state is `starting`, `running` or
  `waiting-permission`, or lines were appended after the baseline taken before the send.
- **Finished**: after started, the state is `ready` (`turn-done`). Result `ok`.
- **Failed**: after started, `failed`, with `session.runtime.error`.
- **Ended without a result**: after started, `idle`, `offline` or `external`.
- **Blocked** (only with `--until-blocked`): `waiting-permission`, so a script can answer and wait again.

Reading the state right after a send could return `ready` before the agent reported `running`, so `sendPrompt` and
`createSession` return a baseline (line count and state at acceptance) and `waitForTurn(session, { since })`
requires having seen a start after it, with a `startTimeout` (30 seconds) that ends in a timeout, not in success.
`wait <session>` with no preceding send returns at once with `idle` when the session is not running, and otherwise
waits for `ready`. A queued message that starts the next turn after `ready` is a new turn: `wait` returns at the
first end, and `--drain` waits until `ready` with an empty queue. The SDK subscribes before it reads, so no change
is missed, and falls back to polling if the WebSocket is unavailable.

| Option | Cost | Verdict |
|---|---|---|
| Infer the turn from states (chosen) | No protocol change. A narrow race when a turn starts and ends between two observations is closed by seeing the appended lines. | Chosen |
| A `turnId` on `runtime.state` and the prompt result | Exact, also useful to hooks. A change in `shared/src/wire/`, `agent/internal/sync/protocol.go` and each vendor driver. | Revisit if the inference proves wrong |
| Wait for a `turn-done` hook | Needs a receiver. | Rejected for a CLI |

Exit codes of `wait`, `send --wait` and `new --wait`: `0` finished, `1` error (network, API, usage), `4` turn
failed, `5` ended without a result, `6` blocked on a permission request, `7` timeout. Code 3 stays with
`audit verify`. With `--json` the result is `{ "result": "ok|failed|ended|blocked|timeout", "state", "error"? }`.

### The CLI and the SDK

**SDK surface** (the shapes are decided in the code pull request): `new Hoplet({ url, token, fetch?, WebSocket? })`
with `createSession`, `listSessions`, `getSession`, `sendPrompt`, `waitForTurn`, `readSession` (an async iterator
over pages, blocks on demand, never a whole session, ADR 0007), `subscribe` (an async iterator of typed browser
messages that reconnects with a new ticket and re-reads state, as docs/api.md prescribes), `answerPermission`,
`interrupt`, and the host-only calls the CLI keeps (`handoff`, devices, audit, tokens). The SDK generates the
`promptId`, so a retry after a lost response runs a prompt once. A failure is a `HopletError` with `status`, `code`
(an unknown code is passed through) and `reason`; our text is not the contract. Responses are validated with the
schemas of `shared/`.

**CLI**: `new --device --path [--model] [--set option=value] "<prompt>"` (and `--template`), `sessions
[--device] [--state]`, `attach <session>`, `send <session> "<text>"` (alias `prompt`), `wait <session> [--timeout]
[--until-blocked] [--drain]`, `tokens list|create|revoke`, and today's `interrupt`, `handoff`, `devices` and
`audit`, which a scoped token is refused. `--json` prints one document per command and one object per line for
`attach` and `wait`; errors go to standard error as `{ "code", "reason"? }`. `attach` prints streamed output and
shows a permission request; it answers at a terminal prompt or with `--answer allow|deny` only when the token has
`answer`. Configuration is `HOPLET_URL` and `HOPLET_TOKEN` (or `HOPLET_TOKEN_FILE` / `--token-file`); `--token`
stays, and the help warns that arguments are visible to other processes.

*Credentials on disk (decision 8).* Options: a `login` command storing URL and token in a file (convenient; a new
secret store to protect, and the place the host token ends up by habit); the system keychain (right on a Mac, a
native helper per platform); environment and token file only (chosen). A stored login is a revisit trigger.

*Worktree (decision 6).* `hoplet new --worktree` needs the agent to make a git worktree: a field in
`NewSessionRequest`, a change to the runtime start message and to the agent, and the worktree branch must be one
deletion (ADR 0023) can find. Options: (a) include it here, a wire change plus an agent feature in a CLI pull
request, against "small pull requests, one concern each"; (b) its own issue, and the CLI offers the flag when the
core supports it (chosen); (c) the CLI makes the worktree itself, impossible because the project lives on the
machine, not where the CLI runs.

## Cost

Two packages and a build step that bundles part of `shared/`; two more files kept in step with the version; a
release workflow with an approval step and the maintainer's one-time npm setup. The SDK's public API becomes
something people depend on. In the core: an `api_tokens` table and migration, a principal on every request, a scope
check in the router, a visibility filter in every list and session route, a filtered publish in the browser
WebSocket (the change that touches the most code), per-token rate limiting, three audit events and one actor kind,
one error code, and token screens in Settings with strings in `web/src/i18n/zh-Hans.json` and a mockup in
`design/screens/` before UI code. Every route now has an access to decide, and a wrong decision is a security bug.
One more SQLite read per request for a scoped token. A leaked token can do what its grant allows until expiry or
revocation; with `answer` and `create` that includes approving and running tool calls on every machine in its
`allow` list, which is why `answer` is a separate switch and why no token can reach Bypass permissions.

## Alternatives

- *Keep the host token and the host CLI.* Nothing to build, but a script needs the credential that opens every
  machine's shell. Rejected.
- *Signed self-contained tokens (JWT, macaroons) instead of rows.* No lookup, but revocation needs a deny list, so
  a table exists anyway. Rejected; signed tokens fit action links (ADR 0021), where one use is the point.
- *Only per-session tokens (#67), no Settings tokens.* A CI job that starts sessions has no session to hold a token
  from. Rejected; one table serves both.
- *Roles and users.* The long-term answer (ADR 0005 revisit triggers) and a different product: sign-in, people,
  identity in the log. Scoped tokens are the step that does not need it.
- *A CLI over the agent's local socket.* A machine's view, not the fleet's, and the socket is not a control surface
  (ADR 0032). Rejected.
- *An MCP server in place of an SDK.* #67 serves models; the SDK serves scripts and both can sit on it later.

## Documents that change with the implementation

None changes in the pull request that adds this record. Once accepted, the implementation pull requests update
`docs/api.md` (authentication, the Auth column, token routes, `forbidden`, WebSocket filtering), `docs/spec.md`
(the Host CLI section becomes the CLI and SDK, token settings, audit events), `docs/threat-model.md` (a new
credential and trust boundary, the entries for `answer` and `create`), `docs/hosting.md` (the token table in Data,
the rate limit, installing the CLI), `docs/architecture.md` (packages and boundaries), ADR 0030 (actor kind `token`
and the three events), `scripts/boundaries.ts`, `scripts/check-version.sh` and `release-please-config.json`.

## Trust boundary changes

A new credential between "everything" and "nothing": a bearer token, stored hashed, that reaches the REST API and
the browser WebSocket with a subset of the host token's routes and sessions. It adds one boundary (scoped-token
holder to core), moves the authority check from "a token matched" to "a token matched and this route and resource
are allowed", and makes the browser WebSocket publish per connection instead of to every connection. It adds no
route to an agent, no way to change rules, modes, devices or tokens, and no new input to an agent. The sync cursor,
the stored lines and the wire protocol are untouched.

## Revisit triggers

Hoplet gains users or roles; #67 needs a grant the model cannot express; users without Node ask for a binary; a
turn ID becomes necessary because the inferred turn is wrong; a stored login becomes a requirement; a leaked-token
incident argues for IP restriction or a shorter maximum life; the SDK is wanted in the web UI, which then drops
duplicated client code.
