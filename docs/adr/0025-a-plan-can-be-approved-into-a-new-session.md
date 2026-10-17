# 0025 A plan can be approved into a new session

- Status: accepted
- Date: 2026-10-17
- Related: ADR 0002, ADR 0012, ADR 0018, ADR 0019, ADR 0021, ADR 0022, docs/api.md, docs/architecture.md, issue #112

## Context

A plan is written after a long exploration, and the exploration is most of the context window. Claude Code offers
to approve the plan and clear that context, so the implementation starts from the plan alone. ADR 0019 left that
choice out because it needs a new session seeded with the plan.

Hoplet can start a session on a machine (`POST /api/sessions`, `runtime.start`) and knows the plan, the file it
lives in and the options approval switches. The pieces exist; what is missing is the order in which they run and
what happens to the old session.

## Decision

**The driver declares the choice.** `planExit` gains an optional boolean `freshContext`, set by a driver whose
vendor can carry a plan into a new session. The Claude driver sets it. Without it the web UI does not offer the
choice. The wire protocol gains no message and `PROTOCOL_VERSION` stays as it is: the field is optional.

**One core call does the whole flow.** `POST /api/sessions/:sessionId/permission/fresh` takes
`{ requestId, mode, promptId }` and answers `{ ok: true, sessionId, promptId }`, the new session. The plan is read
from the open request, never from the client. The core checks that the request is an open plan request of this
session from its owner, that `planExit.freshContext` is set, that `mode` is one of `planExit.modes` and that the
owner is online. It starts the new session on the owner device, in the session's project, with the same provider,
through the path of `POST /api/sessions` (`runtime.start`): the first prompt is a short framing line, the plan and
the path of its plan file, and the config is `{ [planExit.option]: mode }`. Only when the start succeeds does the
core record the link, refuse the plan in the old session with the message "This plan is being implemented in a new
session." and interrupt the old session's turn (a turn that is not running is fine). If the plan was answered
elsewhere while the new session started, the link is still recorded and the refusal is skipped. If the start fails
nothing changes and the plan waits; the failure raises the usual start-failure notification. The idempotency of
`promptId` is that of a new session.

**The link is stored, not inferred.** A table `session_continuations(from_session_id, to_session_id, request_id,
mode, created_at)` has no foreign keys, because the new session is not in the index until its first lines sync. A
session keeps its latest continuation: a later fresh approval replaces the link. Deleting either session removes
the link (ADR 0023). Session metadata carries optional `continuedIn` and `continuedFrom`, each
`{ sessionId, title }`; the web UI shows one resolved line in the old session and an origin line in the new one,
each a link to the other.

**The web UI offers it inside the plan card.** The Approve menu ends with "Clear context and approve…", which turns
the card footer into a confirmation naming the machine, the project, the mode and what the new session starts
from. While the new session starts the card is locked; on failure the plan still waits with a note. The plan
action link of a notification (ADR 0021) does not offer it. The Mac client (ADR 0022) calls the same route.

## Cost

One optional field, one route, one table with its migration, two optional fields of session metadata, a state of
the plan card and a line in each session. The prompt of the new session is the plan the agent sent, so it is not
held to the length limit of a prompt the user types. The old session keeps its history and stays in Plan mode,
stopped by the refusal. Nothing of the old conversation reaches the new session except the plan and its file,
which is the point; a session that needed more would be a handoff.

This reverses the sentence in the Cost section of ADR 0019 that says Hoplet does not offer Claude Code's "clear
context and approve". ADR 0019 stays as written.

## Alternatives

- *Send `/clear` in the same session.* The transcript and the byte cursor (ADR 0002) would carry a context reset
  that the UI must explain, and one session would hold two unrelated contexts. Rejected.
- *Leave it out.* The user copies the plan into a new session by hand, losing the plan file and the mode.
- *The client starts the session, then answers the plan.* Two calls from every client, each with its own failure
  order; a client that dies between them leaves a running session and a plan that still waits.
- *Refuse the plan first, then start.* A failed start would leave the old session without its plan.

## Revisit triggers

A vendor can approve a plan into a new context without a new session; a plan needs more than the plan and its file
to start (a summary of the exploration); a session needs more than one continuation.
