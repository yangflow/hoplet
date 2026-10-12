# 0015 Control protocol and history source are chosen separately

- Status: accepted
- Date: 2026-10-10
- Related: ADR 0002, ADR 0007, ADR 0008, ADR 0009, ADR 0012, docs/architecture.md#extension-points

## Context

Many coding agents speak the Agent Client Protocol (ACP): JSON-RPC over the process's standard streams with
`session/prompt`, `session/update` notifications, `session/request_permission`, session modes and
`session/cancel`. One generic driver can control every such agent. ACP is a control protocol: it does not
expose the agent's history as a byte stream, `session/load` replays the whole history as notifications, which
would hand a client a whole session (ADR 0007), and an ACP agent may keep no append-only file at all
(ADR 0002).

What Hoplet offers beyond remote control comes from the vendor's own session files: sessions started in a
terminal appear in Hoplet, and handoff installs the file on another machine so the vendor resumes it there
(ADR 0009). Which protocol controls a vendor and where its history comes from are two questions. ADR 0014
answered both with one choice of kind, so a vendor taken on through ACP had no path to terminal sessions and
handoff.

## Decision

A vendor answers two questions separately.

**Control.** Its runtime driver is its own (`agent/internal/runtime/<vendor>/`) or the generic ACP driver,
which runs the vendor's command and speaks ACP over its standard streams. A vendor that speaks ACP uses the
generic driver unless a spike shows ACP cannot carry a control Hoplet needs. Claude Code keeps its own driver
(ADR 0008).

**History.** The session file the agent syncs comes from one of three sources, each a level above the last:

- *Level 0, the ACP log.* The agent appends every ACP notification it receives as one line to a file under its
  state root; one generic provider syncs it and one generic mapping reads it, coalescing streamed chunks into
  blocks. Only sessions Hoplet starts exist. Adding a vendor at this level is configuration: a name and a
  command.
- *Level 1, the vendor's files.* A provider of the vendor's own (`agent/internal/provider/<vendor>/`,
  `provider.Provider`) discovers, locates and reads the vendor's own append-only session files, and a mapping
  of its own (`core/src/api/projection/<vendor>.ts`) reads them. Sessions started in a terminal appear, and
  the store holds what the vendor wrote.
- *Level 2, the vendor's store.* The provider also implements `provider.Store`, so handoff installs the
  session file into the vendor's store on the target (ADR 0009). A vendor at this level is a native vendor.

A vendor enters at the highest level its spike verifies and moves up when a later spike verifies the next;
each step changes its provider and mapping and nothing else. Every session records which source wrote it, so
the core picks the mapping per session and a vendor that moves up keeps its earlier sessions readable. The
provider declares `discovery` and `handoff` beside the driver's capabilities, and clients show only what is
declared.

Every level and every driver meet the same layers above them:

- Vendor knowledge lives only in the three vendor parts. The sync client, `projects`, `workspace`, `locate`,
  the daemon, the store, the wire protocol and the web UI use the provider and driver interfaces and the
  conversation model; they never name a vendor's paths, file layout, tool names or record kinds.
- The runtime model (states, output, permission requests, controls) is Hoplet's own and stays expressible in
  ACP: every runtime event maps onto an ACP `session/update` or request, and a new control follows the ACP
  shape of the same control (session modes for permission modes, permission options for answers).
- Classification a client needs, such as a tool call's kind or whether a block is the vendor's internal
  bookkeeping, is decided by the vendor's core mapping, not by the client.

The generic ACP driver, provider and mapping, and the `discovery` and `handoff` declarations, are built when
the first ACP vendor is taken on.

## Cost

A level-0 vendor has no terminal sessions and no handoff, and its session file holds what Hoplet saw, not what
the vendor stored: turns run outside Hoplet are absent. A vendor that moves up has sessions of two sources that
are not merged. The runtime model cannot adopt a vendor feature that ACP cannot express without a decision.
Claude Code keeps a driver of its own beside the generic one. A vendor above level 0 still needs a spike and a
provider and mapping of its own, and its handoff is only as complete as its `Store`.

## Revisit triggers

ACP gains a byte-addressable history read, which would let level 0 carry the vendor's own history; or Claude
Code ships ACP as its supported control interface.
