import { COMMANDS } from './args.ts';

export const HELP = `hoplet: drive coding-agent sessions on a Hoplet core

Usage:
  hoplet [--url URL] [--token-file FILE] [--json] <command> …

Configuration:
  HOPLET_URL         Core origin (default http://127.0.0.1:7788)
  HOPLET_TOKEN       API token
  HOPLET_TOKEN_FILE  File holding the token (or --token-file); keep it readable by you only
  --token <token>    The token as an argument. Other processes on this machine can read the arguments
                     of a running command; prefer HOPLET_TOKEN_FILE.
  Nothing is stored on disk by this command.

Sessions:
  sessions [--device <machine>] [--state <state>] [--tag <tag>]
                          list the sessions
  new --device <machine> --path <dir> [--provider claude|codex] [--model <id>]
      [--set option=value]… [--wait] "<prompt>"
                          start a session in a project of a machine; <machine> is its id or its name
  attach <session> [--answer allow|deny]
                          follow the live output; permission requests are asked at the terminal, or
                          answered with --answer. Ends when the turn ends (Ctrl-C leaves earlier).
  send <session> "<text>" [--wait]       (alias: prompt)
                          send a prompt; use - as the text to read it from standard input
  wait <session> [--timeout <seconds>] [--until-blocked] [--drain]
                          wait for the turn that is running to end
  interrupt <session>     end the turn that is running

  --wait on new and send waits for the turn too, with --timeout <seconds> and --until-blocked.
  Exit codes of wait and --wait: 0 finished, 1 error, 4 the turn failed, 5 ended without a result,
  6 blocked on a permission request, 7 timeout.

Tokens (host token):
  tokens list
  tokens create --name <name> --can read,send,create,answer [--allow <machine>[:<path>]]… [--days <1-365>]
                          the secret is printed once
  tokens revoke <id>

Machines and administration (host token):
  handoff <session> <machine> [--path <dir>]
                          move the session, with its working tree, to another connected machine;
                          --path names the clone to use when that machine has no watched project
                          with the same origin
  devices                 list the machines with their id, name and connection
  devices add <name> [--id <deviceId>]
                          mint a device token and print the agent's config.json; with --id of an
                          existing machine its token is replaced
  audit list [filters] [--limit <n>] [--after <seq>] [--asc]
                          print audit records, newest first
  audit export [filters] [--format jsonl|csv]
                          print the matching records, oldest first
  audit verify [--file <export.jsonl>] [--head <seq>:<hash>]
                          walk the hash chain and report the first break (exit code 3); --file checks
                          an export offline, --head checks that the record at <seq> still has <hash>
  audit head              print the newest record's seq and hash, to write down elsewhere
                          Filters: --session --device --actor <kind[:id]> --type --from --to --q
                          (from and to are Unix ms)

--json prints one JSON document per command (one object per line for attach); failures go to standard
error as {"code","reason"?}.
`;

/** The help of one command, or the overview when the topic is unknown. */
export function helpFor(topic: string | undefined): string {
  if (topic === undefined) return HELP;
  const spec = COMMANDS[topic];
  return spec === undefined
    ? HELP
    : `Usage: ${spec.usage}\n\nSee hoplet --help for the flags every command takes.\n`;
}
