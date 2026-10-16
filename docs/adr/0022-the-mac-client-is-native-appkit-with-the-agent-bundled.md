# 0022 The Mac client is native AppKit with the agent bundled

- Status: proposed
- Date: 2026-10-16
- Related: ADR 0001, ADR 0005, ADR 0007, ADR 0011, docs/api.md#native-clients, issue #6

## Context

ADR 0011 left phones and native clients for later, on the same REST and WebSocket API, and ADR 0001 named "a
second client language needs the protocol" as a revisit trigger. This is that revisit. The web UI in a browser
cannot show a native notification when Claude Code asks for a permission and the user is in another app, keeps
the host token in the browser's storage, and needs the agent installed on the Mac separately. A conversation is
the screen that matters most: a session of tens of megabytes, streamed into while it is read.

## Decision

**The desktop client targets macOS only, in Swift.** Linux and Windows are not targets; the web UI covers them.
An iOS client comes later and reuses the shared layer below.

**AppKit draws the hot paths, SwiftUI the rest.** The conversation is a view-based `NSTableView` with cell reuse
and TextKit 2 text, its row heights computed off the main thread; the composer is an `NSTextView`. The sidebar,
settings, host management and menu bar are SwiftUI and host the AppKit views. There is no `WKWebView`. Markdown
(swift-markdown) and highlighting (tree-sitter) are rendered off the main thread. Reads are pages and elided
blocks, as for every client (ADR 0007).

**The code lives in `apple/`.** The `HopletKit` Swift package is platform-neutral: Codable models generated from
`docs/schema/hoplet-api.schema.json` and checked in, which `pnpm check` fails to accept when stale; REST over
`URLSession`; the browser WebSocket through ws-tickets; the host token in the Keychain; and the
`GET /api/health` `protocolVersion` check before any connection (docs/api.md#native-clients). The `HopletMac`
app target uses it. The Xcode project is generated with XcodeGen, so no `.xcodeproj` is checked in. `apple/` is
not part of the pnpm workspace; `scripts/boundaries.ts` tracks it and forbids it from importing anything of the
repository but the schema. Swift consumes the JSON Schema, not the TypeScript in `shared/`.

**The app bundles `hoplet-agent`.** The binary is a helper registered with `SMAppService` as a LaunchAgent, so
the Mac a user codes on runs the agent without a separate install. The app manages its configuration, start and
stop, logs and pairing (a device token, ADR 0005). The agent keeps running when the app quits. It is the same Go
program as on any other machine.

**Distribution is Developer ID signing, notarization and Sparkle updates, not the Mac App Store.** The agent
reads `~/.claude`, starts `claude` processes and reads arbitrary repositories, which the App Store sandbox
forbids. The app's version follows `VERSION` like the other parts.

## Cost

Swift is the fourth language, and CI needs a macOS runner. A wire change regenerates the Swift models. The web UI
and the Mac client implement each screen twice. The bundled agent makes the app a second installer of the agent
and a second update channel. When implemented, these documents change: `docs/architecture.md` (the client, its
boundaries), `docs/hosting.md` (the agent installed by the app), `docs/threat-model.md` (the Keychain token, the
bundled agent, the Sparkle channel as a trust boundary), `docs/spec.md` (a Mac client section and permission
notifications) and `docs/roadmap.md`. Open: the minimum macOS version (proposal: macOS 15), the app's name (an iOS
app named "HopLet" exists, see issue #6) and iOS push.

## Revisit triggers

iOS work starts, or Linux or Windows desktop becomes a target.
