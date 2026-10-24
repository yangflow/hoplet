# 0039 Sessions continue across a disconnect, and the agent replays its events

- Status: accepted
- Date: 2026-10-24
- Related: ADR 0002, ADR 0009, ADR 0029, ADR 0030, docs/spec.md, docs/architecture.md#wire-protocol, docs/hosting.md,
  docs/threat-model.md, design/screens/offline.html, issue #95

The maintainer chose the recommended option of every decision below, and direction A of
`design/screens/offline.html` for the web UI: each place says it in place (the row's second line, the turn's badge,
the permission card, the composer and the handoff dialog).

## Context

Issue #95 asks that a session keep running when the link between the agent and the core drops; that the agent
hold what it could not send in a buffer with a cap and replay it by sequence number when the link returns, the
core dropping what it already has; that the web UI show the machine offline and the session as "may still be
running"; that a permission request stay pending across the disconnect, be sent again after it and never be
approved on its own; and that a handoff that moved the owner (`owner_epoch`) is not overwritten by the old owner's
replay.

What the code did before this record:

- **A session keeps running.** The agent's process manager (`agent/internal/runtime`) does not depend on the link;
  a Claude Code or Codex process goes on and writes its transcript.
- **The transcript catches up by byte offset.** The welcome names each session's `byte_size` and `owner_epoch`;
  the agent resumes each transcript at that offset, and `session.resync` realigns a session whose bytes disagree.
  The core is the cursor (ADR 0002), so the transcript needs no sequence number and no buffer: the file on the
  machine is the buffer.
- **The agent reports again on reconnect.** `report()` (`agent/internal/daemon/runtime.go`) sends the rule
  decisions it held while not connected, then each running session's state, its open permission requests and its
  commands.
- **A permission is never approved on its own.** Every timeout and closure denies.
- **A handoff while the owner is offline is fenced.** The core compares and sets `owner_epoch` with the device;
  the old owner's appends get `not-owner`, and its next welcome stops its process for that session.
- **Presence.** The core pings every 30 s, the agent every 20 s with a 90 s read deadline; `device.presence` marks a
  machine offline in the web UI, and a prompt to an offline owner is refused with `owner-offline` and stays in the
  composer or the queue.

What is missing:

1. **A permission request ends at the disconnect.** When the machine detaches, the core closes its open requests as
   abandoned and writes a `permission.answered` audit record (deny, by `session-end`); the web UI closes the card.
   On reconnect the agent sends the same request again and the core opens it as new: a second
   `permission.requested` record, a second notification and hook delivery, and a fresh expiry, while the agent's
   own 10-minute clock (`PermissionTimeout`) kept running through the outage and may deny it moments later.
2. **Lifecycle events during the outage are lost.** The core sets every session of a machine that connects to
   idle and the agent reports only sessions that are still running, so a turn that finished or a session that
   ended during the outage never produces `session.runtime`, its notification or its hook. The turn's lines arrive;
   its events do not.
3. **The web UI cannot say "may still be running".** On offline the live turn settles and permission cards close;
   the session shows as idle on an offline machine.
4. **The queue does not resume.** A queued message waits for a turn end; a session that is idle after the
   reconnect gets none, so its queue stays until the next turn.
5. **Rule decisions sent into a dead socket are lost.** The agent holds decisions only when it knows it is not
   connected; in the up to 90 s before its read deadline fires, a write that the socket accepted is gone.
6. **Live data is not held**: streamed output (the transcript replaces it), hook outcomes, resource samples and
   session MCP calls. Only the last three matter, and they are best effort today.
7. **Appends carry no epoch.** The device check fences the old owner, but a handoff A to B and back to A lets A's
   stale appends in, and lines A wrote after a handoff it did not see diverge silently.
8. **The welcome lists at most 5,000 sessions.** A machine with more sessions resumes the rest from zero bytes on
   their next change, which `session.resync` repairs at the cost of a re-read.

## Decisions

1. What the agent buffers, and how. Chosen: a journal of events on disk with a sequence number per machine,
   capped, never holding transcript lines.
2. Permission requests across a disconnect. Chosen: the request stays open as "waiting for the machine", the
   agent's clock pauses, the agent sends it again and the core matches it by id.
3. Handoff conflicts. Chosen: appends carry `owner_epoch`, and an old owner reports what it wrote after it lost the
   session.
4. What the web UI shows. Chosen: the last known state kept as "may still be running", and "catching up" while the
   backlog drains, said in place (direction A).
5. Notifications and hooks for replayed events. Chosen: replayed events fire hooks with their original time;
   notifications only for what can still be acted on, plus one summary.

Two changes need no decision and go with the implementation: the queue drains for every session that is idle
after a reconnect, and the agent keeps every rule decision until the core confirms it (decision 1 covers it).

## Decision

### What the agent buffers, and how (decision 1)

| Option | Cost | Verdict |
|---|---|---|
| A. Keep today's in-memory queue of rule decisions only | None | Leaves gaps 2, 5 and 6; loses everything when the agent restarts during the outage |
| **B. A journal on disk of events: turn and session state changes, permission requests and their closures, rule decisions** | A file in the agent's state directory, a sequence number per machine, a confirm message, a cap | **Chosen**: one mechanism replaces the re-report of states, permissions and decisions; it survives an agent restart |
| C. Derive lifecycle events from the transcript in the core | No agent change | The transcript does not record permission requests, hook outcomes or a failed start, and Codex and Claude Code write turn ends differently |
| D. Buffer transcript lines too | Doubles the transcript on disk | The transcript file already is the buffer and the byte offset the cursor (ADR 0002) |

- **File.** `journal.ndjson` in the agent's state directory (`hoplet_home`), mode 0600: one line per event, a line
  per confirmation, rewritten without the confirmed events from time to time. The agent journals
  `runtime.state`, `runtime.permission`, `runtime.permission.closed`, `runtime.permission.auto` and `journal.gap`.
- **Sequence.** Every journalled event gets `eventSeq`, monotonic per machine across restarts. The core stores the
  highest `eventSeq` it has applied per device (`devices.event_seq`) and names it in the welcome; the agent replays
  what is after it, in order, and drops what is at or before it. The core confirms with `journal.ack`. A replayed
  event the core has already applied (a crash between apply and confirm) is a no-op by `eventSeq`.
- **Cap.** 10,000 events or 4 MiB, whichever comes first. At the cap the agent drops the oldest events that are not
  open permission requests and writes one `journal.gap` event with the number dropped and their time range; the
  core audits the gap. Open permission requests are never dropped.
- **Order.** On reconnect the agent sends the journal, then `device.backlog` and the transcript appends, then the
  state of every running session and `runtime.reported`. Journalled events are small control messages; the
  transcript keeps the bulk lane (docs/architecture.md#principles).
- **What stays unbuffered.** Streamed output (the transcript replaces it), hook outcomes and resource samples (a
  gap in a graph), and session MCP calls (the call fails and the session retries).

### Permission requests across a disconnect (decision 2)

| Option | Cost | Verdict |
|---|---|---|
| A. Today: the core closes the request as abandoned and opens it again on reconnect | None | Two audit records and two notifications for one request; the card disappears while the session is still waiting |
| **B. The request stays open as "waiting for the machine"; the agent's clock pauses while disconnected; the request is sent again and matched by `requestId`** | A pending state on the core, a paused timer on the agent | **Chosen**: one request, one record, one notification; the person sees what the session is waiting for |
| C. B, but the agent's clock keeps running | No timer change | A short outage near the end of the 10 minutes denies a request no one could answer |

- On detach the core marks each open request of the machine `unreachable`; it is not closed and no
  `permission.answered` record is written. The web UI keeps the card with its buttons disabled and says that an
  answer waits for the machine to reconnect.
- The agent pauses `PermissionTimeout` while it is not connected and resumes it with the time left. A request that
  waits longer than a cap (24 hours) is denied by the agent on its own, as a timeout, and journalled.
- On reconnect the agent replays the request; the core matches `requestId`, marks it open again with the time it
  had left and sends `session.permission` to the web UI, with the original `requestedAt`. No second
  `permission.requested` record and no second notification are written. A request first made while the machine was
  away is notified after the machine's `runtime.reported`, if it still waits then.
- An answer is only sent when the machine is connected; nothing replayed ever approves. A request the agent
  closed during the outage (a timeout) is replayed as `runtime.permission.closed`; one whose process ended is not
  named in the report after the reconnect, and the core closes it at `runtime.reported`.

### Handoff conflicts (decision 3)

| Option | Cost | Verdict |
|---|---|---|
| A. Today: the device check only | None | Fails for A to B to A; lines written by the old owner after it lost the session diverge silently |
| **B. `session.append` carries `owner_epoch`; the core refuses a stale epoch with `not-owner`; the old owner reports `session.diverged` with the bytes and lines it wrote after the epoch it last held** | One field and one message in the wire protocol | **Chosen**: the core keeps one history per session and the person learns that another exists |
| C. B, and the core stores the diverged lines as a forked session | A fork path in ingest | Later, if diverged work is common; B keeps the bytes on the machine |

- The core audits `session.diverged` and the web UI shows a notice on the session that names the machine, the
  number of lines and that they remain in the transcript file on that machine.
- The handoff dialog warns when the owner was running a turn when it was last heard: the work may still be going
  on and its result stays on that machine.

### What the web UI shows (decision 4)

- `session.runtime` with state `offline` carries `lastState` (the state before the disconnect) and `since`. A session
  whose last state was a turn state shows as "may still be running, last heard N ago", its live turn kept frozen
  rather than settled; an idle session shows offline only.
- On reconnect the agent reports the bytes it has to send per session (`device.backlog`); `session.runtime`
  carries `catchUp` with the bytes received and the total until the backlog is in, and the web UI shows "catching
  up" with the progress.
- The composer refuses a prompt with the offline reason and offers the handoff, as today.

### Notifications and hooks for replayed events (decision 5)

| Option | Cost | Verdict |
|---|---|---|
| A. Fire everything as if live | None | A burst of stale notifications after a long outage |
| **B. Hooks fire for every replayed event with its original time and `replayed: true`; notifications fire only for what can still be acted on (an open permission request), plus one summary per machine ("MacBook Pro reconnected: 3 turns finished, 1 session ended")** | A field in the hook payload, a summary notification | **Chosen**: automations see every event; a person is not flooded |
| C. Drop replayed events for hooks and notifications | None | Automations that wait for a turn end never fire |

## Cost

The protocol goes from version 23 to 24 in `shared/src/wire/` and `agent/internal/sync/protocol.go` in the same
pull request: `eventSeq`, `at` and `replayed` on journalled messages and `journal.ack`, the welcome's `eventSeq`,
`ownerEpoch` on `session.append`, `session.diverged`, `journal.gap`, `device.backlog`, `runtime.reported`,
`runtime.permission.closed`, and to browsers `lastState`, `since` and `catchUp` on `session.runtime`, `unreachable`
and `requestedAt` on `session.permission`, and `session.divergence`. The agent
gains a journal file with its cap, a paused permission clock and replay ordering; the core a high-water mark per
device (`devices.event_seq`, migration 0039), the `unreachable` permission state, the epoch check on appends, the
summary notification and queue drain after reconnect; the web UI the frozen live turn, the disabled permission card, the catching-up progress and the
diverged notice, with strings in `web/src/i18n/zh-Hans.json`. Tests cover a disconnect during a turn, during a
permission request, an agent restart during the outage, the cap, a handoff A to B to A and a replay after a crash
between apply and confirm; the end-to-end runtime test drops the agent's link during a permission request.

## Alternatives

- *Keep the link alive harder* (shorter pings, a second transport). Shortens the window but a laptop that sleeps or
  roams still disconnects; the gaps remain.
- *Move permission answers through the transcript.* Claude Code and Codex do not read answers from the transcript.

## Documents that change with the implementation

The pull request that implements this record updates docs/spec.md (the session and permission behaviour across a
disconnect, the offline states in the web UI, hooks' `replayed`), docs/api.md (the browser messages: `lastState`,
`since`, `catchUp`, `unreachable`, `requestedAt`, `session.divergence`; the audit types; hooks' `replayed`; the
summary notification), docs/architecture.md#wire-protocol (version 24, the journal, the order of replay),
docs/hosting.md (the journal file in the agent's state directory and its cap; `devices.event_seq`) and
docs/threat-model.md (below).

## Trust boundary changes

None is added or moved. The journal is a new file on the machine at mode 0600 in the agent's state directory; it
holds permission request summaries (a tool name and its input preview, as the core already stores) and rule
decisions, which docs/threat-model.md lists with the agent's other local state. A replayed event can deny or close
a request but can never approve one, and the core refuses a stale `owner_epoch`, so a machine that was offline
cannot take a session back by replaying.

## Revisit triggers

- Outages long enough to reach the cap are common in the audit's `journal.gap` records.
- Diverged sessions are common enough that people ask for the fork (decision 3 C).
- A provider exposes permission answers that do not need the agent connected.
