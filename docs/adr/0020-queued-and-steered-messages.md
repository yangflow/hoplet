# 0020 Queued and steered messages

- Status: accepted
- Date: 2026-10-16
- Related: ADR 0002, ADR 0008, ADR 0012, docs/architecture.md#wire-protocol, issue #87

## Context

While a turn runs, `POST /prompt` answers `busy`: the user has to wait to say the next thing, or interrupt the turn
and lose it. Claude Code accepts a message on stdin mid-turn and reads it at the next gap after a tool result,
within the same turn (one `result`); with a text-only turn it becomes the next turn. Its own queue cannot be edited
or reordered, and a message sent during a pending permission request is not processed reliably.

## Decision

**Two ways to send while running.** *Queue*: the core holds the message and sends it as the next turn when the
current one ends well. *Steer*: the message goes into the running turn and the model reads it at the next tool gap.
Idle sessions send as before.

**The core holds the queue.** A queue per session lives in `hoplet.db` (`session_queue`, `session_queue_state`),
survives a reload and a restart, is shared by every window (`session.queue` on the control lane, then the browser
reads `GET /queue`), and stays with the session through a handoff: it continues on the new owner. A message has an
ID chosen by the client and a version; edit, delete and steer are compare-and-set on the version (`queue-conflict`
when stale). The head message being sent is `sending` and cannot be edited, moved or removed. At most 20 messages,
each within the prompt limits.

**The core drains.** After a turn ends successfully, and when the session turns idle or ready, a message is added,
the queue is resumed or a handoff ends, the core sends the head with `runtime.prompt` and sends the next one when
that turn succeeds. A rejected or timed-out send pauses the queue with `error`; `busy`, `starting`, `external` and
an absent owner put the message back untouched.

**Pauses.** `error` and `interrupted` are stored and end only by Resume or Clear. A pending permission request
pauses sending until it is answered, derived from the runtime state and not stored; steering is unavailable
then. A restart pauses a non-empty queue as `interrupted`. An ended or archived session keeps its queue.

**Steer is a driver capability.** `RuntimeCapabilities.steer`; without it the UI offers queue only. `runtime.steer`
carries a `steerId`; the agent answers `runtime.steered` once it wrote the message to the process, or
`runtime.rejected` with the `steerId` as `promptId`. The core dedupes a `steerId` for 10 minutes and the audit log
records the steer, never its text. The transcript shows the steered message from the session file
(`attachment queued_command`), flagged `steered` on the entry.

**The web UI.** An "Up next" tray above the composer lists the queue; the send button has a Queue/Steer menu.
While running Enter queues and Alt+Enter steers, reversed when the Settings preference `sendMode` is steer (kept
in the browser).

## Cost

Two tables (migration 0013), three wire messages (`runtime.steer`, `runtime.steered`, `session.queue`) and nine
routes; `PROTOCOL_VERSION` stays 5 as an agent without `steer` never receives `runtime.steer`. Queued images are
uploads on the owner device: after a handoff a missing upload makes the agent reject, and the queue pauses with
`error`. A steered message may show twice in the transcript (the local row and the file's entry) until the
session file arrives.

## Revisit triggers

A vendor can edit or reorder its own queue; steering needs a guarantee of delivery within the turn; a queue is
wanted for sessions on several providers with different rules; queued images must survive a handoff.
