# 0003 The core runs TypeScript source directly

- Status: accepted
- Date: 2026-10-08
- Related: docs/spec.md#technical-baseline

## Context

The core is a small server. A compile step for it adds build configuration, source maps and a second artifact
layout to the image without changing behaviour.

## Decision

The image runs `node core/src/main.ts` with Node.js type stripping. `core/` and `shared/` use only TypeScript
syntax that type stripping supports (no enums, no namespaces, no parameter properties); `tsc --noEmit` checks
types. The web UI is built by Vite to static files that the core serves.

## Cost

TypeScript features that need emit are unavailable in `core/` and `shared/`. The Node.js version must support
type stripping without flags.

## Revisit triggers

Startup time or memory of the core exceeds the budgets in docs/hosting.md#resources because of type stripping.
