# 0018 Session settings are config options

- Status: accepted
- Date: 2026-10-15
- Related: ADR 0008, ADR 0012, ADR 0015, docs/architecture.md#wire-protocol, issue #9

## Context

A session has settings the composer changes: the model, Thinking and Fast today, the permission mode next
(issue #9), more as vendors are added. A setting that is a named field of the driver's capabilities, of a new
session's options and of a prompt touches the schemas, both sides of the wire protocol, the core's checks and a
hand-made control in the web UI, and makes the web UI know which settings each vendor has.

ADR 0015 asks a new control to follow the ACP shape of the same control. ACP first gave permission modes their
own shape (session modes: `availableModes`, `currentModeId`, `session/set_mode`) and now marks it for removal in
favour of session config options: the agent lists every setting as a `select` or `boolean` option with an `id`,
a `name`, an optional `description` and `category` (`mode`, `model`, `model_config`, `thought_level`), its
values and its `currentValue`; the client sets one with `session/set_config_option` and the agent reports
changes with `config_option_update`. Claude Code's desktop app, VS Code extension and claude.ai, Zed and Paseo
all show these settings as controls in the composer that apply to the running session, and reflect a change
made elsewhere (Claude leaving plan mode, a terminal switching mode).

## Decision

**A session's settings are config options, shaped as ACP's.** A runtime driver declares its settings as a list
in its capabilities (`config`): each option has an `id`, a `name`, an optional `description` and `category`, a
`type` of `select` (with its `options`, each a `value`, a `name`, an optional `description`) or `boolean`, and a
`currentValue`. In the capabilities an agent reports for a provider (`device.hello`) the current value is the
default of a new session; in a session's runtime state (`runtime.state`, `session.runtime`, the session list) it
is that session's value. A value may carry `risk: "high"`, which ACP carries in `_meta`; clients mark it. The
list's order is the driver's priority. The capabilities `permissions`, `interrupt` and `images` are not
settings: they declare what the driver does.

**A new session starts with values; a running one is set one option at a time.** A new session's request
carries `config`, a map from option ID to value. A change to a session is its own control-lane message
(`runtime.config`: session, option ID, value), not part of a prompt, and may be sent while the session is idle,
ready, running or waiting for a permission answer. The core accepts only declared options and declared values,
logs every change with the session ID, option ID and value, and the agent reports the session's whole list in
its next runtime state.

**When a change takes effect is the driver's.** It applies at once when the vendor can switch the running
process (Claude Code's permission mode, `set_permission_mode`), before the next turn when the vendor switches
between turns (the model, `set_model`), or by restarting the process between turns with the session's values as
flags (Thinking and Fast). Every process of a session starts with the session's values. A change the vendor
makes itself, such as leaving plan mode, updates the value the agent reports.

**Clients render what is declared.** The web UI groups options by `category`, shows them in the driver's order,
ignores an option of a type it does not know, and never names an option ID or value in logic. It translates a
known option ID or value through its strings and otherwise shows the driver's `name`.

## Cost

A new setting is a change to its driver only, but every client renders settings generically: a control that
needs more than a list of values or a switch, such as a numeric budget, needs a new option type in this
decision. A choice in the composer changes the session when it is made, not when a prompt is sent, so a model
chosen and not used still changes the session. A session's values live in the agent's memory: an agent that
restarts starts its sessions with the defaults. Hoplet's list is ACP's shape but not ACP's wire messages; the
generic ACP driver maps one onto the other.

## Revisit triggers

ACP changes the config option shape or removes `boolean`; or a vendor needs a setting that is neither a choice
nor a switch.
