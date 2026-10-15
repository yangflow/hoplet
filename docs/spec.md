# Hoplet specification

## Decisions

Hoplet is self-hosted remote control and cross-machine handoff for Claude Code sessions, built for sessions of
hundreds of megabytes. An agent on every development machine syncs sessions to a server the user owns; the
web UI on any desktop browser opens a session at once, drives it while Claude Code works, and moves it with its
working tree to another machine.

- **Two goals decide every feature.** A large session opens and is driven remotely without waiting for its
  history; a session moves to another machine and continues there. Anything that serves neither is not built.
- **Vendors plug in behind provider interfaces; Claude Code is the provider implemented.** Only the provider,
  its runtime driver and its core mapping know a vendor's formats; clients render one conversation model
  ([ADR 0012](adr/0012-vendors-plug-in-behind-provider-interfaces.md)). A vendor chooses its control protocol
  and its history source separately: control is a driver of its own or the generic ACP driver; history is the
  ACP log Hoplet writes (remote control of the sessions Hoplet starts), the vendor's own session files (terminal
  sessions appear) or the vendor's store (handoff), and a vendor moves up a level without changing the rest
  ([ADR 0015](adr/0015-control-protocol-and-history-source-are-chosen-separately.md)).
- **The server keeps a verbatim copy of every session**, synced incrementally with the byte offset as the
  cursor ([ADR 0002](adr/0002-the-byte-offset-is-the-sync-cursor.md)). Opening a session never needs the
  owner machine.
- **Reads are projections.** Content blocks larger than 8 KiB are elided on read and fetched on demand
  ([ADR 0007](adr/0007-reads-elide-large-content-blocks.md)).
- **One owner per session.** The device that owns a session is its only writer; ownership moves only by
  handoff.
- **Remote control runs a Hoplet-managed Claude Code process** on the owner, with streaming output,
  permission answers and interruption. Hoplet controls only processes it started
  ([ADR 0008](adr/0008-remote-control-runs-a-hoplet-managed-claude-code-process.md)).
- **Handoff moves a workspace snapshot into the target's own clone**, online or offline
  ([ADR 0009](adr/0009-handoff-moves-a-workspace-snapshot-into-the-target-clone.md)).
- **Two kinds of token.** The host token is the operator; each agent has its own device token
  ([ADR 0005](adr/0005-per-device-tokens.md)).
- **The web UI is for desktop browsers.** Phones and tablets are left to a native client later, which will
  use the same API ([ADR 0011](adr/0011-the-web-ui-is-a-desktop-react-application.md)).
- **Self-hosted, no outside service.** One container image on the user's host; the core and the agent call
  nothing else (docs/hosting.md#network).

### Session identifiers

A session ID is a nonempty ASCII string of letters, digits, underscore and hyphen. Every boundary rejects any
other value before lookup, decoding or writing; no component trims, decodes or normalizes an ID
([ADR 0006](adr/0006-session-identifiers-are-exact-keys.md)).

## Information architecture

The web UI is one window at a desktop viewport of 1024 px wide and more. When no host token is stored, a
token gate asks for it. Then the window shows the Hoplet screen (`design/screens/cockpit.html`), with the
workspace inspector beside the conversation (`design/screens/workspace.html`) and Settings as a second page
(`design/screens/settings.html`).

### Hoplet

Session sidebar and conversation; the workspace inspector (below) opens beside the conversation.

**Session sidebar.** Sessions grouped by device, then by project directory, shown by its name; sessions sit
under their directory, and a directory folds (remembered in the browser), showing its session count
and its most urgent runtime state while folded. A session row shows its provider's mark, the title, and on the right its last
activity or, when its runtime state is not `idle`, a glyph whose colour and shape name the state (its
accessible name says the state in words). A device row folds its directories (remembered in the browser), shows whether its agent is connected and, when it is,
offers Add project on hover: a dialog with a machine select (offline machines disabled; an Add a machine item points to `hoplet devices add`) and a folder browser over the agent's browse root (`browse_root`, the home directory by default): a path bar of clickable segments that becomes a field for a typed path, recent folders (where Claude Code has sessions) at the root, the folders below with a git mark, and the selected path in the footer; the chosen directory is added to the agent's watched projects.
A directory row is one button that folds it, led by a folder glyph that is open while the directory is (only
a machine row has a chevron). Each machine row has view options (an ellipsis button beside Add project,
shown on hover or focus and kept on an offline machine) that set how that machine's sessions are listed and
in which order, remembered in the browser per machine: **By folder** (the default: the machine's directories
with their sessions) or **Flat** (the machine's sessions as one list, each row with a second line naming its
directory, and no directory rows, so Add project stays on the machine row); sorted by **Recent activity**
(the default), **Created** or **Title**. Sessions are never grouped across machines. The sidebar follows the
selected session, however it was selected (a click, the palette, the address): its machine and directory open
and the row scrolls into view, while every other fold stays as the reader left it. Search (⌘K) opens a
command palette: sessions whose title matches, line hits across sessions (selecting one opens the session at
that line) and actions (New session, Settings). New session (⌘N, in the sidebar header and on each directory
row) opens a draft: an empty conversation, shown as the selected row at the top of its directory, whose
composer chooses the machine (online ones) and the project directory (the machine's watched projects, and Add
project), with the provider and the options its runtime driver declares (model, thinking, fast); a directory
row's button presets both, and ⌘N starts where the open session runs. The draft's first prompt starts the session, which then replaces the draft; a
refused start keeps the draft and its text and says why. Selecting another session discards the draft. The sidebar foot shows whether this
window is connected to the core and opens Settings. The sidebar folds away.

**Conversation.** The top bar holds the session's title and the workspace toggle; where the session lives is
the sidebar's to show. The selected session from its last 80 lines; earlier pages load as the reader scrolls up.
The conversation is a sequence of turns: a turn begins with a user prompt, which is its header with the
time, and holds the assistant text, thinking, tool calls, tool results and file edits that follow. A
finished turn's header summarizes it (files, tools, duration). Consecutive tool activity folds into one row.
Elided blocks show a preview and their size, and open in place on request. While the view is at the bottom,
new lines and runtime output appear live; otherwise a pill counts new messages and jumps to the end. The
composer at the foot sends a prompt, typed or spoken; speech is transcribed on the user's own machines and
no audio leaves them (#17). Its first row says where the session runs, machine / directory / git branch (the
branch as the agent reports it, #10), read only once the session exists; its toolbar holds attach, the model
with Thinking and Fast in one menu (changing them for a running session needs its driver to declare it, #10),
dictate and send. The text grows to 8 lines, then scrolls; dragging the composer's top edge sets a height the
browser remembers, at most what leaves the conversation 240 px, and a double-click on the edge resets it.
Images pasted or attached go with the prompt (#10). Actions whose icon carries the intent are icon buttons with an accessible name and a
tooltip; the answers to a permission request and the handoff confirmation stay worded.

The runtime state is always shown, with only the controls its state allows. A state of the current turn
(`starting`, `running`, `waiting-permission`, and `failed` when a turn failed) appears on that turn's header;
a state of the session appears under the composer when it blocks or changes what the user can do
(`offline`, `external`, `ready`, and `failed` when start failed); `idle` shows nothing:

| State | Meaning | Shown on | Controls |
|---|---|---|---|
| `offline` | The owner agent is not connected | composer | Hand off |
| `external` | The session file was written by a process Hoplet did not start | composer | none; reading only |
| `idle` | No Claude Code process for this session | nothing | Send (starts one) |
| `starting` | The process is starting | turn | none |
| `ready` | The process is running, no turn in progress | composer | Send, Stop |
| `running` | A turn is in progress; streamed text appears in the turn | turn | Interrupt |
| `waiting-permission` | Claude Code asks to use a tool; the request opens inside the turn | turn | Allow, Deny, Interrupt |
| `failed` | Start or the last turn failed; the error is named where the state is shown | turn or composer | Send (retries) |

A permission request shows the tool name and its input (elided above 8 KiB like any block). A control appears
only when the session's runtime driver declares its capability (`permissions`, `interrupt`, `thinking`,
`fast`, `models`).

### Workspace inspector

The inspector holds tools as tabs, Changes and Files to start; a tab closes, and the + button opens the tools
menu, which reopens a closed tab with its shortcut (Changes ⇧⌘G, Files ⇧⌘E) and is where later tools join
(Terminal #14, Browser #15) once built. The inspector's fold control carries a mark when the working tree has
changes. Changes lists every changed path with its git status by colour and letter (modified, added, deleted,
untracked) and its added and removed line counts, and opens first when the working tree has changes; with none it
says the tree matches `HEAD`. Files shows the working tree as a folder tree with the same status marks; past the
listing limit it says that only the first paths are shown. Paths carry an icon for their file type. Opening a
path widens the inspector into a viewer with the list on its right, at the window's edge, so the list stays in
view (on narrower windows the sidebar folds meanwhile): a changed path opens its unified diff against `HEAD` with
unchanged regions folded and previous and next file controls, a file opens read-only with syntax highlighting, a
modified file switches between its diff and its content, an image is shown as a preview (a changed image before
and after), and another binary file shows only its size. Long lines in a diff or a file scroll sideways and keep
their columns; a Wrap toggle (⌥Z, remembered in the browser) breaks them under their own line number instead. An
Edit row in a turn opens the file's diff. While the owner is reading the tree the inspector says so, and when the
owner does not answer it names the failure and offers Try again. While the owner is offline, Changes shows the
last snapshot's changes and diffs, labelled with the snapshot's time and base commit, and Files says that it
needs the owner online; without a stored snapshot Changes says that none exists yet. A directory without a git
commit has no Changes and no Hand off, and the inspector says why. Hand off is one row at the foot of the
inspector that opens into the handoff panel, a card in its place that closes with its × and stays open while a
handoff runs, also one another window started. The panel picks a connected target device and asks it for a clone
of the working tree's `origin` among its watched projects: it names the clone the worktree goes next to, or says
that the target has none and asks for the path of one. While the owner is offline it says so and shows the last
snapshot's time, files and size; while a turn runs Hand off waits for its end. During a handoff the panel lists the
five steps (prepare, transfer with the bytes sent, install, verify, commit) with who did each and how long it took;
a failure marks its step, names the reason and says that nothing moved, with Hand off to try again; on success it
names the new worktree and branch, and the session moves to its new owner in the sidebar.

### Settings

A page with a section list that grows with later sections: Appearance (system, light or dark), Language
(English, 简体中文) and Machines (every device with its presence and last connection, and Add a machine: mint a
device token, copy a ready agent configuration, and wait until that agent is connected). Appearance and
language are kept in the browser. Memory and model providers are later sections (#18, #16).

### Host CLI

`hoplet` (from a repository checkout, `pnpm hoplet`) uses the REST API with the host token: `hoplet sessions`,
`hoplet prompt <session> <text>`, `hoplet interrupt <session>`, `hoplet handoff <session> <device> [--path <dir>]`, `hoplet devices` (id, name and connection of every machine) and
`hoplet devices add <name> [--id <deviceId>]` (mints a device token and prints the agent's `config.json`).

### Agent CLI

`hoplet-agent daemon` runs the agent. `hoplet-agent projects add|list|remove <path>` maintains the watched
project paths; `hoplet-agent sessions list|import|remove <session>` marks which sessions sync.

## Data model

Server, under the data directory (docs/hosting.md#data):

- SQLite index `hoplet.db`:
  - `sessions(session_id, project_path, title, created_at, updated_at, line_count, byte_size, chunk_count,
    cur_chunk_lines, device_id, owner_epoch, provider)`. `byte_size` is the sync cursor; `project_path` is the
    `cwd` of the session's lines until a handoff commit sets it to the target's worktree; `device_id` is the
    owner; `owner_epoch` increases with every handoff; `provider` names the vendor (`claude`).
  - `devices(device_id, name, version, first_seen, last_seen, token_hash)`. `token_hash` is the SHA-256 of
    the device token.
  - `snapshots(session_id, snapshot_id, created_at, base_commit, origin_url, tree_hash, byte_size, sha256)`: the latest
    workspace snapshot per session.
  - `append_recovery`, `compression_pending`: undo boundaries and pending compression; no session bytes.
- Chunk files `sessions/<sessionId>/c-<seq>.ndjson`, gzip-compressed to `.ndjson.gz` when full
  (docs/architecture.md#session-chunk-files).
- Snapshot files `snapshots/<sessionId>/<snapshotId>.tar.gz`.

Runtime state is not stored: the core holds the current state per session in memory and rebuilds it from the
agents when they connect.

Device, under the agent state root (`~/.hoplet/` by default): `device-id`, `config.json`, `projects.json`
(watched paths and imported session IDs) and `runtime/` (one record per running Claude Code process). Claude
Code's own store (`~/.claude/projects/<encoded path>/<session>.jsonl`) is read for sync and written only by
Claude Code and by handoff installation.

API, in `shared/src/` (schemas, types and error codes) and `agent/internal/sync/protocol.go`:

- REST, host token: `GET /api/health` (`{ ok, sessions, uptimeSec, coreVersion, protocolVersion }`: the product
  version and the WebSocket protocol version of the running core); `GET /api/sessions` and
  `GET /api/sessions/:id` (each session with its runtime state: `runtime: { state, capabilities?, error? }`,
  `offline` when the owner is not connected);
  `POST /api/sessions` (new session);
  `GET /api/sessions/:id/lines?start=&limit=`; `GET /api/sessions/:id/blocks/:line/:block`;
  `GET /api/search?q=&start=&limit=`; `GET /api/sessions/:id/search?q=&start=&limit=`;
  `POST /api/sessions/:id/prompt`; `POST /api/sessions/:id/permission`; `POST /api/sessions/:id/interrupt`;
  `POST /api/sessions/:id/stop`; `GET /api/sessions/:id/workspace`; `GET /api/sessions/:id/workspace/file?path=&at=`;
  `GET /api/sessions/:id/workspace/diff?path=`; `GET /api/sessions/:id/snapshot/changes`;
  `GET /api/sessions/:id/snapshot/diff?path=`;
  `POST /api/sessions/:id/handoff`; `GET /api/devices` (each device with the `providers` its connected agent
  announced and their capabilities); `POST /api/devices`;
  `GET /api/devices/:id/dirs?path=` (`path` absent or `~` is the browse root; answers `{ path, parent, root, entries, truncated }`
  where `path` is the resolved absolute path, `parent` is null at the root, and `entries` are `{ name, git }` for the visible
  directories only, sorted case-insensitively, at most 500 with `truncated` set beyond that; `target-offline` when the agent
  is not connected, `invalid-request` with a `reason` for a relative path, a path outside the root, a path that is not a
  directory or one that is unreadable, `timeout`);
  `GET /api/devices/:id/clone?origin=` (`{ deviceId, path }`: the main working tree of a watched project whose
  `origin` is the given one, as a handoff would pick it, or `path: null`; `target-offline`, `timeout`);
  `GET /api/devices/:id/projects` (each watched path with its symlinks resolved, the directory its sessions record;
  a path watched through a link matches the sessions run in its real directory);
  `POST /api/devices/:id/projects` (`{ path }`, an absolute path on the machine: the agent resolves symlinks, refuses a path that is not an existing directory, adds the resolved path as `hoplet-agent projects add`
  does and answers with the updated listing; `target-offline` when the agent is not connected); `POST /api/devices/:id/projects/remove`;
  `POST /api/devices/:id/sessions/import|remove`; `POST /api/ws-tickets`.
- Browser WebSocket `/ws?ticket=`: `subscribe`/`unsubscribe` a session; events `session.appended`,
  `session.runtime` (state), `session.output` (streamed deltas of a subscribed session),
  `session.permission`, `handoff.progress`, `device.presence`.
- Agent WebSocket `/ws/agent`, device token: sync (`device.hello` with the providers and their capabilities,
  `welcome`, `session.append`, `session.ack`, `session.resync`, `session.rejected`), control
  (`runtime.prompt|permission.answer|interrupt|stop|start` from the core; `runtime.state` with the driver's
  capabilities, `runtime.output`, `runtime.permission`, `runtime.rejected`, `runtime.started` and
  `runtime.start.rejected` from the agent), workspace (`session.workspace.list|file.read|diff.read` from
  the core; `session.workspace`, `session.workspace.file`, `session.workspace.diff`,
  `session.workspace.rejected` and `workspace.snapshot` from the agent), handoff
  (`handoff.prepare|install|data|verify|commit|abort` from the core;
  `handoff.prepared|installed|verified|failed` from the agent), projects
  (`device.projects.*`, `device.sessions.*`), directory browsing (`device.dirs.list` from the core;
  `device.dirs` or `device.dirs.rejected` from the agent), the clone a handoff would use (`device.clone.find` from
  the core, `device.clone` from the agent), `ping`/`pong`.
- Errors carry a stable `code`; the web UI and the CLI choose their copy from it.

## Modules

| Part | Owns | Code |
|---|---|---|
| core | REST API, browser and agent WebSockets, storage, read projection with per-provider mappings, runtime relay, handoff coordination | `core/src/<module>/`: `config`, `log`, `migrations`, `store`, `ingest`, `control`, `api` |
| shared | Schemas, types and error codes of the REST API and both WebSockets | `shared/src/` |
| web | The token gate and the Hoplet screen | `web/src/screens/`, `web/src/components/`, `web/src/lib/` |
| agent | Session scan and sync, providers and runtime drivers (Claude Code), workspace listing, snapshots and handoff installation | `agent/cmd/hoplet-agent/`, `agent/internal/{config,provider,projects,sessions,locate,daemon,sync,runtime,workspace}` |
| image | The container image and the compose file | `image/` |
| scripts | Repository checks, fixtures, benchmarks, smoke test and the host CLI `hoplet` | `scripts/` |

Module responsibilities and the dependency direction are in docs/architecture.md#components.

## Key flows

### Add a machine

The operator selects Add a machine and names the device. `POST /api/devices` stores the hash of a new device
token and returns the plaintext once with the `device_id`. The panel shows a ready `config.json`
(`server_url`, `token`, `device_id`, `device_name`) and waits until that device connects. Minting again for an
existing `device_id` replaces its token. Add a machine is available as `hoplet devices add` and, per the design,
in Settings (#8).

### Session sync

The agent connects with `device.hello`, which carries its `device_id` and protocol version; the core refuses a
different protocol version and answers with `welcome`, naming the cursor and `owner_epoch` of every session
the device owns. The agent's providers scan their vendors' session stores every second for sessions imported in `projects.json`. For each one
whose file is longer than the server cursor, it reads from the cursor to the last complete line and sends
`session.append` batches of at most 1 MiB raw bytes, gzip-compressed. A line larger than 1 MiB is sent alone;
lines are at most 32 MiB; a longer line is reported once and blocks only its session at that offset. A file
whose size, mtime and inode did not change is not read again. The core checks ownership, then the start offset against the cursor
(`session.resync` on a mismatch, `session.rejected` for a non-owner), appends verbatim, commits, acknowledges
and broadcasts `session.appended`. A file shorter than the cursor is reported once and skipped. Sync work of
one session is bounded per scan round (16 MiB raw) and sessions that grew recently go first, so an import of a
large session does not delay appends of others.

### Reading a session

`GET /api/sessions/:id/lines?start=-80` returns the last 80 lines as entries of the conversation model: the
session provider's mapping turns each stored line into `{ lineNo, role, blocks }` with blocks of type `text`,
`thinking`, `tool-call`, `tool-result`, `image` or `other` ([ADR 0012](adr/0012-vendors-plug-in-behind-provider-interfaces.md)).
A `tool-call` is `{ id, name, kind, mcp?, input }`: the mapping classifies the vendor's tool as one of `read`,
`edit`, `write`, `run`, `search`, `list`, `fetch`, `call`, `view` or `other`, and names the server and tool of an
MCP tool in `mcp`. The mapping leaves out the vendor's internal records, so clients render what they receive.
Every block over 8 KiB becomes `{ elided: true, type, bytes, preview, ref }` with a text preview of at most
1 KiB. `GET /api/sessions/:id/blocks/:line/:block` returns one full block. Each page reads only the chunks it
touches. The conversation keeps at most 1,000 items and 8 MiB of line data; pages that leave that window are
dropped and fetched again when the reader returns.

### Search

`GET /api/search?q=` scans the stored chunks of sessions in order of last activity and returns matching line
numbers with a short excerpt, paged, with `total` when the scan completes. `GET /api/sessions/:id/search?q=`
does the same within one session. Search runs as background work and never delays control
(docs/architecture.md#principles).

### Remote control

1. `POST /api/sessions/:id/prompt` with `{ text, promptId }`. The core refuses with `owner-offline`,
   `external`, `busy` (a turn is running) or `starting`, and forwards `runtime.prompt` to the owner otherwise.
   Sessions run turns in parallel without a limit.
   A repeated `promptId` within 10 minutes returns the first result.
2. With no process for the session, the agent starts Claude Code in the session's working directory with
   streaming JSON input and output, resuming that session (`starting`), and sends the control request
   `initialize`; its answer makes the session `ready`
   ([ADR 0008](adr/0008-remote-control-runs-a-hoplet-managed-claude-code-process.md)). When more than
   `runtime_max_idle_processes` processes are `ready` without a turn, it stops the least recently used one.
3. The agent writes the prompt to the process and reports `running`. Text deltas, thinking activity (a token
   count), tool calls and tool results flow as `runtime.output`; the core forwards them as `session.output` to clients subscribed to
   the session and keeps none of them. The core adds `toolKind` and `mcp` to a tool call, classified by the session
   provider's mapping as in a stored `tool-call`.
4. A permission request becomes `waiting-permission` and `session.permission` with `requestId`, tool, `toolKind`
   and input. `POST /api/sessions/:id/permission` with `{ requestId, decision: "allow" | "deny" }` answers it; the
   first answer wins. A request without an answer for 10 minutes is denied. A denied tool call returns an
   error to Claude Code and the turn continues.
5. `POST /api/sessions/:id/interrupt` ends the turn and keeps the process (`ready`);
   `POST /api/sessions/:id/stop` ends the process (`idle`).
6. Claude Code writes the turn to the session file; sync appends it as usual, and the live view replaces the
   streamed output with the stored lines.
7. After a completed turn the agent uploads a workspace snapshot (see Handoff). After 30 minutes without a
   turn the agent stops the process (`idle`).

After every turn the agent records the session file's size. When the file grows beyond it, or grows while no
Hoplet process runs for the session, the state is `external`: the agent stops its process for the session, and
a prompt is refused until the file has not grown for 60 seconds. When the owner disconnects, its runtime states become `offline`;
when it reconnects, it reports the state of every process it still runs.

### New session

`POST /api/sessions` with `{ deviceId, projectPath, provider, options, text, promptId }`, where `options` holds
only what the provider's runtime driver declares: `model` (one of the driver's `models`), `thinking` (boolean,
needs the `thinking` capability) and `fast` (boolean, needs `fast`). A browser reads the capabilities before any
session exists from `providers` in `GET /api/devices`, which the agent announces in `device.hello`. Each provider also carries the
installed vendor `version` and `tested`, false when that version is newer than the tested ones (older ones are
refused and carry neither).

The core refuses with `target-offline` when the device is not connected, with `invalid-request` when the body
is malformed, the device announced no such provider, an option is not declared (`reason` names the option) or
`projectPath` is not one of the device's watched projects (`reason: projectPath`), and with `too-large` for a
prompt over 8,000 characters. Otherwise it forwards `runtime.start` to the device on the control lane. The
agent checks the same again, starts Claude Code in that directory with streaming input and output and no
session to resume (`--session-id` with a new ID, `--model`, `--thinking enabled|disabled` and, for `fast`, the
setting `fastMode` of that one process), writes the prompt, waits until Claude Code has created the session
file, adds the session to `imported` in `projects.json`, and answers `runtime.started` with the session ID. If
Claude Code exits or does not create the file within 60 seconds, the agent stops the process and answers
`runtime.start.rejected` with the reason, which the core returns as `rejected` with `reason`; with no answer
within 75 seconds the core returns `timeout`. The response is `{ ok: true, sessionId, promptId }`. A repeated
`promptId` within 10 minutes returns the first result, except after a refusal, which can be retried. From then
on the session syncs and is driven as any other (Remote control); the agent reports its runtime state again
after the answer, and the core accepts runtime messages for the session from that device until the session
reaches the index.

### Handoff

`POST /api/sessions/:id/handoff` with `{ targetDeviceId, targetPath? }`. The core refuses when the target is
not connected, the target is the owner, a handoff of this session is in progress, or the session is `running`
or `waiting-permission`. Progress is reported to every browser as `handoff.progress`: the step, the time of the
snapshot of an offline handoff, the bytes sent of the archive and the stored stream during transfer (at most
four times a second), and on `done` the new worktree and branch, which the response also carries
(`{ ok, ownerDeviceId, ownerEpoch, worktree, branch, snapshotAt? }`).

1. **Prepare (owner online).** `handoff.prepare` to the owner: it stops the session's Claude Code process,
   sends the remaining bytes, and creates a snapshot: base commit, `origin` URL, binary diff of tracked
   changes, untracked files that are not ignored, and a git bundle of commits that `origin` does not contain.
   The snapshot travels to the core in ordered chunks of at most 1 MiB. With the owner offline, the core uses
   the latest stored snapshot and reports its time.
2. **Install.** `handoff.install` to the target with the snapshot and the stored session stream. The target
   picks the clone: a watched project path whose `origin` equals the snapshot's, else `targetPath`; without
   either the handoff fails with `no-clone`. It adds a git worktree next to that clone, named
   `<clone directory>-<first 8 characters of the session ID>`, on branch `hoplet/<first 8 characters>`, at the
   base commit, applies the diff and untracked files, writes the session file into the Claude Code store for
   the worktree path, adds the worktree to `projects.json` with the session imported, and answers
   `handoff.installed` with the worktree and its branch. The store directory
   is the realpath of the worktree with every character other than ASCII letters and digits replaced by `-`
   (one `-` per UTF-16 unit); if the result is longer than 200 characters, it is truncated to 200 characters
   followed by `-` and the base-36 absolute value of a 32-bit hash of the realpath over its UTF-16 units.
   The session file is written byte for byte; sidecar files Claude Code keeps beside it are not carried.
3. **Verify.** Before any Claude Code process starts in the worktree, the target reports the SHA-256 and size
   of the installed session file and the git tree hash of the worktree. The core compares them with the stored stream at the current cursor and the snapshot.
4. **Commit.** In one transaction the core checks that owner and cursor are unchanged, sets the owner to the
   target, sets `project_path` to the target's worktree and increases `owner_epoch`. It sends `handoff.commit`
   to the target, which starts syncing at the cursor, and to the old owner when connected, which stops
   controlling the session. Appends from the old owner are rejected from then on.

Any failure before commit leaves owner and cursor unchanged, sends `handoff.abort`, and the target removes
what it installed; an installation that an agent restart interrupted is removed when the agent starts, or
when it reconnects and the core does not name it the owner. A session whose directory is not in a git
repository with a commit has no snapshot and cannot be handed off. Snapshots are at most 64 MiB compressed and 256 MiB uncompressed; both limits are enforced
while reading and decompressing. A working tree whose snapshot exceeds them cannot be handed off offline.

The target applies the tracked diff to the working tree, so changes that were staged on the source arrive
unstaged. When the source's `origin` has no remote-tracking branches, the snapshot carries no bundle; a target
that does not already have the base commit fails the install with code `rejected` and reason
`base-commit-unavailable`.

### Workspace

`GET /api/sessions/:id/workspace` lists the owner's working tree (tracked and untracked, not ignored, without
files deleted from it; at most 400 paths, with `truncated` beyond: every changed path first, then the others
shallowest first, each directory giving its next entry in turn) and, for every changed path, its git status (`modified`, `added`, `deleted`,
`untracked`) against `HEAD` and its added and removed line counts, or `binary`. Paths are relative to the
session's working directory, and only what is inside it is listed; a rename is a deletion and an addition. An
untracked file larger than 1 MiB, or any once 16 MiB were read for one listing, has no line counts. The listing
carries the URL of the repository's remote `origin` (empty without one). A directory
that is not in a repository with a commit answers `git: false` with no paths and no changes.
`GET /api/sessions/:id/workspace/file?path=` returns one regular file, resolved inside the working tree
([ADR 0010](adr/0010-workspace-reads-use-a-filesystem-root.md)); with `at=head` it returns the file as `HEAD`
has it. Text is UTF-8 without a NUL byte in its first 256 KiB and is returned up to there, with `truncated`
beyond; an image (PNG, JPEG, GIF, WebP, SVG by extension and content) of at most 4 MiB is returned whole as
base64 with its media type; any other file is `binary` with its size. Paths that reach `.git` are refused.
`GET /api/sessions/:id/workspace/diff?path=` returns the unified diff of one changed path against `HEAD`: its
hunks without the header lines, at most 256 KiB cut at a line end with `truncated` and the full size beyond
that, or `binary` with the old and new sizes; an untracked file is all added lines. These three need the owner
online (`owner-offline`); the agent's refusal (`not-changed`, `not-in-head`, `path-escape` and the like) is
`rejected` with that `reason`. Requests and the listing travel on the control lane, file and diff answers on
the bulk lane, so a large image never delays a prompt.
`GET /api/sessions/:id/snapshot/changes` and `GET /api/sessions/:id/snapshot/diff?path=` answer the same from
the latest stored snapshot (its tracked diff against the base commit and its untracked files), with the
snapshot's time and base commit (the changes also with its `origin` and the size of its archive), whether or not the owner is online: `no-snapshot` when there is none and
`not-found` for a path the snapshot does not change. The core streams the archive to answer and keeps the
changes of recent snapshots in memory; both are bulk work. The web UI never writes files.

### Updates

The operator pulls the new image and restarts it, and replaces `hoplet-agent` on each machine
(docs/hosting.md#upgrades). Nothing updates automatically.

## Technical baseline

- Core: TypeScript on Node.js 26, run from source
  ([ADR 0003](adr/0003-the-core-runs-typescript-source-directly.md)); SQLite through `node:sqlite`; one
  runtime dependency, `ws`.
- Shared: TypeScript source with schemas; types are derived from the schemas.
- Web: React and Vite, built to static files the core serves
  ([ADR 0011](adr/0011-the-web-ui-is-a-desktop-react-application.md)); English source copy and `zh-Hans`.
- Agent: Go 1.27, one static binary; Claude Code versions it supports (a minimum, tested and untested newer ones) are in docs/hosting.md#hosts.
- Work classes: control (prompts, permissions, interrupts, runtime events, handoff steps) is served before
  bulk work (sync batches, search, snapshots, history pages) in the core and on the agent connection.

Budgets, measured by `scripts/bench/` on the reference setup: core in Docker on an Apple-silicon Mac, agent on
another machine on the same 1 Gbit LAN, Chrome on desktop. Fixtures are generated with the byte distribution
of real sessions (86% tool results) at 23, 128 and 256 MB.

| Measure | Budget |
|---|---|
| Open a session: selection to first rendered tail page | ≤ 1 s for every fixture |
| Bytes transferred for the tail page | ≤ 512 KiB |
| Earlier page while scrolling up | ≤ 300 ms |
| Prompt accepted (`POST` to `running`) with the process `ready` | ≤ 300 ms, also during a global search and a 256 MB import |
| Runtime event, agent to browser | ≤ 150 ms at p95 |
| Process start (`idle` to `initialize` answered) on the 256 MB fixture, cold page cache | ≤ 5 s |
| Online handoff of the 128 MB fixture with a 10 MiB snapshot | ≤ 30 s |
| Web UI after an 8-hour live session | ≤ 1,000 mounted items, JavaScript heap ≤ 300 MB |

## Acceptance checklist

- [ ] The 256 MB fixture syncs completely; the server `byte_size` equals the device file size
- [ ] Restarting the agent or the core during sync neither duplicates nor loses a byte
- [ ] A non-owner append is rejected with `not-owner`; a wrong offset is answered with `session.resync`
- [ ] Opening each fixture meets the open, transfer and page budgets; elided blocks open on request
- [ ] A prompt from the web UI starts Claude Code, streams output live, and the stored turn replaces it
- [ ] A permission request is answered from the web UI; Deny and Interrupt leave the session usable
- [ ] A session written from a terminal shows `external` and refuses prompts
- [ ] Prompt acceptance meets its budget during a global search and a 256 MB import
- [ ] An online handoff with uncommitted and untracked changes continues on the target in a worktree of its
      own clone, with identical files and session
- [ ] An offline handoff uses the last snapshot and states its time; the old owner's appends are rejected
      after it reconnects
- [ ] A failed handoff at any step leaves owner, cursor and both machines' files unchanged
- [ ] An 8-hour live session stays within the web UI memory budget
- [ ] `docker compose up -d` serves the web UI on `127.0.0.1` and the health check passes
