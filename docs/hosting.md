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
  token travels in a URL.
- Outbound connections: none from the core. The agent connects only to the core; Claude Code processes it
  starts make their own connections to Anthropic, and `git` may fetch from the repository's `origin` during
  handoff installation.

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
| Docker volume `hoplet-data` (`/data` in the container) | `hoplet.db` (SQLite index, which also holds what the user did to a session in the web UI: a title of their own, a pin, an archive, the last slash commands and skills a session reported per machine and directory, and the messages queued for a session), `sessions/<sessionId>/c-<seq>.ndjson[.gz]` (session lines), `snapshots/<sessionId>/<snapshotId>.tar.gz` (latest workspace snapshot per session) |
| Agent state root (`~/.hoplet/` by default) | `device-id`, `config.json`, `projects.json`, `runtime/` (running processes), `handoff/` (installation records), `attachments/` (images uploaded for prompts: at most 64 files and 64 MiB, each kept at most an hour) |
| Development machine | Claude Code's store and working trees; worktrees created by handoff, next to the clone they belong to |

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
| `claude_bin` | `claude` on `PATH` | Claude Code executable |
| `poll_interval_ms` | `1000` | scan interval |
| `runtime_idle_minutes` | `30` | idle time after which a Claude Code process stops |
| `runtime_max_idle_processes` | `3` | Claude Code processes kept running between turns; processes in a turn are not limited |

`<hoplet_home>/projects.json` lists watched project paths and the session IDs imported from each:
`{ "projects": [{ "path": "/abs/project", "imported": ["<session>"] }] }`. A missing or empty file syncs
nothing. `hoplet-agent projects` and `hoplet-agent sessions` maintain it; the daemon rereads it on every scan.

## Resources

- Idle memory: core under 80 MB; agent under 30 MB. Each Claude Code process the agent runs holds memory that
  grows with its session: about 230 MB for a small session and 600 MB for a 256 MB session (Claude Code
  2.1.289). Processes in a turn are not limited; size the machine for the sessions you run in parallel.
- Image size under 250 MB.
- Disk: session bytes / 20 after compression, plus the index, plus at most one snapshot per session (at most
  64 MiB each). There is no quota; a full disk fails appends, which the agent retries.
- Session lines are at most 32 MiB; append batches at most 1 MiB raw; WebSocket messages at most 64 MiB.
- Snapshots: at most 64 MiB compressed and 256 MiB uncompressed, enforced while reading and decompressing.
- Workspace file reads return at most 256 KiB of text, or an image of at most 4 MiB whole; workspace diffs at
  most 256 KiB.

## Upgrades

Pull the new image and restart: `docker compose pull && docker compose up -d`. Migrations in
`core/src/migrations/` run forward at startup; a data directory with a newer schema is refused. Replace
`hoplet-agent` on every machine with the version built from the same commit as the image; the agent and the core
refuse a `device.hello` whose protocol version differs. Back up the stopped data directory before upgrading;
downgrading means restoring that backup with the matching binaries.

## Uninstall

`docker compose down`, remove the `hoplet-data` volume, and on every development machine stop the agent and
remove its state root. Worktrees created by handoff stay in place as ordinary git worktrees.
