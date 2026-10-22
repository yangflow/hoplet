# Hosting

## Hosts

| Host | Requirements | How it runs |
|---|---|---|
| Server: Linux or macOS with Docker | x86_64 or arm64, Docker Compose, 512 MB RAM free, disk for the sessions (Resources) | `image/compose.yaml` runs the image |
| Development machine: macOS or Linux | `hoplet-agent` binary, Claude Code 2.1.289 or newer (2.1.289 and 2.1.295 are tested; a newer version runs and `device.hello` reports it as untested; an older one is refused), `git` | `hoplet-agent daemon` as the user who runs Claude Code |

The server is one container image (`image/Dockerfile`, linux/amd64 and linux/arm64). One agent runs on every
machine whose Claude Code sessions are synced or controlled.

## Network

- The core listens on `HOPLET_PORT` (default 7788) inside the container; the compose file publishes it on
  `127.0.0.1` only. Access from other machines goes through a TLS reverse proxy on the same host.
- Paths: `/` (web UI), `/api/*` (REST), `/ws` (browser WebSocket), `/ws/agent` (agent WebSocket). The proxy
  upgrades WebSockets on both `/ws` paths and keeps them open for at least a day.
- Authentication: the host token (`HOPLET_TOKEN`) as `Authorization: Bearer` for REST; the browser WebSocket
  presents a single-use ticket from `POST /api/ws-tickets`, valid for 30 seconds; each agent presents its
  device token as `Authorization: Bearer` on `/ws/agent` ([ADR 0005](adr/0005-per-device-tokens.md)). No
  credential travels in a URL, with one exception: an action link `<public URL>/a#<token>` carries a signed,
  single-use token in its fragment, which the browser never sends to a server; the page posts it in a body to
  `/api/action/inspect` and `/api/action/decide`, the two routes that take no host token. Keep the proxy from
  logging request bodies on these routes.
- Outbound connections: the core makes HTTPS requests (HTTP only when a webhook URL says so) to the push
  services of the browsers the user subscribed and to the webhook URLs the user configured, and to nothing else;
  it does not follow redirects. With no subscription and no webhook it makes none. The agent connects only to
  the core; Claude Code processes it starts make their own connections to Anthropic, and `git` may fetch from
  the repository's `origin` during handoff installation. When a handoff clones a repository on a machine that
  has none ([ADR 0024](adr/0024-a-handoff-clones-the-repository-on-a-target-that-has-none.md)), that machine's
  agent runs `git clone` and `git ls-remote` against the `origin` over `https` or `ssh` and nothing else. It
  needs a credential helper or an ssh key that works without a prompt; a repository that asks for a password
  fails as `no-access`.
- Planned (ADR 0031): webhook hooks add outbound requests to the hook URLs the user configured, over `https`
  only. The core resolves the host and refuses loopback, private, shared, link-local (cloud metadata included) and
  other non-public addresses before it connects, follows no redirect and keeps no response body. Setting
  `HOPLET_HOOK_ALLOW_PRIVATE` lets a hook name a loopback or private address, over `http` too; link-local
  addresses are never allowed. Script hooks open no connection: the agent runs a local executable.

### Public URL

`HOPLET_PUBLIC_URL` is the address the user reaches the core at through the proxy, for example
`https://hoplet.example.com`. It is the base of the action links in notifications. An address with a path is
allowed; credentials, a query or a fragment are refused at startup. Without it the notifications carry no link.
Web Push also needs a secure origin: the browser subscribes only over HTTPS or on localhost.

### Reverse proxy

The core does not terminate TLS. A proxy on the same host accepts HTTPS and WSS and forwards plain HTTP to
`127.0.0.1:7788`.

```caddy
hoplet.example.internal {
  reverse_proxy 127.0.0.1:7788
}
```

```nginx
map $http_upgrade $connection_upgrade {
  default upgrade;
  ''      close;
}
server {
  listen 443 ssl;
  server_name hoplet.example.internal;
  ssl_certificate     /etc/ssl/hoplet.crt;
  ssl_certificate_key /etc/ssl/hoplet.key;
  client_max_body_size 1m;
  location / {
    proxy_pass http://127.0.0.1:7788;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_read_timeout 1d;
    proxy_send_timeout 1d;
  }
}
```

Machines on a Tailscale network reach the proxy by its Tailnet name; Tailscale Serve can terminate TLS in
place of the proxy. Never publish port 7788 on `0.0.0.0`.

## Data

| Where | What |
|---|---|
| Docker volume `hoplet-data` (`/data` in the container) | `hoplet.db` (SQLite index, which also holds what the user did to a session in the web UI: a title of their own, a pin, an archive, the last slash commands and skills a session reported per machine and directory, the messages queued for a session, the worktree Hoplet made for a handed-off session, the provider configuration each session belongs to, the IDs of deleted sessions, the session templates, `templates`, and the permission rules, `permission_rules`, the switches of the built-in ones, `permission_builtins`, and their revisions, `permission_revisions`; planned, ADR 0030: the audit log, `audit_events`; planned: how far it was derived from each session, `audit_cursor`), `sessions/<sessionId>/c-<seq>.ndjson[.gz]` (session lines), `snapshots/<sessionId>/<snapshotId>.tar.gz` (latest workspace snapshot per session), `notify-secrets.json` (mode 0600: the VAPID key pair for Web Push, the key that signs action links and the signing secrets of webhooks; the push subscriptions, the webhooks without their secrets and the preferences are in `hoplet.db`) |
| Agent state root (`~/.hoplet/` by default) | `device-id`, `config.json`, `projects.json`, `permission-rules.json` (mode 0600: the last permission rules the core sent, rewritten atomically on each push and read at start; without it the agent uses the built-in rules), `runtime/` (running processes), `handoff/` (installation records and the journal of a clone in progress, `<handoffId>.clone.json`, which the agent uses on start-up to remove a half-made clone), `hook.sock` (the local Unix socket of the Codex rule hook, mode 0600 in a 0700 directory; no network port, absent on Windows, [ADR 0032](adr/0032-codex-rule-hook-calls-the-agent-over-a-local-socket.md)), `attachments/` (images uploaded for prompts: at most 64 files and 64 MiB, each kept at most an hour) |
| Development machine | Claude Code's store, Codex's `sessions/` (read-only, except that handoff adds one rollout file) and working trees; worktrees created by handoff, next to the clone they belong to |

Rotating the notification secrets is deleting `notify-secrets.json` while the core is stopped: the next start
creates new ones. Every action link issued before stops working, push subscriptions made under the old VAPID key
are removed (the browsers subscribe again), and the signing secrets of webhooks must be entered again.

The audit log ([ADR 0030](adr/0030-the-audit-log-records-actions-in-a-hash-chain.md)) is in `hoplet.db`, so
the backup and the restore above cover it and nothing else needs running. It grows with the actions taken and,
once the tool calls are derived (planned), with the tool calls of your sessions, at roughly 0.5 KiB a record: a few
MiB a day for a machine making a few thousand tool calls, a few hundred MiB at the default 180 days of retention
(an estimate until measured). `HOPLET_AUDIT_RETENTION_DAYS` bounds it; only retention deletes records, once a day.
If the log cannot be written (a full disk, a read-only database), actions that must be recorded fail with
`audit-unavailable` and are not carried out. A restored backup is a chain that ends where the backup ended; copy `hoplet audit head`
somewhere off the host if you want to notice that records were removed.

Planned (ADR 0031): lifecycle hooks are kept in `hoplet.db` (the hooks, and one row per delivery with its body,
state and last status, kept 14 days after the delivery settles, at most 500 per hook, and up to 1000 waiting
deliveries per hook) and their signing secrets are in `notify-secrets.json`, next to the webhook secrets. A hook
secret is rotated from the web UI, which keeps the old one valid for 24 hours; deleting the file voids every hook
secret, and each hook then shows `needs-secret` and sends nothing until it is rotated.

Back up the stopped data directory, including SQLite WAL and SHM files. Restore by putting it back and
starting the container. Run one core per data directory, on a local filesystem with fsync and atomic rename.

## Configuration

### Core (environment variables)

| Setting | Default | Meaning |
|---|---|---|
| `HOPLET_TOKEN` | required; the core refuses to start without it | host token |
| `HOPLET_PORT` | `7788` | listening port inside the container |
| `HOPLET_DATA` | `/data` in the image | data directory |
| `HOPLET_WEB_DIR` | `/app/web/dist` in the image | built web UI |
| `HOPLET_AUDIT_RETENTION_DAYS` | `180` | days the audit log keeps records (ADR 0030); `0` keeps all. An environment variable only, so the host token cannot shorten it |
| `HOPLET_PUBLIC_URL` | none | the address of the core behind the proxy; the base of action links ([Public URL](#public-url)) |
| `HOPLET_HOOK_ALLOW_PRIVATE` | unset (planned, ADR 0031) | when set to `1`, webhook hooks may name loopback and private addresses, over `http` too; link-local and metadata addresses stay refused |

### Agent (`~/.hoplet/config.json`, or the file passed with `--config`)

| Setting | Default | Meaning |
|---|---|---|
| `server_url` | required | the core's base URL |
| `token` | required | the device token |
| `device_id` | generated once into `<hoplet_home>/device-id` | stable identity |
| `device_name` | host name | shown in the web UI |
| `hoplet_home` | `~/.hoplet` | agent state root |
| `browse_root` | the agent user's home directory | the directory the web UI's Add project browser may list; directories only, never files, and never outside it |
| `claude_projects` | `~/.claude/projects` | Claude Code session store |
| `codex_home` | `$CODEX_HOME`, else `~/.codex` | Codex home; the agent reads `sessions/` (rollouts, `.jsonl` and `.jsonl.zst`) and writes only the rollout of a handed-off session, `sessions/YYYY/MM/DD/rollout-<time>-<threadId>.jsonl` (mode 0600, uncompressed, named in the machine's local time from the session_meta timestamp, as Codex names its rollouts); it writes no other file under `codex_home` |
| `claude_bin` | `claude` on `PATH` | Claude Code executable |
| `codex_bin` | `codex` on `PATH` | Codex executable; the agent runs `codex app-server` from it, and only the codex-cli versions the driver was tested against (`0.163.0`) run |
| `poll_interval_ms` | `1000` | scan interval |
| `runtime_idle_minutes` | `30` | idle time after which a session's process (Claude Code or Codex) stops |
| `runtime_max_idle_processes` | `3` | processes kept running between turns; processes in a turn are not limited |
| `terminal` | `false` | allow the web UI to open a shell on this machine ([Terminal](#terminal)) |
| `preview_host` | the machine's host name | the host in the links to a session's dev servers ([Dev server links](#dev-server-links)) |
| `permission_deny` | none | deny rules of the operator, a JSON array of rules in the shape of the permission rules (docs/spec.md#permission-rules); they hold against any rule the core sends, and at most 100; the agent announces them to the core, which shows them read-only; a configuration with an invalid rule or more rules does not load |
| `hooks` | none (planned, ADR 0031) | script hooks the agent runs on session events ([Lifecycle hooks](#lifecycle-hooks)) |

`<hoplet_home>/projects.json` lists watched project paths and the session IDs imported from each:
`{ "projects": [{ "path": "/abs/project", "imported": ["<session>"] }] }`. A missing or empty file syncs
nothing. `hoplet-agent projects` and `hoplet-agent sessions` maintain it; the daemon rereads it on every scan.

### Provider configurations

`<hoplet_home>/providers.json` (mode 0600) holds the machine's provider configurations
([ADR 0028](adr/0028-provider-configurations-live-on-the-machine.md)): each has an `id` (a slug), a `name`, the
vendor it `extends` (`claude` or `codex`), an optional absolute `command`, `env` (variable name to value) and
`defaults` (new-session values of the driver's config options). The built-ins `claude` and `codex` exist without
the file and accept only `name`, `enabled` and `defaults`. `hoplet-agent provider` maintains the file: `list`,
`add <id> [--preset glm|zai|deepseek|minimax|minimax-cn|qwen]`, `set`, `env <id> <NAME>` (the value from standard
input, never echoed), `unset`, `remove`. A variable's value is a secret: it stays in this file and in the process the agent
starts, and `list` and the core see only the name and whether it is set. The daemon rereads the file on every
scan; a file that fails to parse is logged and the last good configurations stay in force.
`<hoplet_home>/sessions-config.json` records which configuration started each session, so a resumed session runs
under the same one. A session it does not list runs under its vendor's built-in configuration.

### Terminal

`"terminal": true` in the agent's `config.json`, then restart the agent, lets the web UI open a terminal on this
machine ([ADR 0026](adr/0026-the-inspector-opens-a-restricted-terminal-and-links-dev-servers.md)). It is off by
default, and only the operator of the machine can turn it on: the core cannot. The terminal runs the agent user's
`$SHELL` in the working directory of a session, only while no turn of that session runs, and anyone with the host
token can then run commands there as that user. Turn it on only for machines and users where that is acceptable;
with the key removed or `false` the agent does not offer the terminal. A terminal closes after 60 minutes without
input, after 8 hours, when a turn or a handoff starts and when the agent loses its connection to the core; at most
three run on a machine. The agent keeps `<hoplet_home>/terminal/` (one small record per open terminal, no content)
and removes what a crash left on start-up. The terminal's traffic uses the agent's existing connection to the core.

### Lifecycle hooks

Planned ([ADR 0031](adr/0031-the-session-lifecycle-events-and-the-two-hook-kinds.md)). `"hooks": [...]` in the
agent's `config.json` declares scripts the agent runs on session events. Each entry has an `id` (a slug, unique on
the machine), `command` (an absolute path to an executable), `events` (a list of event types, or `"*"`), optional
`projects` (absolute paths it applies to), `timeout_ms` (default 10000, at most 60000) and `env` (names of
variables to pass through). Only the operator of the machine can add or change a script hook: the core and the web
UI list, enable and disable them, and never add, edit or point one. The file is read at start, so a change takes
an agent restart. The agent refuses at load an entry whose `command` is relative, not a regular file, not
executable, or writable by group or others, and logs why.

A script runs as the agent's user, without a shell and without arguments, with the event JSON on standard input,
the project directory as its working directory, and an environment of only `PATH`, `HOME`, `USER`, `LANG`, `TZ`,
`TMPDIR`, the names in `env` and `HOPLET_HOOK_ID`, `HOPLET_EVENT_ID`, `HOPLET_EVENT_TYPE`. After `timeout_ms` it is
stopped (`SIGTERM`, then `SIGKILL` after 2 seconds). One run at a time per hook, at most 16 events waiting; more
are dropped and counted. Standard output is discarded; the exit status and the first 1 KiB of standard error are
reported to the core.

### Dev server links

The agent reports the TCP ports that processes started from a session's working directory listen on, for the
sessions open in a browser; the inspector shows them with a link, and no traffic passes through Hoplet. The link
opens in the browser, so the browser must reach the owner machine itself: the same network, a VPN such as
Tailscale, or a tunnel the user opens. The link's host is `preview_host`, else the machine's host name; set
`preview_host` to the name or address the browser uses (for example the machine's Tailscale name). A server bound
to loopback is linked as `localhost` and opens only on the owner machine or through a tunnel. Nothing needs to be
published on the server running the core, and the compose file does not change.

## Resources

- Idle memory: core under 80 MB; agent under 30 MB. Each Claude Code process the agent runs holds memory that
  grows with its session: about 230 MB for a small session and 600 MB for a 256 MB session (Claude Code
  2.1.289). Processes in a turn are not limited; size the machine for the sessions you run in parallel.
- Image size under 250 MB.
- Machine resources: every 15 seconds the agent reads the machine's CPU, memory and the disk of its browse root
  and the process trees of the sessions it started, on Linux (`/proc`) and macOS (one `ps`, `sysctl` and
  `vm_stat`), and sends about 100 bytes per session. The core keeps the last 240 readings of each machine in memory
  (a few hundred kilobytes in all) and stores none. The thresholds at which machines warn are host settings, one
  small row in the core's database.
- Machine inventory: about once an hour, and after each connection, the agent runs `--version` of a fixed list of tools
  (at most four at once, 5 seconds each), `nvidia-smi` or `system_profiler`, and `git remote get-url origin` per
  watched project; the core keeps the latest report of each machine (a few kilobytes) and the host's labels in the
  `devices` table of its database.
- Disk: session bytes / 20 after compression, plus the index, plus at most one snapshot per session (at most
  64 MiB each). There is no quota; a full disk fails appends, which the agent retries.
- A clone made by a handoff takes the disk the repository takes, inside the browse root of the target; it has
  600 s in all and 60 s without progress, and a full disk fails it as `no-space`. A clone is kept when the
  handoff later fails or is cancelled, because it is the user's.
- Session lines are at most 32 MiB; append batches at most 1 MiB raw; WebSocket messages at most 64 MiB.
- Snapshots: at most 64 MiB compressed and 256 MiB uncompressed, enforced while reading and decompressing.
- Workspace file reads return at most 256 KiB of text, or an image of at most 4 MiB whole; workspace diffs at
  most 256 KiB.

## Upgrades

Pull the new image and restart: `docker compose pull && docker compose up -d`. Migrations in
`core/src/migrations/` run forward at startup; a data directory with a newer schema is refused. Replace
`hoplet-agent` on every machine with the version built from the same commit as the image; the agent and the core
refuse a `device.hello` whose protocol version differs (the version is 13). Back up the stopped data directory before upgrading;
downgrading means restoring that backup with the matching binaries.

The first start that runs migration 0021 (token usage) reads every stored session once in the background to
fill the `usage` table: it reads each session's chunk files sequentially (the IO of reading them once, about the
size of the stored session bytes) and takes seconds per gigabyte of session data, while the core serves requests
as usual; `GET /api/usage` says `backfilling` until it ends. If the core stops meanwhile the next start resumes.
The table adds a few tens of bytes per assistant message to the database (tens of megabytes for hundreds of
thousands of messages).

Migration 0022 (budgets) adds two small tables, `budgets` and `budget_alerts`; it reads nothing and needs no
action. Budgets are evaluated from the `usage` table in the core's process: no new port, resource or dependency.

The migration that adds the audit log (ADR 0030) creates `audit_events` and reads nothing; it needs no action. The log starts at the first start after the upgrade and does not cover earlier sessions. No new
port, resource or dependency; the database grows as the hosting section on data says.

Migration 0024 (session templates) adds the small table `templates`; it reads nothing and needs no action. No new port,
resource or dependency.

Migration 0025 (permission rules) adds the small tables `permission_rules`, `permission_builtins` and
`permission_revisions`; it reads nothing and needs no action. No new port, resource or dependency.

Migration 0026 (agent deny rules) adds the column `permission_deny` to `devices`, filled by the next `device.hello` of each
agent; it reads nothing and needs no action. No new port, resource or dependency.

## Uninstall

`docker compose down`, remove the `hoplet-data` volume, and on every development machine stop the agent and
remove its state root. Worktrees created by handoff stay in place as ordinary git worktrees.
