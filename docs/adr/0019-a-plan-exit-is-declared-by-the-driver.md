# 0019 A plan exit is declared by the driver

- Status: accepted
- Date: 2026-10-16
- Related: ADR 0007, ADR 0012, ADR 0018, docs/architecture.md#wire-protocol, issue #105

## Context

In plan mode Claude Code researches and writes a plan, then asks to leave plan mode (the tool `ExitPlanMode`) and
waits for the user. Its own clients show the plan, offer to approve it into a permission mode and let the user
keep planning with feedback. Hoplet showed that request as a tool permission with a JSON input and an
Allow/Deny pair that expired after ten minutes: the plan was unreadable, the mode after approval was not the
user's choice, and a refusal could not say what to change.

Other vendors have the same moment under other names (ACP's `session/request_permission` for a plan switch, a
vendor's own approve step). The web UI must not know the name of Claude's tool or the values of its modes
(ADR 0012, ADR 0018).

## Decision

**The driver declares that a permission request is a plan exit.** `runtime.permission` and `session.permission`
carry an optional `planExit`: the `plan` as Markdown, the `planFile` it lives in when there is one, the `option`
(the ID of the select setting that approval switches, ADR 0018), the `modes` approval may switch to and the
`default` among them. A request without `planExit` is an ordinary tool permission. The Claude driver sets it for
`ExitPlanMode` from the tool's input, offering Accept edits (the default), Manual and Auto; Bypass permissions is
not offered.

**The answer names the mode or the feedback.** `runtime.permission.answer` and `POST /api/sessions/:id/permission`
gain an optional `mode` (a value of `planExit.modes`, valid only for an approval) and an optional `message` (the
reason of a refusal, told to the model). The core and the agent reject a `mode` outside `planExit.modes` and leave
the request open. The Claude driver answers an approval with `updatedPermissions: setMode` for the chosen mode,
which switches the session in the same step, and a refusal with `behavior: deny` and the message, which leaves
the session in plan mode. An approval without a `mode` restores the mode the session had before the plan.

**A plan waits as long as the user takes.** A plan exit has no timer in the core, the agent or the web UI; it ends
when answered, when the turn is interrupted or when the process ends. The first answer wins; other windows show it
as answered.

**The web UI renders the declaration.** `planExit` selects the plan card (the plan as Markdown, one button that
approves into a mode with a menu of the others, and "Keep planning" with a feedback field). Mode names,
descriptions and icons come from the session's config option `planExit.option`. A client or agent without
`planExit` shows the generic permission card.

## Cost

Two optional fields on the permission messages and two on the answer; `PROTOCOL_VERSION` stays 5. An older agent
sends no `planExit`, so its plan is the generic card; an older core ignores `mode` and `message`. The plan is
carried whole in the permission message (a plan is prose of a few KiB; the elision of ADR 0007 does not apply to a
request the user must read before deciding). Hoplet does not offer Claude Code's "clear context and approve":
it needs a new session seeded with the plan, which is a handoff-like flow of its own.

## Revisit triggers

A vendor's plan step is not a permission request; a plan needs more than approve or refuse (comments on a step);
the plan grows past what a control message should carry.
