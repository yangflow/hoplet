# 0017 Prompt images are uploaded before the prompt

- Status: accepted
- Date: 2026-10-15
- Related: ADR 0007, ADR 0008, ADR 0012, docs/architecture.md#principles, issue #10

## Context

The composer takes pasted, dropped and attached images, and Claude Code accepts images as base64 content in a
user message on its streaming input. A prompt is control work: it must never wait behind sync batches, search,
snapshots or history pages. The agent WebSocket writes a control message at once and a bulk message only after
the previous bulk message has left; every control message is small, so none holds the connection for long.

Images are not small. Sending them inside `runtime.prompt` would make one control message several megabytes:
every permission answer or interrupt sent after it waits until it is written, on a slow uplink for seconds,
and a proxy that limits WebSocket frames may drop it. Comparable projects upload first and send a reference
(Happy uploads an encrypted blob and the session carries a `ref`; Claude Code UI stores the upload and the
socket carries a path; Jupyter keeps uploads off the kernel socket entirely). Asking the vendor to read an image
from a path in the prompt was tried by early versions of those projects and dropped: it depends on the model
choosing to read the file, and leaves files in the project.

## Decision

An image is uploaded to the machine when it is attached, and the prompt names it:

- The browser shrinks an image to at most 1568 px on its long edge (a GIF is left as it is) and uploads it at
  once with `POST /api/devices/:deviceId/attachments`. The core checks it (strict base64, at most 4 MiB, the
  first bytes against the declared type, a provider of the device that declares `images`) and sends it as
  `attachment.put` on the **bulk** lane in chunks of at most 1 MiB, numbered and closed by `last` as a snapshot
  travels, so no message holds the connection longer than one chunk; the agent appends the chunks in the order
  they arrive, checks the whole image again once the last one is in, stores it under
  `<hoplet_home>/attachments/` readable by its user only, and answers `attachment.stored` with the ID.
- The prompt carries only `attachments`, at most 4 IDs, on the control lane, so control messages stay small.
  The agent reads the images, checks that they are at most 8 MiB together, and the driver writes them as base64
  image content before the text.
- The agent keeps an image for an hour, so a refused prompt or a failed start can be sent again with the same
  IDs; beyond 64 images or 64 MiB the oldest go first. Images are never logged, only their count.
- A driver declares the `images` capability (ADR 0012); the composer offers attach, paste and drop only then.

## Cost

An upload waits behind the bulk messages already queued from the core to the machine (acknowledgements, and
snapshot chunks during a handoff); the session sync runs the other way and does not delay it. The composer shows
an image uploading and holds Send until it is ready. An image crosses the link twice: once uploaded, and again
when the agent syncs the session line in which Claude Code recorded it; the second is the vendor's record and is
kept as written (ADR 0002). A draft that changes machine
uploads its images again. The agent's state root holds up to 64 MiB of images for up to an hour. The session
file grows by the images' base64, as it does when Claude Code is used in a terminal.

## Revisit triggers

The vendor accepts images by reference, images larger than 4 MiB after shrinking are needed, or the bulk queue
makes uploads noticeably slow.
