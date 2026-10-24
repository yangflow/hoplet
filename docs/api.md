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

A client that does not need the whole machine uses a scoped API token instead ([API tokens](#api-tokens)); it is
sent the same way and sees only what its grant reaches.

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

## TypeScript SDK

`@hoplet/sdk` (`sdk/`) is a typed client of the routes and the browser WebSocket below: `new Hoplet({ url, token })`,
then `listSessions`, `createSession`, `sendPrompt`, `waitForTurn`, a paged `readSession`, `subscribe` and the
rest. It checks `protocolVersion` once, mints a ticket for every socket connection, and raises a `HopletError`
whose `code` is one of [Error codes](#error-codes) (or `network-error`, `invalid-response`,
`protocol-version-mismatch`, `aborted`). See `sdk/README.md`.

The `hoplet` command (`@hoplet/cli`, `cli/`) is built on the SDK and prints these codes' messages; with `--json`
it prints failures as `{"code", "reason"?}` on standard error. See `cli/README.md`.

## Conventions

- Requests and responses are JSON (UTF-8). A request body is sent with `content-type: application/json`; each
  route limits its size (4 KiB to 64 KiB) and answers `too-large` beyond it.
- A success is `200`, except where the route table says otherwise. Every other response is an error:
  `{ "code": "<code>", "reason"?: "<text>", "promptId"?: "<id>", "budget"?: { "id", "name" } }` with the HTTP status of its code
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

The Auth column says who may call a route. `host` is the host token as `Authorization: Bearer` and nothing else.
`read`, `send`, `create` and `answer` are the abilities of a scoped API token ([API tokens](#api-tokens)): the
host token may call the route as well, and so may a token that holds that ability (`send` is held by a token that
may send, `create` by one that may create, and so on; a preset holds the abilities below it). A route that is not
marked with an ability is the host's alone. The two action routes (`token`) take no header; the signed action
token in the body is the credential ([Notifications](#notifications)). No route is open without one of these.
The Query and Body columns name the schema of the query string and of the JSON body; the
Response column names the schema of the success body and its status when it is not `200`.

| Method | Path | Auth | Lane | Query | Body | Response | Errors |
|---|---|---|---|---|---|---|---|
| POST | `/api/ws-tickets` | read | control | — | — | `WsTicketResponseSchema` | — |
| GET | `/api/health` | read | control | — | — | `HealthResponseSchema` | — |
| GET | `/api/sessions` | read | control | — | — | `SessionListResponseSchema` | — |
| POST | `/api/sessions` | create | control | — | `NewSessionRequestSchema` | `NewSessionResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `over-budget`, `target-offline`, `timeout`, `rejected`, `forbidden`, `loop-limit`, `depth-limit`, `children-limit`, `budget-exceeded`, `rate-limited` |
| GET | `/api/sessions/:sessionId` | read | control | — | — | `LiveSessionMetaSchema` | `session-not-found` |
| GET | `/api/sessions/:sessionId/children` | read | control | — | — | `SessionChildrenResponseSchema` | `session-not-found` |
| GET | `/api/sessions/:sessionId/children/status` | read | control | `ChildStatusQuerySchema` | — | `ChildStatusesSchema` | `invalid-request`, `session-not-found` |
| POST | `/api/sessions/:sessionId/children/:childId/collect` | create | control | `ChildCollectQuerySchema` | — | `ChildCollectSchema` | `invalid-request`, `session-not-found` |
| GET | `/api/sessions/:sessionId/children/:childId/diff` | read | bulk | `ChildDiffQuerySchema` | — | `WorkspaceDiffSchema` | `invalid-request`, `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/children/:childId/cancel` | create | control | `ChildCancelQuerySchema` | — | `ChildCancelSchema` | `invalid-request`, `session-not-found`, `owner-offline` |
| PATCH | `/api/sessions/:sessionId` | host | control | — | `SessionPatchRequestSchema` | `LiveSessionMetaSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found` |
| DELETE | `/api/sessions/:sessionId` | host | control | — | `DeleteSessionRequestSchema` | `DeleteSessionResponseSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `session-running`, `owner-offline`, `delete-partial`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/terminal` | host | control | — | `TerminalOpenRequestSchema` | `TerminalInfoSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `terminal-unavailable`, `terminal-busy`, `external`, `starting`, `session-running`, `handoff-in-progress`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/terminal` | host | control | — | — | `TerminalStateSchema` | `session-not-found` |
| DELETE | `/api/terminals/:terminalId` | host | control | — | — | `TerminalCloseResultSchema` | `not-found` |
| GET | `/api/sessions/:sessionId/ports` | host | control | — | — | `SessionPortsResultSchema` | `session-not-found` |
| GET | `/api/sessions/:sessionId/deletion` | host | control | — | — | `DeletionPreviewSchema` | `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/lines` | read | bulk | `LinesQuerySchema` | — | `LinesPageSchema` | `session-not-found`, `busy` |
| GET | `/api/sessions/:sessionId/blocks/:line/:block` | read | bulk | — | — | `FullBlockSchema` | `invalid-request`, `session-not-found`, `not-found`, `busy` |
| GET | `/api/sessions/:sessionId/search` | read | bulk | `SessionSearchQuerySchema` | — | `SearchResultSchema` | `invalid-request`, `session-not-found`, `busy` |
| GET | `/api/search` | read | bulk | `GlobalSearchQuerySchema` | — | `GlobalSearchResultSchema` | `invalid-request`, `busy` |
| POST | `/api/sessions/:sessionId/prompt` | send | control | — | `PromptRequestSchema` | `PromptResultSchema`, 202 | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `external`, `starting`, `busy`, `over-budget`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/queue` | read | control | — | — | `QueueSchema` | `session-not-found` |
| POST | `/api/sessions/:sessionId/queue` | send | control | — | `QueueAddRequestSchema` | `QueueSchema`, 201 | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `queue-full` |
| POST | `/api/sessions/:sessionId/queue/order` | send | control | — | `QueueOrderRequestSchema` | `QueueSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `queue-conflict` |
| POST | `/api/sessions/:sessionId/queue/resume` | send | control | — | — | `QueueSchema` | `session-not-found` |
| POST | `/api/sessions/:sessionId/queue/clear` | send | control | — | — | `QueueSchema` | `session-not-found` |
| PATCH | `/api/sessions/:sessionId/queue/:itemId` | send | control | — | `QueueEditRequestSchema` | `QueueSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `queue-conflict` |
| DELETE | `/api/sessions/:sessionId/queue/:itemId` | send | control | `QueueVersionQuerySchema` | — | `QueueSchema` | `invalid-request`, `session-not-found`, `queue-conflict` |
| POST | `/api/sessions/:sessionId/queue/:itemId/steer` | send | control | — | `QueueSteerRequestSchema` | `QueueSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `external`, `starting`, `not-running`, `steer-unavailable`, `queue-conflict`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/steer` | send | control | — | `SteerRequestSchema` | `SteerResultSchema`, 202 | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `external`, `starting`, `not-running`, `steer-unavailable`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/config` | host | control | — | `ConfigRequestSchema` | `RuntimeCommandResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `external` |
| POST | `/api/sessions/:sessionId/permission` | answer | control | — | `PermissionRequestSchema` | `RuntimeCommandResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `request-closed` |
| POST | `/api/sessions/:sessionId/permission/fresh` | answer | control | — | `FreshPermissionRequestSchema` | `NewSessionResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `request-closed`, `over-budget`, `target-offline`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/interrupt` | send | control | — | — | `RuntimeCommandResultSchema` | `session-not-found`, `owner-offline`, `not-running` |
| POST | `/api/sessions/:sessionId/stop` | host | control | `StopQuerySchema` | — | `StopResultSchema` | `session-not-found`, `owner-offline`, `not-running` |
| GET | `/api/sessions/:sessionId/commands` | host | control | — | — | `CommandsResultSchema` | `session-not-found` |
| GET | `/api/devices/:deviceId/commands` | host | control | `DeviceCommandsQuerySchema` | — | `CommandsResultSchema` | `invalid-request` |
| GET | `/api/sessions/:sessionId/workspace` | host | control | — | — | `WorkspaceListingSchema` | `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/workspace/file` | host | control | `WorkspaceFileQuerySchema` | — | `WorkspaceFileSchema` | `invalid-request`, `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/workspace/diff` | host | control | `WorkspaceDiffQuerySchema` | — | `WorkspaceDiffSchema` | `invalid-request`, `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/workspace/changes` | host | control | — | — | `WorkspaceChangesSchema` | `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/snapshot/changes` | host | bulk | — | — | `SnapshotChangesSchema` | `session-not-found`, `no-snapshot`, `busy` |
| GET | `/api/sessions/:sessionId/export` | host | bulk | `SessionExportQuerySchema` | — | `SessionExportMarkdownSchema`, streamed as Markdown ([Session export](#session-export)) | `invalid-request`, `session-not-found`, `rate-limited`, `busy` |
| GET | `/api/sessions/:sessionId/snapshot/diff` | host | bulk | `SnapshotDiffQuerySchema` | — | `SnapshotDiffSchema` | `invalid-request`, `session-not-found`, `no-snapshot`, `not-found`, `busy` |
| POST | `/api/sessions/:sessionId/handoff` | host | control | — | `HandoffRequestSchema` | `HandoffResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `same-device`, `target-offline`, `owner-offline`, `handoff-in-progress`, `busy`, `no-snapshot`, `no-clone`, `clone-failed`, `session-changed`, `verify-failed`, `cancelled`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/handoff/rules` | host | control | `HandoffRulesQuerySchema` | — | `HandoffRulesPreviewSchema` | `invalid-request`, `session-not-found`, `not-found` |
| POST | `/api/sessions/:sessionId/handoff/cancel` | host | control | — | — | `HandoffCancelResultSchema` | `session-not-found`, `handoff-not-running` |
| POST | `/api/handoff-requests` | request-handoff | control | — | `SessionHandoffRequestCreateSchema` | `SessionHandoffRequestSchema` (201) | `invalid-json`, `invalid-request`, `too-large`, `forbidden`, `not-found`, `same-device`, `target-offline`, `loop-limit`, `rate-limited` |
| GET | `/api/sessions/:sessionId/handoff-requests` | host | control | — | — | `SessionHandoffRequestListSchema` | `session-not-found` |
| POST | `/api/handoff-requests/:requestId/approve` | host | control | — | — | `SessionHandoffRequestSchema` | `not-found`, `request-closed` |
| POST | `/api/handoff-requests/:requestId/decline` | host | control | — | — | `SessionHandoffRequestSchema` | `not-found`, `request-closed` |
| POST | `/api/sessions/:sessionId/config-changes` | write-config | control | — | `SessionConfigChangeCreateSchema` | `SessionConfigChangeSchema` (201) | `invalid-json`, `invalid-request`, `too-large`, `forbidden`, `not-found`, `owner-offline`, `timeout`, `unsupported`, `stale`, `unsafe-content`, `rejected`, `request-closed` |
| GET | `/api/sessions/:sessionId/config-changes` | read-config | control | — | — | `SessionConfigChangeListSchema` | `not-found` |
| GET | `/api/config-changes/:changeId` | read-config | control | — | — | `SessionConfigChangeSchema` | `not-found` |
| GET | `/api/sessions/:sessionId/config` | read-config | control | — | — | `SessionConfigViewSchema` | `not-found` |
| GET | `/api/sessions/:sessionId/config-files` | read-config | control | `ConfigFileListQuerySchema` | — | `ConfigFileReadSchema` | `invalid-request`, `not-found`, `session-not-found`, `owner-offline`, `timeout`, `unsupported`, `rejected` |
| GET | `/api/sessions/:sessionId/config-files/:kind` | read-config | control | `ConfigFileReadQuerySchema` | — | `ConfigFileReadSchema` | `invalid-request`, `not-found`, `session-not-found`, `owner-offline`, `timeout`, `unsupported`, `rejected` |
| POST | `/api/config-changes/:changeId/approve` | host | control | — | — | `SessionConfigChangeSchema` | `not-found`, `request-closed` |
| POST | `/api/config-changes/:changeId/decline` | host | control | — | — | `SessionConfigChangeSchema` | `not-found`, `request-closed` |
| POST | `/api/config-changes/:changeId/undo` | host | control | — | — | `SessionConfigChangeSchema` | `not-found`, `request-closed`, `owner-offline`, `stale`, `unsupported`, `rejected`, `busy` |
| GET | `/api/devices` | read | control | — | — | `DeviceListResponseSchema` | — |
| GET | `/api/devices/:deviceId/resources` | host | control | — | — | `DeviceResourcesResultSchema` | `not-found` |
| PUT | `/api/devices/:deviceId/labels` | host | control | — | `DeviceLabelsRequestSchema` | `DeviceLabelsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `not-found` |
| POST | `/api/devices` | host | control | — | `DeviceMintRequestSchema` | `DeviceMintResultSchema` | `invalid-json`, `invalid-request`, `too-large` |
| POST | `/api/devices/:deviceId/attachments` | send | bulk | — | `PromptImageSchema` | `AttachmentResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `target-offline`, `timeout`, `forbidden` |
| GET | `/api/devices/:deviceId/clone` | host | control | `DeviceCloneQuerySchema` | — | `DeviceCloneResultSchema` | `invalid-request`, `target-offline`, `timeout` |
| GET | `/api/devices/:deviceId/dirs` | host | control | `DeviceDirsQuerySchema` | — | `DeviceDirsResultSchema` | `invalid-request`, `target-offline`, `timeout` |
| GET | `/api/devices/:deviceId/projects` | read | control | — | — | `DeviceProjectsResultSchema` | `owner-offline`, `timeout`, `rejected`, `cancelled`, `forbidden` |
| POST | `/api/devices/:deviceId/projects` | host | control | — | `DeviceProjectsAddRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/providers` | host | control | — | `DeviceProvidersUpdateRequestSchema` | `DeviceProvidersResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `not-found`, `owner-offline`, `timeout`, `rejected` |
| POST | `/api/devices/:deviceId/projects/remove` | host | control | — | `DeviceProjectsRemoveRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/sessions/import` | host | control | — | `DeviceSessionsImportRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/sessions/remove` | host | control | — | `DeviceSessionsRemoveRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| GET | `/api/notify/preferences` | host | control | — | — | `NotifyPreferencesSchema` | — |
| PUT | `/api/notify/preferences` | host | control | — | `NotifyPreferencesSchema` | `NotifyPreferencesSchema` | `invalid-json`, `invalid-request`, `too-large` |
| GET | `/api/resources/thresholds` | host | control | — | — | `ResourceThresholdsSchema` | — |
| PUT | `/api/resources/thresholds` | host | control | — | `ResourceThresholdsSchema` | `ResourceThresholdsSchema` | `invalid-json`, `invalid-request`, `too-large` |
| GET | `/api/usage` | host | bulk | `UsageQuerySchema` | — | `UsageResponseSchema` | `invalid-request` |
| GET | `/api/sessions/:sessionId/usage` | host | bulk | — | — | `SessionUsageSchema` | `session-not-found` |
| GET | `/api/usage/prices` | host | control | — | — | `UsagePricesSchema` | — |
| PUT | `/api/usage/prices` | host | control | — | `UsagePricesBodySchema` | `UsagePricesSchema` | `invalid-json`, `invalid-request`, `too-large` |
| GET | `/api/budgets` | host | control | — | — | `BudgetListResponseSchema` | — |
| POST | `/api/budgets` | host | control | — | `BudgetBodySchema` | `BudgetSchema`, 201 | `invalid-json`, `invalid-request`, `too-large` |
| PUT | `/api/budgets/:id` | host | control | — | `BudgetBodySchema` | `BudgetSchema` | `invalid-json`, `invalid-request`, `too-large`, `not-found` |
| DELETE | `/api/budgets/:id` | host | control | — | — | `BudgetListResponseSchema` | `not-found` |
| POST | `/api/budgets/:id/allow` | host | control | — | — | `BudgetSchema` | `not-found`, `invalid-request` |
| DELETE | `/api/budgets/:id/allow` | host | control | — | — | `BudgetSchema` | `not-found`, `invalid-request` |
| GET | `/api/templates` | create | control | — | — | `TemplateListResponseSchema` | — |
| POST | `/api/templates` | host | control | — | `TemplateBodySchema` | `TemplateSchema`, 201 | `invalid-json`, `invalid-request`, `too-large` |
| GET | `/api/templates/export` | host | control | — | — | `TemplateFileSchema` | — |
| POST | `/api/templates/import` | host | control | — | `TemplateImportRequestSchema` | `TemplateImportResultSchema` | `invalid-json`, `invalid-request`, `too-large` |
| GET | `/api/templates/:id` | host | control | — | — | `TemplateSchema` | `not-found` |
| PUT | `/api/templates/:id` | host | control | — | `TemplateBodySchema` | `TemplateSchema` | `invalid-json`, `invalid-request`, `too-large`, `not-found` |
| DELETE | `/api/templates/:id` | host | control | — | — | `TemplateListResponseSchema` | `not-found` |
| POST | `/api/templates/:id/start` | create | control | — | `TemplateStartRequestSchema` | `TemplateStartResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `not-found`, `target-offline`, `over-budget`, `timeout` |
| GET | `/api/permission-rules` | host | control | `PermissionRuleListQuerySchema` | — | `PermissionRuleListResponseSchema` | `invalid-request` |
| POST | `/api/permission-rules` | host | control | — | `PermissionRuleBodySchema` | `PermissionRuleEntrySchema`, 201 | `invalid-json`, `invalid-request`, `too-large` |
| PUT | `/api/permission-rules/:id` | host | control | — | `PermissionRulePutSchema` | `PermissionRuleEntrySchema` | `invalid-json`, `invalid-request`, `too-large`, `not-found` |
| DELETE | `/api/permission-rules/:id` | host | control | — | — | `PermissionRuleListResponseSchema` | `invalid-request`, `not-found` |
| GET | `/api/permission-rules/machines` | host | control | — | — | `PermissionRuleMachinesResponseSchema` | — |
| POST | `/api/permission-rules/machines/:deviceId/push` | host | control | — | — | `PermissionRuleMachinesResponseSchema` | `not-found`, `owner-offline` |
| GET | `/api/audit/events` | host | bulk | `AuditEventsQuerySchema` | — | `AuditEventsResponseSchema` | `invalid-request` |
| GET | `/api/audit/export` | host | bulk | `AuditExportQuerySchema` | — | `AuditEventSchema`, streamed as JSON Lines or CSV | `invalid-request`, `rate-limited`, `audit-unavailable` |
| GET | `/api/audit/verify` | host | bulk | `AuditVerifyQuerySchema` | — | `AuditVerifyResponseSchema` | `invalid-request` |
| GET | `/api/tokens` | host | control | — | — | `TokenListResponseSchema` | — |
| POST | `/api/tokens` | host | control | — | `TokenCreateRequestSchema` | `TokenCreateResponseSchema`, 201 | `invalid-json`, `invalid-request`, `too-large` |
| DELETE | `/api/tokens/:id` | host | control | — | — | `TokenRevokeResponseSchema` | `not-found` |
| GET | `/api/sessions/:sessionId/mcp` | host | control | — | — | `SessionMcpResponseSchema` | `session-not-found` |
| PUT | `/api/sessions/:sessionId/mcp` | host | control | — | `SessionMcpPutRequestSchema` | `SessionMcpResponseSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found` |
| GET | `/api/mcp/defaults` | host | control | — | — | `SessionMcpDefaultsResponseSchema` | — |
| PUT | `/api/mcp/defaults` | host | control | — | `SessionMcpDefaultsPutRequestSchema` | `SessionMcpOkResponseSchema` | `invalid-json`, `invalid-request`, `too-large` |
| DELETE | `/api/mcp/defaults` | host | control | `SessionMcpDefaultsDeleteQuerySchema` | — | `SessionMcpOkResponseSchema` | `invalid-request`, `not-found` |
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
| GET | `/api/hooks` | host | control | — | — | `HookListResponseSchema` | — |
| POST | `/api/hooks` | host | control | — | `HookCreateRequestSchema` | `HookSecretResponseSchema`, 201 | `invalid-json`, `invalid-request`, `too-large` |
| GET | `/api/hooks/:id` | host | control | — | — | `HookInfoSchema` | `not-found` |
| PATCH | `/api/hooks/:id` | host | control | — | `HookUpdateRequestSchema` | `HookInfoSchema` | `invalid-json`, `invalid-request`, `too-large`, `not-found` |
| DELETE | `/api/hooks/:id` | host | control | — | — | `HookListResponseSchema` | `not-found` |
| POST | `/api/hooks/:id/rotate-secret` | host | control | — | — | `HookSecretResponseSchema` | `not-found` |
| GET | `/api/hooks/:id/deliveries` | host | control | — | — | `HookDeliveriesResponseSchema` | `not-found` |
| POST | `/api/hooks/:id/test` | host | control | — | — | `HookTestResponseSchema` | `not-found` |
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
  `memory` is true when more than the machine's memory threshold of the memory is used, `disk` when less than its
  disk threshold of the volume is free (90% and 10% unless `GET /api/resources/thresholds` says otherwise), and
  both are false while the device is offline. A stored change applies to the next `GET /api/devices`; the web UI
  polls it.
- `GET /api/devices` also lists `inventory`, the latest `device.inventory` report of the machine (`at`, `toolchains`,
  `gpus`, `repositories`), kept while the device is offline; it is absent for a machine that never reported.
  `toolchains` are the developer tools found on it (`name`, `version`; at most 32), `gpus` its graphics adapters
  (`name`, `memory` in bytes when known; at most 8) and `repositories` the watched projects (`path`, `origin`: the
  URL of the `origin` remote without user, password, query or fragment, `null` when it has none; at most 200).
  `labels` are the host's own tags for the machine, `[]` by default.
- `PUT /api/devices/:deviceId/labels` replaces the labels of a device and answers them. A label is 1 to 32 lowercase
  letters, digits or `-`, starting with a letter or a digit; at most 20; a repeated label is dropped, keeping the
  first. A body that is not JSON is `invalid-json`, a bad label or more than 20 labels `invalid-request`, a body over
  16 KiB `too-large` and an unknown device `not-found`. It works while the device is offline, and the labels survive
  an agent reconnecting.
- `GET /api/resources/thresholds` answers the thresholds at which machines warn: `default` for every machine and
  `machines`, the overrides keyed by device ID. Each is a `{ memory, disk }` pair of whole percents: `memory` is the
  percent of the memory used above which a machine warns (1 to 100), `disk` the percent of the volume free under
  which it warns (0 to 99). A machine without an override uses `default`; with nothing stored the default is
  `{ memory: 90, disk: 10 }` and there are no overrides. `PUT` replaces the whole object, so a machine's override is
  removed by leaving it out; at most 200 overrides, more is `too-large`. The core keeps them in its database. A
  client shows the effective values of a machine from this object (`machines[deviceId] ?? default`), not from
  `GET /api/devices`.
- `GET /api/usage` answers the tokens the sessions used and what they are estimated to cost. `from` and `to` (Unix
  ms, `[from, to)`, open ended when absent) bound the time of the assistant messages; `groupBy` is one or two
  different dimensions, comma separated, of `session`, `project`, `device`, `model` and `day` (a calendar day,
  `YYYY-MM-DD`, at `utcOffset`: whole minutes east of UTC from -840 to 840, 0 by default), `model` by default; `session`, `project`, `device` and `model` narrow the result to one value;
  `offset` and `limit` (default 100, at most 500) page the groups. The answer has `total`, `rows` (each with
  `keys`, a `{ value, label }` per dimension, and the same counts as `total`), `rowCount` over all pages, `offset`,
  `backfilling` and `estimated: true`. The counts are `input`, `output`, `cacheWrite`, `cacheRead` and `messages`;
  `estimatedCostUsd` is an estimate from the price table, `null` when no model of the group has a price, and
  `costComplete` is false when some tokens are of a model without one (they are left out of the cost). `backfilling`
  is true while the usage of sessions that already existed is still being read. Rows are the most tokens first,
  `day` oldest first. A bad `groupBy` or a malformed filter is `invalid-request`.
- `GET /api/sessions/:sessionId/usage` answers one session: `total`, `models` (the rows by model), and `complete`,
  false while its earlier lines are not read yet; an unknown session is `session-not-found`.
- `GET /api/usage/prices` answers `defaults`, the USD prices per million tokens (`input`, `output`, `cacheWrite`,
  `cacheRead`) built into the core, and `overrides`, those the host set, both keyed by model ID. A price applies to
  the model with that ID and to its dated variants (`<id>-YYYYMMDD`); an override wins over a default. `PUT`
  replaces all overrides (at most 500; more is `too-large`). Costs are computed from the prices when asked, so a
  new price applies to past usage at once.
- Budgets cap what a scope spends in a period. `GET /api/budgets` answers `budgets` and `backfilling` (true while
  the usage of earlier sessions is still being read, so spend may grow). A budget (`BudgetSchema`) has `name` (at
  most 80 characters), `scope` (`{ kind, value }`: `global` with an empty `value`, `project` with a project path,
  `device` with a device ID; a scope with the wrong kind of `value` is `invalid-request`), `period` (`day`,
  `week` or `month`, computed at `utcOffset`, whole minutes east of UTC from -840 to 840, 0 by default; a week
  starts on Monday), `unit` (`cost`, estimated US dollars, or `tokens`, all four token kinds summed; a token
  `amount` is a whole number), `amount` (above 0), `alertPercent` (1 to 100, 80 by default) and `onLimit` (`warn`
  by default, or `pause`). Each also carries `periodStart` and `periodEnd` (Unix ms, `[start, end)`), `spent`
  in `unit`, `percent` (`spent` over `amount`, not capped), `state` (`ok`, `alert` from `alertPercent`, `over` from
  100 percent, `allowed` when over but the pause was lifted), `paused` (new prompts are refused now) and
  `costComplete` (false for `cost` when usage of a model without a price is in the scope and period: only priced
  usage is counted, so `spent` is a lower bound). `POST` creates a budget from `BudgetBodySchema`, `PUT` replaces
  its definition and `DELETE` removes it and answers the list; at most 100 budgets, more is `too-large`. Changing
  the scope, period, unit, amount, alert percentage or offset starts the budget afresh: its notifications can be
  sent again and a lifted pause ends. `POST /api/budgets/:id/allow` lifts the pause of a `pause` budget until its
  period ends (it is stored, so a restart keeps it, and it expires by itself); on a `warn` budget it is
  `invalid-request` with `reason: "onLimit"`. `DELETE /api/budgets/:id/allow` takes the lift back, so the pause
  applies again at once; it answers the budget, also when nothing was lifted, and is `invalid-request` with
  `reason: "onLimit"` on a `warn` budget.
- Session templates (docs/spec.md#session-templates) are kept by the host: at most 100, a unique `name` of 1 to
  80 characters, a `text` of at most 8000 characters and the parts of a new session request. `TemplateSchema` adds
  `placeholders` (name and default, in the order of first use). A `text` whose placeholders are wrong is
  `invalid-request` with `reason: "text:<problem>:<offset>"` (`syntax` or `too-many`, the offset 0-based); a taken
  name is `reason: "name"`; the 101st template is `too-large`. `POST /api/templates/:id/start` renders the text and
  starts a session with the rules and the response of `POST /api/sessions`, including the `promptId` repeat; its
  `invalid-request` carries the `promptId` and a `reason`: `values:missing:<names>`, `values:unknown:<names>`,
  `text` (empty once filled), `deviceId` or `projectPath` (neither the request nor the template has one), or the
  reason of a refused session request. `GET /api/templates/export` answers the file (`v: 1`, machines by name);
  `POST /api/templates/import` takes `{ file, machines?, dryRun? }`, gives a taken name a suffix (` (2)`), matches
  `device` by name or by `machines`, stores all entries or none, and answers `items` (and `templates`, empty on a
  dry run). A bad placeholder in an entry is `reason: "file.<index>.text:<problem>:<offset>"`, an ID in `machines`
  that is no machine `reason: "machines"`, and more than 100 templates in all `too-large`. Every change is a
  `config.changed` audit event that never holds the text.
- Permission rules (docs/spec.md#permission-rules) are kept by the host: the eight built-in rules of ADR 0029
  (`default.*`, global, listed first) and at most 500 the user made, in the order they were made. `PermissionRuleEntrySchema`
  is the rule as the agent reads it (`id`, `tool`, `match`, `action`, `scope`, `note`, `enabled`, `builtin`) plus
  where it applies (`deviceId` and `projectPath` for `project`, `sessionId` for `session`) and `createdAt` and
  `updatedAt`; unknown fields are refused. `GET` narrows the rules of a scope by `deviceId`, `projectPath` and
  `sessionId`, and answers a `revision` when `deviceId` or `sessionId` names a machine: the count of changes that
  reached every machine plus those that reached that one, so it only grows. A scope that does not fit its targets is
  `invalid-request` with `reason: "scope"`, a machine that is not known `reason: "deviceId"`, a session that is not
  known `reason: "sessionId"`, and a tool or matcher the agent would refuse `reason: "rule: <problem>"`; the 501st
  rule is `too-large`. `GET` also answers `local`: the deny rules the agents announced from their own configuration
  (`permission_deny`, `LocalPermissionRuleSchema`: `action` `deny`, `scope` `agent`), one `{ deviceId, rules }` per
  machine that has any, only the machine named by `deviceId` or owning `sessionId` when one is given; they are
  read-only and are not among `rules`, and no route creates, changes or deletes one. A built-in is neither edited nor deleted (`reason: "builtin"`); `PUT` takes
  `{ enabled, confirm? }` for it, and turning one off needs `confirm` equal to its ID (`reason: "confirm"`). `PUT` of
  a rule of the user with that short body is `reason: "body"`. `DELETE` answers the list. Session tokens and device
  tokens are `unauthorized` here; a session's rules go with the session. Every create, update, delete and
  built-in switch is a `config.changed` audit event (`area: "permission-rules"`) that names the changed fields and
  never holds the match patterns or the note; a handoff's copy is one such event with `action: "copied"` (and one
  with `action: "removed"` when the handoff fails afterwards) naming `deviceId`, `fromDeviceId`, `sessionId`,
  `count` and `byAction`. A project rule applies also in the worktrees Hoplet made from that clone on that machine
  ([ADR 0037](adr/0037-worktrees-share-their-clones-project-rules-and-a-handoff-can-copy-them.md)); the calls the rules decide are `permission.auto` events
  (`GET /api/audit/events?type=permission.`). `GET /api/permission-rules/machines` answers the `latest` revision and,
  per machine, `revision` (the one the core holds for it), `applied` and `appliedAt` (what it last confirmed with
  `device.rules.applied`, `null` until it does; kept in memory), `pushedAt`, `retrying` (the core is sending the
  rules again) and `online`. `POST .../:deviceId/push` sends the rules to that machine again and answers the same
  list; an unknown device is `not-found`, an offline one `owner-offline` (409).
- A `pause` budget that is over its amount and not lifted refuses new work in its scope with `over-budget` (409,
  with `budget: { id, name }` naming it): `POST /api/sessions/:sessionId/prompt`, a new session
  (`POST /api/sessions`, `POST /api/sessions/:sessionId/permission/fresh`) in its project or on its machine. A
  queued message is not refused at `POST /queue`; the auto-send pauses the queue (`paused: true`, reason
  `over-budget`) instead of retrying, and `POST /queue/resume` ends the pause once the budget allows. A turn that
  is running is never interrupted, and steering it and a handoff are not gated.
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
  `invalid-request` with `reason: config`. `POST /api/sessions` also takes optional `worktree: { branch? }`, which
  starts the session in a new git worktree of `projectPath` ([New session](spec.md#new-session)); the result then
  carries `worktree: { path, branch }`. A refusal is `invalid-request` with a `reason` of `worktree-branch-invalid`,
  `worktree-not-a-git-repository`, `worktree-no-commit`, `worktree-branch-exists` or `worktree-path-exists`; a body
  that fails the schema is `invalid-request` without one. The `create` capability and the allow-list of a token apply
  to `projectPath`. The runtime state (`GET /api/sessions`, `session.runtime`) reports the
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
- `/api/hooks` are the lifecycle webhooks (ADR 0031). They take the host token only: a hook is a standing
  instruction to send events about the user's sessions to an address, so no ticket, action-link token or browser
  session can change one. A hook has a `label`, a `url`, a non-empty `events` list (each of `session.started`,
  `turn.completed`, `session.ended`, `permission.requested`, `permission.resolved`, `session.error`,
  `handoff.started`, `handoff.completed`, at most once), a `scope` (`{ "kind": "all" }` or one project of one
  machine), `summary` (adds the opt-in fields to `turn.completed`, `permission.requested` and `session.error`)
  and `enabled`. `POST` and `POST .../rotate-secret` return the signing secret (`whsec_` and 43 characters)
  once; no other route returns it. After a rotation the old secret signs alongside the new one for 24 hours. A
  listed hook shows its `url` without the query string, a `status` of `active`, `disabled` or `needs-secret`
  (the secret file lost it), the number of `pending` deliveries and its `lastDelivery`. A URL must be `https`
  (or `http` with `HOPLET_HOOK_ALLOW_PRIVATE`) and must not name a blocked address; otherwise the route answers
  `invalid-request` with a `reason` (`invalid-url`, `scheme`, `credentials`, `http-needs-allow-private`,
  `unresolvable` or `blocked-address`). At most 20 hooks (`too-large` beyond). `GET .../deliveries` lists the
  last 100 deliveries, newest first; `POST .../test` sends one `hook.test` event now, without retry, also for a
  disabled hook, and returns the hook with the delivery.
  `GET /api/hooks` also lists the script hooks of the machines (`kind: "script"`, `ScriptHookInfoSchema`) that
  agents declared in their own `config.json` (ADR 0031). A script hook has the `id` `script:<deviceId>:<slug>`, shows its
  `machine` (with `online`), `command`, `events`, `projects`, `timeoutMs`, the names (not the values) in `env`, the
  `runs`, `failed` and `dropped` counts since the agent started, `enabled` and a `status` of `active` or `disabled`.
  Only `enabled` can be changed (`PATCH` with anything else, and `DELETE` or `POST .../rotate-secret`, answer
  `invalid-request` with the `reason` `script-managed`). `POST .../test` sends a `hook.test` event to the machine
  and returns the delivery still `pending`; the delivery settles when the agent reports: `delivered`, `failed`
  with `exit-<code>`, `timeout` or `spawn-failed`, `dropped` with `offline` (the machine is not connected),
  `dropped` (the agent did not take the event), or `failed` with `connection-lost` or `interrupted`. A delivery
  of a script hook carries `run` (`exitCode`, `durationMs` and the first 1 KiB of `stderr`, which is not redacted).
  Each event is a JSON `POST` of the envelope `HookEnvelopeSchema` with `Hoplet-Event`, `User-Agent: Hoplet-Hook/1`
  and `Hoplet-Signature: t=<unix seconds>,v1=<hex>`, the HMAC-SHA-256 of `<t>.<body>` under the secret (one `v1`
  per valid secret, newest first). A timeout, a network error, a 408, a 429 and a 5xx are retried after 30
  seconds, 2 minutes, 10 minutes, 30 minutes and then every 2 hours until 24 hours after the event; any other
  answer fails the delivery at once. The core follows no redirect and reads nothing of the answer but its status.
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
- A handoff request with `copyProjectRules: true` copies the source project's rules to the target's clone
  ([ADR 0037](adr/0037-worktrees-share-their-clones-project-rules-and-a-handoff-can-copy-them.md)) in a `rules`
  step after `verify`: `handoff.progress` reports `rules` with `copied`, and the result carries `rulesCopied`. The
  target must confirm the rules with `device.rules.applied`; a refusal fails the handoff with `rejected` and
  `reason: "rules-refused"`, a copy past 500 rules with `rejected` and `reason: "rules-limit"`, a disconnect with
  `target-offline` and no answer with `timeout`, and the copies are deleted again. A hand-off request a session
  made (`POST /api/handoff-requests`) never copies. `GET /api/sessions/:sessionId/handoff/rules?targetDeviceId=&targetPath=`
  previews the copy for the target's clone `targetPath` (the clone the target would use, `targetPath` or
  `cloneInto`): `sourceDeviceId`, `sourcePath` (the project the rules are taken from, `null` when the session has
  none) and `rules`, each with `state` (`copy`: the target lacks it and the handoff would add it, with its paths moved
  onto `targetPath`; `same`: the target has it; `target`: the target's own rule the source does not have), `action`,
  `tool`, `match` and `note`. An unknown target is `not-found`.
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

The query string is text, so the schemas that read numbers or check a search string are not in the JSON
Schema file ([JSON Schema](#json-schema)). Their parameters:

| Route | Parameter | Meaning |
|---|---|---|
| `GET /api/sessions/:sessionId/lines` | `start` | Integer, default `-100`; a negative value counts from the end of the session |
| | `limit` | Integer, default `100`, kept within 1 to 500 |
| `GET /api/sessions/:sessionId/search` | `q` | The search string, required; one of only whitespace is `invalid-request` |
| | `start` | Integer, default `0`, at least `0`: the hit to start from |
| | `limit` | Integer, default `50`, kept within 1 to 200 |
| `POST /api/sessions/:sessionId/stop` | `cascade` | `true` also stops the live sessions below it ([Child sessions](#child-sessions)); the body stays empty |
| `GET /api/search` | `q` | As above |
| | `start` | Integer, default `0`, at least `0` |
| | `limit` | Integer, default `30`, kept within 1 to 100; the response says `exhausted: false` when more may exist |
| `GET /api/audit/events` | `session`, `device`, `type`, `q` | Filters: the session ID, the machine ID, a type or a type prefix ending in `.`, and text searched in the summary |
| | `actor` | `<kind>:<id>` (or `<kind>`) of the actor |
| | `from`, `to` | Integers, milliseconds since the epoch, `from` inclusive and `to` exclusive |
| | `order`, `after` | `desc` (the default, newest first) or `asc`; `after` is the `seq` the page before ended at (its `next`) |
| | `limit` | Integer, default `100`, kept within 1 to 500 |
| `GET /api/audit/export` | the filters above, `format` | `jsonl` (the default) or `csv`; always oldest first, with no paging |

A value that is not a number reads as the default. The other query parameters are in their schemas:
`GET /api/sessions/:sessionId/workspace/file` takes `path` and `at` (`worktree`, the default, or `head`), the
`GET /api/sessions/:sessionId/workspace/diff` takes `path`, `against` (`head`, the default, or `base`), `cursor` and
`limit` (see docs/spec.md#workspace), `GET /api/sessions/:sessionId/snapshot/diff` takes `path`,
`GET /api/devices/:deviceId/clone` takes `origin`, `GET /api/sessions/:sessionId/export` takes the parameters in
[Session export](#session-export), and `GET /api/devices/:deviceId/dirs`
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

### Session export

`GET /api/sessions/:sessionId/export` is the one read that covers a whole session
([ADR 0040](adr/0040-a-session-exports-as-one-streamed-markdown-file.md)): the core writes the session as one
Markdown file and streams it (`text/markdown; charset=utf-8`, `Content-Disposition: attachment` with a file name
made from the title, `Cache-Control: no-store`). Only the host token may call it. At most two exports run at once;
a third answers `rate-limited`. Each call, a preview included, is recorded as `session.exported` before the file
starts. The query:

| Parameter | Meaning |
|---|---|
| `conversation` | `all` (the default: every turn with its tool calls, long output elided), `replies` (the user's messages and the last reply of each turn) or `none` |
| `summary` | Text written under `## Summary` as given, at most 3 KiB; Hoplet writes no summary of its own |
| `changes` | `true` adds the net diff against the base, from the owner when it is online and else from the latest snapshot; at most 256 KiB, the files past it named and counted |
| `commands` | `true` adds a table of the commands run, with their turn and exit code (at most 1,000) |
| `machines` | `true` adds the machines each turn ran on and the hand-off points, from the audit log |
| `mask` | The user's own strings to mask, one per line: at most 20, each three characters or more, 1 KiB in all |
| `tz` | The IANA time zone the times are written in; UTC when absent |

The file opens with front matter (`title`, `session`, `project`, `provider`, `machines` when asked, `turns`,
`exported`, `hoplet`), a warning to check it before sharing and a table of the session; then the summary, the
conversation (`### Turn <n> · <machine> · <time>`, tool calls in `<details>`), the commands and the changes.
`turns` is known before the first turn is written, so a client can show progress. Every part is masked as it
is written: a value that looks like a credential, and each of the user's strings, becomes
`[redacted:<kind>]`. Masking is best effort. An export that fails after the file started ends the connection
without completing the response; the client discards what it received.


| Code | HTTP status | Meaning |
|---|---|---|
| `unauthorized` | 401 | The host token or API token is missing, wrong, expired or revoked |
| `invalid-token` | 400 | The action token is malformed, was not signed by this core or does not match any request |
| `forbidden` | 403 | A scoped API token ([API tokens](#api-tokens)) asked for a machine or project outside its allow-list, for a high-risk configuration option, or for a route its abilities do not cover; a session token asked to prompt or interrupt a session in a more permissive mode than its own (`reason`: `target-mode-more-permissive`, `sender-mode-unknown` or `target-mode-unknown`) |
| `rate-limited` | 429 | Too many requests to the action routes, or more than 300 in a minute with one API token, or a session prompted other sessions too often ([Sessions as callers](#sessions-as-callers)), or two session exports are already running ([Session export](#session-export)); retry after `Retry-After` seconds |
| `self` | 400 | A session addressed itself: a session token asked to prompt or interrupt its own session |
| `loop-limit` | 409 | The prompt would close a loop or make a chain of more than three sessions ([Sessions as callers](#sessions-as-callers)) |
| `depth-limit` | 409 | A session started a session more than two levels below the root of its tree ([Child sessions](#child-sessions)) |
| `children-limit` | 409 | A session already has 4 live children, or its tree 8 live sessions below the root |
| `budget-exceeded` | 409 | A session started a session while a `pause` budget covering the project or the machine is used up (`budget` names it), or while its tree has spent its 3,000,000 input and output tokens (no `budget`) |
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
| `over-budget` | 409 | A `pause` budget covering the session's project, machine or everything is over its limit; the answer names it in `budget` |
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
| `stale` | 409 | The file changed on the machine after the proposal's `baseHash`, so the change was not written or undone |
| `unsafe-content` | 422 | The proposed content holds something shaped like a credential |
| `unsupported` | 501 | The session's agent cannot preview or write configuration files |
| `rejected` | 502 | The agent refused or failed the request; `reason` is its diagnostic text |
| `timeout` | 504 | The agent did not answer in time |
| `internal` | 500 | The core failed |
| `audit-unavailable` | 503 | The audit log could not record the action, so the action was not carried out ([ADR 0030](adr/0030-the-audit-log-records-actions-in-a-hash-chain.md)) |

## API tokens

The host token does everything. For a script, the command-line tool or another program, the host issues a scoped
API token ([ADR 0033](adr/0033-a-command-line-tool-an-sdk-and-scoped-tokens.md)) with `POST /api/tokens` and the
program sends it as `Authorization: Bearer hpt_...` like the host token. The token routes
(`GET`, `POST /api/tokens`, `DELETE /api/tokens/:id`) are the host's; a token cannot issue, list or revoke tokens.

`POST /api/tokens` takes a `name`, a `grant` and `expiresInDays` (1 to 365, 90 by default; a token always
expires). The response is the only place the secret (`token`) appears: Hoplet stores a SHA-256 hash, so a lost
secret is replaced by issuing another token. `GET /api/tokens` lists every token with its state (`active`,
`expired`, `revoked`), its grant and when it was last used; `DELETE /api/tokens/:id` revokes one at once (a token
that is already revoked or expired is removed from the list). At most 100 tokens are active.

The grant holds `capabilities` and an `allow` list.

| Ability | Lets the token |
|---|---|
| `read` | Read what it can see: sessions, their lines, blocks and queue, search, machines and their projects, and open a browser WebSocket |
| `send` | Send a prompt or a steer, edit the queue, interrupt and upload an image, in the sessions and machines it can see |
| `create` | Start a session on a machine and project it can see (`POST /api/sessions`), read the templates and start one |
| `answer` | Answer permission requests of the sessions it can see |

The presets are Read only (`read`), May send (`read` and `send`) and May create (`read`, `send` and `create`);
`answer` is a separate switch that no preset includes. An entry of `allow` names a machine (`deviceId`) and,
optionally, project paths (`paths`) on it: without `paths` the whole machine, with them those projects and what
is below them. An empty `allow` list reaches nothing. A path with `..` is refused.

A token sees only what its `allow` list reaches. A session, line, queue or prompt outside it is answered
`session-not-found`, as if it did not exist; lists (sessions, machines, projects, templates, search) leave those
entries out. Starting a session on a machine or project outside the list is answered `forbidden`. A request
for a route outside the token's abilities is answered `forbidden` too. Bypass permissions stay out of reach:
changing a session's configuration (`POST /api/sessions/:sessionId/config`) is the host's, and a `create` token
that asks for a high-risk configuration option in `POST /api/sessions` is answered `forbidden`.

Every action of a token is audited with the actor kind `token` and the token's ID
([audit log](adr/0030-the-audit-log-records-actions-in-a-hash-chain.md)). A refused request of a known token is a
`token.denied` event, at most one a minute per token, route and reason; the reason is `forbidden`, or the code of a
refused session start (`depth-limit`, `children-limit`, `budget-exceeded`). A token that is unknown, expired or revoked is
answered `unauthorized` and not audited. One token may make 300 requests a minute; beyond it the answer is
`rate-limited` with `Retry-After`.

## Child sessions

A child is a session started by another session (#81, [ADR 0035](adr/0035-child-sessions-work-on-parts-of-a-task-in-parallel.md)).
The relation is recorded when it starts and lasts as long as the session: the last session of its creation chain is the
parent, the first the root of the tree. A prompt, from a user or from another session, does not change it. Every
`LiveSessionMetaSchema` carries `parentId` (the parent's ID, or `null`) and `childCount` (how many sessions it started).

`GET /api/sessions/:sessionId/children` returns `{sessionId, descendants, truncated}`: every descendant in pre-order,
each as the session metadata plus `depth` (1 for a child), at most 200. Sessions the caller may not see are left out
and set `truncated`. `POST /api/sessions/:sessionId/stop?cascade=true` stops the session and then the live sessions
below it, and answers `{ok: true, stopped}` with the IDs it stopped; the route stays the host's. Each session it stops below
the first is audited as `session.stopped` with `cascade` naming the session the stop started from.

The limits of a tree are fixed in the core ([Hosting](hosting.md#resources)):

| Limit | Value | Answer |
|---|---|---|
| Depth below the root | 2 | `depth-limit` |
| Live children of one session | 4 | `children-limit` |
| Live sessions in a tree, root excluded | 8 | `children-limit` |
| Starts an hour per session | 12 | `rate-limited` |
| Input plus output tokens of a tree (cache excluded) | 3,000,000 | `budget-exceeded` at a start; the live sessions are stopped |
| Time a child may run | 30 minutes; a session asks for 1 to 120 with `timeLimitMinutes` | The child is stopped |

A `pause` budget that is used up also refuses the start (`budget-exceeded` with `budget`). The tree watcher checks
the time and token limits every 30 seconds: it interrupts then stops the live sessions past a limit, and each one is a
`child.limit` audit event (actor `core`, summary `root` and `limit` `time` or `tokens`). Only a session token may send
`timeLimitMinutes`; the host's is `invalid-request`.

A session reaches its children with four routes, all under its own `:sessionId` (another session's ID, or a
child that is not a direct child, is `session-not-found`; the host and scoped tokens name any session). A child
has a state: `running`, `done` (the runtime reached `ready`), `failed`, `blocked` (it waits for a permission, which only
the user answers), `ended` (stopped, or its machine is gone), `timed-out` (a limit of the tree stopped it) and
`cancelled` (its parent cancelled it).

- `GET /api/sessions/:sessionId/children/status?ids=` returns `{sessionId, children}`, the direct children by
  creation, each `{sessionId, state, elapsedSeconds}`; `ids` (comma separated) narrows it.
- `POST /api/sessions/:sessionId/children/:childId/collect?release=` needs `create` and returns `{sessionId, state,
  summary, summaryTruncated, changes, usage, released}` in at most 8 KiB: `summary` is the last assistant text of the
  child's last turn cut to 6 KiB, `changes` is `{status, branch?, base?, shared?, total?, files?, truncated?}` with at most
  200 files and no content (`status` is `ok`, `unknown` or `owner-offline`), `usage` is the child's own, estimated.
  With `release` (the default; `false` keeps the process) a child that is `done` or `failed` has its process stopped;
  the session and its worktree stay, and it then reads as `ended`. A running or blocked child is never stopped by
  it. Audit event `child.collected`, and `session.stopped` for a release, both with the parent session as actor.
- `GET /api/sessions/:sessionId/children/:childId/diff?path=&cursor=&limit=` returns one page of one file's diff against
  the base of the child's worktree (`WorkspaceDiffSchema`); `limit` is at most 32 KiB, and `next` is the cursor of the
  following page.
- `POST /api/sessions/:sessionId/children/:childId/cancel?subtree=` needs `create`, takes any descendant, interrupts and
  stops it (and with `subtree=true` the live sessions below it) and returns `{ok: true, cancelled}`. The sessions and
  their worktrees stay. Audit events `child.cancelled`, and `session.interrupted` and `session.stopped` for each
  session it stops, all with the parent session as actor.

The MCP tools are `wait_children` (`sessionIds?`, `until`: `all` or `any`, `timeoutSeconds?`: the agent asks the status
route every 2 seconds and returns when the condition holds or after at most 50 seconds with `pending: true`; a
child that is not `running` counts as settled), `collect_child` (`sessionId`, `release?`), `read_child_diff`
(`sessionId`, `path`, `cursor?`; pages of 12 KiB) and `cancel_child` (`sessionId`, `subtree?`). The first and third need
only `read`: they change nothing; the others need `create`, and Claude Code and Codex ask the user before them.

A session started by another session has the fixed report instruction in front of its first prompt: another session
started it, reads its last message as the report, and wants it to end with what it did, did not do and where to look
in under 1,500 characters. Without `worktree` a session starting a session in a git project gets a new worktree;
`worktree: false` shares the directory. The child may start sessions of its own only when its parent's call set
`nested` and the depth limit allows it; it has `send` with `create`, its parent's `readConfig` and `visibility`, and
no hand-off, no configuration changes and no automatic approval.

## Sessions as callers

A running session can call the API for the sessions it may see and direct
([ADR 0034](adr/0034-sessions-view-and-direct-other-sessions-over-mcp.md)). The core mints it a **session token**
(kind `session`, `hpt_...`) when its runtime starts and revokes it when the runtime ends or fails, when the session is
deleted, when it is handed off, and when its machine stays disconnected past the reconnect window; it lives 24 hours
at most. It is not in `GET /api/tokens`, cannot be issued or revoked with the token routes, and never holds `answer`.

The grant of a session token is not stored in the token. The core reads the session's settings on every request
(`GET`/`PUT /api/sessions/:sessionId/mcp`, the host's), so a change applies to the next call and to a call that is
already running:

| Setting | Effect |
|---|---|
| `send` | The `send` ability: prompt, steer and interrupt other sessions |
| `create` | The `create` ability, which includes `send` |
| `requestHandoff` | The `request-handoff` ability, which only a session token holds; it lets the session ask for its own handoff (the host confirms unless auto-approve applies) |
| `autoApproveHandoff`, `autoApproveMachines` | Part of the settings of `requestHandoff` and rejected without it (`invalid-request`): whether a handoff request to one of these machines, which must be known, is approved without the host (stored and shown; the handoff request flow acts on it) |
| `readConfig` | The `read-config` ability, which only a session token holds; on by default. It reads the session's own change requests, its options and its memory, skills and commands ([Configuration reads](#configuration-reads-of-a-session)) |
| `writeConfig` | The `write-config` ability, which only a session token holds; off by default and implies `readConfig`. It lets the session propose changes to its own configuration, which the host approves ([Configuration changes](#configuration-changes-of-a-session)) |
| `visibility` | `project` (sessions of its own machine and project), `machine` (every project of its machine) or `all`; a session outside it is answered `session-not-found`. `read` is always on |

`GET`/`PUT /api/mcp/defaults` hold the defaults a session gets when it begins: a global one and one per project
(`deviceId` and `path` together; `DELETE` removes a project's). A new session takes the project's default, else the
global one, else everything off with `project` visibility. Sessions that already have settings keep them. Changes
are `config.changed` events of the audit log.

The agent receives the token over its own WebSocket and keeps it in memory: it sends `runtime.mcp.token.request`
(`requestId`, `sessionId`) and the core answers `runtime.mcp.token` (`requestId`, `sessionId`, and `token` with
`expiresAt`, or `reason`: `unknown-session`, `not-owner`, `not-allowed`). Both are control-lane messages; the core
answers only the device that owns the session and announced `capabilities.sessionMcp` in `device.hello`. The agent
turns the model's MCP calls (`list_machines`, `list_sessions`, `read_session`, `get_config`, `list_skills`, `read_skill`, `read_memory`, `propose_config_change`, `propose_memory_change`, `propose_skill_change`, `get_change`, `send_prompt`, `interrupt_session`, `start_session`, `wait_children`, `collect_child`, `read_child_diff`, `cancel_child`, `request_handoff`) into the requests below.

A session token reaches only the routes marked for sessions: the list and read of sessions, lines and blocks, the
device list, prompt, queue (`POST /api/sessions/:sessionId/queue`), interrupt, `POST /api/sessions`, `POST /api/handoff-requests` and its own configuration changes (below); every other route answers `forbidden`. A session sees
itself in the list with `self: true` in its metadata. The rules for a prompt or an interrupt from a session:

- It may not address itself: `self`.
- The target's permission mode may not be more permissive than the sender's. The modes are ranked `plan`, then
  `default` and `read-only`, then `acceptEdits` and `workspace-write`, then `auto` and `auto-review`, then
  `bypassPermissions` and `full-access`. A mode that is not in the ranking is refused. Both answer `forbidden`
  with a `reason`.
- Every prompt carries the chain of sessions that led to it; a prompt that a user sends resets the chain. A chain
  that contains the target, or would hold more than three sessions, is `loop-limit`.
- Per source session: 6 prompts a minute and 60 an hour; per target 3 a minute; and at most 2 prompts to a target
  that has not finished a turn since. Beyond a limit the answer is `rate-limited` with `Retry-After`. A prompt that
  was refused does not count.

`send_prompt` (`sessionId`, `text` of up to 8000 characters) is a `POST /api/sessions/:sessionId/queue`: the
message waits for the end of the target's current turn like any queued message and goes out as a prompt of the
session. `interrupt_session` (`sessionId`) is `POST /api/sessions/:sessionId/interrupt`. Both need the `send` ability,
and Claude Code asks the user before each call (the built-in rule allows only the read tools). A refusal reaches the
model as text with the error code, the `reason` where there is one and, for `rate-limited`, the seconds of
`Retry-After`: `forbidden`, `self`, `loop-limit`, `rate-limited` or `session-not-found`.

`start_session` (`prompt`; optional `machine`, `projectPath`, `permissionMode`, `worktree`, `branch`, `title`, `nested`, `timeLimitMinutes`) is a
`POST /api/sessions` and needs the `create` ability. The machine and project default to those of the calling
session, and the reach of the grant (project, machine or all) applies to them. The new session's permission mode may
not be more permissive than the caller's: a named mode above it is `forbidden` with the `reason`
`target-mode-more-permissive`, an unranked one `target-mode-unknown`, and a caller in an unknown mode
`sender-mode-unknown`; without a mode the new session gets the caller's own, or the highest one below it that is not
`risk: "high"`. A `risk: "high"` option is refused as for any token. The new session's chain is the caller's chain
plus the caller (`loop-limit` beyond three sessions), recorded once the device has started it. Per source session at
most 12 starts an hour (`rate-limited` with `Retry-After`) and 4 live children (`children-limit`); the limits of the
whole tree are in [Child sessions](#child-sessions). A start that was refused or failed does not count. The core sends `runtime.start` with the same `source` as a prompt
carries, and the new session's agent puts the fixed line naming the caller before the first prompt. The tool
does not wait: it returns the new session id and the worktree, if any. Claude Code asks the user before each call.
`worktree` is a `{branch?}` object or `false` in `POST /api/sessions`; left out by a session it means a new worktree in a
git project, and `title` sets the session's title. Only a session token may send `nested` and `timeLimitMinutes` (the
host's is `invalid-request`).

`request_handoff` (`machine`, an ID or name from `list_machines`; `reason` of 1 to 500 characters) is a
`POST /api/handoff-requests` `{targetDeviceId, reason}` and needs the `request-handoff` ability (the `requestHandoff`
setting). It creates a request in the state `pending` and returns at once; the tool tells the model to finish its turn.
The refusals are `forbidden` (the setting is off) with the `reason` `session-only` (a host token called) or
`request-open` (the session already has an open request), `not-found` (an unknown machine or one the grant does not
reach, so the session's `visibility` must include the target), `same-device`, `target-offline`, `loop-limit` (the chain of the session would hold more than three
sessions) and `rate-limited` with `Retry-After` (6 requests an hour per session).

A request is `pending`, `approved`, `declined`, `expired`, `done` or `failed`, and has a `decision` once decided:
`user` or `auto`. The web shows it as a card in the session's turn, and the notification of the existing kinds
(`permission`, `tool` `request_handoff`, the target's name as `summary`, no link) goes to the channels; the
decision is the host's, `POST /api/handoff-requests/:requestId/approve` or `.../decline` (`request-closed` when it is no
longer `pending`, `not-found` when unknown), never an action link. `GET /api/sessions/:sessionId/handoff-requests`
lists a session's requests, newest first. A request is approved automatically only when `autoApproveHandoff` is on, the
target is in `autoApproveMachines` and the session has had fewer than 3 automatic approvals in the last hour; beyond it
the request asks the user. An approved request runs the hand-off of `POST /api/sessions/:sessionId/handoff` (the
default clone of the target) when the session is `ready`, after the queue has drained, so it waits for the end of the
turn; it is then `done`, or `failed` with `error` and `errorReason` (for example `no-clone`, `clone-failed`). A request
nobody answers in 24 hours, or whose session ended, failed or moved, is `expired`. The audit events are
`handoff.requested` (actor `session`) and `handoff.decided` (actor: the host for `user`, `core` for `auto`).
The new wire message `handoff.request` is core to browser only, so the protocol version stays 19.

### Configuration reads of a session

A session reads its own configuration ([ADR 0036](adr/0036-sessions-change-their-own-configuration-over-mcp.md)); the
MCP tools `get_config`, `list_skills`, `read_skill` and `read_memory` are these routes. They need `read-config`; a
session token reads only its own session (another ID is `not-found`), a host token any session. `GET
/api/sessions/:sessionId/config` answers from what the runtime reports (`provider`, `state`, and each option with its
`currentValue` and, for a select, its `values`) and asks no agent. `GET /api/sessions/:sessionId/config-files` lists
the skills and commands (`entries`, at most 200, each `kind`, `name` and a one-line `description`) and
`GET /api/sessions/:sessionId/config-files/:kind` with `kind` `memory`, `skill` or `command` reads one file; `scope` is
`project` (the session's working directory, the default) or `user` (the user's own directory, read only). A `skill` or
`command` needs `name`, matching `[a-z0-9][a-z0-9-]{0,63}`; `memory` takes none. The agent maps the kind and name to a
path inside the scope and never takes a path from the caller. `offset` is a byte offset.

The answer is a `ConfigFileRead`: `exists` (a missing file is `exists` false, not an error), `size` and `hash`
(SHA-256 of the whole file), `text` from byte `offset` and `nextOffset` while more follows. A page is at most 32 KiB
and ends on a character boundary; a file of more than 1 MiB, a file with a NUL byte or invalid UTF-8, and a path that
leaves the scope through a symbolic link are refused (`rejected`, with a `reason`). Only the `claude` provider answers;
another provider is `unsupported`. The user scope of Claude Code is `~/.claude`; `CLAUDE_CONFIG_DIR` is not honored.
The core asks the owner with `session.config.read` and waits as long as for a workspace read (`timeout`,
`owner-offline`, `session-not-found` as there).

### Configuration changes of a session

A session proposes changes to its own configuration, and the host approves them
([ADR 0036](adr/0036-sessions-change-their-own-configuration-over-mcp.md)). `POST /api/sessions/:sessionId/config-changes`
needs the `write-config` ability (the `writeConfig` setting) and only the session itself may call it for its own
`sessionId`: a host token is `forbidden` with the `reason` `session-only`, another session's ID is `not-found`, and a
setting that is off is `forbidden`. The tokens of `POST /api/tokens` cannot hold `read-config` or `write-config`
(`invalid-request`). `GET /api/sessions/:sessionId/config-changes` and `GET /api/config-changes/:changeId` need
`read-config`; a session token reads only its own session's requests, the host reads all.

The body is `{reason, objects}`: `reason` of 1 to 500 characters and 1 to 5 objects, 64 KiB of content together. An
object is `{kind, name, content, baseHash}`. The kinds `memory` (`name` `project`: the agent resolves it to the project's
`CLAUDE.md`, `.claude/CLAUDE.md` or `AGENTS.md`, whichever Claude Code reads), `skill` and `command` are files in the
working directory; `content` is the whole new file, or null to delete a `skill` or `command`;
`baseHash` is the hash of the file the proposal was made against, empty or absent for a new file. The kind `config` has
`name` `model` or `effort` and the value as `content`, and ignores `baseHash`. A `name` that does not fit its kind, or an
object twice, is `invalid-request`. A `config` value must be a value the driver declares for a select option: an
unknown option or value is `forbidden` with the `reason` `unknown-option` or `unknown-value`, a `risk: "high"`
choice `high-risk`, and an option of the category `mode` `mode` (it is not changed this way); a session the agent
does not run (`external`) cannot change an option. Content shaped like a credential is `unsafe-content`. The core asks the owner's agent to preview the files and answers with the change and its diff:
`owner-offline` when the agent is not connected, `timeout` when it does not answer in time, `unsupported` when it cannot
write this object (a command of a Codex session; a skill or command while the vendor version is not one the driver
was tested with; an `AGENTS.md` that a `CLAUDE.md` which does not import it shadows; a Codex project directory with an
`AGENTS.override.md`), `stale` when `baseHash` differs from the file now,
`too-large` when the file now is over 64 KiB, and `rejected` for the agent's other refusals, with its code and reason in
`reason` (`not-found`: deleting a file that is not there; `invalid`: a symbolic link or a non-regular file on the way).
The agent's content check answers `unsafe-content` with the rule as `reason`: `front-matter-key` (a skill or command
may set only `name` and `description`), `front-matter`, `front-matter-unclosed`, `byte-order-mark`, `shell-command`
(any `` !` `` and a fence opened with `!`), and for a new `@` import `import-outside` (outside the working directory),
`import-denied` (a file of the floor) and `import-symlink`. An object the agent would create directories for carries
`newDirectories`, relative to the working directory. A session has one open request: another proposal while one is
`pending` or `approved` is `forbidden` with the `reason` `request-open`.

Each object of a change carries `takesEffect`, when it reaches the session once the change is applied: `next-turn`
for `model` and `effort`, which the agent switches before the session's next turn, and `next-start` for the memory
file, a skill and a command, which the vendor reads when its process starts (the running conversation keeps the
instructions it loaded). `now` is for a skill or command of a vendor that rescans them while it runs; no driver
reports that, so no change says `now` yet.

A change is `pending`, `approved`, `applied`, `declined`, `expired`, `failed` or `undone`. The host decides with
`POST /api/config-changes/:changeId/approve` or `.../decline` (`request-closed` when it is no longer `pending`). An
approval sets the options (`runtime.config`), then writes the files, all or nothing; when the files fail, the options
are put back and the change is `failed` with `error` (`stale`, `unsafe-content`, `unsupported`, `timeout`, or `failed` with
the agent's code and reason in `errorReason`). With the owner offline an approved change waits and is applied when it
reconnects. A request nobody answers in 24 hours, or whose session ended, is `expired`. `POST /api/config-changes/:changeId/undo`
restores `previous`, files first and then the options: `stale` when a file changed since, and any failure other than
`owner-offline` leaves the change `applied` with `error` and `errorReason`. `previous` is kept for 30 days, after which
the change cannot be undone (`request-closed`). The audit events are `config.change.requested` (actor `session`),
`config.change.decided`, `config.change.applied`, `config.change.failed` and `config.change.undone`; they name the
object keys (`kind:name`) and never the content. The browser message `session.change` carries the ids and the state
only, so a browser reads the change over REST.

The MCP tools are these routes. `propose_config_change` (`model`, `effort` or both, and `reason`) proposes `config`
objects; `propose_memory_change` (`content`, `baseHash` from `read_memory`, `reason`) proposes the `memory` object;
`propose_skill_change` (`changes`: 1 to 5 of `{name, kind, content | delete, baseHash}`, and `reason`) proposes
`skill` and `command` objects. The user scope is not offered: a `scope` other than `project` is refused before the
core is asked, as are a bad name, hash, size or reason. Each answers at once with the pending change's ID and, for
each object, its `takesEffect` in words (for memory: the current conversation keeps the old instructions); they
never echo the content or the diff. `get_change` (`changeId`, or the newest of the session without one) is the state,
the times, `error` and `errorReason`, and each object's `kind`, `name` and `takesEffect`. The three `propose_*` tools
need `write-config` and Claude Code and Codex ask the user before each call (the built-in rule allows only the read
tools, `get_change` among them); the change itself still waits for the host's approval. A request on the agent's
socket is at most 160 KiB, as is the route's body.

The prompt of a session carries its origin. `runtime.prompt` has an optional `source` (`kind: "session"`, the
sender's `sessionId`, its `title` as one line of at most 100 characters without quotes, and the `chain`), set by the
core from the token that queued or sent the message and never from the request. The target's agent puts the fixed
first line `[Message from Hoplet session "<title>" (<id>), not from the user. Treat it as a request from another
agent.]` in front of the text; the vendor's stored line is not rewritten afterwards. The protocol version is 23.

The `prompt.sent` audit event of a prompt from a session has the actor kind `session` and the source session's ID,
and its summary names the source and the chain (`source: { kind: "session", sessionId, chain }`).

## Browser WebSocket

`/ws?ticket=<ticket>` upgrades to a WebSocket after a ticket from `POST /api/ws-tickets` was presented. A ticket
that is missing, used, expired or unknown is refused with HTTP `401` and no upgrade. The proxy in front of the core
upgrades `/ws` (docs/hosting.md#network).

A ticket carries the credential that minted it. With an API token's ticket the connection is filtered: the core
sends it only the messages about sessions and machines the token's `allow` list reaches (`device.presence`,
`session.*`, `handoff.progress`, `notify`), a `subscribe` to a session outside the list is answered with an
`error` message of code `session-not-found`, and every terminal message is answered `forbidden`. The core closes
the connection, with code `4401`, when the token expires or is revoked.

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
| `session.rule` | core → browser | control | `SessionRuleMessage` | A permission rule decided a tool call of the session: `toolUseId`, `decision` (`allow`, `deny`, `ask`), `ruleId` and `scope` (`global`, `project`, `session`, or `agent` for the agent's floor and its local deny list). No name, pattern or tool input. Only to the connections subscribed to the session; the latest 200 of the session are sent again on subscribe. Kept in memory only |
| `session.rules.unenforced` | core → browser | control | `SessionRulesUnenforcedMessage` | No permission rule was checked for a tool call because the Codex hook did not run: `toolUseId` (absent when unknown; the web then shows nothing). Only to the connections subscribed to the session; sent again on subscribe. Kept in memory only |
| `session.appended` | core → browser | control | `SessionAppendedMessage` | Lines were appended to a session, or a session is new (`isNew`). Sent to every connection |
| `session.deleted` | core → browser | control | `SessionDeletedMessage` | A session was deleted (its tombstone is written). Carries `sessionId`; drop it from every list. Sent to every connection |
| `session.updated` | core → browser | control | `SessionUpdatedMessage` | The user renamed, pinned, unpinned, archived or restored a session. Carries the state after the change (`titleOverride`, `pinnedAt`, `archivedAt`; absent means cleared). Sent to every connection |
| `device.presence` | core → browser | control | `DevicePresenceMessage` | A device connected or disconnected. Sent to every connection, followed by `session.runtime` for each session the connection is subscribed to |
| `session.runtime` | core → browser | control | `SessionRuntimeMessage` | The runtime state of a session and its capabilities. Sent to every connection |
| `session.queue` | core → browser | control | `SessionQueueMessage` | The queue of a session changed; fetch `GET /api/sessions/:sessionId/queue`. Sent to every connection |
| `session.commands` | core → browser | control | `SessionCommandsMessage` | The commands and skills of a session changed; fetch `GET /api/sessions/:sessionId/commands`. Sent to every connection |
| `session.output` | core → browser | control | `SessionOutputMessage` | Streamed content of the running turn: text, thinking, tool calls and results. Only to the connections subscribed to the session. Transient: the session's lines are the record |
| `session.permission` | core → browser | control | `SessionPermissionMessage` | A tool wants permission; answer with `POST /api/sessions/:sessionId/permission`. `planExit` (`plan`, `planFile`, `option`, `modes`, `default`, `freshContext`) marks a request to leave a plan; `freshContext` says approval can start a new session. Sent to every connection |
| `handoff.progress` | core → browser | control | `HandoffProgressMessage` | A step of a handoff: `sent` of `total` bytes during `transfer`, the rules `copied` during `rules`, the new `worktree` and `branch` on `done`, the `reason` on `failed`. Sent to every connection |
| `session.change` | core → browser | control | `SessionChangeMessage` | A change request of a session was made or changed state (`sessionId`, `changeId`, `state`); read the change with `GET /api/config-changes/:changeId`. Only to the connections whose token sees the session |
| `handoff.request` | core → browser | control | `HandoffRequestMessage` | A hand-off request of a session was made or changed state (`request`: `requestId`, `sessionId`, `targetDeviceId`, `reason`, `state`, `decision`, `createdAt`, `expiresAt`, `decidedAt`, and `error` with `errorReason` when it failed). Only to the connections whose token sees the session. The web shows it as a card in the turn |
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
host name); a client builds `http://<previewHost>:<port>` from it. `GET /api/devices` also lists `terminal` and `sessionMcp` (the
announced capabilities) and `previewHost` for each connected device.

## Notifications

The core notifies on Web Push, on webhooks and, later, on connected native clients (ADR 0021). The events are
`permission`, `plan`, `turn-done` (only when no browser has the session open), `failure` (a session that
failed to start or a handoff that failed) and `budget` (a budget reached its alert percentage or its limit; once
per budget, period and threshold). The payload (`NotifyPayloadSchema`) carries the kind, the session,
the project's name, the session title and, for a permission request, the tool and its command or path redacted
and cut to 80 characters. A `budget` payload carries `budget: { id, name, threshold, percent, pause }` (`threshold` is `alert` or `limit`,
`pause` whether the limit pauses new prompts) and no session. It never carries the prompt, file contents or a diff. A webhook receives it as a JSON
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
| `AuditEventsQuerySchema` | `GET /api/audit/events` | It coerces numbers and splits `actor` into kind and ID (a `transform`) |
| `AuditExportQuerySchema` | `GET /api/audit/export` | The same as `AuditEventsQuerySchema` |
| `SessionExportQuerySchema` | `GET /api/sessions/:sessionId/export` | It bounds `summary` and `mask` in UTF-8 bytes and splits `mask` into lines (a `transform`) |

Their parameters are in [Query parameters](#query-parameters). The messages between the agents and the core
(`/ws/agent`) are not part of this contract.
