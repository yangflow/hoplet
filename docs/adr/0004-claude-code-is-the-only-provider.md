# 0004 Claude Code is the only provider

- Status: superseded by 0012
- Date: 2026-10-08
- Related: docs/spec.md#decisions, ADR 0008

## Context

Remote control needs a long-lived vendor process with streaming output, permission requests and interruption
(ADR 0008), and handoff needs to install the vendor's native session store on another machine (ADR 0009).
Both depend on vendor-specific behaviour that has to be verified end to end. Each additional vendor doubles
that verification.

## Decision

Hoplet supports Claude Code only. The agent reads Claude Code session files, runs Claude Code for remote
control and installs Claude Code session files during handoff. The stored line shape is Claude Code's JSONL;
the web UI renders that shape and nothing else.

## Cost

Users of other coding agents cannot use Hoplet. Adding a vendor later means a provider interface in the agent
and a decision on how its sessions map onto stored lines.

## Revisit triggers

Remote control and handoff meet their acceptance checklist (docs/spec.md#acceptance-checklist), and a second
vendor offers a documented non-interactive control interface and an append-only session store.
