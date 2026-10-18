# 0027 Codex runs on its app server and enters at level 2

- Status: proposed
- Date: 2026-10-18
- Related: ADR 0002, ADR 0006, ADR 0007, ADR 0009, ADR 0012, ADR 0015, ADR 0017, ADR 0018, ADR 0019, ADR 0020,
  issues #5, #156

## Context

The v0.7 theme is a second provider, and the maintainer chose Codex. ADR 0015 asks a new vendor two separate
questions: which protocol controls it, and where its history comes from. The spike (#156) answered both against
codex-cli 0.163.0. It read the app server's generated schemas (`codex app-server generate-json-schema
--experimental`) and the `codex-acp` package. It then ran eleven short turns through `codex app-server` on a
small model. What it found:

**Control.**

- Codex's own interface is `codex app-server`: JSON-RPC over standard streams, with threads, turns, items and
  server requests for approvals.
- `codex-acp` is a separate Node package that starts `codex app-server` and translates it to ACP. Codex itself
  does not speak ACP.
- Everything Hoplet's runtime model uses is on the app server:
  - `thread/start` and `thread/resume` (with `excludeTurns`, so nothing is replayed).
  - `turn/start`, `turn/interrupt` (the turn ends `interrupted` within milliseconds) and `turn/steer`.
  - Approval requests, the model list with each model's reasoning efforts, `thread/settings/update`, and plan
    mode as a collaboration mode.
- `turn/steer` takes the expected turn ID. The message arrives as a user message item between the running tool
  call and the next model step, and the model acts on it. With no turn running, the call fails with
  `no active turn to steer`.
- Approval requests come in two shapes, `item/commandExecution/requestApproval` and
  `item/fileChange/requestApproval`. Answers are `accept`, `acceptForSession`, `decline` and `cancel`, and none of
  them carries a reason. After a `decline` the model tries another way, which can raise another request.
- A plan turn streams a `plan` item and then ends `completed`. No request asks to leave plan mode.
- Codex assigns the thread ID, a UUIDv7, and no field of `thread/start` chooses it.
- Only `--experimental` runs expose `collaborationMode` and `thread/settings/update`.

**History.**

- A thread's rollout file is `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<time>-<threadId>.jsonl`, and
  `thread/start` and `thread/resume` return its path.
- The file is created by the first turn.
- Its first line is `session_meta`, with the ID, the `cwd` and the full base instructions (about 70 KB).
- Each line is one JSON object with an `ordinal`. Every copy taken before a turn, a resume or an interrupt was a
  byte prefix of the file after it, and the file always ended in a newline.
- Codex compresses older rollouts in place to `.jsonl.zst`.
- In an empty `CODEX_HOME` with no index, a rollout copied unchanged under `sessions/` resumed by thread ID from
  a different working directory:
  - Codex added its own index row.
  - It appended to the copy, and the new `turn_context` recorded the new `cwd`.
  - The copied bytes stayed a prefix.
- A `.jsonl.zst` copy resumed too. Codex decompressed it in place to the same bytes plus appended lines.
- A resume without a turn still appends a `thread_settings_applied` line.

## Decision

**Control: a driver of its own on the app server.**

- `agent/internal/runtime/codex/` runs `codex app-server` and speaks its JSON-RPC.
- The generic ACP driver stays unbuilt. ADR 0015 sends to it a vendor *that speaks ACP*, and Codex reaches ACP
  only through a third-party Node adapter over this same server. Using the adapter would add a Node runtime
  beside the agent's single binary. It would also lose the shape of approvals and settings in translation.
- The driver implements `runtime.Versioned` and tests against named codex-cli versions. It requests the
  experimental API, which plan mode and setting changes need.

Codex's controls map onto the runtime model (`agent/internal/runtime/types.go`) as follows:

- **Session ID.** A Codex session's Hoplet session ID is its thread ID, so a session started in a terminal and
  one Hoplet starts are found the same way (ADR 0006).
  - For a new session the driver, not the runtime manager, chooses the ID: a driver whose vendor assigns IDs
    starts the thread and returns its ID.
  - The agent answers `runtime.start` with that ID, as it already answers with the ID it mints for Claude Code.
- **Prompt, steer, interrupt.**
  - A prompt is `turn/start`; images are `localImage` inputs naming the uploaded file (ADR 0017).
  - A steer is `turn/steer` with the running turn's ID. This matches ADR 0020's delivery at the next tool gap.
    When the turn has ended, the driver runs the message as the next turn, as `Process.Steer` promises.
  - An interrupt is `turn/interrupt`.
  - A process resumes with `thread/resume` and `excludeTurns`, so it never replays a whole session (ADR 0007).
- **Permissions.**
  - Both approval requests become `PermissionRequest`s; the answers are `accept`, `acceptForSession` and
    `decline`.
  - A refusal's message has no field in Codex. The driver declines and then steers the message into the running
    turn, so the model reads it before it tries another way.
- **Settings (ADR 0018).** The driver declares Codex's own options in Codex's terms:
  - `mode`: `read-only`, `workspace-write`, `auto-review` and `full-access`, each a pair of approval policy and
    sandbox. `full-access` is marked high risk.
  - `model`, from `model/list`.
  - `effort`, whose values follow the chosen model.
  - `collaboration`: `default` or `plan`.
  - Each applies at the next turn through `turn/start`'s overrides. `thread/settings/updated` reports a change
    Codex makes itself.
- **Plan exit (ADR 0019).** Codex has no approval for leaving plan mode, so the driver declares one:
  - When a plan-mode turn completes with a `plan` item, the driver raises a `PermissionRequest` carrying
    `planExit`. Its plan is the item's text, and its `modes` are the `mode` values other than `full-access`.
  - Approval starts the next turn in the `default` collaboration mode with the chosen mode.
  - A refusal's message starts the next turn still in plan mode.

**History: level 2.** The spike verified every level, and ADR 0015 enters a vendor at the highest level its
spike verifies.

- **Provider.** `agent/internal/provider/codex/` discovers rollout files under `$CODEX_HOME/sessions`. It reads
  each session's ID and `cwd` from the first line.
- **Cursor.** The cursor counts the bytes of the uncompressed rollout. A `.jsonl.zst` file is the same session
  file in another encoding: the provider reads it by decompressing from the start up to the cursor. The cursor
  never counts compressed bytes (ADR 0002).
- **Store.** `provider.Store` installs a session file unchanged under `sessions/` at the date and name of its
  original, never replacing an existing file. It writes no Codex index: resume finds the file by its thread ID.
  - A file installed from a `.jsonl.zst` source is installed uncompressed.
  - The target's working directory goes to `thread/resume`. Codex records it in the next `turn_context` and
    rewrites nothing.
- **Mapping.** `core/src/api/projection/codex.ts` reads `response_item` records (messages, reasoning, function
  and custom tool calls and their outputs) into the conversation model, and classifies Codex's tools into tool
  kinds. `session_meta`, `turn_context`, `event_msg`, `token_usage_record` and `world_state` are Codex's
  bookkeeping.

## Cost

- **Upgrades.** A driver of Codex's own protocol breaks when the app server changes, and part of what it uses is
  marked experimental. Each Codex upgrade needs the driver tested and its version list extended.
- **Large first line.** Every session file starts with about 70 KB of base instructions that the store keeps and
  reads elide.
- **Compressed files.** Reading a compressed rollout decompresses it from the start each time, and the agent
  carries a zstd decoder.
- **Resumes write.** A resume alone appends a line, so opening a session's process changes its file even without
  a turn.
- **Refusal reasons.** A refusal's reason reaches the model as a steered message rather than as the refusal
  itself.
- **Approval answers.** Codex's narrower answers, an execpolicy amendment and a network rule, are not offered.

## Revisit triggers

- Codex ships ACP as its own interface.
- The app server lets a client choose the thread ID or carry a refusal reason.
- Codex stops writing append-only rollouts, or rewrites lines already written.
