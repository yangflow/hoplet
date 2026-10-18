# 0026 The inspector opens a restricted terminal and links dev servers

- Status: accepted
- Date: 2026-10-18
- Related: ADR 0005, ADR 0008, ADR 0010, ADR 0020, ADR 0022, ADR 0024, docs/architecture.md#principles, docs/threat-model.md, docs/hosting.md, issues #4, #14, #15

## Context

The v0.6 theme is verifying what Claude Code did without a separate SSH session. Today every check is a prompt: a
turn, a permission answer and a model in between. Two things are missing: running a command in the session's
working directory, and seeing the web app the session is building (`pnpm dev` on `localhost:5173` of the owner
machine). The workspace design reserves a Terminal and a Browser tab in the inspector.

Both reach beyond what Hoplet touches now. The agent runs Claude Code, whose tools the user answers one by one
(ADR 0008), and reads or writes files inside a working tree (ADR 0010). A terminal is a shell as the agent's user,
which the host token has never been. A preview is the owner machine's network, which the core has never carried.

## Decision

### A restricted terminal (issue #14, option 2)

**The agent starts a PTY.** `agent/internal/terminal/` runs the user's `$SHELL` (`/bin/sh` without it) as an
interactive shell on a PTY, in a process group of its own, in the working directory of the session, resolved at
open and refused when it is not an existing directory. `TERM` is `xterm-256color` and the agent's `HOPLET_*`
variables are removed from the environment. Neither the agent nor the core stores what the shell prints or reads.

**It is off unless the user turned it on.** The agent configuration key `terminal` is `false` by default
(docs/hosting.md#configuration). With it off the agent does not announce the capability in `device.hello` and
refuses `terminal.open` with `disabled`; the core refuses to ask a device that did not announce it
(`terminal-unavailable`). Each side checks, as for a new session (docs/threat-model.md).

**It opens only when nobody else is working in the directory.** The core and the agent each refuse to open a
terminal while the session's turn runs or waits for a permission (`turn-running`), while the session is
`external` (a Claude Code process Hoplet did not start may be writing; `external`), while a handoff of the session
runs (`handoff-running`) and while the owner is offline (`owner-offline`). A session with no process, an idle one
and a stopped one qualify.

**The stream is on the control lane.** It is a bidirectional stream of small control messages, so typing and the
interrupt of a runaway command are never queued behind sync batches, snapshots or history pages
(docs/architecture.md#principles). Because a terminal can print without end, the stream runs under a credit window,
and the PTY, not the lane, absorbs the excess:

- `terminal.output` frames hold at most 4 KiB of raw bytes, base64 in JSON like an attachment chunk. The agent
  reads the PTY into a frame at most every 16 ms.
- The agent may have at most 64 KiB of output not yet acknowledged. The core acknowledges (`terminal.ack`, the
  bytes taken) when the browser connection took a frame and its send buffer is not growing. At the limit the agent
  stops reading the PTY; the kernel buffer fills and the process blocks in `write`, as in any terminal. So at
  most 64 KiB of terminal output is ever ahead of another control message on the lane.
- `terminal.input` is at most 4 KiB per message and 64 KiB per second; beyond that the terminal closes with reason
  `input-flood`. The browser sends a larger paste in chunks within the rate.
- `terminal.resize` takes 1 to 500 columns and 1 to 200 rows.

**Limits.** One terminal per session and three per machine (`terminal-busy` beyond). A terminal closes by itself
after 60 minutes without input from an attached browser (`idle`; output alone does not keep it open, so a
`tail -f` does not), after 8 hours (`max-age`), and 60 seconds after the last browser window attached to it left
(`detached`). Within that minute a window that attaches again receives the last 64 KiB of output, which the agent
keeps in memory; this is the only copy and it dies with the terminal. The shell's exit closes the terminal with
its exit code.

**What ends a terminal at once.** A turn starts (`turn-started`): the core sends `terminal.close` before the
`runtime.prompt` on the same lane, and the agent also closes when its runtime begins a turn, so a prompt or a
queued message (ADR 0020) never waits for the terminal and never races it for the working tree. A handoff starts
(`handoff`), the session is deleted (`deleted`), the user closes the tab (`closed`), or the agent loses its
connection to the core (`owner-offline`): a terminal does not outlive the connection and is not resumed after a
reconnect.

**Cleanup.** To close, the agent sends `SIGHUP` to the process group, waits 3 seconds, then sends `SIGKILL` to the
group and to any process of the shell's tree that left it, found by parent PID before the first signal. It writes
`<hoplet_home>/terminal/<terminalId>.json` (process ID, its start time and the working directory, no content) when
it opens a terminal and removes it on close; at start-up it kills a recorded process whose start time still
matches and deletes the record, so a crashed agent leaves nothing running (as for the clone journal of
[ADR 0024](0024-a-handoff-clones-the-repository-on-a-target-that-has-none.md)). Closing the PTY master also hangs
the shell up if the agent dies.

**Audit.** The core and the agent log `terminal.open` (session ID, terminal ID, device ID) and `terminal.close`
(reason, duration, bytes in, bytes out, exit code), never a byte of input or output and not the command lines.

**The wire.** New messages, all on the control lane, mirrored in `shared/src/wire/` and
`agent/internal/sync/protocol.go`; `PROTOCOL_VERSION` is raised in the pull request that adds them. From the core:
`terminal.open` (`terminalId`, `sessionId`, `cols`, `rows`), `terminal.input`, `terminal.resize`, `terminal.ack`
and `terminal.close`. From the agent: `terminal.opened`, `terminal.output` (`terminalId`, `seq`, `data`),
`terminal.closed` (`reason`, `exitCode`) and `terminal.rejected` (`reason`). `device.hello` announces
`capabilities.terminal`. The browser opens a terminal with one REST call and attaches to its stream on the browser
WebSocket, whose ticket is the same as for every other message; these routes and messages are listed in
docs/api.md when the code that exposes them exists. The Mac client (ADR 0022) uses the same ones.

### Dev servers are detected and linked (issue #15, option 1)

**The agent reports ports; nothing flows through Hoplet.** For each session a browser has open, the agent scans the
listening TCP sockets of its user every 3 seconds and reports those held by a process started from the session's
working directory: a process whose current directory is inside it, or that descends from the session's Claude Code
process or from its terminal shell. The core tells the agent which sessions to watch (`ports.watch`, the set of
sessions with a subscribed browser, replaced as it changes); the agent answers `session.ports` on the control lane
when the set of ports changes, at most once a second per session. Nothing is scanned for a session nobody has
open.

**What is reported.** For each port: the port number, the address class of the socket (`loopback`, `any` or
`other`) and the executable name of the process (its basename, cut at 64 characters, control characters removed),
at most 16 ports per session. **Not reported:** command lines, arguments, environment, process IDs, processes
outside the session's directory, UDP and Unix sockets, whether the port speaks HTTP, and any content of any
connection. A server in a container whose port a process outside the working directory publishes is not seen.

**The inspector links, it does not embed.** The Browser tab lists the ports: "localhost:5173 is running · node ·
loopback". Open is a link in a new tab (`rel="noopener noreferrer"`), never a frame, so Hoplet has no CSP, cookie
or mixed-content problem with the dev server and sends nothing to it. The host in the link is the agent key
`preview_host` when set, else the machine's host name as the agent reports it; a `loopback` port is linked as
`localhost`, and the tab says that it opens only on the owner machine or through the user's own tunnel. Whether the
browser reaches the machine is the user's network (same LAN, Tailscale); the tab shows the address it used so that
a wrong one is visible. A Copy button gives `host:port`.

## Cost

The terminal is a new trust boundary: with `terminal` on, the host token can run any command as the agent's user on
that machine whenever no turn runs. Whoever holds the token can already do much of this through Claude Code with
Bypass permissions (docs/threat-model.md); the terminal removes the model and the permission trail between the
holder and the shell, which is why it is a switch the operator sets per machine, off by default.

It also costs an agent package with a PTY (a dependency such as `creack/pty` or the system calls directly, decided
with the code), process-tree cleanup and a start-up journal, a credit window on the control lane, a message family
and a protocol version, an announced capability, and a tab in the web UI with a terminal emulator (a bundled
dependency, decided with the UI mockup, which comes first). A dev server started in the terminal stops with the
idle timeout and at the start of a turn; the user restarts it or lets Claude Code start it. Port detection costs a
scan every 3 seconds per open session on the owner machine (`/proc` on Linux, the process and socket APIs on
macOS).

A preview works only where the browser reaches the owner machine, and a dev server bound to loopback reaches no
other machine at all. Hoplet holds no credential, no cookie and no traffic of the dev server.

## Alternatives

- *No terminal.* Every check is a prompt and a permission answer. Cost: none, and none of the value.
- *An unrestricted terminal*, any directory and any time, a turn included. The same boundary with the widest
  exposure, and a shell that races the working tree of a running turn. Rejected.
- *A terminal that survives disconnects, like tmux.* It needs a store of the output and a process of a machine
  nobody controls; it would outlive the connection that the audit and the limits rest on. Rejected.
- *The terminal on the bulk lane.* Output would queue behind snapshots and history pages, and the interrupt of a
  runaway command would wait. Rejected; the credit window bounds the control lane instead.
- *Proxy the dev server through the core.* A new trust boundary (dev servers exposed through the core), cookie and
  CSP handling, and bulk traffic that must not delay control work. Not built; a concrete need reopens it.
- *No preview.* The user opens the app on the owner machine. Cost: none.

## Revisit triggers

A preview is needed from a network that cannot reach the machine (the proxy option); a terminal that survives a
reconnect; more than one terminal per session; a vendor whose turn state the agent cannot read (the "no turn"
rule); a terminal on a Windows agent.
