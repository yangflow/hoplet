# 0024 A handoff clones the repository on a target that has none

- Status: accepted
- Date: 2026-10-17
- Related: ADR 0002, ADR 0009, ADR 0022, ADR 0023, docs/api.md, docs/threat-model.md, docs/hosting.md, issue #13

## Context

A handoff installs the snapshot into a clone of the same `origin` on the target (ADR 0009). When the target has no
clone, the check answers `no-clone` and the user must clone by hand on that machine and start over. That is the
commonest first handoff to a new machine.

Facts that shape the design. `workspace.ResolveClone` finds a clone among the projects the agent watches and treats
the ssh and https forms of one repository as equal. `Install` creates the worktree `<clone>-<8>` on `hoplet/<8>` and
registers it as a project. The `handoff.*` messages run one at a time through the agent's ordered queue, so a clone
of minutes would block every other handoff message on that machine; messages of other types run in their own
goroutines. The core refuses a connection whose protocol version differs at `device.hello`
(`protocol-version-mismatch`) and the agent closes on a Welcome with another version: there is no negotiation, so an
older agent is refused, not degraded. A clone runs `git` against a URL the session's repository named, so it must
not be able to ask for a password, run a helper or read a local path.

## Decision

**The clone happens before the owner stops.** The core clones on the target as the first step of the handoff, ahead
of `prepare`. A clone that fails or is cancelled leaves the session running on its owner, which still owns it.

**Full clone, no checkout.** The agent runs `git clone --no-checkout <origin> <destination>`: history is complete (the
base commit and a later `git fetch` always work) and no working tree is written. The existing install then adds the
worktree on `hoplet/<8>` as for any other clone. Nothing of the session is written into the clone's own checkout.

**The new clone becomes a watched project on the target.** The agent registers it with `Projects.Watch` once the clone
is complete, so the next handoff of any session of that repository finds it through `ResolveClone`. It belongs to the
user from then on: deleting a session (ADR 0023) removes the worktree and branch Hoplet made, never the clone.

**The core computes the destination and the user may change it.** The check answers a suggestion: the parent
directory shared by the target's watched projects that sit next to each other, plus the repository name; with none,
the target's browse root plus the repository name. The user changes it with the existing directory browser
(`GET /api/devices/:id/dirs`). All logic stays in the core; a client shows the suggestion, the browser and the progress.

The agent accepts a destination only if it does not exist, lies inside the browse root, has a parent that is a
directory, is writable and is not reached through a symlink. The check reports why not (below).

**REST contract.**

- `GET /api/devices/:deviceId/clone?origin=` (the existing lookup, which answers `{ deviceId, path }`) gains an
  optional `cloneInto` query and a `clone` object in its answer. With a clone found, `path` is set and `clone` is
  absent. With none, `path` is `null` and `clone` is `{ origin, repoName, cloneInto, reason }`, where `origin` has
  credentials removed, `cloneInto` is the suggested absolute path (or the chosen one when the request carried
  `cloneInto`), and `reason` is one of:
  - `ok`: `cloneInto` can be cloned into and the origin is reachable from the target.
  - `no-origin`: the origin is missing, has credentials in it, or is neither https nor ssh; nothing can be cloned.
  - `no-access`: the target could not reach the origin (`git ls-remote` with the settings below failed).
  - `destination-taken`: the path exists, is outside the browse root, is a symlink or cannot be written; `detail`
    says which: `exists`, `outside-root`, `symlink`, `not-writable`.
  The web UI today learns `no-clone` only from a failed handoff; with this answer it asks before it starts.
- `POST /api/sessions/:sessionId/handoff` gains `cloneInto` (`HandoffRequestSchema`, next to `targetPath`; the two are
  exclusive). When set, the handoff starts with the clone step. The error `no-clone` still answers a request that
  names neither.
- `POST /api/sessions/:sessionId/handoff/cancel` stops a handoff that is still in its clone step and answers
  `{ ok: true }`, or `handoff-not-running` (409) when no handoff of that session is in its clone step. It kills `git`
  and removes the directory this clone created; the handoff then fails with `cancelled` and the owner never stopped.
  The later steps keep their own timeouts and rollback (ADR 0009).
- A failure in the clone step is the error `clone-failed` (502) with a `reason`: `no-access`, `no-space`,
  `destination-taken`, `timeout`, `git-failed`. The text of `git` is never in the answer, except a `detail` of
  `git-failed` after credentials are stripped (below). A cancel keeps the existing `cancelled`.

`clone-failed` (502) and `handoff-not-running` (409) are new codes in `shared/src/errors.ts`.

The `handoff.progress` browser message gains the step `clone` as the first of the steps, with `done` and `total`
bytes (a clone has no known total before the end: `total` is `null` until `git` reports it).

**Wire messages.** Named `device.clone.*`, on the control lane, handled in their own goroutine, never `handoff.*`:

- `device.clone.check` `{ requestId, origin, cloneInto? }` → `device.clone.checked` `{ requestId, reason, detail?, cloneInto }`;
  the agent computes `cloneInto` from its watched projects and browse root. `device.clone.find` is unchanged.
- `device.clone.start` `{ requestId, handoffId, origin, cloneInto }` → progress `device.clone.progress`
  `{ requestId, received, total? }` (at most four a second) and a final `device.clone.done` `{ requestId, path }` or
  `device.clone.failed` `{ requestId, reason, detail? }`.
- `device.clone.cancel` `{ requestId }` → `device.clone.failed` with `cancelled` after the directory is gone.

`PROTOCOL_VERSION` and `ProtocolVersion` go from 5 to 6 in `shared/src/version.ts` and
`agent/internal/sync/protocol.go`, with `shared/src/wire/` and `docs/architecture.md#wire-protocol` in the same pull
request. A core and an agent of different versions refuse each other at `device.hello`; upgrading both is already
what `docs/hosting.md#upgrades` asks. An older agent never sees a clone message.

**Cleanup is journalled.** Before `git` starts, the agent writes `handoff/<handoffId>.clone.json` in its state root
with the destination. It is removed when the clone completes and the project is registered. `Recover` at start-up
finds a journal whose clone never completed and removes that directory, only if it still has the marker the clone
wrote (the `.git` of this clone and no path outside the destination); a journal for a completed clone is dropped.
Cancel and failure do the same at once. Only the directory this clone created is removed; a destination that already
existed is refused up front and is never touched.

**`git` is restricted.**

- `-c protocol.allow=never -c protocol.https.allow=always -c protocol.ssh.allow=always`: only https and ssh;
  `file`, `ext`, `git` and the rest are off. An origin of another scheme or a local path is `no-origin`.
- Environment: `GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=true` (an empty answer, never a prompt),
  `GIT_SSH_COMMAND='ssh -o BatchMode=yes'` (an unknown host key fails; Hoplet does not accept new keys), `core.hooksPath=/dev/null`, `core.fsmonitor=false`; no helper is added and none is removed, so the
  user's own credential helper and SSH keys work.
- A URL with credentials in it (`https://user:token@host/...`) is refused as `no-origin`, never passed on.

**No access.** A target that cannot read the origin gets a fixed explanation and advice in the web UI: give `git` on
the target an SSH key or a credential helper for that host, or clone there yourself and hand off again. Hoplet never
asks for a password and stores none. Any `git` stderr that is shown (`detail` of `git-failed`) goes through one
function that removes `user:secret@` from URLs and anything after `token=`, `password=`, `Authorization:` and
`Bearer`; it is cut at 2 KiB. Logs carry the same stripped text and the origin without credentials.

**Timeouts.** The clone has its own limits and is not part of the 120 s of the handoff request: 600 s in all, and a
stall of 60 s without progress from `git` ends it as `timeout`. The stall timer restarts on every progress line.
The core's handoff state machine waits for `device.clone.done` with these two limits, then continues with `prepare`.

**The Mac client (ADR 0022) reuses** the check answer, `cloneInto`, the cancel route, the progress message and the
`clone-failed` reasons; it draws its own folder picker over the same directory browser route. It does not call the
bundled agent to clone: the core is the only caller.

## Cost

A query and an answer field, a request field, a REST route, seven control-lane messages, a protocol bump with the break it makes between mixed versions, a
clone step in the core's handoff state machine, a journal and its recovery in the agent, a stripping function for
`git` output, and a web card and folder picker (`design/screens/workspace.html`, states `handoff-clone-*`,
`handoff-no-access`, `handoff-browse*`, `handoff-cloning`, `handoff-clone-failed`). A full clone takes the disk of
the repository's history on the target and the time of its download; Hoplet does not measure either before it
starts. The clone's own directory has no files checked out (`--no-checkout`) until the user checks out a branch
there; the session works in its worktree. `git` on the target now fetches from `origin` before a handoff, not only during installation. The clone stays
on the target after a failed handoff that got past the clone step, by design: the user owns it. Documents that
change with it: `docs/spec.md`, `docs/api.md`, `docs/architecture.md` (wire protocol), `docs/threat-model.md` (the
agent runs `git clone` of a URL from the session's repository), `docs/hosting.md` (outbound connections, data,
upgrades) and `design/`. ADR 0009 stays as written: its cost (a clone of the same `origin` on the target) now has this
way out.

## Alternatives

- *Clone inside the handoff's `prepare`/`install` step.* The owner would already be stopped while a clone of unknown
  length runs, and a failure would need an abort of a stopped session. Cloning first costs nothing to roll back.
- *Shallow clone or a partial clone.* Smaller and faster, but the base commit may be outside it and later
  `git fetch`, `git log` and merges would surprise the user. A full clone is the clone the user would have made.
- *Send a bundle from the owner instead of cloning.* It would leave the target without a remote and make the
  owner build and the core store the whole history; a normal clone has `origin` set.
- *Keep the clone out of the watched projects.* It would be forgotten and the next handoff would ask to clone again
  into another folder; registering it is how every other clone is found.
- *Prompt for credentials in the web UI.* Hoplet would handle a secret on a path it can avoid (docs/threat-model.md);
  the user's SSH key or credential helper on the target already is the credential.
- *Run the clone on the ordered handoff queue.* One slow clone would hold every handoff message on that machine.
- *Negotiate the protocol so an older agent keeps working.* The core and the agent ship together and a mismatch is
  already refused; a negotiation would add a path to test for one message family.

## Revisit triggers

A user needs a shallow clone for a very large repository; a target needs an origin that is not https or ssh; a
clone needs a mirror or a proxy; the stall timeout ends clones that are slow but alive; the core and the agents are
upgraded separately; a vendor's repositories live behind an interactive login.
