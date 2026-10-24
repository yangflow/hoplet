/**
 * The Markdown export of a session (ADR 0040): the one read that covers a whole session. It runs on the bulk
 * lane, at most two at once, reads the lines in pages up to the line count it saw when it started, yields to
 * waiting work between pages, masks credentials as it writes, and streams the file instead of building it.
 */
import type { ServerResponse } from 'node:http';
import {
  type Entry,
  EXPORT_DIFF_MAX_BYTES,
  type SessionExportQuery,
  type WorkspaceChange,
} from '@hoplet/shared';
import corePackage from '../../package.json' with { type: 'json' };
import type { Control } from '../control/index.ts';
import { log } from '../log/index.ts';
import { redactForExport } from '../scrub/index.ts';
import type { Store } from '../store/index.ts';
import {
  code,
  commandsOf,
  diffFileHeader,
  EXPORT_MAX_COMMANDS,
  type ExportCommand,
  type ExportTurn,
  fenced,
  renderCommands,
  renderHandoff,
  renderHeader,
  renderTurn,
  sizeText,
  TurnBuilder,
  timeParts,
} from './export-markdown.ts';
import { ApiError, RawResponse } from './http.ts';
import { fullEntry } from './projection/index.ts';

export const SESSION_EXPORT_CONCURRENCY = 2;

/** Lines read per page; the export yields to waiting work between pages. */
export const SESSION_EXPORT_PAGE = 500;

/** The most hand-off records an export reads. */
const MAX_HANDOFFS = 500;

export interface SessionExportDeps {
  store: Store;
  control: Pick<Control, 'ownerOf' | 'workspaceChanges' | 'workspaceDiff'>;
  now: () => number;
}

/** Writes with backpressure; false once the client is gone. */
async function write(res: ServerResponse, chunk: string): Promise<boolean> {
  if (res.destroyed || res.writableEnded) return false;
  if (res.write(chunk)) return true;
  await new Promise<void>((resolve) => {
    const done = () => {
      res.off('drain', done);
      res.off('close', done);
      resolve();
    };
    res.on('drain', done);
    res.on('close', done);
  });
  return !res.destroyed;
}

const yieldToWaiting = () => new Promise<void>((resolve) => setImmediate(resolve));

/** The session's entries in order, up to line `upTo`, a page at a time. */
async function* entriesOf(
  store: Store,
  sessionId: string,
  provider: Parameters<typeof fullEntry>[0],
  upTo: number,
): AsyncGenerator<Entry> {
  let start = 0;
  while (start < upTo) {
    const page = await store.readLines(
      sessionId,
      start,
      Math.min(SESSION_EXPORT_PAGE, upTo - start),
    );
    if (!page || page.end <= start) return;
    for (const [i, line] of page.lines.entries()) {
      const entry = fullEntry(provider, line, page.start + i);
      if (entry) yield entry;
    }
    start = page.end;
    await yieldToWaiting();
  }
}

type Handoff = { at: number; from: string; to: string };

/** The committed hand-offs of a session, oldest first. */
function handoffsOf(store: Store, sessionId: string): Handoff[] {
  const { events } = store.listAudit(
    { session: sessionId, type: 'session.handoff' },
    { order: 'asc', limit: MAX_HANDOFFS },
  );
  return events.flatMap((e) => {
    const { from, to, outcome } = e.summary as { from?: unknown; to?: unknown; outcome?: unknown };
    return outcome === 'committed' && typeof from === 'string' && typeof to === 'string'
      ? [{ at: e.at, from, to }]
      : [];
  });
}

/** Where each turn ran and where the session moved: from the hand-off records and the turns' start times. */
export function placeTurns(
  starts: (string | undefined)[],
  handoffs: Handoff[],
  owner: string,
): { machineOf: string[]; points: { turn: number; handoff: Handoff }[] } {
  const machineOf: string[] = [];
  const points: { turn: number; handoff: Handoff }[] = [];
  let next = 0;
  let machine = handoffs[0]?.from ?? owner;
  starts.forEach((ts, i) => {
    const at = ts ? Date.parse(ts) : Number.NaN;
    if (Number.isFinite(at)) {
      let moved: Handoff | undefined;
      while (next < handoffs.length && (handoffs[next] as Handoff).at <= at) {
        moved = handoffs[next] as Handoff;
        next++;
      }
      if (moved && i > 0) {
        points.push({ turn: i + 1, handoff: moved });
        machine = moved.to;
      } else if (moved) machine = moved.to;
    }
    machineOf.push(machine);
  });
  return { machineOf, points };
}

/** `MacBook Pro (turns 1–8) → studio (turns 9–12)` */
function machineRow(names: string[]): string {
  const runs: { name: string; first: number; last: number }[] = [];
  names.forEach((name, i) => {
    const last = runs[runs.length - 1];
    if (last && last.name === name) last.last = i + 1;
    else runs.push({ name, first: i + 1, last: i + 1 });
  });
  return runs
    .map((r) =>
      r.first === r.last ? `${r.name} (turn ${r.first})` : `${r.name} (turns ${r.first}–${r.last})`,
    )
    .join(' → ');
}

type Changes = {
  source: 'live' | 'snapshot';
  base: string;
  files: WorkspaceChange[];
  total: number;
  owner?: string;
};

/** The changed files, from the owner when it answers, else from the latest snapshot; null when neither has them. */
async function changesOf(deps: SessionExportDeps, sessionId: string): Promise<Changes | null> {
  const owner = deps.control.ownerOf(sessionId);
  if (owner) {
    const live = await deps.control.workspaceChanges(sessionId, owner);
    if (live.ok) {
      return {
        source: 'live',
        base: live.changes.base,
        files: live.changes.files,
        total: live.changes.total,
        owner,
      };
    }
  }
  const snapshot = await deps.store.snapshotChanges(sessionId);
  if (!snapshot) return null;
  return {
    source: 'snapshot',
    base: snapshot.baseCommit,
    files: snapshot.changes,
    total: snapshot.changes.length,
  };
}

/** The diff of one file: hunks, or a note for a binary file; null when it cannot be read. */
async function fileDiff(
  deps: SessionExportDeps,
  sessionId: string,
  changes: Changes,
  path: string,
  limit: number,
): Promise<{ kind: 'text'; diff: string; truncated: boolean } | { kind: 'binary' } | null> {
  if (changes.source === 'live' && changes.owner) {
    const out = await deps.control.workspaceDiff(sessionId, changes.owner, path, {
      base: true,
      limit: Math.max(1, limit),
    });
    if (!out.ok) return null;
    return out.diff.kind === 'binary'
      ? { kind: 'binary' }
      : { kind: 'text', diff: out.diff.diff, truncated: out.diff.truncated };
  }
  const found = await deps.store.snapshotDiff(sessionId, path);
  if (!found.ok) return null;
  return found.diff.kind === 'binary'
    ? { kind: 'binary' }
    : { kind: 'text', diff: found.diff.diff, truncated: found.diff.truncated };
}

/** The Changes section: the net diff against the base, at most EXPORT_DIFF_MAX_BYTES of hunks. */
async function renderChanges(
  deps: SessionExportDeps,
  sessionId: string,
  changes: Changes | null,
  lastTurn: number,
): Promise<string> {
  const out = ['## Changes', ''];
  if (!changes) {
    out.push(
      '*The changes could not be read: the machine that owns the session is offline and Hoplet holds no snapshot of it.*',
      '',
    );
    return `${out.join('\n')}\n`;
  }
  if (changes.total === 0) {
    out.push('*The session changed no files.*', '');
    return `${out.join('\n')}\n`;
  }
  const added = changes.files.reduce((n, f) => n + (f.added ?? 0), 0);
  const removed = changes.files.reduce((n, f) => n + (f.removed ?? 0), 0);
  const from = changes.source === 'snapshot' ? ' from the latest snapshot' : '';
  out.push(
    `Net diff against \`${changes.base.slice(0, 12)}\`${from} at turn ${lastTurn} · ${changes.total} ${changes.total === 1 ? 'file' : 'files'} · +${added} −${removed}.`,
    '',
  );
  let used = 0;
  let body = '';
  const notes: string[] = [];
  let shown = 0;
  for (const file of changes.files) {
    const left = EXPORT_DIFF_MAX_BYTES - used;
    if (left <= 0) break;
    const diff = await fileDiff(deps, sessionId, changes, file.path, left);
    shown++;
    body += diffFileHeader(file.path, file.status);
    if (!diff) {
      notes.push(`*The diff of ${code(file.path)} could not be read.*`);
      continue;
    }
    if (diff.kind === 'binary') {
      body += `Binary files a/${file.path} and b/${file.path} differ\n`;
      continue;
    }
    let text = diff.diff;
    let cut = diff.truncated;
    if (Buffer.byteLength(text) > left) {
      const bytes = Buffer.from(text).subarray(0, left).toString('utf8');
      const end = bytes.lastIndexOf('\n');
      text = end > 0 ? bytes.slice(0, end + 1) : '';
      cut = true;
    }
    used += Buffer.byteLength(text);
    body += text.endsWith('\n') || text === '' ? text : `${text}\n`;
    if (cut)
      notes.push(`*The diff of ${code(file.path)} is cut at ${sizeText(EXPORT_DIFF_MAX_BYTES)}.*`);
    await yieldToWaiting();
  }
  out.push(fenced(body, 'diff'), '');
  const hidden = changes.total - shown;
  if (hidden > 0) {
    notes.push(
      `*${hidden} more ${hidden === 1 ? 'file is' : 'files are'} not shown: the diff stops at ${sizeText(EXPORT_DIFF_MAX_BYTES)}.*`,
    );
  }
  for (const note of notes) out.push(note, '');
  return `${out.join('\n')}\n`;
}

/** The file's name: its title in lowercase words joined by dashes. */
export function exportFileName(title: string): string {
  const slug = title
    .normalize('NFKD')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60)
    .replace(/-+$/, '');
  return `${slug || 'session'}.md`;
}

/** Exports running, per store. */
const active = new WeakMap<Store, number>();

export function sessionExport(
  deps: SessionExportDeps,
  sessionId: string,
  query: SessionExportQuery,
): RawResponse {
  const { store } = deps;
  const meta = store.getSession(sessionId);
  if (!meta) throw new ApiError('session-not-found');
  const running = (): number => active.get(store) ?? 0;
  if (running() >= SESSION_EXPORT_CONCURRENCY) throw new ApiError('rate-limited');
  const options = {
    conversation: query.conversation,
    changes: query.changes === 'true',
    commands: query.commands === 'true',
    machines: query.machines === 'true',
  };
  const mask = query.mask ?? [];
  const tz = query.tz ?? 'UTC';
  active.set(store, running() + 1);
  try {
    store.audit({
      type: 'session.exported',
      sessionId,
      summary: {
        ...options,
        summary: query.summary !== undefined && query.summary.trim() !== '',
        masks: mask.length,
      },
    });
  } catch (err) {
    active.set(store, running() - 1);
    throw err;
  }
  return new RawResponse(async (res) => {
    const redact = (text: string): string => redactForExport(text, mask).text;
    try {
      const upTo = meta.lineCount;
      const cwd = meta.projectPath;
      const names = new Map(store.listDevices().map((d) => [d.deviceId, d.name]));
      const nameOf = (id: string): string => names.get(id) ?? id.slice(0, 8);

      // The first pass counts the turns and their times: the header names them before they are written.
      const index = new TurnBuilder(cwd, true);
      const starts: (string | undefined)[] = [];
      let first: string | undefined;
      let last: string | undefined;
      const record = (t: ExportTurn | null) => {
        if (!t) return;
        starts.push(t.ts);
        first ??= t.ts;
        if (t.lastTs) last = t.lastTs;
      };
      for await (const entry of entriesOf(store, sessionId, meta.provider, upTo)) {
        record(index.add(entry));
      }
      record(index.finish());
      const turns = index.turns;

      const owner = meta.ownerDeviceId ?? '';
      const placed = placeTurns(
        starts,
        options.machines ? handoffsOf(store, sessionId) : [],
        owner,
      );
      const machineOf = placed.machineOf.map(nameOf);
      const pointAt = new Map(placed.points.map((p) => [p.turn, p.handoff]));
      const changes = options.changes ? await changesOf(deps, sessionId) : null;

      const title =
        store.sessionState(sessionId)?.titleOverride?.trim() || meta.title || 'Untitled session';
      const project =
        meta.projectPath
          .replace(/[/\\]+$/, '')
          .split(/[/\\]/)
          .pop() || meta.projectPath;
      const span = first && last ? Date.parse(last) - Date.parse(first) : Number.NaN;
      const header = redact(
        renderHeader({
          title,
          sessionId,
          project,
          ...(meta.gitBranch ? { branch: meta.gitBranch } : {}),
          provider: meta.provider,
          ...(options.machines
            ? {
                machines: [...new Set(machineOf.length > 0 ? machineOf : [nameOf(owner)])],
                ...(machineOf.length > 0 ? { machineRow: machineRow(machineOf) } : {}),
                handoffRows: placed.points.map((p) => {
                  const when = timeParts(p.handoff.at, tz);
                  return `turn ${p.turn}${when ? `, ${when.date} ${when.time}` : ''}`;
                }),
              }
            : {}),
          turns,
          ...(Number.isFinite(span) && span >= 0 ? { durationMs: span } : {}),
          ...(changes && changes.total > 0
            ? {
                diff: {
                  files: changes.total,
                  added: changes.files.reduce((n, f) => n + (f.added ?? 0), 0),
                  removed: changes.files.reduce((n, f) => n + (f.removed ?? 0), 0),
                },
              }
            : {}),
          exported: new Date(deps.now()).toISOString().replace(/\.\d{3}Z$/, 'Z'),
          version: corePackage.version,
          ...(query.summary !== undefined ? { summary: query.summary } : {}),
          conversation: options.conversation,
        }),
      );
      const titleLine = /^title: (.*)$/m.exec(header)?.[1];
      res.writeHead(200, {
        'content-type': 'text/markdown; charset=utf-8',
        'content-disposition': `attachment; filename="${exportFileName(titleLine ? String(JSON.parse(titleLine)) : 'session')}"`,
        'cache-control': 'no-store',
        'x-content-type-options': 'nosniff',
      });
      if (!(await write(res, header))) return;

      // The second pass writes the turns and collects the commands.
      const commands: ExportCommand[] = [];
      let more = 0;
      if (options.conversation !== 'none' || options.commands) {
        const builder = new TurnBuilder(cwd);
        const emit = async (turn: ExportTurn | null): Promise<boolean> => {
          if (!turn) return true;
          const machine = options.machines ? machineOf[turn.n - 1] : undefined;
          if (options.commands) {
            for (const c of commandsOf(turn, machine)) {
              if (commands.length < EXPORT_MAX_COMMANDS) commands.push(c);
              else more++;
            }
          }
          if (options.conversation === 'none') return true;
          let text = '';
          const point = pointAt.get(turn.n);
          if (point) {
            const when = timeParts(point.at, tz);
            text += renderHandoff(nameOf(point.from), nameOf(point.to), turn.n, when?.time);
          }
          const when = turn.ts ? timeParts(turn.ts, tz) : null;
          text += renderTurn(turn, options.conversation, {
            ...(machine ? { machine } : {}),
            ...(when ? { time: when.time } : {}),
          });
          return write(res, redact(text));
        };
        for await (const entry of entriesOf(store, sessionId, meta.provider, upTo)) {
          if (!(await emit(builder.add(entry)))) return;
        }
        if (!(await emit(builder.finish()))) return;
      }
      if (options.commands) {
        if (!(await write(res, redact(renderCommands(commands, more, options.machines))))) return;
      }
      if (options.changes) {
        const section = await renderChanges(deps, sessionId, changes, turns);
        if (!(await write(res, redact(section)))) return;
      }
      res.end();
    } catch (err) {
      log('error', 'api', 'session export failed', { sessionId, error: String(err) });
      res.destroy();
    } finally {
      active.set(store, running() - 1);
    }
  });
}
