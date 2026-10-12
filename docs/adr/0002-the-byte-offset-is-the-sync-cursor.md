# 0002 The byte offset is the sync cursor

- Status: accepted
- Date: 2026-10-08
- Related: docs/spec.md#session-sync, docs/architecture.md#session-chunk-files

## Context

A Claude Code session is an append-only JSONL file that grows to hundreds of megabytes. Any transfer that is
proportional to the whole file makes large sessions slow to sync and to open. The agent restarts, loses its
connection and runs on several machines.

## Decision

The core stores session lines verbatim and counts the stored bytes in `sessions.byte_size`. That count equals
the offset in the device file and is the only sync cursor. The agent keeps no cursor: on every connection the
core's `welcome` names each session's `byte_size`, and the agent sends the bytes after it. An append whose
start offset differs from the cursor is answered with `session.resync` and changes nothing.

Lines are stored in chunk files of at most 500 lines. A full chunk is compressed with gzip after the index
commit. An append records its undo boundary in SQLite, syncs the chunk file, commits the index and only then
acknowledges; startup rolls back recorded uncommitted work before serving cursors. A read decompresses only
the chunks it touches.

## Cost

No line is ever filtered, rewritten or deduplicated on the way in, so the store keeps every byte the vendor
wrote, including large tool output and images. Reducing what a client receives happens on the read path
(ADR 0007).

## Revisit triggers

Claude Code stops writing sessions as append-only files.
