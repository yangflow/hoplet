# Roadmap

Each version has one theme. Its GitHub milestone holds the issues it will ship and an overview issue with its
goal, scope and acceptance; the issues of a planned version are candidates until its work starts
(`GOVERNANCE.md`). Nothing here is a promise of a date.

## Versions

| Version | Status | Theme |
|---|---|---|
| v0.2 | in progress | **Remote control and handoff.** Drive a large Claude Code session from the web UI with streaming, permissions and interruption, and move it with its working tree to another machine. |
| v0.3 | planned | **Daily driver.** Driving a session from the web UI is as fluent as the terminal: composer v2 and draft sessions (#10), permission modes (#9), a session actions menu (#11), commands, mentions and skills (#12), and the settings screen (#8). |
| v0.4 | planned | **Handoff to any machine.** A session moves to a machine that has no clone yet: the target clones `origin` before the owner stops (#13). |
| v0.5 | planned | **Inspection tools.** Verify what Claude Code did without a separate SSH session: a terminal (#14) and the dev server preview (#15) on the owner machine. |
| v0.6 | planned | **A second provider.** Codex or Pi behind the provider interfaces through the ACP driver, starting with a spike, with model providers in Settings (#16). |

## Dependencies

- v0.3 starts when v0.2 meets its acceptance checklist (docs/spec.md#acceptance-checklist); it begins by splitting
  `web/src/screens/cockpit/` into sidebar, conversation and composer before the composer changes.
- v0.5 needs an ADR and a threat-model change for running commands and reaching ports on the owner machine before
  any code.
- v0.6 starts only after remote control and handoff meet their acceptance checklist
  ([ADR 0012](docs/adr/0012-vendors-plug-in-behind-provider-interfaces.md)).
- Scheduled prompts (Later) depend on permission modes (v0.3).

## Later

Not scheduled; each needs the maintainer's decision before it joins a version.

- Scheduled prompts for a session (#20).
- Share a session as a read-only view (#19): a new trust boundary.
- Manage Claude Code memory from Settings (#18): the web UI would write files on an agent.
- Transcribe spoken prompts on the user's own machines (#17).
- End-to-end encryption of sessions and snapshots at rest on the server.
- A native client for phones and the Mac, once Hoplet is mature (#6).
