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
  ([ADR 0015](adr/0015-control-protocol-and-history-source-are-chosen-separately.md)). The agent's Codex provider
  reads Codex's rollout files (`$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<time>-<threadId>.jsonl`, or `.jsonl.zst`
  once Codex has compressed it), with the thread ID as session ID and the byte cursor counting
  uncompressed bytes ([ADR 0027](adr/0027-codex-runs-on-its-app-server-and-enters-at-level-2.md)); the core stores
  the lines, takes a Codex session's project path and start time from the `session_meta` line and its title from
  the first user message, maps the `item_completed` events into entries. The Codex provider can install a rollout under
  `$CODEX_HOME/sessions/` and `handoff.install` names the vendor, so a Codex session hands off like a Claude Code
  one (see Handoff). Codex sessions are controlled through
  `codex app-server`: new sessions, prompts, steering, interrupt, approvals and the settings `mode`, `model`,
  `effort` and `collaboration` (with a plan exit), on the tested codex-cli versions only.
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
accessible name says the state in words). A device row is led by its system's mark (macOS, Windows, Linux; a generic computer otherwise) at the folder glyph's size, with a dot at the mark's bottom-right that shows whether its agent is connected (the row's accessible name says so in words); it folds its directories (remembered in the browser) and, when its agent is connected,
offers Add project on hover: a dialog with a machine select (each machine with its mark, offline machines disabled; an Add a machine item points to `hoplet devices add`) and a folder browser over the agent's browse root (`browse_root`, the home directory by default): a path bar of clickable segments that becomes a field for a typed path, recent folders (where Claude Code has sessions) at the root, the folders below with a git mark, and the selected path in the footer; the chosen directory is added to the agent's watched projects.
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
project), with the config options its runtime driver declares ([ADR 0018](adr/0018-session-settings-are-config-options.md)) and, when the machine has more than one enabled provider configuration (or only one that is not the vendor's own), a configuration chip that picks which one runs the session, grouped by vendor, with a configuration whose key is not set dimmed and not selectable, and a link to Settings; the configuration's defaults prefill the options; a directory
row's button presets both, and ⌘N starts where the open session runs. The draft's first prompt starts the session, which then replaces the draft; a
refused start keeps the draft and its text and says why. A session that a configuration other than the vendor's own runs shows its name as a tag next to the title in the top bar. Selecting another session discards the draft. The sidebar foot shows whether this
window is connected to the core and opens Settings. The sidebar folds away.

**Session actions.** The session menu (the ellipsis in the conversation's top bar, and the context menu of a
sidebar row: right click, the context-menu key or Shift+F10) acts on one session and has three groups:
**Rename** (⌥⌘R) and **Pin** or **Unpin** (⌥⌘P); **Copy**, a submenu of Session ID, Link (the address that
opens the session in this web UI) and Last answer as Markdown (the text of the session's last assistant
message, as Markdown), and **Open in new window**; **Archive** or **Unarchive** (⇧⌘A). A copy is confirmed by a
toast that fades after a few seconds. The shortcuts act on the selected session. A last group holds **Delete…**. There
is no Fork here.

- **Rename** opens a dialog with the title field, the vendor's title ("Claude's title: ...") and a Use Claude's
  title action. The name is the session's title in Hoplet: the sidebar, the top bar and the palette show it, and
  clearing the field shows the vendor's title again. It is stored beside the vendor's title and rewrites no
  line.
- **Delete…** ([ADR 0023](adr/0023-deleting-a-session-and-what-hoplet-made-for-it.md)) opens one confirmation that lists
  what goes. Hoplet's copy is always deleted. The session file on the owning machine, and the worktree and branch a
  handoff made for it, are optional and unticked; a worktree with uncommitted changes or commits no remote has
  shows how many. A session with a running Hoplet process is not deleted until it is stopped. With the owner offline
  only Hoplet's copy goes, and the machine stops syncing the session when it reconnects. A step that fails keeps
  Hoplet's copy hidden and the same request continues it. A deleted session does not come back from a sync;
  importing it again does. Copies on a previous owner stay, and the dialog says so. There is no undo.
- **Pin** keeps a session at the top. By folder, a Pinned group opens each machine's sessions, and its rows show
  their folder as a second line; Flat lists pinned sessions first, each with a pin mark, a hairline closing
  them. Pinned sessions are ordered by the machine's sort.
- **Archive** hides a session from the sidebar and from the palette's session list. It is not deleted: it stays
  in the store, keeps syncing, and stays archived when lines are appended to it or it is handed off, until it
  is restored. The archive icon in the sidebar foot opens the Archived page in the conversation's place:
  archived sessions grouped by machine, then by project folder (name, path, count), newest first, always,
  whatever the machine's view options. Restore appears on a row's hover, and Restore all on a folder header's
  hover, without confirmation. Sessions of an offline machine are listed and can be restored. An empty list says
  so, and a back button returns to the conversation.

**Conversation.** The top bar holds the session's title, the session menu and the workspace toggle; where the session lives is
the sidebar's to show. The selected session from its last 80 lines; earlier pages load as the reader scrolls up.
The conversation is a sequence of turns: a turn begins with a user prompt, which is its header with the
time, and holds the assistant text, thinking, tool calls, tool results and file edits that follow. A
finished turn's header summarizes it (files, tools, duration). Consecutive tool activity folds into one row.
Elided blocks show a preview and their size, and open in place on request. While the view is at the bottom,
new lines and runtime output appear live; otherwise a pill counts new messages and jumps to the end. The
composer at the foot sends a prompt, typed or spoken; speech is transcribed on the user's own machines and
no audio leaves them (#17). Its first row says where the session runs, machine / directory / git branch (for a
session the branch its lines last recorded, for a draft the one checked out in the directory), read only once
the session exists; its toolbar holds attach (when the driver declares `images`), a permission-mode pill, the model menu, dictate
and send. In a draft and in a session alike the menu lists the config options the driver declares, in its order,
as radio lists for a choice and switches for a yes or no, and shows each option's current value. In a
session a pick is sent at once (`POST /api/sessions/:id/config`) and holds from the next turn on: the driver says
how it applies, and a running Claude Code process switches its model in place (`set_model`, before the next
turn) while a change of Thinking or Fast restarts it between turns with the session's values, which
shows as `starting`. The permission mode is the driver's config option of the category `mode`, shown as the pill
left of the model button: a session starts in Manual and each mode is picked from the pill's menu or, with the
field focused, by Shift+Tab, which moves to the next mode in the driver's order and wraps. The mode is a setting
of the one session, not of the machine or the browser; a pick applies at once, also while a turn runs or waits for a
permission, and the pill follows what the process reports, so a change Claude Code makes itself (leaving plan mode)
shows too. Every mode is selectable remotely, Bypass permissions included, with no confirmation; while it is on
the pill is amber and carries the shield-alert glyph. In a draft the pick goes into the new session's `config`. The text grows to 8 lines, then scrolls; dragging the composer's top edge sets a height the
browser remembers, at most what leaves the conversation 240 px, and a double-click on the edge resets it.
Typing `/` at the start of the text opens a list of the session's slash commands (group Commands) and skills
(group Skills), filtered as the user types; there is no second trigger for skills. The agent reports the list its
Claude Code process offers, with each entry's source (project, user, plugin or built-in). Of Claude Code's own
commands only `compact`, `context`, `usage`, `cost`, `init`, `review`, `security-review`, `pr-comments` and
`release-notes` are offered; the others need a terminal or change what Hoplet manages. A draft has no process:
it offers the list last reported for that machine and directory, or the built-in commands, and says that project
commands and skills show once the session has started. `@` lists the paths of the session's working tree
(`GET /api/sessions/:id/workspace`), needs the owner online and is not offered in a draft.
When the session's driver declares `images`, images pasted, dropped or attached go with the prompt: each is
shrunk to at most 1568 px on its long edge (a GIF is left as it is), must then be PNG, JPEG, GIF or WebP of at
most 4 MiB, and is uploaded to the session's machine at once; a prompt takes at most 4, together at most 8 MiB.
Each shows as a thumbnail above the text, dimmed with a spinner while it uploads; Send waits for the uploads,
and a file that is refused or an upload that failed says why under the field. The machine keeps an uploaded
image for an hour, so a refused prompt comes back with its images
([ADR 0017](adr/0017-prompt-images-are-uploaded-before-the-prompt.md)). Actions whose icon carries the intent are icon buttons with an accessible name and a
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

A permission request shows the tool name and its input (elided above 8 KiB like any block). A request the driver declares a plan exit (ADR 0019) shows the plan instead, as Markdown in the turn (collapsed above 4 KiB, with an Open button): one button approves into a mode (Accept edits by default, a menu for the others the driver offers, never Bypass permissions) and Keep planning sends the user's feedback to the model. A plan has no countdown; it waits for the answer, and a window that did not answer shows it as answered. When the driver declares `freshContext` ([ADR 0025](adr/0025-a-plan-can-be-approved-into-a-new-session.md)), the approve menu ends with Clear context and approve…, which turns the card's footer into a confirmation: a new session on the same machine, in the same project and in the chosen mode, started from the plan and its file and nothing else of this conversation; this session stops and keeps its history, with a link to the new one. Start new session locks the card while the new session starts; if it does not start the plan still waits, with a note, and the user can try again, approve here or keep planning. When it starts, the cockpit opens the new session. This session then shows one resolved line, Approved in a new session, with a chip that opens the new one, and the new session's first turn shows From the plan in the old session's title, linking back. A control appears
only when the session's runtime driver declares its capability (`permissions`, `interrupt`, `images`); its settings are the config options it declares.

### Workspace inspector

The inspector holds tools as tabs, Changes and Files to start; a tab closes, and the + button opens the tools
menu, which reopens a closed tab with its shortcut (Changes ⇧⌘G, Files ⇧⌘E) and is where Terminal and Browser join
([ADR 0026](adr/0026-the-inspector-opens-a-restricted-terminal-and-links-dev-servers.md)). The inspector's fold
control carries a mark when the working tree has changes. Changes lists every changed path with its git status by colour and letter (modified, added, deleted,
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
handoff runs, also one another window started. The panel picks a connected target device, and the configuration that continues the session there when that machine does not have the session's own enabled (without a choice Hand off stays disabled; a machine whose configurations of the vendor are all disabled lists them with an Enable button that turns one on there and then preselects it, and a machine that does not offer the vendor says to install it and restart its agent), and asks it for a clone
of the working tree's `origin` among its watched projects: it names the clone the worktree goes next to, or says
that the target has none, and then offers to clone the repository into a directory of the target (a proposed
path under its browse root, which the user may change) or asks for the path of an existing clone. A clone is a
sixth step before prepare, with the bytes received and a Cancel while it runs. While the owner is offline it says so and shows the last
snapshot's time, files and size; while a turn runs Hand off waits for its end. During a handoff the panel lists the
five steps (prepare, transfer with the bytes sent, install, verify, commit) with who did each and how long it took;
a failure marks its step, names the reason and says that nothing moved, with Hand off to try again; on success it
names the new worktree and branch, and the session moves to its new owner in the sidebar.

#### Terminal and dev server links

The Terminal tab opens a shell in the session's working directory on the owner machine, to check what Claude Code
did without a separate SSH session. It exists only on a machine whose operator turned the agent's `terminal`
setting on (off by default, docs/hosting.md#terminal); on any other machine the tab says so and names the setting.
It opens only while the owner is online and the session has no running turn, no `external` writer and no handoff,
and it says which of these stops it. It is one terminal per session (three per machine), starts in the session's
directory as the agent's user, closes after 60 minutes without input and when a turn or a handoff starts, and never
survives the owner's connection; closing the tab ends it. A started turn closes it with a note, never the other
way round: a prompt is not delayed by the terminal. Hoplet stores nothing of what is typed or printed and logs only
when a terminal opened and closed, with the reason.

The Browser tab lists the TCP ports that processes started from the session's working directory listen on: the
port, whether it listens on loopback only, and the process name ("localhost:5173 is running · node"). Open is a
link in a new tab to the owner machine, which works where the browser reaches it directly (same network,
Tailscale); the tab shows the address it used. Hoplet does not proxy, embed or inspect a dev server's traffic, and
reports no command line, arguments, environment or process ID. It lists nothing while the owner is offline.

What is not built: a terminal on a machine that did not opt in, in another directory, during a turn or after the
owner disconnects, and a preview through the core.

### Settings

A full page that replaces the window (no session sidebar, no status bar), opened from the sidebar foot, the
palette or ⌘, and left with the back button or Esc; the address carries it as `?settings=<section>`. The cockpit stays
mounted underneath, so drafts, scroll and live updates survive, its shortcuts are off while Settings shows, and
focus returns to what opened it. A section
list at its left grows with later sections; the sections are:

- **Appearance**: the theme (System, Light, Dark, as preview cards), the interface language (English, 简体中文)
  and Text: the interface font (Inter, System, Geist), the code font (JetBrains Mono, System, Geist Mono), the
  size (Small, Default, Large: the four sizes move together and line heights stay), Full width (the conversation
  fills the main area) and Ligatures in code and tool output, with a live preview. Every choice is kept in the
  browser and only swaps design tokens; the faces are bundled with the web UI and never fetched. The terminal's
  font is not one of them.
- **Machines**: every device with its system's mark (macOS, Windows, Linux; a generic computer for any other system or an agent that does not report one), its presence as a dot at the mark's bottom-right, and its last connection, and Add a machine: mint a device token,
  copy a ready agent configuration, and wait until that agent is connected. A tab per machine, like Model providers,
  opens the machine's facts and its resources (#88): CPU, memory used and free disk space with their last hour, a
  warning line when memory is above 90% used or the disk under 10% free by default (never for an offline machine;
  both thresholds are adjustable, for every machine and per machine), the running sessions as the main list
  (title, project, last-hour memory, CPU, memory, child processes; each row opens its session) and where the memory and CPU go as stacked bars. An offline machine greys its last values and says when
  they were reported, and its sessions are unknown; a machine whose agent reports nothing shows only its facts.
  The agent reports every 15 seconds; the core keeps the last hour of the machine and of each session in memory only.
- **Model providers**: from what each connected agent announces. A tab per machine (system mark with the presence dot, and
  name, in the order of Machines; it opens on the first connected one) and, grouped by vendor, a row per provider
  configuration that machine defines: the mark, the name and "version · N sessions · used <time>" (the counts and
  the last use come from the session list the web UI already holds, archived sessions included, and a session counts
  for the machine that owns it; a part that is zero or unknown is left out), with Tested, or Newer than tested
  when the version is newer than the ones the driver was verified against, and no badge when the version is
  unknown. A configuration that still lacks the value of a variable is marked Key not set. A row opens to the
  configuration: its name and an enabled switch, the new-session defaults (each setting of the driver with its
  default value, which a new session starts from), and, read-only, the command it runs and its variables by name,
  each marked set or not set. The web changes only the name, enabled and the defaults, and never sees a variable's
  value: the command and the variables are created on the machine, and the page shows the command that adds a
  configuration or sets a variable ([ADR 0028](adr/0028-provider-configurations-live-on-the-machine.md)). An
  offline machine shows only its last connection, a connected one with no providers says so, and with no machines
  the section points to Machines. It states that Hoplet never asks for an API key: each provider signs in with its
  own command-line tool on the machine, and a configuration with its own variables keeps them there.
- **Notifications**: a preset (Needs me, Needs me and done, Everything, Off) with an override of the channels per
  event and per project, quiet hours in a time zone of the user's choice, the browsers subscribed to Web Push
  (subscribe this browser, a label, remove), and the webhooks (URL, optional signing secret, the events it
  receives, enable, test, the
  status of the last delivery). Preferences live in the core, not in the browser.

Memory is a later section (#18).

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
    cur_chunk_lines, device_id, owner_epoch, provider, git_branch, title_override, pinned_at, archived_at)`.
    `byte_size` is the sync cursor; `project_path` is the
    `cwd` of the session's lines until a handoff commit sets it to the target's worktree; `device_id` is the
    owner; `owner_epoch` increases with every handoff; `provider` names the vendor (`claude` or `codex`); `title` is the vendor's
    title, and `title_override`, `pinned_at` and `archived_at` (null until set) are what the user did in Hoplet:
    sync and handoff never write them.
  - `session_continuations(from_session_id, to_session_id, request_id, mode, created_at)`: a plan approved into a new
    session (ADR 0025). `from_session_id` is the key and `to_session_id` is unique; there are no foreign keys, since the
    new session is not in the index until its first lines sync. A session keeps its latest continuation, and deleting
    either session removes the row. A session's metadata carries `continuedIn` and `continuedFrom`, each
    `{ sessionId, title }`, when a row names it.
  - `devices(device_id, name, version, os, arch, first_seen, last_seen, token_hash)`. `os` and `arch` are what the agent announced in its last `device.hello` (`GOOS`, `GOARCH`; NULL when it
    announced none). `token_hash` is the SHA-256 of the device token.
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
  `POST /api/sessions/:id/permission/fresh` (approve a plan into a new session);
  the terminal's open and close calls ([ADR 0026](adr/0026-the-inspector-opens-a-restricted-terminal-and-links-dev-servers.md));
  `GET /api/sessions/:id/lines?start=&limit=`; `GET /api/sessions/:id/blocks/:line/:block`;
  `GET /api/search?q=&start=&limit=`; `GET /api/sessions/:id/search?q=&start=&limit=`;
  `POST /api/sessions/:id/prompt`; `POST /api/sessions/:id/permission`; `POST /api/sessions/:id/interrupt`;
  `POST /api/sessions/:id/stop`; `GET /api/sessions/:id/workspace`; `GET /api/sessions/:id/workspace/file?path=&at=`;
  `GET /api/sessions/:id/workspace/diff?path=`; `GET /api/sessions/:id/snapshot/changes`;
  `GET /api/sessions/:id/snapshot/diff?path=`;
  `POST /api/sessions/:id/handoff`; `GET /api/devices` (each device with the `system` its agent announced and the `providers` its connected agent
  announced and their capabilities); `POST /api/devices`;
  `GET /api/devices/:id/dirs?path=` (`path` absent or `~` is the browse root; answers `{ path, parent, root, entries, truncated }`
  where `path` is the resolved absolute path, `parent` is null at the root, and `entries` are `{ name, git }` for the visible
  directories only, sorted case-insensitively, at most 500 with `truncated` set beyond that; `target-offline` when the agent
  is not connected, `invalid-request` with a `reason` for a relative path, a path outside the root, a path that is not a
  directory or one that is unreadable, `timeout`);
  `GET /api/devices/:id/clone?origin=` (`{ deviceId, path }`: the main working tree of a watched project whose
  `origin` is the given one, as a handoff would pick it, or `path: null` with a `clone` offer (`origin` without credentials, `repoName`, the proposed `cloneInto`, `reason`
  and `detail`); the optional `cloneInto` query names the destination to check; `target-offline`, `timeout`);
  `POST /api/sessions/:id/handoff/cancel` (ends a handoff that is in its clone step, which fails as `cancelled`;
  `handoff-not-running` otherwise);
  `GET /api/devices/:id/projects` (each watched path with its symlinks resolved, the directory its sessions record;
  a path watched through a link matches the sessions run in its real directory);
  `POST /api/devices/:id/projects` (`{ path }`, an absolute path on the machine: the agent resolves symlinks, refuses a path that is not an existing directory, adds the resolved path as `hoplet-agent projects add`
  does and answers with the updated listing; `target-offline` when the agent is not connected); `POST /api/devices/:id/projects/remove`;
  `POST /api/devices/:id/sessions/import|remove`; `POST /api/ws-tickets`;
  `PATCH /api/sessions/:id` (`{ title?, pinned?, archived? }`: the session actions above; `session-not-found`);
  `GET /api/sessions/:id/deletion` (what a deletion would remove) and `DELETE /api/sessions/:id` (`{ vendorFile, worktree }`;
  `session-running`, `owner-offline`, `delete-partial`).
- Browser WebSocket `/ws?ticket=`: `subscribe`/`unsubscribe` a session; events `session.appended`,
  `session.deleted`, `session.updated` (title, pin and archive state), `session.runtime` (state), `session.output` (streamed deltas of a subscribed session),
  `session.permission`, `handoff.progress`, `device.presence`.
- Agent WebSocket `/ws/agent`, device token: sync (`device.hello` with the system, the providers and their capabilities,
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
  the core, `device.clone` from the agent), and cloning a repository for a handoff (`device.clone.check|checked`,
  `device.clone.start|progress|done|failed` and `device.clone.cancel`), `ping`/`pong`.
- Terminal and dev servers (ADR 0026), all on the control lane: `terminal.open|input|resize|ack|close` from the
  core; `terminal.opened|output|closed|rejected` from the agent; `ports.watch` from the core and `session.ports`
  from the agent. `device.hello` announces `capabilities.terminal` only when the agent's `terminal` setting is on.
  The browser WebSocket carries a terminal's input and output and `session.ports`. Their routes and messages are
  listed in docs/api.md with the code that exposes them.
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
existing `device_id` replaces its token. Add a machine is available as `hoplet devices add` and in Settings
(Machines), where the token is minted with the same request and the panel waits on the device list.
`GET /api/devices` carries each stored device's `lastSeen` (milliseconds; when its agent last connected or disconnected, absent until it first connects).

### Session sync

The agent connects with `device.hello`, which carries its `device_id` and protocol version; the core refuses a
different protocol version and answers with `welcome`, naming the cursor and `owner_epoch` of every session
the device owns. The agent's providers scan their vendors' session stores every second for sessions imported in `projects.json`. For each one
whose file is longer than the server cursor, it reads from the cursor to the last complete line and sends
`session.append` batches of at most 1 MiB raw bytes, gzip-compressed, each naming the `provider` of its source. A line larger than 1 MiB is sent alone;
lines are at most 32 MiB; a longer line is reported once and blocks only its session at that offset. A file
whose size, mtime and inode did not change is not read again. The core checks ownership, then the start offset against the cursor
(`session.resync` on a mismatch, `session.rejected` for a non-owner), appends verbatim (a new session is stored under the batch's `provider`, which never changes afterwards), commits, acknowledges
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
   While a turn runs the user queues or steers a message instead ([ADR 0020](adr/0020-queued-and-steered-messages.md)):
   a queued message is held by the core (at most 20 per session, kept through reloads, other windows and a
   handoff) and sent as the next turn when the turn ends well; a steered message goes into the running turn and is
   read at its next tool gap, when the provider declares `steer` and no permission request is open. The queue
   pauses on an error or an interrupt until Resume or Clear, and waits while a permission request is open.
   A repeated `promptId` within 10 minutes returns the first result.
2. With no process for the session, the agent starts Claude Code in the session's working directory with
   streaming JSON input and output, resuming that session (`starting`), and sends the control request
   `initialize`; its answer makes the session `ready`
   ([ADR 0008](adr/0008-remote-control-runs-a-hoplet-managed-claude-code-process.md)). When more than
   `runtime_max_idle_processes` processes are `ready` without a turn, it stops the least recently used one.
3. The agent writes the prompt to the process and reports `running`. The agent starts Claude Code with
   `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1`, whatever environment it runs in; the runtime states `running`,
   `waiting-permission` and `ready` follow Claude Code's `session_state_changed` events, and the turn's `result`
   alone does not end the turn (Claude Code may keep working after it). Text deltas, thinking activity (a token
   count), tool calls and tool results flow as `runtime.output`; the core forwards them as `session.output` to clients subscribed to
   the session and keeps none of them. The core adds `toolKind` and `mcp` to a tool call, classified by the session
   provider's mapping as in a stored `tool-call`.
4. A permission request becomes `waiting-permission` and `session.permission` with `requestId`, tool, `toolKind`
   and input. `POST /api/sessions/:id/permission` with `{ requestId, decision: "allow" | "deny" }` answers it; the
   first answer wins. A request without an answer for 10 minutes is denied; a plan to review (ADR 0019) is not and
   stays open. A denied tool call returns an
   error to Claude Code and the turn continues.
5. `POST /api/sessions/:id/interrupt` ends the turn and keeps the process (`ready`);
   `POST /api/sessions/:id/stop` ends the process (`idle`).
6. Claude Code writes the turn to the session file; sync appends it as usual, and the live view replaces the
   streamed output with the stored lines.
7. After a completed turn the agent uploads a workspace snapshot (see Handoff). After 30 minutes without a
   turn the agent stops the process (`idle`).

After every turn the agent records the session file's size; lines Claude Code appends in the 5 seconds after a
turn result (title, cost) are Hoplet's own. When the file grows beyond it later, or grows while no
Hoplet process runs for the session, the state is `external`: the agent stops its process for the session, and
a prompt is refused until the file has not grown for 60 seconds. When the owner disconnects, its runtime states become `offline`;
when it reconnects, it reports the state of every process it still runs.

### Notifications

The core tells the user when a session needs them, so a turn does not wait unseen (ADR 0021).

- **Events**: a permission request (`permission`), a plan to review (`plan`), a finished turn (`turn-done`) and
  a failure (`failure`: a session that failed to start, or a handoff that failed; a refused or cancelled handoff
  is not one). A finished turn is not sent while a browser has the session open.
- **Which events go out**: the preset decides (Needs me: permission, plan and failure; Needs me and done adds
  turn-done; Everything the same events on every channel; Off none), then the overrides of the event, then those
  of the project. An override names the channels of an event, none turns it off. Quiet hours hold back turn-done
  and failure but never the events that need the user (permission and plan).
- **Channels**: Web Push to the subscribed browsers and a JSON `POST` to each enabled webhook, signed with
  `X-Hoplet-Signature: sha256=<hex>` when the webhook has a secret. A webhook receives an event only when the
  preferences route it to the webhook channel and the event is one of the webhook's own events (permission, plan
  and failure unless chosen otherwise). A native client is a third channel that is
  not sent yet. Each send runs apart from the event that caused it, waits at most 5 seconds and is retried once
  after a network error, a timeout or a 5xx answer; a push service that answers 404 or 410 loses its
  subscription. The last delivery of each subscription and webhook is kept and shown.
- **Content**: the kind, the project's name, the session title and, for a permission, the tool and its command
  or path redacted (tokens, keys, URL credentials) and cut to 80 characters. Never the prompt, a file or a diff.
- **Action links**: a permission or plan notification carries a link, `<public URL>/a#<token>`, to a page that
  shows the request and offers Allow and Deny without the host token. The token is signed, bound to one request
  and session, decides once, and lasts as long as the request but at most 10 minutes; a plan lasts 10 minutes and
  is approved with its default mode, never into a new session. A Bash command with the word `rm`, `sudo` or `git push`, a Write or Edit
  outside the project, every MCP tool and a request whose input is not recognised show `webui-only` and are
  answered in the web UI. This list catches slips, not an attacker: the link token is the boundary. Without a configured public URL, notifications carry no link.

### New session

`POST /api/sessions` with `{ deviceId, projectPath, provider, config, text, promptId }`, where `config` maps
option IDs to values and holds only options the provider's runtime driver declares, with a value it declares
(a `select`'s value, a `boolean`'s true or false); an option left out has the driver's default. A browser reads the capabilities before any
session exists from `providers` in `GET /api/devices`, which the agent announces in `device.hello`. Each provider also carries the
installed vendor `version` and `tested`, false when that version is newer than the tested ones (older ones are
refused and carry neither).

The core refuses with `target-offline` when the device is not connected, with `invalid-request` when the body
is malformed, the device announced no such provider, a config option or value is not declared (`reason: config`) or
`projectPath` is not one of the device's watched projects (`reason: projectPath`), with `invalid-request` and
`reason: images` for `attachments` when the provider's driver does not declare images, and with `too-large` for
a prompt over 8,000 characters. Otherwise it forwards `runtime.start` to the device on the control lane. The
agent checks the same again, starts Claude Code in that directory with streaming input and output and no
session to resume (`--session-id` with a new ID and the config as flags: `--permission-mode`, `--model`, `--thinking enabled|disabled` and, for `fast`, the
setting `fastMode` of that one process), writes the prompt, waits until Claude Code has created the session
file, adds the session to `imported` in `projects.json`, and answers `runtime.started` with the session ID. If
Claude Code exits or does not create the file within 60 seconds, the agent stops the process and answers
`runtime.start.rejected` with the reason, which the core returns as `rejected` with `reason`; with no answer
within 75 seconds the core returns `timeout`. The response is `{ ok: true, sessionId, promptId }`. A repeated
`promptId` within 10 minutes returns the first result, except after a refusal, which can be retried. From then
on the session syncs and is driven as any other (Remote control); the agent reports its runtime state again
after the answer, and until the session reaches the index the core accepts runtime messages for the session from
that device, reports its state to clients and sends that device the session's control requests (a steer, an
interrupt, a permission answer).

#### A plan approved into a new session

`POST /api/sessions/:id/permission/fresh` with `{ requestId, mode, promptId }` approves an open plan request into a
new session in one call ([ADR 0025](adr/0025-a-plan-can-be-approved-into-a-new-session.md)). The plan is read from
the open request, never from the client. The core refuses with `request-closed` when the request is not an open plan
request of the session, with `invalid-request` and `reason: fresh-context` when the driver did not declare
`freshContext` and `reason: mode` when `mode` is not one of `planExit.modes`, and with `owner-offline` when the
session's owner is not connected. Otherwise it
starts a new session on the owner device, in the session's project and with the same provider, as for a new session
(`runtime.start`, with `config` `{ [planExit.option]: mode }`). The first prompt is a short line that frames the
plan, the plan and the path of its plan file. Only when the start succeeds does the core record the link, refuse the
plan in the old session with "This plan is being implemented in a new session." and interrupt its turn (a turn that
is not running is fine). A plan answered elsewhere while the new session started leaves the link recorded; that
plan is neither refused nor interrupted. A second call for a plan whose new session is starting is
`request-closed`. When the start fails (`timeout`, or `rejected` with the agent's `reason`) nothing changes, the plan
waits and the usual start-failure notification is raised. The response is `{ ok: true, sessionId, promptId }`, the new
session; `promptId` is deduplicated as for a new session: a repeat within 10 minutes returns the first result,
except after a refusal, which can be retried.

### Handoff

`POST /api/sessions/:id/handoff` with `{ targetDeviceId, targetPath? }`. The core refuses when the target is
not connected, the target is the owner, a handoff of this session is in progress, or the session is `running`
or `waiting-permission`. Progress is reported to every browser as `handoff.progress`: the step, the time of the
snapshot of an offline handoff, the bytes sent of the archive and the stored stream during transfer (at most
four times a second), and on `done` the new worktree and branch, which the response also carries
(`{ ok, ownerDeviceId, ownerEpoch, worktree, branch, snapshotAt? }`).

0. **Clone (only with `cloneInto`).** `device.clone.start` to the target, before the owner is touched: it clones
   the session's `origin` into `cloneInto` with a restricted git, within 600 s and 60 s without progress, and
   registers the clone as a watched project. A failure or a cancel fails the handoff (`clone-failed`,
   `cancelled`) with the owner and the cursor unchanged.
1. **Prepare (owner online).** `handoff.prepare` to the owner: it stops the session's Claude Code process,
   sends the remaining bytes, and creates a snapshot: base commit, `origin` URL, binary diff of tracked
   changes, untracked files that are not ignored, and a git bundle of commits that `origin` does not contain.
   The snapshot travels to the core in ordered chunks of at most 1 MiB. With the owner offline, the core uses
   the latest stored snapshot and reports its time.
2. **Install.** `handoff.install` to the target with the snapshot and the stored session stream. The target
   picks the clone: a watched project path whose `origin` equals the snapshot's (the one just made, after a
   clone step), else `targetPath`; without either the handoff fails with `no-clone`. It adds a git worktree next to that clone, named
   `<clone directory>-<first 8 characters of the session ID>`, on branch `hoplet/<first 8 characters>`, at the
   base commit, applies the diff and untracked files, writes the session file into the store of the session's vendor (`handoff.install` carries `provider`) for
   the worktree path, adds the worktree to `projects.json` with the session imported, and answers
   `handoff.installed` with the worktree, its branch and the clone it belongs to. The store directory
   is the realpath of the worktree with every character other than ASCII letters and digits replaced by `-`
   (one `-` per UTF-16 unit); if the result is longer than 200 characters, it is truncated to 200 characters
   followed by `-` and the base-36 absolute value of a 32-bit hash of the realpath over its UTF-16 units.
   The session file is written byte for byte; sidecar files Claude Code keeps beside it are not carried.
   For a Codex session the file is written uncompressed (a `.jsonl.zst` source arrives as its uncompressed stream)
   as `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<time>-<threadId>.jsonl`: the date and time are those of the first
   line's `session_meta` timestamp, in the target machine's local time, as Codex names its rollouts. No other file
   under `$CODEX_HOME` is written, and Codex's own index is left alone; it finds the rollout by its thread ID. The
   install fails with `session-exists` when the target already has a rollout of that thread anywhere under
   `sessions/`, in either encoding, and never replaces it. The working directory of the worktree is not stored in
   the file; it is passed to Codex when the thread is resumed.
3. **Verify.** Before any agent process starts in the worktree, the target reports the SHA-256 and size
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
- [ ] Rename, pin and archive of a session reach every open window, survive a restart of the core and change no stored line or cursor
- [ ] With `terminal` off on a machine, no terminal opens there; with it on, a terminal opens only while no turn
      runs, a started turn closes it, and a flood of output does not delay a prompt
- [ ] A dev server started from a session's directory is listed with its port; Hoplet carries none of its traffic
- [ ] An 8-hour live session stays within the web UI memory budget
- [ ] `docker compose up -d` serves the web UI on `127.0.0.1` and the health check passes
