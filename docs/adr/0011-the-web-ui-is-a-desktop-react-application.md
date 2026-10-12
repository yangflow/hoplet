# 0011 The web UI is a desktop React application

- Status: accepted
- Date: 2026-10-08
- Related: docs/spec.md#information-architecture, docs/design.md#layout-and-breakpoints

## Context

The web UI is served by the core and used from desktop browsers on any machine, including Linux. Phones need
notifications for permission requests and a native feel; a native client comes later (`docs/roadmap.md`) and will use
the same REST and WebSocket API.

## Decision

The web UI is a React application built with Vite, without a component library. It declares one viewport,
desktop at 1024 px wide and more. Phones and tablets are left to that native client; the web UI is not designed
or built for them. Every capability the web UI uses is a REST or WebSocket API in `shared/`, so a native client
can offer it too.

## Cost

A phone browser gets the desktop layout. Components are written in the repository instead of taken from a
library.

## Revisit triggers

Phones need Hoplet before a native client exists, or a second screen needs components that a library provides with
accessibility the repository cannot match.
