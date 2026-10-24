# 0037 Worktrees share their clone's project rules, and a handoff can copy them

- Status: accepted
- Date: 2026-10-24
- Related: ADR 0009, ADR 0023, ADR 0024, ADR 0029, ADR 0030, ADR 0034, docs/api.md, docs/architecture.md,
  docs/threat-model.md, docs/spec.md, design/screens/handoff-rules.html, issue #330

The maintainer chose direction A of `design/screens/handoff-rules.html` and the worktree expansion below on
2026-10-24. This record amends the project scope of ADR 0029.

## Context

ADR 0029 gives a project rule a machine and a project path, and the agent applies it to a session whose directory
is that path or below it. A handoff installs the session in a new git worktree next to the target's clone,
`<clone>-<8>` (ADR 0009), and a session started in a new worktree sits next to its clone the same way. Such a
worktree is beside the clone, not below it, so the rules of the clone's project never reach a session in it: a
session that was stopped by a deny on the source runs unguarded on the target, even when the target's clone has
the same rules. ADR 0029 also says the handoff dialog offers to copy the source project's rules that the target's
project does not have; until now the dialog said only that project rules stay on their machine.

The agent refuses a pushed set in which a rule ID repeats (ADR 0029), so the same rule cannot simply be listed
under a second path.

## Decision

**A worktree shares its clone's project rules.** For each project rule of a machine, the core lists the rule again
under every worktree Hoplet made from that clone on that machine: the worktrees recorded for sessions
(`session_worktrees`, written by a handoff's commit and by a new session started in a worktree) and the worktree a
running handoff has installed on its target. A worktree that already lies under the rule's path is not listed
again. The patterns are not rewritten: `{workspace}` already names the session's worktree. The agent accepts a
project rule that repeats with the same ID and the same body and still refuses one ID with two bodies; because an
older agent would refuse such a set whole, the protocol version rises to 23.

**A handoff can copy the project rules.** `POST /api/sessions/:id/handoff` takes `copyProjectRules`. Only the
owner sets it, in the handoff panel: it is a checkbox row, checked when the target lacks any of the rules, with
the counts by action and a Show list that marks each rule as copied, already there, or the target's own. A
session's `request_handoff` (ADR 0034) cannot carry it, and approving such a request hands off without copying.

- *The rules copied* are the source machine's enabled project rules of the session's project: the clone the
  session's worktree belongs to when Hoplet made one, else the session's project path, and the worktree path
  itself.
- *Paths.* An absolute pattern in `paths` or `except` (in a rule or one of its `or` leaves) that is the source
  project or lies below it is rewritten onto the target's clone; `{workspace}`, `~` and every other pattern are
  copied as they are.
- *Merge.* A rule is copied only when the target's project (the target's clone, by exact path) has no rule with
  the same tool, action and match after the rewrite. The target's own rules stay, and nothing on the source
  changes. The copy keeps the note and is enabled. It counts against the 500 rules of the host and fails the
  handoff with `rejected` and reason `rules-limit` when it would exceed them.
- *Step.* A Rules step runs after Verify and before Commit: the core stores the copies in one transaction, sends
  the target its set, and waits for `device.rules.applied` of that revision. A refusal (`rules-refused`), a
  disconnect or the handoff timeout fails the handoff like any step before Commit: nothing moves, and the copies
  are deleted again. Without anything to copy the step is skipped.
- *Audit.* The `session.handoff` record carries `rulesCopied`. The copy is one `config.changed` record with
  `area: "permission-rules"`, `action: "copied"`, the target machine, the source machine, the session, the count
  and the count by action; a rollback is one record with `action: "removed"`. Neither names a pattern or a path.

## Cost

- The rule set of a machine grows with every worktree that has rules, and each worktree is pushed again when a
  session is started in one or handed to one.
- A rewritten pattern that pointed into the source project but outside it on the target (a symlink, a sibling
  directory) is copied as written; the user reviews the copies in Settings.
- One more message waits in a handoff, and a target that does not confirm fails it.
- Agents of protocol 22 must be updated together with the core.

## Alternatives considered

- *B: a Project rules section comparing both machines with Copy, Deny and ask, Don't copy.* More choice than the
  first version needs; the per-action choice can come later.
- *C: Hand off becomes a second step when the rules differ.* Hides the choice until the end and adds a click to
  every handoff between machines with different rules.
- *A new wire field naming a project's worktrees.* The agent would match them itself, but every older agent would
  then need the field, and the core already knows the worktrees.
- *Rewriting patterns for each worktree.* `{workspace}` already does it, and a rewritten copy would need its own
  ID.
- *Copying by default from `request_handoff`.* A session could then widen the target's rules by asking for a
  handoff; the copy stays a decision of the owner.

## Revisit triggers

- Users want a worktree that is not one Hoplet made (one they added by hand) to share its clone's rules.
- Users ask to copy only some actions, or to keep the source and target rules in sync after the handoff.
- The 500-rule cap is reached by copies.
