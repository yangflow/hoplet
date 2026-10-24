import { CliError } from './errors.ts';

/** `optional` is a switch that takes a value only in the `--name=value` form, so a word after it stays a word. */
type FlagKind = 'bool' | 'optional' | 'string' | 'list';

/** Every flag of every command; a command accepts the ones its spec lists, and the global ones. */
const FLAGS: Record<string, FlagKind> = {
  url: 'string',
  token: 'string',
  'token-file': 'string',
  json: 'bool',
  help: 'bool',
  // sessions, new
  device: 'string',
  state: 'string',
  tag: 'string',
  path: 'string',
  provider: 'string',
  model: 'string',
  set: 'list',
  template: 'string',
  worktree: 'optional',
  // new, send, wait
  wait: 'bool',
  timeout: 'string',
  'start-timeout': 'string',
  'until-blocked': 'bool',
  drain: 'bool',
  // attach
  answer: 'string',
  // devices add
  id: 'string',
  // audit
  session: 'string',
  actor: 'string',
  type: 'string',
  from: 'string',
  to: 'string',
  q: 'string',
  limit: 'string',
  after: 'string',
  asc: 'bool',
  format: 'string',
  file: 'string',
  head: 'string',
  // tokens create
  name: 'string',
  can: 'list',
  allow: 'list',
  days: 'string',
};

const GLOBAL = ['url', 'token', 'token-file', 'json', 'help'];
const WAITING = ['wait', 'timeout', 'start-timeout', 'until-blocked', 'drain'];
const AUDIT_FILTERS = ['session', 'device', 'actor', 'type', 'from', 'to', 'q'];

export type CommandSpec = {
  flags: string[];
  min: number;
  max: number;
  usage: string;
};

/** The commands, by name (`audit list`, `tokens create`, …). */
export const COMMANDS: Record<string, CommandSpec> = {
  sessions: {
    flags: ['device', 'state', 'tag'],
    min: 0,
    max: 0,
    usage: 'hoplet sessions [--device <machine>] [--state <state>]',
  },
  new: {
    flags: ['device', 'path', 'provider', 'model', 'set', 'template', 'worktree', ...WAITING],
    min: 1,
    max: Number.POSITIVE_INFINITY,
    usage:
      'hoplet new --device <machine> --path <dir> [--provider claude|codex] [--model <id>] [--set option=value] [--worktree[=<branch>]] [--wait] "<prompt>"',
  },
  attach: {
    flags: ['answer'],
    min: 1,
    max: 1,
    usage: 'hoplet attach <session> [--answer allow|deny]',
  },
  send: {
    flags: WAITING,
    min: 2,
    max: Number.POSITIVE_INFINITY,
    usage: 'hoplet send <session> "<text>" [--wait] [--timeout <seconds>]',
  },
  wait: {
    flags: ['timeout', 'until-blocked', 'drain'],
    min: 1,
    max: 1,
    usage: 'hoplet wait <session> [--timeout <seconds>] [--until-blocked] [--drain]',
  },
  interrupt: { flags: [], min: 1, max: 1, usage: 'hoplet interrupt <session>' },
  handoff: {
    flags: ['path'],
    min: 2,
    max: 2,
    usage: 'hoplet handoff <session> <machine> [--path <dir>]',
  },
  devices: { flags: [], min: 0, max: 0, usage: 'hoplet devices' },
  'devices add': {
    flags: ['id'],
    min: 1,
    max: Number.POSITIVE_INFINITY,
    usage: 'hoplet devices add <name> [--id <deviceId>]',
  },
  'audit list': {
    flags: [...AUDIT_FILTERS, 'limit', 'after', 'asc'],
    min: 0,
    max: 0,
    usage: 'hoplet audit list [filters] [--limit <n>] [--after <seq>] [--asc]',
  },
  'audit export': {
    flags: [...AUDIT_FILTERS, 'format'],
    min: 0,
    max: 0,
    usage: 'hoplet audit export [filters] [--format jsonl|csv]',
  },
  'audit verify': {
    flags: ['file', 'head'],
    min: 0,
    max: 0,
    usage: 'hoplet audit verify [--file <export.jsonl>] [--head <seq>:<hash>]',
  },
  'audit head': { flags: [], min: 0, max: 0, usage: 'hoplet audit head' },
  'tokens list': { flags: [], min: 0, max: 0, usage: 'hoplet tokens list' },
  'tokens create': {
    flags: ['name', 'can', 'allow', 'days'],
    min: 0,
    max: 0,
    usage:
      'hoplet tokens create --name <name> --can read,send,create,answer [--allow <machine>[:<path>]] [--days <1-365>]',
  },
  'tokens revoke': { flags: [], min: 1, max: 1, usage: 'hoplet tokens revoke <id>' },
};

/** Words that are other names for a command. */
const ALIASES: Record<string, string> = { prompt: 'send' };
const SUBCOMMANDS: Record<string, string[]> = {
  devices: ['add'],
  audit: ['list', 'export', 'verify', 'head'],
  tokens: ['list', 'create', 'revoke'],
};

export type Flags = Record<string, string | boolean | string[]>;

export type Parsed =
  | { kind: 'help'; topic?: string }
  | { kind: 'command'; name: string; positionals: string[]; flags: Flags };

function usage(reason: string): CliError {
  return new CliError('usage', reason);
}

/** Splits the arguments into flags and words, then checks them against the command they name. */
export function parseArgs(argv: readonly string[]): Parsed {
  const words: string[] = [];
  const flags: Flags = {};
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === undefined) break;
    if (arg === '--') {
      words.push(...argv.slice(i + 1));
      break;
    }
    if (arg === '-h') {
      flags.help = true;
      continue;
    }
    if (arg === '-' || !arg.startsWith('--')) {
      words.push(arg);
      continue;
    }
    const eq = arg.indexOf('=');
    const name = eq < 0 ? arg.slice(2) : arg.slice(2, eq);
    const kind = FLAGS[name];
    if (kind === undefined) throw usage(`Unknown flag: --${name}`);
    if (kind === 'optional') {
      flags[name] = eq < 0 ? true : arg.slice(eq + 1);
      continue;
    }
    if (kind === 'bool') {
      if (eq >= 0) throw usage(`--${name} takes no value`);
      flags[name] = true;
      continue;
    }
    let value: string | undefined;
    if (eq >= 0) value = arg.slice(eq + 1);
    else {
      value = argv[++i];
      if (value === undefined) throw usage(`--${name} needs a value`);
    }
    if (kind === 'list') {
      const list = flags[name];
      if (Array.isArray(list)) list.push(value);
      else flags[name] = [value];
    } else {
      flags[name] = value;
    }
  }

  const first = words[0];
  if (first === undefined || first === 'help') {
    const topic = first === 'help' ? words[1] : undefined;
    return topic === undefined ? { kind: 'help' } : { kind: 'help', topic };
  }
  let name = ALIASES[first] ?? first;
  let rest = words.slice(1);
  const subs = SUBCOMMANDS[name];
  if (subs !== undefined) {
    const sub = rest[0];
    if (sub !== undefined && subs.includes(sub)) {
      name = `${name} ${sub}`;
      rest = rest.slice(1);
    } else if (name !== 'devices') {
      if (flags.help === true) return { kind: 'help', topic: name };
      throw usage(`Usage: hoplet ${name} ${subs.join('|')} (see hoplet --help)`);
    }
  }
  const spec = COMMANDS[name];
  if (spec === undefined) throw usage(`Unknown command: ${first}. See hoplet --help.`);
  if (flags.help === true) return { kind: 'help', topic: name };

  const allowed = new Set([...GLOBAL, ...spec.flags]);
  for (const flag of Object.keys(flags)) {
    if (!allowed.has(flag)) throw usage(`--${flag} does not belong to hoplet ${name}`);
  }
  if (rest.length < spec.min || rest.length > spec.max) {
    throw usage(`Usage: ${spec.usage}`);
  }
  return { kind: 'command', name, positionals: rest, flags };
}

export function str(flags: Flags, name: string): string | undefined {
  const value = flags[name];
  return typeof value === 'string' ? value : undefined;
}

export function bool(flags: Flags, name: string): boolean {
  return flags[name] === true;
}

export function list(flags: Flags, name: string): string[] {
  const value = flags[name];
  return Array.isArray(value) ? value : [];
}
