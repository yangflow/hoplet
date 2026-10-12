# 0001 Monorepo with a TypeScript core and a Go agent

- Status: accepted
- Date: 2026-10-08
- Related: docs/architecture.md#components, docs/architecture.md#wire-protocol

## Context

Hoplet has three parts that share one wire protocol: the core (server and web UI in one container image), the
web UI, and an agent on every development machine. The agent runs next to Claude Code on macOS and Linux, must
be a single binary without a runtime, and holds long-lived processes and file handles. The protocol changes
often while remote control and handoff are being built.

## Decision

One repository holds `core/`, `shared/`, `web/` and `agent/`. The core, `shared/` and the web UI are
TypeScript; the agent is Go, built as one static binary `hoplet-agent`. A protocol change updates `shared/` and `agent/internal/sync/protocol.go` in the same
pull request, and the contract test compares both.

## Cost

The protocol exists twice, in TypeScript and Go, and is kept equal by a test instead of by a single source.
CI installs both toolchains.

## Revisit triggers

The agent needs a release cadence independent of the core, or a second client language needs the protocol.
