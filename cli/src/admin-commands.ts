import type { TokenCreateRequest } from '@hoplet/sdk';
import { bool, list, str } from './args.ts';
import { verifyAuditFile } from './audit-verify.ts';
import type { Context } from './context.ts';
import { CliError } from './errors.ts';
import { resolveDevice } from './session-commands.ts';

const CAPABILITIES = ['read', 'send', 'create', 'answer'] as const;
type Capability = (typeof CAPABILITIES)[number];
const DEFAULT_DAYS = 90;
const HEAD = /^(\d{1,15}):([0-9a-f]{64})$/;

export async function devices(ctx: Context): Promise<number> {
  const found = await ctx.client.listDevices();
  if (ctx.json) {
    ctx.doc({ devices: found });
    return 0;
  }
  for (const d of found)
    ctx.line([d.deviceId, d.name, d.connected ? 'connected' : 'offline'].join('\t'));
  return 0;
}

export async function devicesAdd(ctx: Context, words: string[]): Promise<number> {
  const name = words.join(' ');
  const minted = await ctx.client.mintDevice(name, str(ctx.flags, 'id'));
  ctx.doc({
    server_url: ctx.client.url,
    token: minted.token,
    device_id: minted.deviceId,
    device_name: minted.name,
  });
  if (!ctx.json) {
    ctx.note(
      'Save as ~/.hoplet/config.json on that machine and run hoplet-agent daemon. The token is shown once.',
    );
  }
  return 0;
}

const FILTERS = ['session', 'device', 'actor', 'type', 'from', 'to', 'q'];

function filters(ctx: Context): Record<string, string> {
  const query: Record<string, string> = {};
  for (const name of FILTERS) {
    const value = str(ctx.flags, name);
    if (value !== undefined) query[name] = value;
  }
  return query;
}

export async function auditList(ctx: Context): Promise<number> {
  const query: Record<string, string> = filters(ctx);
  for (const name of ['limit', 'after']) {
    const value = str(ctx.flags, name);
    if (value !== undefined) query[name] = value;
  }
  if (bool(ctx.flags, 'asc')) query.order = 'asc';
  const page = await ctx.client.auditEvents(query);
  if (ctx.json) {
    ctx.doc(page);
    return 0;
  }
  for (const e of page.events) {
    ctx.line(
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

export async function auditExport(ctx: Context): Promise<number> {
  const query = filters(ctx);
  const format = str(ctx.flags, 'format');
  if (format !== undefined) {
    if (format !== 'jsonl' && format !== 'csv') {
      throw new CliError('usage', '--format is jsonl or csv');
    }
    query.format = format;
  }
  const text = await ctx.client.auditExport(query);
  ctx.deps.io.stdout(text);
  return 0;
}

export async function auditHead(ctx: Context): Promise<number> {
  const page = await ctx.client.auditEvents({ order: 'desc', limit: 1 });
  const newest = page.events[0];
  if (ctx.json) {
    ctx.doc({ head: newest ? { seq: newest.seq, hash: newest.hash } : null });
  } else {
    ctx.line(newest ? `${newest.seq}:${newest.hash}` : 'empty');
  }
  return 0;
}

export async function auditVerify(ctx: Context): Promise<number> {
  const head = str(ctx.flags, 'head');
  const match = head === undefined ? null : HEAD.exec(head);
  if (head !== undefined && match === null) {
    throw new CliError('usage', '--head is <seq>:<hash>, as audit head prints it');
  }
  const file = str(ctx.flags, 'file');
  let verdict: {
    ok: boolean;
    checked: number;
    head: { seq: number; hash: string } | null;
    firstBreak: { seq: number; reason: string; message: string } | null;
  };
  if (file !== undefined) {
    if (!ctx.deps.readFile) throw new CliError('usage', 'Files cannot be read here.');
    let text: string;
    try {
      text = ctx.deps.readFile(file).text;
    } catch (err) {
      throw new CliError(
        'usage',
        `Cannot read ${file}: ${err instanceof Error ? err.message : err}`,
      );
    }
    verdict = verifyAuditFile(
      text,
      match?.[1] && match[2] ? { seq: Number(match[1]), hash: match[2] } : undefined,
    );
  } else {
    verdict = await ctx.client.auditVerify(head);
  }
  ctx.doc(verdict);
  if (verdict.ok) return 0;
  const broken = verdict.firstBreak;
  const reason = `audit chain broken at seq ${broken?.seq ?? '?'}: ${broken?.message ?? 'unknown'}`;
  if (ctx.json) {
    ctx.deps.io.stderr(`${JSON.stringify({ code: 'audit-broken', reason })}\n`);
  } else {
    ctx.note(reason);
  }
  return 3;
}

export async function tokensList(ctx: Context): Promise<number> {
  const { tokens } = await ctx.client.listTokens();
  if (ctx.json) {
    ctx.doc({ tokens });
    return 0;
  }
  for (const t of tokens) {
    ctx.line(
      [
        t.id,
        t.state,
        t.name,
        t.grant.capabilities.join(','),
        new Date(t.expiresAt).toISOString(),
      ].join('\t'),
    );
  }
  return 0;
}

function capabilities(ctx: Context): Capability[] {
  const found: Capability[] = [];
  for (const word of list(ctx.flags, 'can').flatMap((entry) => entry.split(','))) {
    const name = word.trim();
    if (name === '') continue;
    if (!CAPABILITIES.includes(name as Capability)) {
      throw new CliError('usage', `--can takes ${CAPABILITIES.join(', ')}, not "${name}"`);
    }
    if (!found.includes(name as Capability)) found.push(name as Capability);
  }
  if (found.length === 0) {
    throw new CliError(
      'usage',
      `tokens create needs --can ${CAPABILITIES.join(',')} (some of them)`,
    );
  }
  return found;
}

export async function tokensCreate(ctx: Context): Promise<number> {
  const name = str(ctx.flags, 'name');
  if (name === undefined || name.trim() === '') {
    throw new CliError('usage', 'tokens create needs --name <name>.');
  }
  const can = capabilities(ctx);
  const rawDays = str(ctx.flags, 'days');
  const days = rawDays === undefined ? DEFAULT_DAYS : Number(rawDays);
  if (!Number.isInteger(days) || days < 1 || days > 365) {
    throw new CliError('usage', '--days is a whole number from 1 to 365');
  }
  const allow: { deviceId: string; path?: string }[] = [];
  for (const entry of list(ctx.flags, 'allow')) {
    const colon = entry.indexOf(':');
    const machine = colon < 0 ? entry : entry.slice(0, colon);
    const path = colon < 0 ? undefined : entry.slice(colon + 1);
    if (machine === '' || path === '') {
      throw new CliError('usage', `--allow is <machine>[:<path>], not "${entry}"`);
    }
    const deviceId = await resolveDevice(ctx.client, machine);
    allow.push(path === undefined ? { deviceId } : { deviceId, path });
  }
  const request: TokenCreateRequest = {
    name,
    grant: { capabilities: can, ...(allow.length > 0 ? { allow } : {}) },
    expiresInDays: days,
  };
  const created = await ctx.client.createToken(request);
  if (ctx.json) {
    ctx.doc(created);
  } else {
    ctx.line(created.token);
    ctx.note(`Token ${created.id} created. The secret is shown once.`);
  }
  return 0;
}

export async function tokensRevoke(ctx: Context, id: string): Promise<number> {
  await ctx.client.revokeToken(id);
  if (ctx.json) ctx.doc({ ok: true });
  else ctx.line('revoked');
  return 0;
}
