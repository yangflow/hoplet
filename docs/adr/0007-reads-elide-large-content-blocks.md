# 0007 Reads elide large content blocks

- Status: accepted
- Date: 2026-10-08
- Related: ADR 0002, docs/spec.md#reading-a-session

## Context

In large Claude Code sessions most bytes are not conversation. Measured on a 127 MB session of 9,215 lines:
tool results are 85.9% of the bytes, assistant text is 0.6%, the largest 1% of lines hold 64.6% of the bytes,
and a single line reaches 1.2 MB. A client that receives lines verbatim downloads, parses and holds megabytes
for every page of history it shows. The store must stay verbatim (ADR 0002).

## Decision

The read API returns a projection of each line. Every content block whose encoded size exceeds 8 KiB is
replaced by an elision object that names the block's type, its size in bytes, a preview of at most 1 KiB of
text (none for images) and the reference `lineNo/blockIndex`. `GET /api/sessions/:id/blocks/:lineNo/:blockIndex`
returns the full block. Lines without large blocks are returned unchanged. The projection is computed on read
from the stored bytes; nothing about it is stored.

## Cost

Opening a large block costs one extra request. The core parses every line it serves, which uses CPU that a
verbatim read does not; the parse happens per page, never for the whole session.

## Revisit triggers

Projection CPU makes a page read exceed the open-session budget in docs/spec.md#technical-baseline.
