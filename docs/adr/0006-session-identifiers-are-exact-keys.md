# 0006 Session identifiers are exact keys

- Status: accepted
- Date: 2026-10-08
- Related: docs/spec.md#session-identifiers

## Context

A session ID names a directory in the data directory, a row in the index, a file in the Claude Code store on
every machine and a key in agent state. A value that is trimmed, decoded or remapped in one place and not in
another addresses two different sessions, or a path outside the intended directory.

## Decision

A session ID is a nonempty ASCII string of letters, digits, underscore and hyphen. Every boundary that accepts
one (REST path, wire message, persisted record, agent lookup) rejects any other value before lookup, decoding
or any write. No component trims, decodes or normalizes an ID. `shared/` and the agent use the same rule, and
the contract test checks both against the same cases.

## Cost

A vendor that starts using other characters in session IDs is rejected until this rule changes.

## Revisit triggers

Claude Code session IDs contain characters outside this alphabet.
