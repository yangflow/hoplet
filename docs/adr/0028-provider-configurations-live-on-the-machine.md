# 0028 Provider configurations live on the machine

- Status: accepted
- Date: 2026-10-20
- Related: ADR 0005, ADR 0008, ADR 0012, ADR 0015, ADR 0018, ADR 0026, docs/architecture.md#wire-protocol, docs/threat-model.md, docs/hosting.md, issues #16, #170

## Context

A machine runs the stock Claude Code and Codex the agent finds: one driver per vendor, built from `claude_bin` and
`codex_bin` in the agent configuration and the agent's own environment (`agent/internal/daemon/runtime.go`). Users
route Claude Code to another backend (GLM, DeepSeek, MiniMax, Qwen through `ANTHROPIC_BASE_URL` and
`ANTHROPIC_AUTH_TOKEN`), keep a work and a personal login apart (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`), or run a CLI
from a path that is not the default. None of this can be said in Hoplet, and New session cannot choose between
such setups.

What a setup holds is what makes it dangerous. A command path is code the agent runs. An environment variable can
be code too (`NODE_OPTIONS`, `LD_PRELOAD`), and a base URL decides where the machine's API key is sent. Today the
host token can start Claude Code in a watched project with declared config values and nothing else; no path,
command or environment variable crosses the agent WebSocket (docs/threat-model.md). The only setting the core may
never turn on is the terminal, which the operator switches per machine (ADR 0026).

## Decision

**A configuration extends a built-in provider.** A provider configuration has an `id` (a slug, unique on the
machine), a `name`, the vendor it `extends` (`claude` or `codex`), an optional `command` (an absolute path, else the
vendor's `claude_bin` or `codex_bin`), environment variables, new-session defaults (values of the driver's config
options, ADR 0018) and an `enabled` switch. Each vendor the agent supports is also a configuration whose `id` is the
vendor name, with no command and no variables; it cannot be removed, only disabled. The provider kind stays the
closed `claude` or `codex` of ADR 0012: stores, history readers and handoff keep working by kind, and a
configuration only changes how a process is launched.

**The machine defines what is code; the web may set what is not.** Configurations live in
`<hoplet_home>/providers.json`, written by the agent at mode 0600 like `projects.json`. They are created, given a
command and given variables only on the machine: by editing the file or with `hoplet-agent provider` (`list`,
`add [--preset <name>]`, `set`, `env <id> <NAME>` reading the value from standard input without echo, `unset`,
`remove`). Through the core the web may change only a configuration's `name`, `enabled` and new-session defaults
(`device.providers.update`); the agent refuses any other field and any value a default's option does not declare,
and the core checks the same. Whoever holds the host token therefore gains no command and no variable, and the
trust boundary does not move.

**No variable value leaves the machine.** `device.hello` announces each enabled and disabled configuration with
its `id`, `name`, `extends`, `enabled`, `command` (the path, which is not secret), the names of its variables each
with `set` (a value in the file or in the agent's environment), its defaults and the driver's capabilities and
version run with that command and environment. It never carries a value. The agent re-announces with
`device.providers` when `providers.json` changes, read on a change of its modification time. The agent never logs a
variable's value, and `hoplet-agent provider list` prints names and `set` only.

**Presets fill in a setup.** A preset is a closed list in the agent (GLM, DeepSeek, MiniMax and Qwen over Claude
Code first): the base URL, the model variables and the name of the secret variable, whose value the user then gives
with `hoplet-agent provider env`. The web shows the presets and the command to run on the machine; it does not run
it. The exact endpoints are set and tested in the code that adds them.

**Launch, resume and handoff use the configuration.** `runtime.start` gains `configuration` (an `id`; the vendor
`id` when absent). The agent starts the configuration's command with the agent's environment, the configuration's
variables over it and the agent's `HOPLET_*` variables removed; the version gate runs per configuration. A
configuration that sets `CLAUDE_CONFIG_DIR` or `CODEX_HOME` is also a session source of its own, scanned like the
default one, so a separate login's sessions are found and belong to it. The agent records the configuration of each
session it starts (`<hoplet_home>/sessions-config.json`, 0600); a session found in a configuration's own directory
belongs to it; any other session belongs to the vendor's built-in configuration. A resume uses the session's
configuration and is refused with `configuration-unavailable` when it was removed or disabled, never quietly run
with another backend. A handoff names the target configuration (`handoff.install.configuration`): the same `id`
when the target has it enabled, else the user picks one of the same vendor on the target, which installs the
session into that configuration's store. The core keeps each session's configuration `id` and `name`, which are not
secret, to show which setup a session runs on.

**The wire.** `ProviderInfo` gains `configuration` (`id`, `name`, `extends`, `enabled`, `command`, `env` as
`{name, set}`, `defaults`); `runtime.start` and `handoff.install` gain `configuration`; new are
`device.providers` (agent to core), `device.providers.update` (core to agent) and its answers. All mirrored in
`shared/src/wire/` and `agent/internal/sync/protocol.go`, with `PROTOCOL_VERSION` raised in the pull request that
adds them. The REST routes are listed in docs/api.md when the code that exposes them exists.

## Cost

Adding a backend takes a step on the machine: an SSH session or a terminal there, a command the web gives ready to
copy. A machine the user reaches only through Hoplet needs the restricted terminal (ADR 0026) for it. The agent
gains a file it writes, a CLI, a file watch, a source per separate login, a session-to-configuration record, a
version check per configuration and per-configuration Codex app-server processes. New session, the Model providers
section and the handoff dialog choose a configuration instead of a vendor; the core stores one more field per
session. Quota and usage per configuration stay with #75 and #82.

## Alternatives

- *The web writes everything, values included, write-only.* Any host token holder can run code on every machine
  (`command`, `NODE_OPTIONS`) and send its keys anywhere (`ANTHROPIC_BASE_URL`). Rejected.
- *The web writes allow-listed variables behind an operator switch.* A base URL is still where the key goes; the
  allow list becomes a security surface to maintain per vendor. Rejected for now.
- *Read-only in the web, everything on the machine.* Safe and simplest, but renaming, disabling and defaults would
  also need the machine. Rejected; they carry no code.
- *Plain-text keys in a file the web can read back (Paseo).* Leaks keys to every client. Rejected.
- *A provider kind per backend (`glm`, `deepseek`).* Every backend would need a store, a history reader and handoff
  rules that are Claude Code's. Rejected.

## Revisit triggers

Users need to add backends where they have no shell on the machine; a vendor whose setup is not a command and
variables (an API key flow inside the CLI, an ACP agent of ADR 0015's level 0); a secret store on the machine
(Keychain, libsecret) replacing values in `providers.json`.
