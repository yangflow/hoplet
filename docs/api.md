# Hoplet API reference

The HTTP API and the browser WebSocket of the core, for clients other than the web UI. The contract is in
`shared/src/`; this document lists every route and message with the schema that describes it, and
[`docs/schema/hoplet-api.schema.json`](schema/hoplet-api.schema.json) holds those schemas as JSON Schema
(see [JSON Schema](#json-schema)). `scripts/test/api-docs.test.ts` fails when a route, a browser message, an error
code or a schema is added, removed or changed without this document and that file.

## Native clients

A native client (a future Apple app, for example) talks to the core like the web UI does, with one
credential, the host token (`HOPLET_TOKEN`, docs/hosting.md#network).

1. Send `Authorization: Bearer <host token>` on every request to `/api/*`. A request without it, or with
   another token, is answered `401` with the code `unauthorized`, whatever its path.
2. Before the first browser WebSocket connection, mint a ticket with `POST /api/ws-tickets` and connect to
   `/ws?ticket=<ticket>` ([Browser WebSocket](#browser-websocket)). A ticket is single-use and valid for 30
   seconds; mint a new one for every connection, also for a reconnect.
3. Call `GET /api/health` first. Its response carries `coreVersion` (the core's `VERSION`, a string) and
   `protocolVersion` (a number, the `v` of every WebSocket message). Compare `protocolVersion` with the
   `protocolVersion` of the schema file the client was built from. When they differ, the client does not connect
   (no further requests, no WebSocket) and tells the user that the app and the core speak different protocol
   versions, with both numbers. `coreVersion` is for display and for bug reports; clients do not decide by it.

The host token has full authority: it does everything the web UI does, including driving Claude Code on every
connected machine and minting device tokens (`POST /api/devices`). The client keeps it in the system secure
storage (the Keychain on Apple platforms), never in preferences, files, logs, URLs or crash reports, and sends it
only to the host the user entered, over TLS unless the host is local
(docs/hosting.md#reverse-proxy). `POST /api/devices` returns a new device token in its response;
a client that calls it treats that response as a credential too.

```bash
curl -s -H "Authorization: Bearer $HOPLET_TOKEN" "$HOPLET_URL/api/health"
curl -s -X POST -H "Authorization: Bearer $HOPLET_TOKEN" "$HOPLET_URL/api/ws-tickets"
```

## Conventions

- Requests and responses are JSON (UTF-8). A request body is sent with `content-type: application/json`; each
  route limits its size (4 KiB to 64 KiB) and answers `too-large` beyond it.
- A success is `200`, except where the route table says otherwise. Every other response is an error:
  `{ "code": "<code>", "reason"?: "<text>", "promptId"?: "<id>" }` with the HTTP status of its code
  ([Error codes](#error-codes)). Clients choose their copy from `code`. `reason` is diagnostic text from the
  agent, never copy for the user.
- Schemas do not forbid extra properties; a client ignores properties it does not know.
- A session ID is letters, digits, underscore and hyphen. A path with another character in a parameter is
  `not-found`.
- Lanes: `control` routes run on arrival. `bulk` routes (history pages, blocks, search, snapshot reads) run
  through a queue so they never delay control work; when the queue is full the route answers `busy`, and the
  client retries after a short delay (docs/architecture.md#principles).
- Every route can also answer `unauthorized` and `internal`, and a path that no route serves answers `not-found`.
  A path that exists for another method answers `method-not-allowed` with an `Allow` header. They are not
  repeated in the table.

## REST routes

Auth is `host` for every route but the two action routes: the host token as `Authorization: Bearer`. The action
routes (`token`) take no header; the signed action token in the body is the credential
([Notifications](#notifications)). No route is open without one of the two. The Query and Body columns name the schema of the query string and of the JSON body; the
Response column names the schema of the success body and its status when it is not `200`.

| Method | Path | Auth | Lane | Query | Body | Response | Errors |
|---|---|---|---|---|---|---|---|
| POST | `/api/ws-tickets` | host | control | — | — | `WsTicketResponseSchema` | — |
| GET | `/api/health` | host | control | — | — | `HealthResponseSchema` | — |
| GET | `/api/sessions` | host | control | — | — | `SessionListResponseSchema` | — |
| POST | `/api/sessions` | host | control | — | `NewSessionRequestSchema` | `NewSessionResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `target-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId` | host | control | — | — | `LiveSessionMetaSchema` | `session-not-found` |
| PATCH | `/api/sessions/:sessionId` | host | control | — | `SessionPatchRequestSchema` | `LiveSessionMetaSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found` |
| DELETE | `/api/sessions/:sessionId` | host | control | — | `DeleteSessionRequestSchema` | `DeleteSessionResponseSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `session-running`, `owner-offline`, `delete-partial`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/terminal` | host | control | — | `TerminalOpenRequestSchema` | `TerminalInfoSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `terminal-unavailable`, `terminal-busy`, `external`, `starting`, `session-running`, `handoff-in-progress`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/terminal` | host | control | — | — | `TerminalStateSchema` | `session-not-found` |
| DELETE | `/api/terminals/:terminalId` | host | control | — | — | `TerminalCloseResultSchema` | `not-found` |
| GET | `/api/sessions/:sessionId/ports` | host | control | — | — | `SessionPortsResultSchema` | `session-not-found` |
| GET | `/api/sessions/:sessionId/deletion` | host | control | — | — | `DeletionPreviewSchema` | `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/lines` | host | bulk | `LinesQuerySchema` | — | `LinesPageSchema` | `session-not-found`, `busy` |
| GET | `/api/sessions/:sessionId/blocks/:line/:block` | host | bulk | — | — | `FullBlockSchema` | `invalid-request`, `session-not-found`, `not-found`, `busy` |
| GET | `/api/sessions/:sessionId/search` | host | bulk | `SessionSearchQuerySchema` | — | `SearchResultSchema` | `invalid-request`, `session-not-found`, `busy` |
| GET | `/api/search` | host | bulk | `GlobalSearchQuerySchema` | — | `GlobalSearchResultSchema` | `invalid-request`, `busy` |
| POST | `/api/sessions/:sessionId/prompt` | host | control | — | `PromptRequestSchema` | `PromptResultSchema`, 202 | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `external`, `starting`, `busy`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/queue` | host | control | — | — | `QueueSchema` | `session-not-found` |
| POST | `/api/sessions/:sessionId/queue` | host | control | — | `QueueAddRequestSchema` | `QueueSchema`, 201 | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `queue-full` |
| POST | `/api/sessions/:sessionId/queue/order` | host | control | — | `QueueOrderRequestSchema` | `QueueSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `queue-conflict` |
| POST | `/api/sessions/:sessionId/queue/resume` | host | control | — | — | `QueueSchema` | `session-not-found` |
| POST | `/api/sessions/:sessionId/queue/clear` | host | control | — | — | `QueueSchema` | `session-not-found` |
| PATCH | `/api/sessions/:sessionId/queue/:itemId` | host | control | — | `QueueEditRequestSchema` | `QueueSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `queue-conflict` |
| DELETE | `/api/sessions/:sessionId/queue/:itemId` | host | control | `QueueVersionQuerySchema` | — | `QueueSchema` | `invalid-request`, `session-not-found`, `queue-conflict` |
| POST | `/api/sessions/:sessionId/queue/:itemId/steer` | host | control | — | `QueueSteerRequestSchema` | `QueueSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `external`, `starting`, `not-running`, `steer-unavailable`, `queue-conflict`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/steer` | host | control | — | `SteerRequestSchema` | `SteerResultSchema`, 202 | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `external`, `starting`, `not-running`, `steer-unavailable`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/config` | host | control | — | `ConfigRequestSchema` | `RuntimeCommandResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `external` |
| POST | `/api/sessions/:sessionId/permission` | host | control | — | `PermissionRequestSchema` | `RuntimeCommandResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `request-closed` |
| POST | `/api/sessions/:sessionId/permission/fresh` | host | control | — | `FreshPermissionRequestSchema` | `NewSessionResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `request-closed`, `target-offline`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/interrupt` | host | control | — | — | `RuntimeCommandResultSchema` | `session-not-found`, `owner-offline`, `not-running` |
| POST | `/api/sessions/:sessionId/stop` | host | control | — | — | `RuntimeCommandResultSchema` | `session-not-found`, `owner-offline`, `not-running` |
| GET | `/api/sessions/:sessionId/commands` | host | control | — | — | `CommandsResultSchema` | `session-not-found` |
| GET | `/api/devices/:deviceId/commands` | host | control | `DeviceCommandsQuerySchema` | — | `CommandsResultSchema` | `invalid-request` |
| GET | `/api/sessions/:sessionId/workspace` | host | control | — | — | `WorkspaceListingSchema` | `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/workspace/file` | host | control | `WorkspaceFileQuerySchema` | — | `WorkspaceFileSchema` | `invalid-request`, `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/workspace/diff` | host | control | `WorkspaceDiffQuerySchema` | — | `WorkspaceDiffSchema` | `invalid-request`, `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/snapshot/changes` | host | bulk | — | — | `SnapshotChangesSchema` | `session-not-found`, `no-snapshot`, `busy` |
| GET | `/api/sessions/:sessionId/snapshot/diff` | host | bulk | `WorkspaceDiffQuerySchema` | — | `SnapshotDiffSchema` | `invalid-request`, `session-not-found`, `no-snapshot`, `not-found`, `busy` |
| POST | `/api/sessions/:sessionId/handoff` | host | control | — | `HandoffRequestSchema` | `HandoffResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `same-device`, `target-offline`, `owner-offline`, `handoff-in-progress`, `busy`, `no-snapshot`, `no-clone`, `clone-failed`, `session-changed`, `verify-failed`, `cancelled`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/handoff/cancel` | host | control | — | — | `HandoffCancelResultSchema` | `session-not-found`, `handoff-not-running` |
| GET | `/api/devices` | host | control | — | — | `DeviceListResponseSchema` | — |
| GET | `/api/devices/:deviceId/resources` | host | control | — | — | `DeviceResourcesResultSchema` | `not-found` |
| POST | `/api/devices` | host | control | — | `DeviceMintRequestSchema` | `DeviceMintResultSchema` | `invalid-json`, `invalid-request`, `too-large` |
| POST | `/api/devices/:deviceId/attachments` | host | bulk | — | `PromptImageSchema` | `AttachmentResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `target-offline`, `timeout` |
| GET | `/api/devices/:deviceId/clone` | host | control | `DeviceCloneQuerySchema` | — | `DeviceCloneResultSchema` | `invalid-request`, `target-offline`, `timeout` |
| GET | `/api/devices/:deviceId/dirs` | host | control | `DeviceDirsQuerySchema` | — | `DeviceDirsResultSchema` | `invalid-request`, `target-offline`, `timeout` |
| GET | `/api/devices/:deviceId/projects` | host | control | — | — | `DeviceProjectsResultSchema` | `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/projects` | host | control | — | `DeviceProjectsAddRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/providers` | host | control | — | `DeviceProvidersUpdateRequestSchema` | `DeviceProvidersResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `not-found`, `owner-offline`, `timeout`, `rejected` |
| POST | `/api/devices/:deviceId/projects/remove` | host | control | — | `DeviceProjectsRemoveRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/sessions/import` | host | control | — | `DeviceSessionsImportRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/sessions/remove` | host | control | — | `DeviceSessionsRemoveRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| GET | `/api/notify/preferences` | host | control | — | — | `NotifyPreferencesSchema` | — |
| PUT | `/api/notify/preferences` | host | control | — | `NotifyPreferencesSchema` | `NotifyPreferencesSchema` | `invalid-json`, `invalid-request`, `too-large` |
| GET | `/api/notify/push/key` | host | control | — | — | `PushKeyResponseSchema` | — |
| GET | `/api/notify/push/subscriptions` | host | control | — | — | `PushSubscriptionListResponseSchema` | — |
| POST | `/api/notify/push/subscriptions` | host | control | — | `PushSubscribeRequestSchema` | `PushSubscriptionInfoSchema`, 201 | `invalid-json`, `invalid-request`, `too-large` |
| DELETE | `/api/notify/push/subscriptions/:id` | host | control | — | — | `PushSubscriptionListResponseSchema` | `not-found` |
| POST | `/api/notify/push/unsubscribe` | host | control | — | `PushUnsubscribeRequestSchema` | `PushSubscriptionListResponseSchema` | `invalid-json`, `invalid-request`, `too-large` |
| GET | `/api/notify/webhooks` | host | control | — | — | `WebhookListResponseSchema` | — |
| POST | `/api/notify/webhooks` | host | control | — | `WebhookCreateRequestSchema` | `WebhookInfoSchema`, 201 | `invalid-json`, `invalid-request`, `too-large` |
| PATCH | `/api/notify/webhooks/:id` | host | control | — | `WebhookUpdateRequestSchema` | `WebhookInfoSchema` | `invalid-json`, `invalid-request`, `too-large`, `not-found` |
| DELETE | `/api/notify/webhooks/:id` | host | control | — | — | `WebhookListResponseSchema` | `not-found` |
| POST | `/api/notify/webhooks/:id/test` | host | control | — | — | `WebhookTestResponseSchema` | `not-found` |
| POST | `/api/action/inspect` | token | control | — | `ActionInspectRequestSchema` | `ActionInspectResponseSchema` | `invalid-json`, `invalid-request`, `too-large`, `invalid-token`, `rate-limited` |
| POST | `/api/action/decide` | token | control | — | `ActionDecideRequestSchema` | `ActionDecideResponseSchema` | `invalid-json`, `invalid-request`, `too-large`, `invalid-token`, `owner-offline`, `rate-limited` |

Behavior a client relies on:

- `GET /api/devices` lists every stored device and every connected one. `connected` is its presence; `lastSeen`
  (milliseconds) is when its agent last connected or disconnected and is absent for a device that never connected.
  `system` (`os` and `arch`, Go's `GOOS` and `GOARCH` such as `darwin` and `arm64`) is what its agent announced in
  `device.hello`, kept while the device is offline; it is `null` for a device whose agent never announced it.
  `resources` is the latest `device.resources` report of the machine (`at`, `cpu`, `memUsed`, `memTotal`,
  `diskFree`, `diskTotal`), kept while the device is offline so a client can say when it was taken; it is absent for
  a machine that never reported (an agent on a system the agent does not measure sends none). `warn` comes with it:
  `memory` is true when more than 90% of the memory is used, `disk` when less than 10% of the volume is free, and
  both are false while the device is offline.
- `GET /api/devices/:deviceId/resources` answers `samples`, the last hour of the machine's readings (at most 240, one
  every 15 seconds, oldest first, `cpu` null while the agent had no earlier reading), and `sessions`, the processes
  of the sessions Hoplet started on it as of the latest report, summed over their process trees: `cpu` (percent of
  the whole machine's CPU, null when unknown), `rss` (bytes), `children` (descendant processes), the session's
  `title` and `projectPath` (null for a session the core does not hold), and `history`, the session's readings
  within the last hour (`at`, `cpu`, `rss`, oldest first, the last one the latest report), largest memory first. `at` is the
  latest reading's time. A machine that never reported answers `samples: []` and `sessions: []`; an unknown device
  answers `not-found`. The core keeps the readings in memory only: they survive the agent disconnecting and are lost
  when the core restarts. They are not pushed to browsers; a client polls.
- `POST /api/sessions` answers when the agent has created and imported the session. Its error body carries the
  `promptId` that was sent, and a `reason` where the agent gave one.
- `POST /api/sessions/:sessionId/prompt` answers `202` when the prompt reached the owner. A prompt is at most
  8,000 characters (`too-large` beyond). Both prompt routes take optional `attachments`, at most 4 IDs
  returned by `POST /api/devices/:deviceId/attachments` for the session's owner (or the new session's device);
  the driver must declare `images` (`invalid-request` with `reason: images` otherwise). The agent refuses the
  prompt with `attachment-missing` when an ID is unknown or older than an hour, and with `invalid-image` when the
  images are more than 8 MiB together.
- A message written while a turn runs is queued or steered ([ADR 0020](adr/0020-queued-and-steered-messages.md)); `POST /prompt` still
  answers `busy` then. The core holds a session's queue (at most 20 messages of at most 8,000 characters and 4
  images): `GET /queue` returns it, head first, with `paused` (`error` or `interrupted`) and `error` when it does
  not send. `POST /queue` appends `{id, text, attachments}` (a repeated `id` changes nothing), `PATCH` and `DELETE
  /queue/:itemId` name the `version` the client read and answer `queue-conflict` for a stale version, a message
  already `sending` or one that is gone, `POST /queue/order` takes the IDs not yet sending in their new order,
  `POST /queue/resume` ends a pause and `POST /queue/clear` empties the queue. Every answer is the queue after the
  change, and `session.queue` tells the other windows. The head message is sent as the next turn once the session
  is idle, ready and not paused.
- `POST /api/sessions/:sessionId/steer` `{steerId, text, attachments}` writes a message into the running turn,
  which the model reads at its next tool gap; a repeated `steerId` within 10 minutes returns the first result.
  `POST /queue/:itemId/steer` does the same with a queued message and removes it from the queue. Both answer
  `steer-unavailable` when the provider does not declare `steer` in its capabilities or a permission request is
  open, and `not-running` when no turn runs.
- A session's settings are config options (ADR 0018): the driver declares them in `capabilities.config` (ACP's
  shape: `select` or `boolean`, `id`, `name`, `category`, `currentValue`; a select value may declare `risk: "high"` and an `icon` from the shared vocabulary `CONFIG_ICONS`). `POST /api/sessions` takes optional
  `config`, option ID to value, for the options it sets, and optional `configuration`, the ID of a provider configuration of the device ([ADR 0028](adr/0028-provider-configurations-live-on-the-machine.md); default: the provider's built-in), which must be enabled and extend `provider` (`invalid-request` otherwise); `POST /api/sessions/:sessionId/config` takes one
  `{configId, value}` and answers `200` once the owner has it. A setting or value the driver does not declare is
  `invalid-request` with `reason: config`. The runtime state (`GET /api/sessions`, `session.runtime`) reports the
  session's values as `capabilities.config[].currentValue`; the capabilities in `device.hello` carry the defaults
  of a new session. A prompt carries no settings.
- `POST /api/devices/:deviceId/attachments` uploads one image (`mediaType`, base64 `data`; a body of at most
  6 MiB) on the bulk lane: PNG, JPEG, GIF or WebP of at most 4 MiB (`too-large` beyond) whose first bytes are
  the declared type, for a connected device with a provider that declares `images` (`invalid-request` with
  `reason: images` otherwise, `target-offline` when the device is not connected). The core sends it to the device in
  chunks of at most 1 MiB and answers with the `attachmentId` once the device has stored it (ADR 0017). The `promptId` is chosen by the client; the same one sent again within
  10 minutes returns the first result, so a retry after a lost response does not run the prompt twice. The output
  of the turn arrives on the browser WebSocket.
- A session's `gitBranch` is the branch its lines last recorded (Claude Code's `gitBranch`), and the branch of the
  new worktree once a handoff commits; it is absent until a line names one. A watched project in
  `GET /api/devices/:deviceId/projects` carries the `branch` checked out there, read from its git `HEAD` (the
  commit's first 7 hex digits when detached), and none outside a repository.
- `POST /api/sessions/:sessionId/permission` answers a `session.permission` request; the first answer wins. A refusal may carry `message`, its reason told to the model. A request with `planExit` (ADR 0019) is a plan to review: an approval may carry `mode`, one of `planExit.modes`, which the session switches to (another value is `invalid-request` and the request stays open), and a plan request never expires on its own.
- `POST /api/sessions/:sessionId/permission/fresh` `{requestId, mode, promptId}` approves an open plan request into a new session ([ADR 0025](adr/0025-a-plan-can-be-approved-into-a-new-session.md)) and answers `{ok: true, sessionId, promptId}`, the new session. The plan and its file are read from the open request, never from the client. `request-closed` when the request is not an open plan request of the session; `invalid-request` with `reason: fresh-context` when its `planExit` does not declare `freshContext`, and with `reason: mode` when `mode` is not one of `planExit.modes`; `owner-offline` when the session's owner is not connected. The core then starts the session as `POST /api/sessions` does, on the owner device, in the session's project, with the same provider and `config` `{ [planExit.option]: mode }`, and its errors (`target-offline`, `timeout`, `rejected` with the agent's `reason`, each with `promptId`) are this route's; the plan then still waits. Only when the start succeeds does the core record the link, refuse the plan with "This plan is being implemented in a new session." and interrupt the old session's turn (a turn that is not running is fine). A plan answered elsewhere meanwhile keeps the link and is neither refused nor interrupted; a second call for a plan whose new session is starting is `request-closed`. `promptId` is deduplicated as for a new session: a repeat within 10 minutes returns the first result, except after a refusal.
- `GET /api/notify/push/key` returns the VAPID public key (unpadded base64url), the `applicationServerKey` for
  `pushManager.subscribe`. A subscription is stored by its endpoint, so subscribing again updates it. A listed
  subscription shows only the host of its push service (`service`), never the endpoint. A webhook is listed with
  `hasSecret`; its secret is write-only and `secret: null` in a PATCH removes it. A webhook has its own `events`, a
  non-empty list of `permission`, `plan`, `turn-done` and `failure`, each at most once (`invalid-request`
  otherwise); a create without `events` gets `permission`, `plan` and `failure`, and the list is returned in that
  order. At most 50 subscriptions,
  20 webhooks and 200 projects in the preferences (`too-large` beyond). `POST /api/notify/webhooks/:id/test`
  sends a test payload and returns the webhook with its `lastDelivery`.
- `POST /api/action/inspect` and `POST /api/action/decide` are the only routes without the host token. The
  token comes from the fragment of an action link (`<public URL>/a#<token>`); a token that is malformed, forged
  or from before the secrets were rotated is `invalid-token`. Both routes are rate limited per address and in
  total (`rate-limited`, `Retry-After: 60`). `state` of `inspect` is `open`, `answered` (another window or
  device decided, or the request closed), `expired`, `used` (the link already decided) or `webui-only`
  (a request that only the web UI may approve); `decide` returns `decided` when this call answered, or the
  state that kept it from answering, with status `200`. A `webui-only` or `owner-offline` result does not use up
  the link. The answer goes through the same path as `POST /api/sessions/:sessionId/permission`; approving
  a plan uses the plan's default mode.
- `POST /api/sessions/:sessionId/handoff` answers when the handoff is over, naming the new worktree and branch;
  `handoff.progress` reports its steps meanwhile. A failed handoff leaves the owner and the cursor where they were.
  Before one, `GET /api/devices/:deviceId/clone?origin=` with the `origin` of the workspace listing or the snapshot
  changes names the clone the target would use, or `path: null` when the request needs a `targetPath` or a
  clone. Then `clone` says what cloning would do ([ADR 0024](adr/0024-a-handoff-clones-the-repository-on-a-target-that-has-none.md)):
  the `origin` with its credentials removed, the `repoName`, the proposed `cloneInto`, a `reason` (`ok`,
  `no-origin`, `no-access`, `destination-taken`; `no-access` covers a failed probe, whether the network or the
  credentials) and, for `destination-taken`, a `detail` (`exists`,
  `outside-root`, `symlink`, `not-writable`). The query takes an optional `cloneInto` to check instead of the
  proposal.
- A session carries `configuration` (`{id, name}`) when it belongs to a provider configuration other than the vendor's
  built-in ([ADR 0028](adr/0028-provider-configurations-live-on-the-machine.md)); the owner agent announces the ID with
  `session.append`, and the name is the one the owner device announced (the ID while that device is offline).
- `POST /api/devices/:deviceId/providers` changes one provider configuration on a connected machine: `id` and any of
  `name`, `enabled` and `defaults` (which replaces the defaults as a whole). The command and the environment are not
  changeable from here. It answers `{deviceId, providers}` with the machine's list after the change. An unknown device
  is `not-found`, an offline one `owner-offline`, an unknown `id` or a default the driver does not declare
  `invalid-request` (`reason: configuration` or `config`), no answer in time `timeout`, and a refusal by the agent
  `rejected` with its text as `reason`.
- A handoff request takes an optional `configuration`, the ID of a provider configuration of the target; it must be
  enabled on the target and extend the session's provider (`invalid-request` with `reason: configuration`
  otherwise). The target installs the session in that configuration's session store; the default is the provider's built-in. A target that changed the configuration meanwhile refuses with `rejected` and the reason `configuration-unavailable`.
- A handoff request with `cloneInto` (exclusive with `targetPath`) makes the target clone the repository there
  first, before the owner is touched; `handoff.progress` reports `clone` with the bytes received. A failed clone
  answers `clone-failed` with the `reason` (`no-access`, `no-space`, `destination-taken`, `timeout`,
  `git-failed`) and the `detail`, and leaves the owner and the cursor where they were. `no-access` is a refusal
  by the remote (credentials, a missing repository); a network failure during the clone (a dropped connection,
  a TLS error, an unresolved host) is `git-failed`, with git's credential-stripped text as the `detail`.
  `POST /api/sessions/:sessionId/handoff/cancel` ends a handoff that is cloning, which then fails as
  `cancelled`; it never stops the owner, and answers `handoff-not-running` when no handoff of the session is in
  its clone step.
- `GET /api/sessions/:sessionId/deletion` previews a deletion (ADR 0023): `owner` (`deviceId`, `name`, `online`), `running`,
  `vendorFile` (`path`, or `null` when the agent finds none), `worktree` (`path`, `branch`, `dirty` with `modified` and
  `untracked`, `unpushed` commits that no remote and no other local branch has; `null` when Hoplet made none for
  the session or the agent no longer finds it) and `previousOwner` (`deviceId`, `name`, or `null`): a machine whose
  worktree Hoplet created and that stays untouched. With the owner offline the core answers alone: `online` is
  false, `vendorFile` and `worktree` are `null`.
- `DELETE /api/sessions/:sessionId` with `{vendorFile, worktree, worktreeDirty}` deletes the core's copy of the session and, when
  asked, the owner's vendor file and the worktree Hoplet made with its branch. It answers `{steps}`, each step
  `{step, status, reason?}` with `step` one of `stop-syncing`, `worktree`, `branch`, `vendor-file`, `core-copy` and
  `status` `done`, `failed` or `skipped`. The core hides the session at once (a tombstone) and ingest refuses it
  with `session.rejected` reason `deleted`. A running session is `session-running`; an optional item with the owner
  offline is `owner-offline`; with nothing optional asked for, the core copy goes and the agent forgets the session
  when it reconnects. A failed step is `delete-partial` with `steps` in the body, the core copy and the tombstone
  are kept and the same request continues. `worktreeDirty` is true only when the preview showed the worktree dirty
  and the user ticked it anyway; the agent checks right before removing, and a dirty worktree without it fails the
  `worktree` step with reason `dirty` (the `branch` step is skipped), so nothing with uncommitted changes goes silently. Importing the session again lifts the tombstone.
- `GET /api/sessions/:sessionId/lines` returns the stored lines `[start, end)` as conversation entries; a line
  without conversation has no entry, so `entries` can be shorter than the range. Large content blocks are
  elided in a page and read whole with `GET /api/sessions/:sessionId/blocks/:line/:block`; `:line` and `:block`
  are non-negative integers.
- `GET /api/sessions/:sessionId/commands` and `GET /api/devices/:deviceId/commands?cwd=` list the slash commands
  and skills a composer offers. `source` says where the list comes from: `session` (the owner reported it for that
  session), `directory` (the last list a session reported on that device in that directory) or `builtin` (the
  fixed set of built-in commands, when nothing was reported). Neither route asks the owner, so both answer while
  it is offline. A `session.commands` tells browsers to fetch the session's list again.
- The snapshot routes read the latest stored workspace snapshot whether or not the owner is online. The
  `workspace` routes ask the owner and need it online.
- `GET /api/devices/:deviceId/dirs` lists directories under the device's browse root and answers `invalid-request`
  with a `reason` for a path the agent refuses.
- The device project routes answer with the updated listing; the agent refuses a path that is not an existing
  directory with `rejected`.

### Query parameters

The query string is text, so the three schemas that read numbers or check a search string are not in the JSON
Schema file ([JSON Schema](#json-schema)). Their parameters:

| Route | Parameter | Meaning |
|---|---|---|
| `GET /api/sessions/:sessionId/lines` | `start` | Integer, default `-100`; a negative value counts from the end of the session |
| | `limit` | Integer, default `100`, kept within 1 to 500 |
| `GET /api/sessions/:sessionId/search` | `q` | The search string, required; one of only whitespace is `invalid-request` |
| | `start` | Integer, default `0`, at least `0`: the hit to start from |
| | `limit` | Integer, default `50`, kept within 1 to 200 |
| `GET /api/search` | `q` | As above |
| | `start` | Integer, default `0`, at least `0` |
| | `limit` | Integer, default `30`, kept within 1 to 100; the response says `exhausted: false` when more may exist |

A value that is not a number reads as the default. The other query parameters are in their schemas:
`GET /api/sessions/:sessionId/workspace/file` takes `path` and `at` (`worktree`, the default, or `head`), the
diff routes take `path`, `GET /api/devices/:deviceId/clone` takes `origin`, and `GET /api/devices/:deviceId/dirs`
takes an optional `path` (absent or `~` is the browse root).

`PATCH /api/sessions/:sessionId` changes what the user did to a session, beside what its vendor wrote: `title` is
the name Hoplet shows (stored trimmed, and at most 200 characters after trimming, else `invalid-request`; blank,
`null` or the vendor's own title clears it, so the vendor's title shows), `pinned` pins or unpins and `archived`
archives or restores; pinning a pinned session or archiving an archived one keeps its time. A field left out stays as it is, and a
body that names none of the three is `invalid-request`. The response is the session as `GET` returns it, with
`titleOverride`, `pinnedAt` and `archivedAt` (the times of the pin and of the archive) present only while set,
and every browser receives `session.updated`. The change writes no line and moves no cursor, and a session that
is archived stays archived when lines are appended to it or it is handed off. `GET /api/sessions` returns
archived sessions too; the client decides what to show.

A session's metadata (the list and `GET /api/sessions/:sessionId`) carries `continuedIn` and `continuedFrom`, each
`{ sessionId, title }`, while a plan was approved from it into a new session or it was started from one
([ADR 0025](adr/0025-a-plan-can-be-approved-into-a-new-session.md)). `title` is the one the list shows for that
session, or `""` while the session is not in the index yet; the client shows its untitled placeholder then.

## Error codes

| Code | HTTP status | Meaning |
|---|---|---|
| `unauthorized` | 401 | The host token is missing or wrong |
| `invalid-token` | 400 | The action token is malformed, was not signed by this core or does not match any request |
| `rate-limited` | 429 | Too many requests to the action routes; retry after `Retry-After` seconds |
| `not-found` | 404 | No route serves the path, or the block or path asked for does not exist |
| `method-not-allowed` | 405 | The path exists for another method; `Allow` lists them |
| `session-not-found` | 404 | No session has this ID |
| `invalid-json` | 400 | The request body is not JSON |
| `invalid-request` | 400 | The body, the query or a path parameter does not match its schema, or the agent refused the request (`reason` says what) |
| `same-device` | 400 | The handoff target already owns the session |
| `too-large` | 413 | The body is over the route's limit, a prompt is over 8,000 characters, or an uploaded image is over 4 MiB |
| `owner-offline` | 409 | The agent of the session's owner (or of the addressed device) is not connected |
| `target-offline` | 409 | The device the request goes to is not connected: the device of a new session, the target of a handoff, the device to browse |
| `busy` | 409 | The request cannot run now: the bulk queue is full, or the session's process is running a turn, or a running session cannot be handed off. Retry later |
| `steer-unavailable` | 409 | The session's provider cannot take a message into a running turn, or a permission request is open |
| `queue-conflict` | 409 | The queued message changed, is already sending or is gone; read the queue again |
| `queue-full` | 409 | The session already holds 20 queued messages |
| `starting` | 409 | The session's process is still starting |
| `external` | 409 | The session's process was not started by Hoplet, so it cannot be controlled |
| `not-running` | 409 | The session has no running process to interrupt or stop |
| `request-closed` | 409 | The permission request was already answered or has closed |
| `cancelled` | 409 | The operation was cancelled before it completed |
| `handoff-in-progress` | 409 | A handoff of this session is already running |
| `no-clone` | 409 | The target has no clone of the session's repository; send `targetPath` or `cloneInto` |
| `handoff-not-running` | 409 | No handoff of this session is in its clone step, so there is nothing to cancel |
| `clone-failed` | 502 | The target could not clone the repository; the details carry the `reason` and `detail` |
| `no-snapshot` | 409 | No workspace snapshot is stored for the session |
| `session-changed` | 409 | The session changed while the handoff ran |
| `verify-failed` | 502 | What the target installed did not match what was sent |
| `terminal-unavailable` | 409 | The owner's agent did not announce the terminal capability (its `terminal` switch is off) |
| `terminal-busy` | 409 | The session already has a terminal, or the machine already has three |
| `session-running` | 409 | A process Hoplet started is alive in the session; stop it before deleting |
| `delete-partial` | 502 | A step of a deletion failed; `steps` lists each step and the request can be sent again |
| `rejected` | 502 | The agent refused or failed the request; `reason` is its diagnostic text |
| `timeout` | 504 | The agent did not answer in time |
| `internal` | 500 | The core failed |

## Browser WebSocket

`/ws?ticket=<ticket>` upgrades to a WebSocket after a ticket from `POST /api/ws-tickets` was presented. A ticket
that is missing, used, expired or unknown is refused with HTTP `401` and no upgrade. The proxy in front of the core
upgrades `/ws` (docs/hosting.md#network).

Every message is a JSON text frame with `v` (the protocol version, the `protocolVersion` of
`GET /api/health`) and `type`. A message from the client with another `v` is answered with an `error` message
whose code is `protocol-version-mismatch`; one that is not JSON, `invalid-json`; one that does not match its
schema or has an unknown `type`, `invalid-message`. The client sends at most 64 KiB per message and subscribes to
at most 256 sessions at once (`too-large` beyond). The core closes a connection whose reader falls behind or
that stops answering WebSocket pings; the client then mints a new ticket, reconnects and reads the state again
over REST.

| Type | Direction | Lane | Schema | Meaning |
|---|---|---|---|---|
| `subscribe` | browser → core | control | `SubscribeMessage` | Receive the streamed output of a session. The core answers with its `session.runtime` and each open `session.permission` |
| `unsubscribe` | browser → core | control | `UnsubscribeMessage` | Stop receiving the output of a session |
| `terminal.attach` | browser → core | control | `TerminalAttachMessage` | Receive the output of an open terminal (opened with `POST /api/sessions/:sessionId/terminal`). The core answers with `terminal.attached` and replays up to 64 KiB of recent output as `terminal.data`; an unknown terminal is answered with `terminal.ended` |
| `terminal.detach` | browser → core | control | `TerminalDetachMessage` | Stop receiving a terminal. The core closes it with reason `detached` 60 seconds after the last connection detached or went away |
| `terminal.keys` | browser → core | control | `TerminalKeysMessage` | Input for an attached terminal: base64 bytes, at most 4 KiB |
| `terminal.size` | browser → core | control | `TerminalSizeMessage` | The size of an attached terminal's window: 1 to 500 columns, 1 to 200 rows |
| `terminal.attached` | core → browser | control | `TerminalAttachedMessage` | The connection is attached to the terminal; carries the session and the current size. Only to the attached connection |
| `terminal.data` | core → browser | control | `TerminalDataMessage` | Output of a terminal: `seq` and base64 bytes, at most 4 KiB. Only to attached connections. Never stored; the core withholds the agent's credit while a connection's send buffer is backed up |
| `terminal.ended` | core → browser | control | `TerminalEndedMessage` | The terminal is gone: `reason` (`exit`, `idle`, `max-age`, `detached`, `turn-started`, `handoff`, `deleted`, `owner-offline`, `closed`, `input-flood`, ...) and the `exitCode` when the shell exited. Only to attached connections |
| `session.listening` | core → browser | control | `SessionListeningMessage` | The TCP ports the session's processes listen on (`port`, `address`, `process`; at most 16) and the `previewHost` to build links with. Sent to the connections subscribed to the session when they subscribe and on every change; `ports` is empty when the owner went offline. Kept in memory only |
| `session.appended` | core → browser | control | `SessionAppendedMessage` | Lines were appended to a session, or a session is new (`isNew`). Sent to every connection |
| `session.deleted` | core → browser | control | `SessionDeletedMessage` | A session was deleted (its tombstone is written). Carries `sessionId`; drop it from every list. Sent to every connection |
| `session.updated` | core → browser | control | `SessionUpdatedMessage` | The user renamed, pinned, unpinned, archived or restored a session. Carries the state after the change (`titleOverride`, `pinnedAt`, `archivedAt`; absent means cleared). Sent to every connection |
| `device.presence` | core → browser | control | `DevicePresenceMessage` | A device connected or disconnected. Sent to every connection, followed by `session.runtime` for each session the connection is subscribed to |
| `session.runtime` | core → browser | control | `SessionRuntimeMessage` | The runtime state of a session and its capabilities. Sent to every connection |
| `session.queue` | core → browser | control | `SessionQueueMessage` | The queue of a session changed; fetch `GET /api/sessions/:sessionId/queue`. Sent to every connection |
| `session.commands` | core → browser | control | `SessionCommandsMessage` | The commands and skills of a session changed; fetch `GET /api/sessions/:sessionId/commands`. Sent to every connection |
| `session.output` | core → browser | control | `SessionOutputMessage` | Streamed content of the running turn: text, thinking, tool calls and results. Only to the connections subscribed to the session. Transient: the session's lines are the record |
| `session.permission` | core → browser | control | `SessionPermissionMessage` | A tool wants permission; answer with `POST /api/sessions/:sessionId/permission`. `planExit` (`plan`, `planFile`, `option`, `modes`, `default`, `freshContext`) marks a request to leave a plan; `freshContext` says approval can start a new session. Sent to every connection |
| `handoff.progress` | core → browser | control | `HandoffProgressMessage` | A step of a handoff: `sent` of `total` bytes during `transfer`, the new `worktree` and `branch` on `done`, the `reason` on `failed`. Sent to every connection |
| `notify` | core → browser | control | `NotifyMessage` | A notification (`kind`, `sessionId`, `project`, `title`, `tool`, `summary`, `requestId`, `reason`, `link`) for a native client that is connected. Defined for native clients and not sent by the core yet; the payload is the one of every channel ([Notifications](#notifications)) |
| `error` | core → browser | control | `ErrorMessage` | The core could not take a message from the client; `code` says why |

### Terminal and dev server ports

A terminal is opened over REST and streamed over the WebSocket ([ADR 0026](adr/0026-the-inspector-opens-a-restricted-terminal-and-links-dev-servers.md)).
`POST /api/sessions/:sessionId/terminal` (host token) makes the core generate a `terminalId` and ask the owner's
agent for a shell; it answers `terminal-unavailable` when the agent did not announce the capability, `owner-offline`,
`session-running` (a turn runs or waits for permission), `external`, `starting`, `handoff-in-progress` or
`terminal-busy` (one terminal per session, three per machine) without contacting the shell. The connection that
opens the terminal sends `terminal.attach`; after a reload `GET /api/sessions/:sessionId/terminal` returns the open
terminal (or `null`) and the page attaches again. The browser does not acknowledge output: the core passes the
agent's credit on once the connection's send buffer has drained. A prompt sent to the session closes the terminal
first (`turn-started`). Handoff, deletion and the loss of the owner's connection close it too. Input, output and
command lines are never logged or stored; the log holds open and close with IDs, reason, duration and byte counts.

`GET /api/sessions/:sessionId/ports` and `session.listening` carry the listening ports of the session's processes.
The core asks the agent to watch the sessions that have a subscribed connection and keeps the latest report in
memory. `previewHost` is the host name the agent announced in its hello (`preview_host` of its configuration, or its
host name); a client builds `http://<previewHost>:<port>` from it. `GET /api/devices` also lists `terminal` (the
announced capability) and `previewHost` for each connected device.

## Notifications

The core notifies on Web Push, on webhooks and, later, on connected native clients (ADR 0021). The events are
`permission`, `plan`, `turn-done` (only when no browser has the session open) and `failure` (a session that
failed to start or a handoff that failed). The payload (`NotifyPayloadSchema`) carries the kind, the session,
the project's name, the session title and, for a permission request, the tool and its command or path redacted
and cut to 80 characters. It never carries the prompt, file contents or a diff. A webhook receives it as a JSON
`POST` with `X-Hoplet-Signature: sha256=<hex>`, the HMAC-SHA-256 of the body under the webhook's secret, when it
has one. A permission or plan payload carries `link`, the action link `<public URL>/a#<token>`, when the core
knows its public URL (docs/hosting.md#public-url). The token is signed by the core, bound to one request and
session, valid for the request's remaining time but at most 10 minutes (a plan: 10 minutes), and decides once.

## JSON Schema

`docs/schema/hoplet-api.schema.json` is generated from `shared/` and the route table by `pnpm schema`
(`scripts/api-schema.ts`) and committed. `pnpm check` fails when it is not what `pnpm schema` produces, so
changing a schema in `shared/` means running `pnpm schema` and committing the result.

The file is one JSON Schema document (draft 2020-12). `$defs` holds one schema per name used in this document:
the request, query and response schemas of the routes, `ErrorResponseSchema`, and one `...Message` schema per
browser message. Parse a body against `#/$defs/<Name>`. The other keys index it: `protocolVersion`, `routes`
(method, path, auth, lane, status and the schema names of each route), `browserMessages` (type, direction, lane
and schema), `errorResponse`, `errorStatus` (the HTTP status of every error code) and `excluded`.

A string that `shared/` trims before it checks its length is described for the value as it arrives, so the file
accepts what `shared/` accepts without the client trimming first. With no upper bound it has the pattern `\S`
(in JSON, `"pattern": "\\S"`): the value must hold a character other than whitespace. With an upper bound, as
`targetPath` of `HandoffRequestSchema` and `projectPath` of `NewSessionRequestSchema` have (4096), the bound
counts the trimmed text only: the string has no `maxLength`, and its pattern
`^\s*\S(?:[\s\S]{0,4094}\S)?\s*$` allows any whitespace around 1 to 4096 characters, so `" ".repeat(4096) + "x"`
is valid. `\s` is the whitespace that `trim` strips. `shared/` counts length in UTF-16 code units
(`string.length`) and a JSON Schema validator counts code points, so for text outside the Basic Multilingual
Plane the file can accept a value that `shared/` rejects.

A schema that JSON Schema cannot express is not converted and not dropped: it is listed in `excluded` with the
routes that use it and the reason. These are the exclusions:

| Schema | Route | Reason |
|---|---|---|
| `LinesQuerySchema` | `GET /api/sessions/:sessionId/lines` | It coerces text to numbers and keeps them in range (a `transform`) |
| `SessionSearchQuerySchema` | `GET /api/sessions/:sessionId/search` | It checks that `q` holds more than whitespace (a `check`) and coerces numbers |
| `GlobalSearchQuerySchema` | `GET /api/search` | The same as `SessionSearchQuerySchema` |

Their parameters are in [Query parameters](#query-parameters). The messages between the agents and the core
(`/ws/agent`) are not part of this contract.
