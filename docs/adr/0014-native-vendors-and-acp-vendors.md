# 0014 Native vendors and ACP vendors

- Status: superseded by 0015
- Date: 2026-10-10
- Related: ADR 0002, ADR 0007, ADR 0008, ADR 0012, docs/architecture.md#extension-points

## Context

ADR 0012 lets a vendor plug in as an agent provider, a runtime driver and a core mapping. Each of the three is
written per vendor, so the cost of a vendor grows with every format it has. Many coding agents (Gemini
CLI and others) speak the Agent Client Protocol (ACP): JSON-RPC over the process's standard streams with
`session/prompt`, `session/update` notifications, `session/request_permission`, session modes and
`session/cancel`. ACP is a control protocol. It does not expose the vendor's session history as a byte stream:
`session/load` replays the whole history as notifications, which would hand a client a whole session
(ADR 0007), and an ACP agent may keep no append-only file at all (ADR 0002).

What Hoplet offers beyond remote control depends on the vendor's own session files: sessions started in a
terminal appear in Hoplet, and handoff installs the file on another machine so the vendor resumes it there
(ADR 0009). ACP gives neither.

## Decision

A vendor is one of two kinds, decided by where its history comes from:

- **Native vendor.** It writes append-only session files in a stable format. It has its own agent provider
  (`agent/internal/provider/<vendor>/`), runtime driver (`agent/internal/runtime/<vendor>/`) and core mapping
  (`core/src/api/projection/<vendor>.ts`), and supports terminal sessions and handoff. Claude Code is a
  native vendor and keeps its own driver (ADR 0008).
- **ACP vendor.** It speaks ACP. One generic ACP runtime driver serves every such vendor; the agent appends
  each ACP notification it receives as one line to a session file under its state root, which one generic
  provider syncs and one generic core mapping reads. Adding one is configuration: a name and a command. Only
  sessions Hoplet starts exist, and handoff is not offered. The ACP driver is built when the first ACP vendor
  is taken on.

Both kinds meet the same layers above them:

- Vendor knowledge lives only in the three vendor parts. The sync client, `projects`, `workspace`, `locate`,
  the daemon, the store, the wire protocol and the web UI use the provider and driver interfaces and the
  conversation model; they never name a vendor's paths, file layout, tool names or record kinds.
- The runtime model (states, output, permission requests, controls) is Hoplet's own and stays expressible in
  ACP: every runtime event maps onto an ACP `session/update` or request, and a new control follows the ACP
  shape of the same control (session modes for permission modes, permission options for answers).
- Classification a client needs, such as a tool call's kind or whether a block is the vendor's internal
  bookkeeping, is decided by the vendor's core mapping, not by the client.

## Cost

An ACP vendor gives up terminal sessions and handoff. The session file of an ACP vendor holds what Hoplet saw,
not what the vendor stored, so turns run outside Hoplet are absent. The runtime model cannot adopt a vendor
feature that ACP cannot express without a decision. Claude Code keeps a driver of its own beside the
generic one.

## Revisit triggers

ACP gains a byte-addressable history read, or Claude Code ships ACP as its supported control interface.
