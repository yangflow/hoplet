# 0023 Deleting a session and what Hoplet made for it

- Status: accepted
- Date: 2026-10-16
- Related: ADR 0002, ADR 0007, ADR 0012, ADR 0022, docs/api.md, docs/threat-model.md, issues #106, #115

## Context

A session has no end today. Archive hides it (docs/spec.md) and nothing removes it: Hoplet's copy stays in the
store, the vendor's session file stays on the owning machine, and the worktree and branch that a handoff created
stay in the user's clone. Deleting is more than a row in the store. The per-session data of the core lives in many
tables without foreign keys and in the session and snapshot directories (`store/layout.ts`), the vendor file is
the agent's to reach (`provider.Store.Remove`), and the worktree is a git directory the user may have worked in.

Two facts shape the design. The ingest creates a session on the first append of an unknown ID, and the agent
syncs every session of an imported project, so a session deleted in the core only comes back at the next sync of
its file. And the core stores a worktree and a branch, not the clone they belong to: the agent forgets its install
record when a handoff finishes (`workspace.Finish`), so nothing says later what Hoplet created.

## Decision

**The dialog lists, the core decides, the agent acts.** All logic is in the core behind REST and the control
lane; a client only shows the choices. The core copy is always deleted. The vendor file and the worktree with its
branch are optional, and both are unticked by default.

**What "running" means.** A session whose runtime state is not `idle`, `failed` or `external`
(`shared/src/runtime.ts`), which is a Hoplet-managed process alive, including a warm `ready` one. It cannot be
deleted; the dialog offers Stop (the existing stop route), then Delete. A session whose process the user started
in a terminal (`external`) does not block and cannot be stopped by Hoplet.

**Only the current owner's copies.** A previous owner's file and worktree stay; the dialog says so when the core
knows of them.

**Preview.** `GET /api/sessions/:id/deletion` asks the owner agent to inspect and answers

```
{ owner: { deviceId, name, online },
  running: boolean,
  vendorFile: { path } | null,
  worktree: { path, branch, dirty: { modified, untracked }, unpushed } | null,
  previousOwner: { deviceId, name } | null }
```

`vendorFile` is `null` when the agent finds no file; `worktree` is `null` when the session has no record in
`session_worktrees` or the agent no longer finds it, or `worktreeIsOurs` fails (the agent checks again, as it
does before every removal). With the owner offline the preview answers from the core: `owner.online` is false and
the two optional fields are `null`. `dirty` comes from `git status --porcelain --untracked-files=all`; a dirty
worktree is unticked with a warning (it is unticked anyway), and `unpushed` is the count of commits on the
branch that no remote and no other local branch has. A worktree whose directory is gone but whose branch remains is
reported with zero dirty counts. `previousOwner` is the device that handed the session over when that handoff
created a worktree (`session_worktrees.from_device_id`); it tells the client that copies stay elsewhere, and is
`null` when Hoplet knows of none.

**Delete.** `DELETE /api/sessions/:id` with the body `{ vendorFile: boolean, worktree: boolean, worktreeDirty: boolean }` (the branch is
part of `worktree`, deleted with `git branch -D` so that an unmerged branch goes). Errors:

| Code | Status | When |
| --- | --- | --- |
| `session-not-found` | 404 | no such session, or already deleted |
| `session-running` | 409 | a Hoplet-managed process is alive; stop it first |
| `owner-offline` | 409 | an optional item is asked for and the owner is offline |
| `delete-partial` | 502 | a step failed; the body lists each step as `done`, `failed` or `skipped` |
| `timeout` | 504 | the agent did not answer in the workspace timeout |

`session-running` and `delete-partial` are new in `shared/src/errors.ts`; the others exist. The body of
`delete-partial` is `{ code, steps }`, each step `{ step, status, reason? }` with `step` one of `stop-syncing`,
`worktree`, `branch`, `vendor-file`, `core-copy`. `worktreeDirty` is true only when the preview showed the worktree dirty and the user ticked it anyway. The agent checks
`git status --porcelain --untracked-files=all` right before removing; a dirty worktree with `worktreeDirty` false fails the
`worktree` step with reason `dirty` and the later steps are skipped, so a worktree that turned dirty after the preview is
never deleted silently. The user sees the partial state and can delete again with it ticked. With
the owner offline and no optional item asked for, the request succeeds and deletes the core copy (see
*Offline owner*).

**Order and retry.** The core writes the tombstone first (see *Tombstones*): from then on the session is out
of every list and the ingest refuses it. Then each step runs; each is idempotent and a missing target is a
success:

1. Stop syncing: the agent removes the session from the `imported` list of every project and forgets it.
2. The worktree, then the branch, only when `worktreeIsOurs` still holds. The agent derives both from the session ID
   (the handoff naming), and the core's `session_worktrees` record decides whether they are offered. Removing the
   worktree also drops its project from the agent's project list, which the core then receives.
3. The vendor file, every copy of it in the vendor store (a session can exist twice, up to 8 are removed). It goes
   after the worktree because its working directory is what finds the worktree again on a retry.
4. The core copy last: its rows, the session and snapshot directories.

The agent reports only the steps asked for; the core adds `core-copy`.

On a failure the later steps are `skipped`, the session stays tombstoned (hidden and not synced), and `DELETE`
can be sent again; it continues at the first step that is not done. The core copy is kept until all
the steps asked for are done, so a retry always has something to start from. There is no undo, no grace period
and no bulk delete in v1.

**Control-lane messages**, named after `device.sessions.remove`, which cannot serve (it takes a project path and
removes one import):

- `device.session.inspect` `{ requestId, sessionId }` → `device.session.inspected` `{ requestId, vendorFile, worktree }`
  with the shape of the preview, or `device.session.rejected` `{ requestId, reason }`.
- `device.session.delete` `{ requestId, sessionId, vendorFile, worktree, worktreeDirty }` →
  `device.session.deleted` `{ requestId, steps }`, each step `done`, `failed` (with a reason code) or `skipped`.
- `device.session.forget` `{ requestId, sessionId }` → `device.session.forgotten` `{ requestId, sessionId }`: step 1 alone,
  used for a tombstone.

A reason code is one of `not-ours`, `dirty`, `remove-failed`, `forget-failed`, `invalid-session-id`.

They are control-lane messages (ADR 0007's bulk lane stays free for sync). A change updates `shared/src/wire/` and
`agent/internal/sync/protocol.go` in the same pull request. The worktree step uses a new variant of
`workspace.RemoveWorktree` that keeps the branch when only the worktree is asked for; today it always deletes both.

**Knowing what Hoplet made.** The agent's install record is gone after a handoff, so the core keeps it. The
`handoff.installed` message gains `clone`, the path of the clone the worktree belongs to, and the core stores a
row when the handoff commits:

- `session_worktrees` (`session_id` primary key, `device_id`, `from_device_id`, `clone`, `worktree`, `branch`, `created_at`),
  written in the same transaction as `handoffOwner`, and only when the handoff created a worktree. A session with
  no row has no worktree to offer.
- Migration `0015` creates this table and the tombstone table.

**Tombstones.** `session_tombstones` (`session_id` primary key, `device_id`, `deleted_at`, `forgotten`) holds the ID of a deleted
session, the owner at that time, and whether that owner's agent confirmed it forgot the session.
`session.append` and the resync of a tombstoned ID are answered with `session.rejected` with the new reason
`deleted`, and the agent suppresses the session in memory like it does for `not-owner`, so a deleted session is
not recreated and, after an agent restart, costs one rejection. An explicit import of that session
(`device.sessions.import`) removes the tombstone; the user asked for it back. The startup purge (`session_purge`)
that removes the data of a session also covers a core that was stopped half way through step 4.

**Offline owner.** Only the core copy and a tombstone are deleted, and the stop-syncing step is reported `skipped` with
reason `owner-offline`. When the owner agent attaches, the core sends `device.session.forget` for each unforgotten
tombstone of that device, which clears the import; the file and the worktree are untouched, and the dialog says so. The tombstone is kept: it
is what keeps the file from coming back.

**Browser WebSocket.** `session.deleted` `{ sessionId }`, sent to every browser when the tombstone is written, so
the sidebar, the Archived page and an open tab drop the session; a session that waits for a retry is reached from
the dialog's partial state, which the client keeps.

**Logs.** A deletion logs the session ID and what was deleted (`core-copy`, `vendor-file`, `worktree`, `branch`),
each step's outcome and the device ID. It never logs a file's contents, a path under the user's home beyond what
`docs/threat-model.md` already allows, or the title.

**Trust boundary.** The host token holder, who is the owner, can now ask a device agent to delete a file and a
git worktree. The agent deletes only what the core names by session ID and then finds itself: the vendor file by
the provider's own lookup (`Finder.Find`), and a worktree only when `worktreeIsOurs` holds (the clone's `git worktree list` has that
path on its `hoplet/` branch). The core never sends a path to delete. `docs/threat-model.md`
states it, `docs/api.md` lists the routes, the messages and `session.deleted`, and `docs/hosting.md` lists the worktree records and tombstones in `hoplet.db`.

## Cost

A migration and two tables, a deletion module in the core, a preview and a delete route, six control-lane messages
and `clone` on `handoff.installed`, a rejection reason, three agent handlers, an inspect step with `git status`
and a count of unpushed commits, a web dialog with its progress and partial states, and the tombstone check on the
ingest hot path (one primary-key lookup per append batch). A handoff that created a worktree before this version
has no row, so its worktree is never offered. A handoff in progress counts as running. A session that exists twice on the owner is fully deleted, which
a user who copied a file by hand may not expect. Documents that change with it: `docs/api.md`,
`docs/threat-model.md`, `docs/hosting.md`, `docs/architecture.md` (wire protocol), `docs/spec.md` and `design/`.

**The Mac client (ADR 0022) reuses** both routes, the `session.deleted` message and the step list of
`delete-partial`; it draws its own dialog from the preview. It does not talk to its bundled agent about deletion:
the core is the only caller, so the client has no second path to a file.

## Alternatives

- *Delete on the agent first, the core on its own.* The core would not know what was left after a crash, and a
  failed step could not be retried from one place. The core copy goes last for that reason.
- *Soft delete with a grace period.* A deleted session would still take its store and keep syncing, and "delete"
  would not mean the file is gone. Archive is the soft way.
- *The agent keeps the install record and the core asks for it.* The record is removed on purpose when a handoff
  finishes and an agent that was reinstalled would forget it; a table in the core survives both.
- *Send the path to delete from the core.* The agent would trust a path from the wire; finding the target from
  the session ID keeps a bad message from naming a file.
- *Clear the import with `device.sessions.remove`.* It takes a project path and fails with "unknown project" when
  the project is gone; a deleted session must be forgotten wherever it is.
- *Delete the previous owners' copies too.* A machine that is offline or gone cannot answer, and a session may
  have passed through several; v1 handles the owner and says that the rest stays.

## Revisit triggers

A user asks for bulk delete or an undo; a vendor keeps session data outside one file; a session can have several
live owners; worktrees are created by something other than a handoff; the tombstone table grows enough to need an
expiry.
