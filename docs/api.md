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

Auth is `host` for every route: the host token as `Authorization: Bearer`. There is no unauthenticated route
under `/api/`. The Query and Body columns name the schema of the query string and of the JSON body; the
Response column names the schema of the success body and its status when it is not `200`.

| Method | Path | Auth | Lane | Query | Body | Response | Errors |
|---|---|---|---|---|---|---|---|
| POST | `/api/ws-tickets` | host | control | — | — | `WsTicketResponseSchema` | — |
| GET | `/api/health` | host | control | — | — | `HealthResponseSchema` | — |
| GET | `/api/sessions` | host | control | — | — | `SessionListResponseSchema` | — |
| POST | `/api/sessions` | host | control | — | `NewSessionRequestSchema` | `NewSessionResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `target-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId` | host | control | — | — | `LiveSessionMetaSchema` | `session-not-found` |
| GET | `/api/sessions/:sessionId/lines` | host | bulk | `LinesQuerySchema` | — | `LinesPageSchema` | `session-not-found`, `busy` |
| GET | `/api/sessions/:sessionId/blocks/:line/:block` | host | bulk | — | — | `FullBlockSchema` | `invalid-request`, `session-not-found`, `not-found`, `busy` |
| GET | `/api/sessions/:sessionId/search` | host | bulk | `SessionSearchQuerySchema` | — | `SearchResultSchema` | `invalid-request`, `session-not-found`, `busy` |
| GET | `/api/search` | host | bulk | `GlobalSearchQuerySchema` | — | `GlobalSearchResultSchema` | `invalid-request`, `busy` |
| POST | `/api/sessions/:sessionId/prompt` | host | control | — | `PromptRequestSchema` | `PromptResultSchema`, 202 | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `external`, `starting`, `busy`, `timeout`, `rejected` |
| POST | `/api/sessions/:sessionId/permission` | host | control | — | `PermissionRequestSchema` | `RuntimeCommandResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `owner-offline`, `request-closed` |
| POST | `/api/sessions/:sessionId/interrupt` | host | control | — | — | `RuntimeCommandResultSchema` | `session-not-found`, `owner-offline`, `not-running` |
| POST | `/api/sessions/:sessionId/stop` | host | control | — | — | `RuntimeCommandResultSchema` | `session-not-found`, `owner-offline`, `not-running` |
| GET | `/api/sessions/:sessionId/workspace` | host | control | — | — | `WorkspaceListingSchema` | `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/workspace/file` | host | control | `WorkspaceFileQuerySchema` | — | `WorkspaceFileSchema` | `invalid-request`, `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/workspace/diff` | host | control | `WorkspaceDiffQuerySchema` | — | `WorkspaceDiffSchema` | `invalid-request`, `session-not-found`, `owner-offline`, `timeout`, `rejected` |
| GET | `/api/sessions/:sessionId/snapshot/changes` | host | bulk | — | — | `SnapshotChangesSchema` | `session-not-found`, `no-snapshot`, `busy` |
| GET | `/api/sessions/:sessionId/snapshot/diff` | host | bulk | `WorkspaceDiffQuerySchema` | — | `SnapshotDiffSchema` | `invalid-request`, `session-not-found`, `no-snapshot`, `not-found`, `busy` |
| POST | `/api/sessions/:sessionId/handoff` | host | control | — | `HandoffRequestSchema` | `HandoffResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `session-not-found`, `same-device`, `target-offline`, `owner-offline`, `handoff-in-progress`, `busy`, `no-snapshot`, `no-clone`, `session-changed`, `verify-failed`, `cancelled`, `timeout`, `rejected` |
| GET | `/api/devices` | host | control | — | — | `DeviceListResponseSchema` | — |
| POST | `/api/devices` | host | control | — | `DeviceMintRequestSchema` | `DeviceMintResultSchema` | `invalid-json`, `invalid-request`, `too-large` |
| POST | `/api/devices/:deviceId/attachments` | host | bulk | — | `PromptImageSchema` | `AttachmentResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `target-offline`, `timeout` |
| GET | `/api/devices/:deviceId/clone` | host | control | `DeviceCloneQuerySchema` | — | `DeviceCloneResultSchema` | `invalid-request`, `target-offline`, `timeout` |
| GET | `/api/devices/:deviceId/dirs` | host | control | `DeviceDirsQuerySchema` | — | `DeviceDirsResultSchema` | `invalid-request`, `target-offline`, `timeout` |
| GET | `/api/devices/:deviceId/projects` | host | control | — | — | `DeviceProjectsResultSchema` | `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/projects` | host | control | — | `DeviceProjectsAddRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/projects/remove` | host | control | — | `DeviceProjectsRemoveRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/sessions/import` | host | control | — | `DeviceSessionsImportRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |
| POST | `/api/devices/:deviceId/sessions/remove` | host | control | — | `DeviceSessionsRemoveRequestSchema` | `DeviceProjectsResultSchema` | `invalid-json`, `invalid-request`, `too-large`, `owner-offline`, `timeout`, `rejected`, `cancelled` |

Behavior a client relies on:

- `POST /api/sessions` answers when the agent has created and imported the session. Its error body carries the
  `promptId` that was sent, and a `reason` where the agent gave one.
- `POST /api/sessions/:sessionId/prompt` answers `202` when the prompt reached the owner. A prompt is at most
  8,000 characters (`too-large` beyond). Both prompt routes take optional `attachments`, at most 4 IDs
  returned by `POST /api/devices/:deviceId/attachments` for the session's owner (or the new session's device);
  the driver must declare `images` (`invalid-request` with `reason: images` otherwise). The agent refuses the
  prompt with `attachment-missing` when an ID is unknown or older than an hour, and with `invalid-image` when the
  images are more than 8 MiB together.
- `POST /api/sessions/:sessionId/prompt` takes optional `options` (`model`, `thinking`, `fast`), checked as a
  new session's are (`invalid-request` with `reason` naming the option the driver does not declare); they hold
  from that turn on. The runtime state (`GET /api/sessions`, `session.runtime`) reports what the session runs
  with as `options`, the model absent while it runs on the vendor's default.
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
- `POST /api/sessions/:sessionId/permission` answers a `session.permission` request; the first answer wins.
- `POST /api/sessions/:sessionId/handoff` answers when the handoff is over, naming the new worktree and branch;
  `handoff.progress` reports its steps meanwhile. A failed handoff leaves the owner and the cursor where they were.
  Before one, `GET /api/devices/:deviceId/clone?origin=` with the `origin` of the workspace listing or the snapshot
  changes names the clone the target would use, or `path: null` when the request needs a `targetPath`.
- `GET /api/sessions/:sessionId/lines` returns the stored lines `[start, end)` as conversation entries; a line
  without conversation has no entry, so `entries` can be shorter than the range. Large content blocks are
  elided in a page and read whole with `GET /api/sessions/:sessionId/blocks/:line/:block`; `:line` and `:block`
  are non-negative integers.
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

## Error codes

| Code | HTTP status | Meaning |
|---|---|---|
| `unauthorized` | 401 | The host token is missing or wrong |
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
| `starting` | 409 | The session's process is still starting |
| `external` | 409 | The session's process was not started by Hoplet, so it cannot be controlled |
| `not-running` | 409 | The session has no running process to interrupt or stop |
| `request-closed` | 409 | The permission request was already answered or has closed |
| `cancelled` | 409 | The operation was cancelled before it completed |
| `handoff-in-progress` | 409 | A handoff of this session is already running |
| `no-clone` | 409 | The target has no clone of the session's repository; send `targetPath` |
| `no-snapshot` | 409 | No workspace snapshot is stored for the session |
| `session-changed` | 409 | The session changed while the handoff ran |
| `verify-failed` | 502 | What the target installed did not match what was sent |
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
| `session.appended` | core → browser | control | `SessionAppendedMessage` | Lines were appended to a session, or a session is new (`isNew`). Sent to every connection |
| `device.presence` | core → browser | control | `DevicePresenceMessage` | A device connected or disconnected. Sent to every connection, followed by `session.runtime` for each session the connection is subscribed to |
| `session.runtime` | core → browser | control | `SessionRuntimeMessage` | The runtime state of a session and its capabilities. Sent to every connection |
| `session.output` | core → browser | control | `SessionOutputMessage` | Streamed content of the running turn: text, thinking, tool calls and results. Only to the connections subscribed to the session. Transient: the session's lines are the record |
| `session.permission` | core → browser | control | `SessionPermissionMessage` | A tool wants permission; answer with `POST /api/sessions/:sessionId/permission`. Sent to every connection |
| `handoff.progress` | core → browser | control | `HandoffProgressMessage` | A step of a handoff: `sent` of `total` bytes during `transfer`, the new `worktree` and `branch` on `done`, the `reason` on `failed`. Sent to every connection |
| `error` | core → browser | control | `ErrorMessage` | The core could not take a message from the client; `code` says why |

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
