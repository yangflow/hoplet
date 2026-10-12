# Roadmap

What Hoplet plans to build next, roughly in order. Each version has one theme. Nothing here is a promise of a
date, and plans change.

- **v0.2: remote control and handoff.** Drive a large Claude Code session from the web UI with streaming,
  permissions and interruption, and move it with its working tree to another machine.
- **v0.3: daily driver.** Driving a session from the web UI is as fluent as the terminal: composer v2 and draft
  sessions (#10), permission modes (#9), a session actions menu (#11), commands, mentions and skills (#12), and the
  settings screen (#8).
- **v0.4: handoff to any machine.** A session moves to a machine that has no clone yet: the target clones
  `origin` before the owner stops (#13).
- **v0.5: inspection tools.** Verify what Claude Code did without a separate SSH session: a terminal (#14) and the
  dev server preview (#15) on the owner machine.
- **v0.6: a second provider.** Codex or Pi behind the provider interfaces through the ACP driver, with model
  providers in Settings (#16).

## Later

Not scheduled:

- Scheduled prompts for a session (#20).
- Share a session as a read-only view (#19).
- Manage Claude Code memory from Settings (#18).
- Transcribe spoken prompts on the user's own machines (#17).
- End-to-end encryption of sessions and snapshots at rest on the server.
- A native client for phones and the Mac, once Hoplet is mature (#6).
