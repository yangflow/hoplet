# 0008 Remote control runs a Hoplet-managed Claude Code process

- Status: accepted
- Date: 2026-10-08
- Related: ADR 0012, docs/spec.md#remote-control, docs/architecture.md#data-flow

## Context

Driving a session remotely needs four things from the vendor: a turn starts without reloading the session
from disk each time, output streams while it is produced, a tool call that needs permission can be answered
remotely, and a running turn can be interrupted. A process started per prompt in print mode reloads the whole
session for every turn, shows nothing until a line is written to the session file, cannot ask for permission
and cannot be interrupted. A Claude Code process that a user started in a terminal cannot be attached to.

## Decision

The agent runs one long-lived Claude Code process per controlled session:

```
claude -p --input-format stream-json --output-format stream-json --verbose \
  --include-partial-messages --permission-prompt-tool stdio --resume <session>
```

Prompts are `user` messages on standard input. Control uses Claude Code's control protocol on the same
streams: the agent sends `control_request` `initialize` after start, and the process is `ready` when that
request is answered; `control_request` `interrupt` ends the running turn and keeps the process; a
`control_request` `can_use_tool` from Claude Code is a permission request, answered with a `control_response`
whose `behavior` is `allow` or `deny`. Claude Code waits for an answer without limit; the agent denies a
request after 10 minutes. Closing standard input stops the process; the agent kills it when it has not exited
after 10 seconds. The runtime states `running`, `waiting-permission` and `ready` follow Claude Code's
`session_state_changed` events (`running`, `requires_action`, `idle`).

The process is started by Hoplet (the first prompt to an idle session starts it) and stops after 30 minutes
without a turn, on an explicit stop, before handoff and when the agent exits. The number of processes in a
turn is not limited, so sessions run in parallel. The agent keeps at most `runtime_max_idle_processes`
processes `ready` without a turn; when one more becomes `ready`, it stops the least recently used of them.

The agent relays the process's events (text deltas, thinking activity as a token count, tool calls with their
input, tool results, permission requests, turn results) to the core as runtime events; `signature_delta`
events are dropped. Runtime events are transient: the core forwards them to subscribed
clients and keeps only the current runtime state per session. The session file written by Claude Code stays
the durable record and is synced as before (ADR 0002).

Hoplet controls only processes it started. After every turn result the agent records the session file's
size. Growth beyond that size, or any growth while no Hoplet process runs for the session, is a foreign write:
the session's runtime state becomes `external`, the agent stops its process for that session (its history
in memory no longer matches the file), and Hoplet refuses to start a process until the file has not grown for
60 seconds.

## Cost

Each process holds memory that grows with the session: about 600 MB for a 256 MB session, measured with
Claude Code 2.1.289. Parallel turns are bounded only by the machine's memory, which the operator watches; a
stopped idle process costs a cold start (about 1–2 s at 256 MB) on its next prompt. The runtime depends on Claude Code's streaming JSON format and control protocol, which
carry no compatibility promise and are partly undocumented (`--permission-prompt-tool stdio`); the agent
runs Claude Code from the minimum version in `agent/internal/runtime/claude/versions.go` on and reports versions newer than
the tested ones as untested, ignores unknown fields, and each tested version has a contract test. Thinking text is not available, only its token count. Sessions running in a terminal can be read but not
controlled.

## Revisit triggers

Claude Code offers a supported way to attach to a running session, or changes its streaming JSON format.
