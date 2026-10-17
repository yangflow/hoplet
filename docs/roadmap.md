# Roadmap

What Hoplet plans to build next, roughly in order. Each version has one theme. Nothing here is a promise of a
date, and plans change. What has shipped is in `CHANGELOG.md`.

## Next

Fixes and small improvements, taken one at a time before and between the versions below:

- A session turns external when Claude appends title and cost lines after a turn (#118).
- A new session's first turn shows idle while Claude runs (#114).
- Approve a plan into a fresh context (#112).
- Comment on a diff line by line and send the comments as the next prompt (#84).
- Export a session as Markdown (#93).
- A session keeps running while its agent is disconnected from the core, and the events catch up on reconnect
  (#95).
- Finish the desktop command palette and shortcuts (#92).
- Tags, pinning, archiving and grouping by project for sessions (#91).
- Search past sessions across machines (#85).

## Versions

- **v0.5: inspection tools.** Verify what Claude Code did without a separate SSH session: a terminal (#14) and the
  dev server preview (#15) on the owner machine.
- **v0.6: a second provider.** Codex or Pi behind the provider interfaces through the ACP driver, with model
  providers in Settings (#16).
- **v0.7: machines and resources.** See each machine's state at a glance: its system icon with a status dot (#65),
  its CPU, memory, disk and running sessions (#88), the capabilities it reports (#70, reporting only), and usage
  and cost by session, project and machine with budget alerts (#75).
- **v0.8: unattended sessions.** Let a session run without someone watching: per-project permission policy rules
  (#98, an ADR first), lifecycle hooks (#97), a searchable audit log (#80) and session templates (#76).
- **v0.9: session orchestration.** Drive Hoplet from code and let sessions drive each other: a command-line tool
  and a TypeScript SDK (#71), sessions that view and direct other sessions over MCP (#67), child sessions (#81),
  and sessions that read and change their own configuration (#69).

## Later

Not scheduled; each needs the maintainer's decision before it joins a version:

- A native Mac client with the agent bundled (#115, ADR 0022); phones after it (#6).
- Scheduled prompts for a session (#20).
- Share a session as a read-only view (#19).
- Manage Claude Code memory from Settings (#18).
- Transcribe spoken prompts on the user's own machines (#17).
- Choose a machine by its capabilities and move a session to a standby machine when its machine goes offline
  (#70).
- Generate design prototypes in a session and preview and annotate them in the inspector (#66).
- Start a session from GitHub and other external services (#68).
- Run one prompt on several machines and compare the results (#72).
- A snapshot timeline across machines, with rollback and forking from a snapshot (#73).
- Run tests, lint and build when a session says it is done, and feed failures back (#79).
- Remaining quota in the machine list, with a hint to move when rate-limited (#82).
- Sync MCP configuration, skills and rule files across machines (#83).
- Summarise a long session's context and carry it with a handoff (#89).
- A per-project environment setup script for handoffs and new worktrees (#90).
- Install the agent with one command and upgrade it with the core (#94).
- View and control sessions from chat bots such as Feishu and Telegram (#96).
- End-to-end encryption of sessions and snapshots at rest on the server.
