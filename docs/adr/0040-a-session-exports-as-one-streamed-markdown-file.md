# 0040 A session exports as one streamed Markdown file

- Status: accepted
- Date: 2026-10-24
- Related: ADR 0007, ADR 0030, docs/spec.md#exporting-a-session, docs/api.md#session-export,
  docs/threat-model.md

## Context

Users want to hand a session to someone who does not use Hoplet: a pull request description, a review, an
incident note. The pieces exist (the conversation, the net diff, the commands, the machines and hand-offs) but
only as pages and blocks, because no client receives a whole session (ADR 0007). Building the file in the
browser would mean the browser reading every page and every elided block, then holding the whole session; a
long session is hundreds of megabytes. The file also leaves Hoplet's trust boundary: whoever receives it reads
whatever the session holds, including credentials pasted into a prompt or printed by a command.

## Decision

`GET /api/sessions/:id/export` is the one read that covers a whole session. The core writes the Markdown file
and streams it; it never builds the file in memory and no client receives the session's lines.

- **Bulk lane, two at a time.** The route runs on the bulk lane, reads the lines in pages of 500 up to the line
  count it saw when it started, and yields to waiting work between pages, so control work is never queued
  behind it. At most two exports run at once in a core; a third is refused with `rate-limited`.
- **Two passes.** The first pass counts the turns and their times, so the front matter can say how many turns
  follow and a client can show progress; the second writes them. Writes respect backpressure; a client that
  goes away stops the export.
- **What the file keeps.** The conversation (every turn, the user's messages and final replies, or none), an
  optional summary the user writes (it starts from the last reply in the browser; Hoplet calls no model), the
  net diff against the base (at most 256 KiB, the files past it named), the commands and their exit codes, and
  the machines and hand-off points from the audit log. A tool's output is kept only for a command or a failure
  no longer than the elision threshold of ADR 0007; longer output is named with its size.
- **Masked as written.** Every chunk passes the credential rules of `core/src/scrub` and the user's own strings
  (at most 20, three characters or more) before it is written, as `[redacted:<kind>]`. There is no rule for
  long random-looking strings: it masks hashes, IDs and paths and makes the file useless. The file opens with a
  warning that masking is best effort.
- **Audited, host only.** Each export, a preview included, is recorded as `session.exported` with its options
  and the number of extra strings, never their text. Only the host token may export; a device token or a scoped
  API token may not.
- **The browser saves only a complete file.** The web UI holds the stream until it ends and then saves it; an
  export that stops leaves nothing in the downloads.

## Cost

An export reads every line of the session twice and parses each one, so a long session costs seconds of core
CPU on the bulk lane. Credential masking is best effort: a secret without a known shape reaches the file
unless the user names it. The browser holds one complete file in memory while it is written.

## Revisit triggers

Exports delay bulk work users notice (a page read or a sync), or users need exports larger than a browser can
hold, which would move the save to the browser's file system access or to the CLI.
