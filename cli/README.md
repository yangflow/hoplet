# @hoplet/cli

The `hoplet` command: list, start, follow and prompt coding-agent sessions on a Hoplet core, and wait for a turn
with an exit code a script can branch on. It is built on [`@hoplet/sdk`](../sdk/README.md) and needs Node.js 22+.

```bash
export HOPLET_URL=https://hoplet.example.com
export HOPLET_TOKEN_FILE=~/.config/hoplet/token      # a file only you can read

hoplet sessions --state ready
hoplet new --device laptop --path ~/work/app --wait "Fix the failing test"
hoplet send 5b1c… "Now add a regression test" --wait --timeout 600
hoplet attach 5b1c…
```

From a repository checkout, `pnpm hoplet` runs the same command.

## Credentials

The token is read from `--token-file` / `HOPLET_TOKEN_FILE`, else `--token` / `HOPLET_TOKEN`. The core address is
`--url` / `HOPLET_URL` (default `http://127.0.0.1:7788`). Prefer a file or the environment: the arguments of a
running command are visible to other processes on the machine. Nothing is stored on disk and there is no login
command. A token file readable by other users draws a warning.

A scoped API token (Settings → API tokens, or `hoplet tokens create`) limits what a script may do: `send` and
`interrupt` need `send`, `new` needs `create`, answering in `attach` needs `answer`, and `sessions`, `devices`, `attach` and
`wait` need `read`. The commands that need the host token (`handoff`, `devices add`, `audit`, `tokens`) fail with
the error code `forbidden` under a scoped one.

## Commands

| Command | |
| --- | --- |
| `sessions [--device] [--state]` | list sessions (id, state, machine, provider, path, title) |
| `new --device --path [--provider] [--model] [--set option=value]... [--worktree[=<branch>]] [--wait] "<prompt>"` | start a session; prints its id |
| `attach <session> [--answer allow\|deny]` | follow the live output; ends with the turn |
| `send <session> "<text>" [--wait]` (alias `prompt`) | send a prompt; `-` reads it from standard input |
| `wait <session> [--timeout s] [--until-blocked] [--drain]` | wait for the running turn |
| `interrupt <session>` | end the running turn |
| `handoff <session> <machine> [--path]` | move the session to another machine |
| `devices`, `devices add <name> [--id]` | list machines; mint a device token and print the agent's `config.json` |
| `tokens list`, `tokens create --name --can ... [--allow machine[:path]]... [--days n]`, `tokens revoke <id>` | manage API tokens; the secret is printed once |
| `audit list`, `audit export`, `audit head`, `audit verify [--file] [--head]` | read and verify the audit log |

A machine is its id or its name. `new --worktree` starts the session in a new git worktree on a generated branch, `new --worktree=<branch>` on the branch you name (write the `=`; a space would make the next word the prompt). The worktree is made next to the project from its `HEAD`; the session id goes to standard output and `worktree <path> (<branch>)` to standard error (`worktree` in the `--json` document). It fails with `invalid-request` and a `reason` of `worktree-branch-invalid`, `worktree-not-a-git-repository`, `worktree-no-commit`, `worktree-branch-exists` or `worktree-path-exists`.

## Output and exit codes

`--json` prints one JSON document per command (one object per line for `attach`). Failures go to standard error as
`{"code","reason"?}` with `--json`, and as the message of the stable error code otherwise.

| Code | Meaning |
| --- | --- |
| 0 | done; for waiting commands the turn finished (`ok`) |
| 1 | error or wrong usage |
| 3 | `audit verify` found a broken chain |
| 4 | the turn failed |
| 5 | the session ended without a result |
| 6 | blocked on a permission request or a question |
| 7 | timeout |
| 130 | stopped with Ctrl-C while waiting |

A waiting command with `--json` prints `{"result","state","error"?}`.

## Build

`pnpm --filter @hoplet/cli build` writes `dist/hoplet.js`, one ESM file; `@hoplet/sdk` stays a dependency.
