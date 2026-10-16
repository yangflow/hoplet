# 0021 Notifications and signed action links

- Status: accepted
- Date: 2026-10-16
- Related: ADR 0005, ADR 0012, ADR 0019, ADR 0020, ADR 0022, docs/hosting.md#network, docs/threat-model.md, issues #77, #80, #115

## Context

Claude Code waits for the user at a permission request, at a plan exit (ADR 0019) and at the end of a turn, and a
session can fail to start or fail to move to another machine. Hoplet tells the user only when a tab is open: a
request that waits ten minutes (`PERMISSION_OPEN_MS`) is denied and the turn goes on without the user. The
core already sees the moments: `runtime.permission` and `runtime.state` arrive in `control/runtime.ts`, and a
consumer can hook onto them as the queue does (ADR 0020). A session start failure and a handoff failure are not
events today: `startSession` and `handoff` return them to the HTTP caller and nothing else sees them.

## Decision

**The core decides, channels deliver.** The core turns an event into a notification by event kind × the
project's preferences × the preset × quiet hours. A channel receives a finished message and only delivers it.
There are three channels:

- *Web Push* to the browser subscriptions, with VAPID. It is built on `node:crypto` and adds no dependency:
  the payload encryption of RFC 8291 (`aes128gcm`) needs ECDH P-256 (`createECDH`), HKDF-SHA-256 (`hkdfSync`) and
  AES-128-GCM (`createCipheriv`), and the VAPID JWT of RFC 8292 is ES256, which `sign` produces with
  `dsaEncoding: 'ieee-p1363'` as the 64-byte signature a JWT needs. All four are present in the Node version the
  image runs.
- *Webhooks*: a JSON `POST` to a URL the user added, with `X-Hoplet-Signature: sha256=<hex>`, the HMAC-SHA-256 of
  the body, when the webhook has a signing secret.
- *Connected native clients*: an authenticated browser WebSocket receives a `notify` message. It is reserved for
  the Mac client (ADR 0022); the web UI does not use it in this version.

The browser Notification API is only an in-page hint (a tab that is open and hidden); it is not a channel.
Preferences are stored in the core, read and written over REST, so that a native client uses the same ones.

**Events in v1.** A permission request; a plan review (the plan exit); a turn finished, only when no tab has the
session open; a failure, which is a session start failure or a handoff failure. Failures become a new internal
event of the control module, published where `startSession` and `handoff` fail, as a consumer of it needs. The
expiry of a request comes later. Only the needs-you events (permission, plan, failure) are on by default.

**Presets.** "Only when Claude needs me" (the default), "Needs me, and when a turn finishes", "Everything" and
"Off". In v1 "Everything" equals the one before it and gains the events added later; the UI says so. A project
and an event can override the preset. Quiet hours hold back every notification except the needs-you events.

**Payload.** The event kind, the project, the session title, the tool name and a redacted summary of the command
truncated to about 80 characters. Never the prompt, file contents or a diff.

**Signed action links.** A Web Push or webhook notification of a permission request or a plan review carries a
link that opens a confirmation page without login.

- A token is bound to one `requestId`, is single use, and lives for the request's remaining open time, at most 10
  minutes; a plan exit never expires, so it gets the 10 minutes. It is the request and an expiry signed with
  HMAC-SHA-256 by a key of the core, and the core verifies it.
- The token rides in the URL fragment (`/a#<token>`), which a browser never sends: it stays out of request lines,
  proxy and access logs and `Referer`. The page posts it to read the request and posts it again to decide; loading
  the page never decides. The page tells apart *answered* (by another window or device), *expired* and *used*.
- Some requests can only be approved in the web UI: a Bash command containing `rm`, `git push` or `sudo`, a
  Write or Edit outside the session's workspace, and every MCP tool. Their page shows the request and an "Open in
  Hoplet" button, with no Allow.
- A Web Push OS notification only opens the page. It has no Allow or Deny buttons until the audit log of #80
  exists.
- A native authenticated client answers through the existing permission API and may show Allow and Deny
  on its own notifications. It does not use action tokens.

**Duplicates.** When a native client of the same device is connected, the core can skip Web Push for that device.
In v1 settings only show a hint, and the user turns off the browser's push.

**Secrets at rest.** The VAPID key pair, the action-link key and the webhook signing secrets live in one file in
the data directory with mode 0600, generated on first use, not in `hoplet.db`, so that a copy or a backup of the
database does not hold them. To rotate, delete the file: the core generates a new one, every subscription and
every outstanding link become invalid and the browsers subscribe again.

**Logs.** A notification sent, failed or a link used is logged with the event kind, the channel, the session ID
and the `requestId`. The token, a secret and the payload text are never logged.

## Cost

A table or two for preferences, subscriptions and webhooks; a notify module in the core with the three channels;
routes for preferences, subscriptions, webhooks and the action page; a service worker in the web UI. An
action link is a credential in a URL fragment, which the server never receives but the notification, the webhook
receiver and the browser history hold: `docs/hosting.md` and `docs/threat-model.md` state it as a new trust
boundary, together with the core's outbound HTTPS connections to push
services and webhook URLs (a webhook URL to a private address is the user's own choice, as the host token holder
is the owner). Web Push needs a secure context and a push service the user's browser can reach. Documents that
change with it: `docs/hosting.md` (outbound connections, the secrets file), `docs/threat-model.md`, `docs/spec.md`
(the notification section; the permission-expiry text of the remote-control steps no longer says a plan expires)
and `docs/api.md`.

**The Mac client (ADR 0022) reuses** the stored preferences and their REST routes, the `notify` message, the payload
and its redaction, and the permission API for answers. It does not use subscriptions, VAPID or action tokens.

## Alternatives

- *The `web-push` npm package.* It does what is needed, but adds a dependency and its tree to a core whose
  supply chain is small, for about 150 lines of `node:crypto`.
- *Allow and Deny buttons on OS notifications.* A tap from a lock screen answers without the user reading the
  request, and there is no audit log yet (#80).
- *Links that require login.* A phone notification would open a login page: the token is the credential and is
  made narrow instead (one request, one use, minutes).
- *Secrets in the database.* A database copy would carry the keys that sign links and push; a file at 0600 can
  be dropped and regenerated alone.

## Revisit triggers

The audit log of #80 lands (action buttons); a vendor's request is not a permission request; expiry
notifications are wanted; the Mac client ships (the duplicate rule becomes automatic); a second core is run per
data directory.
