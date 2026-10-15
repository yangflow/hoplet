# Threat model

## Assets

- **Session content**: prompts, code, tool output, images. It can contain secrets that the user pasted or a
  tool printed.
- **Working trees and clones** on development machines, which remote control and handoff change.
- **The host token**: full operator control (read, prompt, answer permissions, hand off, mint devices).
- **Device tokens**: one per agent; enough to sync as that device and to receive its control messages.
- **Claude Code permissions**: a permission answer lets Claude Code run a tool on a development machine.

## Trust boundaries

- Browser ↔ core: the host token on REST; a single-use ticket on the browser WebSocket.
- Agent ↔ core: a device token on the agent WebSocket.
- Reverse proxy ↔ core: plain HTTP on the host's loopback; the proxy terminates TLS.
- Contributors ↔ repository: the source is public and anyone can propose a change; only the maintainer merges.
- Core ↔ data directory: trusted when written, parsed defensively when read.
- Core ↔ agent control: a control message makes the agent start Claude Code, answer a permission, or write a
  worktree and a session file. The agent accepts control only from the core it is configured for.
- Agent ↔ Claude Code: the agent reads the session file and the streaming output as untrusted data and
  never executes their content.
- Agent ↔ filesystem: request paths, snapshot entries and git output are untrusted until resolved inside
  their root.

## Threats and mitigations

| Threat | Boundary | Mitigation | Verified by |
|---|---|---|---|
| Token theft on the network | browser/agent ↔ core | TLS at the proxy; compose publishes only on `127.0.0.1`; no token in any URL | `image/compose.yaml` review; `core/test/auth.test.ts` |
| Request without a valid token | browser/agent ↔ core | Every `/api/*` request, ticket and WebSocket upgrade is checked; tokens are compared in constant time | `core/test/auth.test.ts` |
| Host token used as an agent | browser ↔ core | `/ws/agent` accepts device tokens only; `device.hello` must match the token's `device_id` | `core/test/auth.test.ts` |
| Leaked device token | agent ↔ core | The token is that device only and is stored hashed; minting again for the `device_id` replaces it | `core/test/auth.test.ts` |
| Remote prompt, permission answer or new session by an attacker | browser ↔ core ↔ agent | Requires the host token; every prompt and answer is logged with session ID and `promptId` or `requestId`, a new session with device, project path and `promptId`, never content. A new session starts only in a project the device watches, with config options its driver declares; the core and the agent each check this, and an answer or runtime message from a device that was not asked is ignored. A holder of the host token can switch any controlled session to Bypass permissions (`POST /api/sessions/:id/config`), after which its tools run without a permission request; every process is launched with `--allow-dangerously-skip-permissions` so that the switch needs no restart, and each change of a session's setting is logged with the session ID and the option and value, never content. A holder of the host token can start Claude Code in any watched project, with the permission requests answered through Hoplet, and can make an agent watch any directory its user can read (`POST /api/devices/:id/projects`; the agent accepts only an existing directory) | `core/test/control.test.ts`, `core/test/new-session.test.ts`, `core/test/runtime-api.test.ts`, `agent/internal/runtime/manager_test.go`, `agent/internal/runtime/claude/driver_test.go`, `agent/internal/daemon/start_test.go`, `agent/internal/daemon/daemon_test.go` |
| Hostile or oversized images sent with a prompt | browser ↔ core ↔ agent ↔ Claude Code | Only with the host token and only to a machine whose driver declares `images`. Each upload is at most 4 MiB of PNG, JPEG, GIF or WebP, checked by the browser, again by the core (strict base64, decoded size, first bytes against the declared type) and again by the agent, which receives it in chunks of at most 1 MiB in order, refuses a chunk out of order or a total over 4 MiB, and stores it as a file of its own naming (the core's random ID and the type's extension) under `<hoplet_home>/attachments/`, mode 0600; a prompt names at most 4, together at most 8 MiB. The store keeps an image at most an hour and at most 64 images and 64 MiB, the oldest going first, so uploads cannot fill the disk. Images are never logged, only their count; the web UI shows them only through `<img>` ([ADR 0017](adr/0017-prompt-images-are-uploaded-before-the-prompt.md)) | `core/test/new-session.test.ts`, `agent/internal/daemon/attachments_test.go`, `web/test/conversation.test.ts` |
| Non-owner writes a session | agent ↔ core | Owner check on every append; ownership changes only in the handoff commit | `core/test/store.test.ts` |
| Stale owner writes after handoff | agent ↔ core | `owner_epoch` in `welcome` and `handoff.commit`; appends from the old owner are rejected | `core/test/handoff.test.ts` |
| Corrupt or substituted handoff copy | core ↔ agent | Ownership moves only when the installed session file's SHA-256 and size equal the stored stream and the tree hash equals the snapshot | `core/test/handoff.test.ts`, `agent/internal/workspace` tests |
| Snapshot that escapes its worktree or exhausts memory | agent ↔ filesystem | Entries are resolved inside the new worktree with `os.Root`; compressed and uncompressed sizes are bounded while reading | `agent/internal/workspace` tests |
| Stored snapshot read for display exhausts the core or escapes its archive | core ↔ data directory | Only `snapshot.json`, `tracked.diff` and untracked entries are read, by name inside the archive; sizes are bounded while decompressing and nothing is extracted to disk; one diff answer is at most 256 KiB | `core/test/snapshot-read.test.ts` |
| Host-token holder lists directories on a machine | browser ↔ core ↔ agent | A holder of the host token can list the directory **names** under the browse root (`browse_root`, the home directory by default) of every connected machine, not file names or contents. The agent lists directories only, resolves the requested path with its symlinks and refuses one outside the resolved root, reads through `os.Root` so a link that leaves the root is not followed, skips hidden entries, checks `.git` with `Lstat` without reading it, and returns at most 500 entries | `agent/internal/browse/browse_test.go`, `agent/internal/sync/control_test.go`, `core/test/projects.test.ts` |
| Host-token holder learns where a machine keeps its clones, or a remote's credentials | browser ↔ core ↔ agent | A holder of the host token can ask a connected machine which of its watched projects is a clone of a given `origin` and learn that path (`GET /api/devices/:id/clone`), as a handoff to it would; nothing else is listed. The agent drops the user and password a remote URL can carry before an `origin` leaves it (workspace listing, snapshot), and the core drops them again when it reads a stored snapshot, so neither the browser nor the data directory holds them | `agent/internal/workspace` tests, `agent/internal/sync/control_test.go`, `core/test/projects.test.ts` |
| Workspace read escapes the working tree | agent ↔ filesystem | `os.Root`, regular files only, no path through `.git`, 256 KiB text and 4 MiB image bounds ([ADR 0010](adr/0010-workspace-reads-use-a-filesystem-root.md)); `HEAD` reads and diffs take only paths inside the working directory, as names, never patterns | `agent/internal/workspace` tests |
| Hostile file content in the workspace viewer | agent ↔ core ↔ browser | Text is rendered as text with highlighting, never as HTML; images, SVG included, are shown only through `<img>` from a blob URL, so no script runs | `web/test/workspace.test.ts` |
| Path traversal in static files | browser ↔ core | Paths resolve inside the web root or are refused | `core/test/static.test.ts` |
| Hostile session lines or runtime output | Claude Code ↔ agent ↔ core ↔ browser | Stored verbatim, parsed defensively, rendered as text; nothing is evaluated | `core/test/projection.test.ts`, `web/test/items.test.ts` |
| Malformed session IDs | every boundary | Exact alphabet checked before any lookup or write ([ADR 0006](adr/0006-session-identifiers-are-exact-keys.md)) | `scripts/test/contract.test.ts` |
| Secrets in logs | core, agent | Logs carry IDs, sizes and codes, never content or tokens; a test feeds known secrets through every log path | `core/test/log.test.ts` |
| Container escape | proxy ↔ core | UID 10001, read-only root, `cap_drop: ALL`, `no-new-privileges`, only `/data` and `/tmp` writable | `image/compose.yaml` review |
| A weakness found by reading the public source | every boundary | No secret in the design: tokens are minted per install and stored hashed, no key is in the tree (secret scan before publication); vulnerabilities are reported privately (`SECURITY.md`) | gitleaks over the published tree; `SECURITY.md` |
| A contribution that adds a backdoor or a hostile dependency | repository | The maintainer reviews every pull request (`.github/CODEOWNERS`); dependencies are pinned to exact versions in the lockfile and `go.mod` and listed in `THIRD_PARTY.md`; workflows of pull requests from forks get no secrets and a read-only token | review; `pnpm install --frozen-lockfile` in CI |

## Accepted risks

- **The host token is operator-level.** Whoever holds it can run Claude Code with tool permissions on every
  connected machine, and can turn the permission prompts off for any controlled session (Bypass permissions). Accepted because Hoplet has one operator; it is kept off the public internet without TLS.
- **The server sees plaintext sessions and snapshots.** Accepted because the server is the user's own host.
- **No rate limiting on authentication.** Accepted while the core is reachable only through the user's proxy.
- **A terminal Claude Code process can still write a session that Hoplet controls.** Hoplet detects foreign
  growth before it starts a process and reports `external`, but cannot stop a process it did not start.

## Out of scope

- Compromise of a development machine: its agent and Claude Code already read and write everything there.
- The reverse proxy, its TLS configuration and the network between the user's machines.
- Claude Code itself, and what a tool it runs does after the user allowed it.
