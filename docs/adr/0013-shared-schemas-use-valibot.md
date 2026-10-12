# 0013 Shared schemas use valibot

- Status: accepted
- Date: 2026-10-08
- Related: ADR 0003, ADR 0006, ADR 0012, docs/architecture.md#boundaries, docs/architecture.md#wire-protocol

## Context

`shared/` is the only contract between the core, the web UI and the agent. Every value that enters the core
from the network (REST bodies, query strings, WebSocket messages) must be parsed by a schema before use, and
types must derive from those schemas instead of being written a second time. The same schema code runs in the
core under Node type stripping (ADR 0003, no build step) and in the browser bundle of the web UI, so the
library must be plain ESM, need no code generation and stay small in the bundle.

## Decision

`shared/` uses valibot 1.5.0 (MIT), pinned to the exact version. Schemas are the source of truth; types are
`InferOutput` of the schema. valibot is modular and tree-shakable, so the web bundle carries only the
validators in use, and it needs no transpilation beyond what type stripping already allows.

Zod (MIT) was considered and rejected for bundle size; TypeBox (MIT) for emitting JSON Schema we do not use;
hand-written guards for duplicating every type.

## Cost

One runtime dependency, shipped in the image inside `shared/` and in the web bundle. A major version of the
library can change schema semantics, so upgrades go through a pull request with the contract test. The
agent in Go cannot use the schemas; its mirror is compared by `scripts/test/contract.test.ts`.

## Revisit triggers

The library is unmaintained, changes its license, or the web bundle size of the schemas becomes a measurable
cost.
