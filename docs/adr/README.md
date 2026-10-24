# Architecture Decision Records

Each record covers one decision: context, decision, cost. A reversed decision keeps its file: write a new
record and mark the old one "superseded by NNNN".

## Index

| No. | Decision | Status |
|---|---|---|
| [0001](0001-monorepo-with-a-typescript-core-and-a-go-agent.md) | Monorepo with a TypeScript core and a Go agent | accepted |
| [0002](0002-the-byte-offset-is-the-sync-cursor.md) | The byte offset is the sync cursor | accepted |
| [0003](0003-the-core-runs-typescript-source-directly.md) | The core runs TypeScript source directly | accepted |
| [0004](0004-claude-code-is-the-only-provider.md) | Claude Code is the only provider | superseded by 0012 |
| [0005](0005-per-device-tokens.md) | Per-device tokens | accepted |
| [0006](0006-session-identifiers-are-exact-keys.md) | Session identifiers are exact keys | accepted |
| [0007](0007-reads-elide-large-content-blocks.md) | Reads elide large content blocks | accepted |
| [0008](0008-remote-control-runs-a-hoplet-managed-claude-code-process.md) | Remote control runs a Hoplet-managed Claude Code process | accepted |
| [0009](0009-handoff-moves-a-workspace-snapshot-into-the-target-clone.md) | Handoff moves a workspace snapshot into the target clone | accepted |
| [0010](0010-workspace-reads-use-a-filesystem-root.md) | Workspace reads use a filesystem root | accepted |
| [0011](0011-the-web-ui-is-a-desktop-react-application.md) | The web UI is a desktop React application | accepted |
| [0012](0012-vendors-plug-in-behind-provider-interfaces.md) | Vendors plug in behind provider interfaces | accepted |
| [0013](0013-shared-schemas-use-valibot.md) | Shared schemas use valibot | accepted |
| [0014](0014-native-vendors-and-acp-vendors.md) | Native vendors and ACP vendors | superseded by 0015 |
| [0015](0015-control-protocol-and-history-source-are-chosen-separately.md) | Control protocol and history source are chosen separately | accepted |
| [0016](0016-the-source-is-public-under-apache-2-0.md) | The source is public under Apache-2.0 | accepted |
| [0017](0017-prompt-images-are-uploaded-before-the-prompt.md) | Prompt images are uploaded before the prompt | accepted |
| [0018](0018-session-settings-are-config-options.md) | Session settings are config options | accepted |
| [0019](0019-a-plan-exit-is-declared-by-the-driver.md) | A plan exit is declared by the driver | accepted |
| [0020](0020-queued-and-steered-messages.md) | Queued and steered messages | accepted |
| [0021](0021-notifications-and-signed-action-links.md) | Notifications and signed action links | accepted |
| [0022](0022-the-mac-client-is-native-appkit-with-the-agent-bundled.md) | The Mac client is native AppKit with the agent bundled | proposed |
| [0023](0023-deleting-a-session-and-what-hoplet-made-for-it.md) | Deleting a session and what Hoplet made for it | accepted |
| [0024](0024-a-handoff-clones-the-repository-on-a-target-that-has-none.md) | A handoff clones the repository on a target that has none | accepted |
| [0025](0025-a-plan-can-be-approved-into-a-new-session.md) | A plan can be approved into a new session | accepted |
| [0026](0026-the-inspector-opens-a-restricted-terminal-and-links-dev-servers.md) | The inspector opens a restricted terminal and links dev servers | accepted |
| [0027](0027-codex-runs-on-its-app-server-and-enters-at-level-2.md) | Codex runs on its app server and enters at level 2 | accepted |
| [0028](0028-provider-configurations-live-on-the-machine.md) | Provider configurations live on the machine | accepted |
| [0029](0029-permission-rules-match-on-the-agent-and-deny-wins.md) | Permission rules match on the agent and deny wins | accepted |
| [0030](0030-the-audit-log-records-actions-in-a-hash-chain.md) | The audit log records actions in a hash chain | accepted |
| [0031](0031-the-session-lifecycle-events-and-the-two-hook-kinds.md) | The session lifecycle events and the two hook kinds | accepted |
| [0032](0032-codex-rule-hook-calls-the-agent-over-a-local-socket.md) | The Codex rule hook calls the agent over a local socket | accepted |
| [0033](0033-a-command-line-tool-an-sdk-and-scoped-tokens.md) | A command-line tool, an SDK and scoped tokens | accepted |
| [0034](0034-sessions-view-and-direct-other-sessions-over-mcp.md) | Sessions view and direct other sessions over MCP | accepted |
| [0035](0035-child-sessions-work-on-parts-of-a-task-in-parallel.md) | Child sessions work on parts of a task in parallel | accepted |
| [0036](0036-sessions-change-their-own-configuration-over-mcp.md) | Sessions change their own configuration over MCP | accepted |
| [0037](0037-worktrees-share-their-clones-project-rules-and-a-handoff-can-copy-them.md) | Worktrees share their clone's project rules, and a handoff can copy them | accepted |
| [0039](0039-sessions-continue-across-a-disconnect-and-the-agent-replays-its-events.md) | Sessions continue across a disconnect, and the agent replays its events | accepted |
| [0041](0041-a-project-across-machines-is-its-git-remote.md) | A project across machines is its git remote | accepted |

## Record template

Status, date and related links, then Context, Decision, Cost and Revisit triggers; see any record above.
