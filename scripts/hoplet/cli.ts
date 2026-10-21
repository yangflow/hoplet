/** Host CLI: hoplet prompt, hoplet interrupt, hoplet handoff, hoplet devices — host token REST, not hoplet-agent. */
import { readFileSync } from 'node:fs';
import { verifyAuditFile } from './audit-verify.ts';
import { HostClient } from './client.ts';

export type CliIo = {
  stdout: (line: string) => void;
  stderr: (line: string) => void;
};

const HELP = `hoplet — host-token CLI for Hoplet (not hoplet-agent)

Usage:
  hoplet [--url URL] [--token TOKEN] <command> …

Environment:
  HOPLET_URL    Core origin (default http://127.0.0.1:7788)
  HOPLET_TOKEN  Host token (required unless --token)

Commands:
  prompt <sessionId> <text…>   send a prompt to a session's Claude Code process
  interrupt <sessionId>        end the turn that is running
  handoff <sessionId> <deviceId> [--path <dir>]
                               move the session, with its working tree, to another connected
                               machine; --path names the clone to use when that machine has
                               no watched project with the same origin
  devices                      list the machines with their id, name and connection
  devices add <name> [--id <deviceId>]
                               mint a device token and print the agent's config.json; with
                               --id of an existing machine its token is replaced
  audit list [filters] [--limit <n>] [--after <seq>] [--asc]
                               print audit records, newest first
  audit export [filters] [--format jsonl|csv]
                               print the matching records, oldest first
  audit verify [--file <export.jsonl>] [--head <seq>:<hash>]
                               walk the hash chain and report the first break; --file checks an
                               export offline, --head checks that the record at <seq> still has <hash>
  audit head                   print the newest record's seq and hash, to write down elsewhere
                               Filters: --session --device --actor <kind[:id]> --type --from --to --q
                               (from and to are Unix ms)

The local catalog stays on hoplet-agent.
`;

export type ParseResult =
  | {
      ok: true;
      url: string;
      token: string;
      args: string[];
      path: string | undefined;
      id: string | undefined;
      /** The audit flags that were given, by name without the dashes. */
      flags?: Record<string, string>;
    }
  | { ok: false; message: string; showHelp?: boolean };

export function parseArgv(argv: string[], env: NodeJS.ProcessEnv): ParseResult {
  let url = env.HOPLET_URL?.trim() || 'http://127.0.0.1:7788';
  let token = env.HOPLET_TOKEN?.trim() || '';
  const rest: string[] = [];
  let path: string | undefined;
  let id: string | undefined;
  const flags: Record<string, string> = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === undefined) break;
    if (a === '-h' || a === '--help' || a === 'help') {
      return { ok: false, message: HELP, showHelp: true };
    }
    if (a === '--url') {
      const v = argv[++i];
      if (!v) return { ok: false, message: '--url needs a value' };
      url = v;
      continue;
    }
    if (a === '--path') {
      const v = argv[++i];
      if (!v) return { ok: false, message: '--path needs a value' };
      path = v;
      continue;
    }
    if (a === '--id') {
      const v = argv[++i];
      if (!v) return { ok: false, message: '--id needs a value' };
      id = v;
      continue;
    }
    if (a === '--token') {
      const v = argv[++i];
      if (!v) return { ok: false, message: '--token needs a value' };
      token = v;
      continue;
    }
    if (a === '--asc') {
      flags.asc = '1';
      continue;
    }
    if (AUDIT_FLAGS.includes(a)) {
      const v = argv[++i];
      if (!v) return { ok: false, message: `${a} needs a value` };
      flags[a.slice(2)] = v;
      continue;
    }
    if (a.startsWith('-')) {
      return { ok: false, message: `unknown flag: ${a}` };
    }
    rest.push(a);
  }
  if (rest.length === 0) {
    return { ok: false, message: HELP, showHelp: true };
  }
  if (!token) {
    return { ok: false, message: 'HOPLET_TOKEN or --token is required' };
  }
  return {
    ok: true,
    url,
    token,
    args: rest,
    path,
    id,
    ...(Object.keys(flags).length ? { flags } : {}),
  };
}

const AUDIT_FLAGS = [
  '--session',
  '--device',
  '--actor',
  '--type',
  '--from',
  '--to',
  '--q',
  '--limit',
  '--after',
  '--format',
  '--file',
  '--head',
];
const AUDIT_FILTERS = ['session', 'device', 'actor', 'type', 'from', 'to', 'q'];

function auditQuery(flags: Record<string, string>, names: string[]): URLSearchParams {
  const query = new URLSearchParams();
  for (const name of names) {
    const value = flags[name];
    if (value !== undefined) query.set(name, value);
  }
  return query;
}

function joinText(parts: string[]): string {
  return parts.join(' ').trim();
}

async function runAudit(
  client: HostClient,
  rest: string[],
  flags: Record<string, string>,
  io: CliIo,
): Promise<number> {
  const [sub, ...extra] = rest;
  const allowed: Record<string, string[]> = {
    list: [...AUDIT_FILTERS, 'limit', 'after', 'asc'],
    export: [...AUDIT_FILTERS, 'format'],
    verify: ['file', 'head'],
    head: [],
  };
  if (sub === undefined || !(sub in allowed) || extra.length > 0) {
    io.stderr('usage: hoplet audit list|export|verify|head (see hoplet --help)');
    return 1;
  }
  const stray = Object.keys(flags).find((name) => !allowed[sub]?.includes(name));
  if (stray) {
    io.stderr(`--${stray} does not belong to hoplet audit ${sub}`);
    return 1;
  }
  const fail = (error: { status: number; message: string }): number => {
    io.stderr(error.message);
    return error.status === 401 ? 1 : 2;
  };
  const head = flags.head;
  if (head !== undefined && !/^\d{1,15}:[0-9a-f]{64}$/.test(head)) {
    io.stderr('--head is <seq>:<hash>, the hash 64 hex digits');
    return 1;
  }
  if (sub === 'list') {
    const query = auditQuery(flags, [...AUDIT_FILTERS, 'limit', 'after']);
    if (flags.asc) query.set('order', 'asc');
    const result = await client.auditEvents(query);
    if (!result.ok) return fail(result.error);
    for (const e of result.data.events) {
      io.stdout(
        [
          e.seq,
          new Date(e.at).toISOString(),
          e.type,
          `${e.actorKind}:${e.actorId}`,
          e.sessionId ?? '-',
          JSON.stringify(e.summary),
        ].join('\t'),
      );
    }
    return 0;
  }
  if (sub === 'export') {
    const result = await client.auditExport(auditQuery(flags, [...AUDIT_FILTERS, 'format']));
    if (!result.ok) return fail(result.error);
    io.stdout(result.text.replace(/\r?\n$/, ''));
    return 0;
  }
  if (sub === 'head') {
    const result = await client.auditEvents(new URLSearchParams({ limit: '1' }));
    if (!result.ok) return fail(result.error);
    const newest = result.data.events[0];
    io.stdout(newest ? `${newest.seq}:${newest.hash}` : 'empty');
    return 0;
  }
  const [headSeq, headHash] = head?.split(':') ?? [];
  const expected =
    headSeq !== undefined && headHash !== undefined
      ? { seq: Number(headSeq), hash: headHash }
      : undefined;
  let verdict: {
    ok: boolean;
    checked: number;
    head: unknown;
    firstBreak: { seq: number; reason: string; message: string } | null;
  };
  if (flags.file !== undefined) {
    let text: string;
    try {
      text = readFileSync(flags.file, 'utf8');
    } catch {
      io.stderr(`cannot read ${flags.file}`);
      return 1;
    }
    verdict = verifyAuditFile(text, expected);
  } else {
    const result = await client.auditVerify(head);
    if (!result.ok) return fail(result.error);
    verdict = result.data;
  }
  io.stdout(JSON.stringify(verdict));
  if (!verdict.ok) {
    io.stderr(
      `audit chain broken at seq ${verdict.firstBreak?.seq}: ${verdict.firstBreak?.message}`,
    );
    return 3;
  }
  return 0;
}

export async function runCli(
  argv: string[],
  env: NodeJS.ProcessEnv,
  io: CliIo,
  fetchImpl?: typeof fetch,
): Promise<number> {
  const parsed = parseArgv(argv, env);
  if (!parsed.ok) {
    io.stderr(parsed.message.replace(/\n$/, ''));
    return parsed.showHelp ? 0 : 1;
  }
  const client = new HostClient(
    fetchImpl
      ? { baseUrl: parsed.url, token: parsed.token, fetchImpl }
      : { baseUrl: parsed.url, token: parsed.token },
  );
  const [cmd, ...rest] = parsed.args;
  if (parsed.path !== undefined && cmd !== 'handoff') {
    io.stderr('--path belongs to hoplet handoff');
    return 1;
  }
  if (parsed.id !== undefined && !(cmd === 'devices' && rest[0] === 'add')) {
    io.stderr('--id belongs to hoplet devices add');
    return 1;
  }
  const flags = parsed.flags ?? {};
  if (cmd !== 'audit' && Object.keys(flags).length > 0) {
    io.stderr(`--${Object.keys(flags)[0]} belongs to hoplet audit`);
    return 1;
  }
  if (cmd === 'audit') return runAudit(client, rest, flags, io);
  if (cmd === 'prompt') {
    const sessionId = rest[0];
    const text = joinText(rest.slice(1));
    if (!sessionId || !text) {
      io.stderr('usage: hoplet prompt <sessionId> <text…>');
      return 1;
    }
    const result = await client.prompt(sessionId, text);
    if (!result.ok) {
      io.stderr(result.error.message);
      return result.error.status === 401 ? 1 : 2;
    }
    io.stdout(JSON.stringify(result.data));
    return 0;
  }
  if (cmd === 'interrupt') {
    const sessionId = rest[0];
    if (!sessionId || rest.length !== 1) {
      io.stderr('usage: hoplet interrupt <sessionId>');
      return 1;
    }
    const result = await client.interrupt(sessionId);
    if (!result.ok) {
      io.stderr(result.error.message);
      return result.error.status === 401 ? 1 : 2;
    }
    io.stdout(JSON.stringify(result.data));
    return 0;
  }
  if (cmd === 'handoff') {
    const [sessionId, deviceId] = rest;
    if (!sessionId || !deviceId || rest.length !== 2) {
      io.stderr('usage: hoplet handoff <sessionId> <deviceId> [--path <dir>]');
      return 1;
    }
    const result = await client.handoff(sessionId, deviceId, parsed.path);
    if (!result.ok) {
      io.stderr(result.error.message);
      return result.error.status === 401 ? 1 : 2;
    }
    io.stdout(JSON.stringify(result.data));
    return 0;
  }
  if (cmd === 'devices') {
    if (rest.length === 0) {
      const result = await client.devices();
      if (!result.ok) {
        io.stderr(result.error.message);
        return result.error.status === 401 ? 1 : 2;
      }
      for (const d of result.data.devices) {
        io.stdout(`${d.deviceId}\t${d.name}\t${d.connected ? 'connected' : 'offline'}`);
      }
      return 0;
    }
    const name = joinText(rest.slice(1));
    if (rest[0] !== 'add' || !name) {
      io.stderr('usage: hoplet devices [add <name> [--id <deviceId>]]');
      return 1;
    }
    const result = await client.mintDevice(name, parsed.id);
    if (!result.ok) {
      io.stderr(result.error.message);
      return result.error.status === 401 ? 1 : 2;
    }
    io.stdout(
      JSON.stringify(
        {
          server_url: client.baseUrl,
          token: result.data.token,
          device_id: result.data.deviceId,
          device_name: result.data.name,
        },
        null,
        2,
      ),
    );
    io.stderr(
      'Save as ~/.hoplet/config.json on that machine and run hoplet-agent daemon. The token is shown once.',
    );
    return 0;
  }
  io.stderr(`unknown command: ${cmd}\n\n${HELP}`);
  return 1;
}

if (import.meta.main) {
  const code = await runCli(process.argv.slice(2), process.env, {
    stdout: (line) => process.stdout.write(`${line}\n`),
    stderr: (line) => process.stderr.write(`${line}\n`),
  });
  process.exit(code);
}
