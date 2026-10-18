# Roadmap

What Hoplet plans to build next, roughly in order. Each version has one theme. Nothing here is a promise of a
date, and plans change. What has shipped is in `CHANGELOG.md`.

## How planning works

- Every piece of work is a GitHub issue.
- Each version is a milestone. Its `overview` issue holds the goal and the task list.
- The `Next` milestone holds fixes and small improvements between versions. They ship with the next release.
- The `Later` milestone holds what is not scheduled. Each item needs the maintainer's decision before it joins a
  version.
- A version is released when its theme is done.
- To change the plan, change the issue and its milestone. This file changes only when a theme, its order, or a
  version is added or removed.

## Versions

- **v0.6: inspection tools.** Verify what Claude Code did without a separate SSH session: a terminal and the dev
  server preview on the owner machine. [milestone](https://github.com/yangflow/hoplet/milestone/4) · overview #4
- **v0.7: a second provider.** Codex or Pi behind the provider interfaces through the ACP driver, with model
  providers in Settings. [milestone](https://github.com/yangflow/hoplet/milestone/5) · overview #5
- **v0.8: machines and resources.** See each machine's state at a glance, its resources, the capabilities it
  reports, and usage and cost with budget alerts.
  [milestone](https://github.com/yangflow/hoplet/milestone/7) · overview #122
- **v0.9: unattended sessions.** Let a session run without someone watching: permission policy rules, lifecycle
  hooks, a searchable audit log and session templates.
  [milestone](https://github.com/yangflow/hoplet/milestone/8) · overview #123
- **v0.10: session orchestration.** Drive Hoplet from code and let sessions drive each other: a command-line tool
  and a TypeScript SDK, sessions that view and direct other sessions over MCP, child sessions, and sessions that
  read and change their own configuration.
  [milestone](https://github.com/yangflow/hoplet/milestone/9) · overview #124

## Next

Fixes and small improvements between versions: [milestone](https://github.com/yangflow/hoplet/milestone/11).

## Later

Not scheduled: [milestone](https://github.com/yangflow/hoplet/milestone/6).
