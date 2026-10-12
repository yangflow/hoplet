# 0012 Vendors plug in behind provider interfaces

- Status: accepted
- Date: 2026-10-08
- Related: ADR 0007, ADR 0008, ADR 0009, ADR 0015, docs/architecture.md#extension-points

## Context

Remote control and handoff are verified on Claude Code first, and only Claude Code is implemented in this
iteration. Other coding agents (Codex, Pi and others) also write append-only JSONL sessions and offer a
programmatic control mode, so they fit the byte cursor (ADR 0002). Each vendor has its own line format,
control protocol and session store. If the web UI, the stored data or the wire protocol knew Claude Code's
formats, every further vendor would change all three.

## Decision

A vendor is a provider with three parts, and nothing outside them knows the vendor's formats:

- **Agent provider** (`agent/internal/provider/<vendor>/`): discovers sessions, reads them incrementally,
  locates a session file, and installs one into the vendor's store during handoff.
- **Agent runtime driver** (`agent/internal/runtime/<vendor>/`): starts and stops the vendor process, sends
  prompts, maps the vendor's events onto Hoplet's runtime events and states, answers permission requests and
  interrupts. It declares its capabilities (`permissions`, `interrupt`, `thinking`).
- **Core mapping** (`core/src/api/projection/<vendor>.ts`): maps one stored line onto Hoplet's conversation
  model (entries of role and blocks: `text`, `thinking`, `tool-call`, `tool-result`, `image`, `other`). The
  read projection (ADR 0007) elides blocks of that model.

Every session records its `provider`. The web UI and any native client render the conversation model and show only
the controls whose capability the session's runtime driver declares. Claude Code (`claude`) is the only
provider implemented in this iteration.

## Cost

The core parses and maps every line it serves instead of passing vendor lines through, and the conversation
model must be wide enough for every vendor; vendor-specific detail it cannot express appears as `other`. A
new vendor still needs its own spike, provider, runtime driver and mapping, and handoff for it is only as
complete as its provider's installation.

## Revisit triggers

A vendor whose sessions are not append-only, or whose control model cannot be expressed as Hoplet's runtime
states.
