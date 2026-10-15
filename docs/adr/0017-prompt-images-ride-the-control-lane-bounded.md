# 0017 Prompt images ride the control lane, bounded

- Status: accepted
- Date: 2026-10-15
- Related: ADR 0007, ADR 0008, ADR 0010, ADR 0012, docs/architecture.md#principles, issue #10

## Context

The composer takes pasted, dropped and attached images, and Claude Code accepts images as base64 content in a
user message on its streaming input. A prompt is control work: it must never wait behind sync batches, search,
snapshots or history pages. The agent WebSocket writes a control message at once and a bulk message only after
the previous bulk message has left, so a control message waits behind at most one bulk message. Until now every
control message was small; an image is not.

Two shapes were possible. The images could travel on the control lane inside `runtime.prompt` and
`runtime.start`, or they could be uploaded first as bulk messages that the prompt then refers to by ID. The
second keeps control messages small, but a prompt with images would then wait behind any sync batch in the bulk
queue, which is the delay the lanes exist to prevent, and it adds a store of pending uploads on the agent with
its own expiry.

## Decision

Images travel with their prompt on the control lane, and their size is bounded so that one prompt cannot hold
the lane for long:

- At most 4 images, each at most 4 MiB and together at most 8 MiB, of the types PNG, JPEG, GIF and WebP. A
  prompt with images is therefore at most about 11 MB on the wire (base64), written once.
- The browser checks the limits and the type from the file's first bytes before sending; the core checks them
  again (strict base64, the decoded sizes, the first bytes against the declared type) and answers `too-large`
  or `invalid-request` with `reason: images`; the agent checks them a third time before an image reaches the
  driver. The numbers are in `shared/src/runtime.ts` and `agent/internal/daemon/images.go`.
- A driver declares the `images` capability (ADR 0012); the composer offers attach, paste and drop only then,
  and the core and the agent refuse images for a provider that does not declare it.
- The core never logs an image or its bytes, only how many there were; the vendor writes them into the session
  file as it writes any prompt, and reads show them as image blocks, elided like any large block (ADR 0007).

## Cost

A prompt with images delays the control messages sent after it on the same connection by the time one such
message takes to write, up to about 11 MB; on a LAN that is under a second, on a slow uplink longer. The REST
body limit of the two prompt routes rises from 32 KiB to 12 MiB, so the core buffers up to that much per
prompt request. The session file grows by the images' base64, as it does when Claude Code is used in a
terminal.

## Revisit triggers

Images larger or more numerous than these limits are needed, a slow link makes the delay of other control
messages noticeable, or the vendor accepts images by reference (a path or an upload ID) instead of inline.
