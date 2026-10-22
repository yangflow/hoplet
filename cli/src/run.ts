import { Hoplet, type HopletOptions } from '@hoplet/sdk';
import {
  auditExport,
  auditHead,
  auditList,
  auditVerify,
  devices,
  devicesAdd,
  tokensCreate,
  tokensList,
  tokensRevoke,
} from './admin-commands.ts';
import { bool, type Flags, parseArgs, str } from './args.ts';
import { attach } from './attach.ts';
import type { Context, Deps } from './context.ts';
import { CliError, failureOf, failureText } from './errors.ts';
import { HELP, helpFor } from './help.ts';
import { handoff, interrupt, newSession, send, sessions, wait } from './session-commands.ts';

const DEFAULT_URL = 'http://127.0.0.1:7788';

/** The token: `--token`, then `--token-file`, then `HOPLET_TOKEN`, then `HOPLET_TOKEN_FILE`. */
function tokenOf(flags: Flags, deps: Deps, warn: (text: string) => void): string {
  const direct = str(flags, 'token');
  const fileFlag = str(flags, 'token-file');
  if (direct !== undefined && fileFlag !== undefined) {
    throw new CliError('usage', 'Give --token or --token-file, not both.');
  }
  if (direct !== undefined) return direct.trim();
  const path = fileFlag ?? (deps.env.HOPLET_TOKEN ? undefined : deps.env.HOPLET_TOKEN_FILE);
  if (path === undefined || path === '') {
    const env = deps.env.HOPLET_TOKEN?.trim();
    if (env) return env;
    throw new CliError('no-token');
  }
  if (!deps.readFile) throw new CliError('usage', 'Files cannot be read here.');
  let read: { text: string; mode: number };
  try {
    read = deps.readFile(path);
  } catch (err) {
    throw new CliError('usage', `Cannot read ${path}: ${err instanceof Error ? err.message : err}`);
  }
  const token = read.text.trim();
  if (token === '') throw new CliError('usage', `${path} is empty.`);
  if (process.platform !== 'win32' && (read.mode & 0o077) !== 0) {
    warn(`warning: ${path} can be read by other users; run chmod 600 on it.`);
  }
  return token;
}

/** Runs one command line and returns the exit code. */
export async function runCli(argv: readonly string[], deps: Deps): Promise<number> {
  const { io } = deps;
  let json = argv.includes('--json');
  try {
    const parsed = parseArgs(argv);
    if (parsed.kind === 'help') {
      io.stdout(parsed.topic === undefined ? HELP : helpFor(parsed.topic));
      return 0;
    }
    const { name, positionals, flags } = parsed;
    json = bool(flags, 'json');
    const note = (text: string): void => io.stderr(`${text}\n`);
    const needsToken = !(name === 'audit verify' && str(flags, 'file') !== undefined);

    const options: HopletOptions = {
      url: str(flags, 'url') ?? deps.env.HOPLET_URL ?? DEFAULT_URL,
      token: needsToken ? tokenOf(flags, deps, note) : '',
      ...(deps.fetch ? { fetch: deps.fetch } : {}),
      ...(deps.WebSocket ? { WebSocket: deps.WebSocket } : {}),
    };
    const client = deps.createClient ? deps.createClient(options) : new Hoplet(options);
    const ctx: Context = {
      client,
      flags,
      json,
      deps,
      doc: (value) => io.stdout(`${JSON.stringify(value)}\n`),
      line: (text) => io.stdout(`${text}\n`),
      note,
    };
    const [a = '', b = ''] = positionals;
    const rest = positionals.slice(1);

    switch (name) {
      case 'sessions':
        return await sessions(ctx);
      case 'new':
        return await newSession(ctx, positionals);
      case 'attach':
        return await attach(ctx, a);
      case 'send':
        return await send(ctx, a, rest);
      case 'wait':
        return await wait(ctx, a);
      case 'interrupt':
        return await interrupt(ctx, a);
      case 'handoff':
        return await handoff(ctx, a, b);
      case 'devices':
        return await devices(ctx);
      case 'devices add':
        return await devicesAdd(ctx, positionals);
      case 'audit list':
        return await auditList(ctx);
      case 'audit export':
        return await auditExport(ctx);
      case 'audit verify':
        return await auditVerify(ctx);
      case 'audit head':
        return await auditHead(ctx);
      case 'tokens list':
        return await tokensList(ctx);
      case 'tokens create':
        return await tokensCreate(ctx);
      case 'tokens revoke':
        return await tokensRevoke(ctx, a);
      default:
        throw new CliError('usage', `Unknown command: ${name}`);
    }
  } catch (err) {
    const failure = failureOf(err);
    io.stderr(json ? `${JSON.stringify(failure)}\n` : `${failureText(failure)}\n`);
    return failure.code === 'aborted' ? 130 : 1;
  }
}
