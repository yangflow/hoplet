import {
  MAX_QUEUE_ITEMS,
  PromptAttachmentsSchema,
  type Queue,
  type QueueItem,
  type QueuePause,
  safeParse,
} from '@hoplet/shared';
import type { StoreContext } from './layout.ts';

interface Row {
  id: string;
  position: number;
  text: string;
  attachments: string;
  version: number;
  state: string;
}

/** What a queue mutation says: the item was missing, already sending or changed (`conflict`), or the queue is full. */
export type QueueOutcome =
  | { ok: true; item?: QueueItem }
  | { ok: false; reason: 'conflict' | 'full' };

const CONFLICT = { ok: false, reason: 'conflict' } as const;

function item(row: Row): QueueItem {
  let attachments: string[] = [];
  try {
    const parsed = safeParse(PromptAttachmentsSchema, JSON.parse(row.attachments));
    if (parsed.success) attachments = parsed.output;
  } catch {
    // A row that cannot be read has no images.
  }
  return {
    id: row.id,
    text: row.text,
    attachments,
    version: row.version,
    state: row.state === 'sending' ? 'sending' : 'queued',
  };
}

function rows(ctx: StoreContext, sessionId: string): Row[] {
  return ctx.db
    .prepare(
      'SELECT id, position, text, attachments, version, state FROM session_queue WHERE session_id = ? ORDER BY position',
    )
    .all(sessionId) as unknown as Row[];
}

/** A session's queue: its items head first, and why it is paused. */
export function queueOf(ctx: StoreContext, sessionId: string): Queue {
  const st = ctx.db
    .prepare('SELECT paused, error FROM session_queue_state WHERE session_id = ?')
    .get(sessionId) as { paused: string; error: string | null } | undefined;
  const queue: Queue = { items: rows(ctx, sessionId).map(item) };
  if (st && (st.paused === 'error' || st.paused === 'interrupted')) {
    queue.paused = st.paused;
    if (st.error) queue.error = st.error;
  }
  return queue;
}

/** Appends a message; one with an `id` the session already has is that message, unchanged. */
export function queueAdd(
  ctx: StoreContext,
  sessionId: string,
  input: { id: string; text: string; attachments: string[] },
): QueueOutcome {
  const existing = ctx.db
    .prepare(
      'SELECT id, position, text, attachments, version, state FROM session_queue WHERE id = ?',
    )
    .get(input.id) as unknown as Row | undefined;
  if (existing) return { ok: true, item: item(existing) };
  const count = rows(ctx, sessionId).length;
  if (count >= MAX_QUEUE_ITEMS) return { ok: false, reason: 'full' };
  const last = ctx.db
    .prepare('SELECT MAX(position) AS p FROM session_queue WHERE session_id = ?')
    .get(sessionId) as { p: number | null };
  const position = (last.p ?? 0) + 1;
  ctx.db
    .prepare(
      `INSERT INTO session_queue(id, session_id, position, text, attachments, version, state, created_at)
       VALUES(?, ?, ?, ?, ?, 1, 'queued', ?)`,
    )
    .run(input.id, sessionId, position, input.text, JSON.stringify(input.attachments), Date.now());
  return { ok: true, item: { ...input, version: 1, state: 'queued' } };
}

function queued(ctx: StoreContext, sessionId: string, id: string, version: number): Row | null {
  const row = ctx.db
    .prepare(
      'SELECT id, position, text, attachments, version, state FROM session_queue WHERE id = ? AND session_id = ?',
    )
    .get(id, sessionId) as unknown as Row | undefined;
  return row && row.state === 'queued' && row.version === version ? row : null;
}

/** Replaces the text and images of a message that still waits and is still at `version`. */
export function queueEdit(
  ctx: StoreContext,
  sessionId: string,
  id: string,
  version: number,
  input: { text: string; attachments: string[] },
): QueueOutcome {
  if (!queued(ctx, sessionId, id, version)) return CONFLICT;
  ctx.db
    .prepare(
      'UPDATE session_queue SET text = ?, attachments = ?, version = version + 1 WHERE id = ? AND session_id = ?',
    )
    .run(input.text, JSON.stringify(input.attachments), id, sessionId);
  return { ok: true, item: item(queued(ctx, sessionId, id, version + 1) as Row) };
}

/** Removes a message that still waits and is still at `version`. */
export function queueRemove(
  ctx: StoreContext,
  sessionId: string,
  id: string,
  version: number,
): QueueOutcome {
  if (!queued(ctx, sessionId, id, version)) return CONFLICT;
  ctx.db.prepare('DELETE FROM session_queue WHERE id = ? AND session_id = ?').run(id, sessionId);
  tidy(ctx, sessionId);
  return { ok: true };
}

/**
 * Orders the waiting messages as `ids` says. `ids` must name exactly the messages that wait; a message sent or
 * removed meanwhile is a conflict.
 */
export function queueReorder(ctx: StoreContext, sessionId: string, ids: string[]): QueueOutcome {
  const waiting = rows(ctx, sessionId).filter((r) => r.state === 'queued');
  if (ids.length !== waiting.length || new Set(ids).size !== ids.length) return CONFLICT;
  const have = new Set(waiting.map((r) => r.id));
  if (!ids.every((id) => have.has(id))) return CONFLICT;
  // The messages take the places the waiting ones held, so one on its way keeps its own.
  const slots = waiting.map((r) => r.position);
  const set = ctx.db.prepare(
    'UPDATE session_queue SET position = ? WHERE id = ? AND session_id = ?',
  );
  ids.forEach((id, i) => {
    set.run(slots[i] as number, id, sessionId);
  });
  return { ok: true };
}

/** The message that sends next: the head, when nothing of the session is sending. */
export function queueNext(ctx: StoreContext, sessionId: string): QueueItem | null {
  const all = rows(ctx, sessionId);
  const head = all[0];
  if (!head || all.some((r) => r.state === 'sending')) return null;
  return item(head);
}

/** Marks a waiting message at `version` as sending; nothing else of the session may be sending. */
export function queueClaim(
  ctx: StoreContext,
  sessionId: string,
  id: string,
  version: number,
): QueueOutcome {
  const row = queued(ctx, sessionId, id, version);
  if (!row) return CONFLICT;
  if (rows(ctx, sessionId).some((r) => r.state === 'sending')) return CONFLICT;
  ctx.db.prepare("UPDATE session_queue SET state = 'sending' WHERE id = ?").run(id);
  return { ok: true, item: { ...item(row), state: 'sending' } };
}

/** The agent accepted the message: it leaves the queue. */
export function queueSettle(ctx: StoreContext, sessionId: string, id: string): void {
  ctx.db.prepare('DELETE FROM session_queue WHERE id = ? AND session_id = ?').run(id, sessionId);
  tidy(ctx, sessionId);
}

/** A queue with no messages is not paused. */
function tidy(ctx: StoreContext, sessionId: string): void {
  if (rows(ctx, sessionId).length === 0) queueSetPause(ctx, sessionId, null);
}

/** The message did not go: it waits again, at the head. */
export function queueRestore(ctx: StoreContext, sessionId: string, id: string): void {
  ctx.db
    .prepare("UPDATE session_queue SET state = 'queued' WHERE id = ? AND session_id = ?")
    .run(id, sessionId);
}

/** Pauses the queue (with the failure's text), or resumes it with `null`. */
export function queueSetPause(
  ctx: StoreContext,
  sessionId: string,
  pause: QueuePause | null,
  error?: string,
): void {
  if (pause !== null && rows(ctx, sessionId).length === 0) return;
  if (pause === null) {
    ctx.db.prepare('DELETE FROM session_queue_state WHERE session_id = ?').run(sessionId);
    return;
  }
  ctx.db
    .prepare(
      `INSERT INTO session_queue_state(session_id, paused, error) VALUES(?, ?, ?)
       ON CONFLICT(session_id) DO UPDATE SET paused = excluded.paused, error = excluded.error`,
    )
    .run(sessionId, pause, error ?? null);
}

/** Removes the messages that wait and resumes the queue; a message already sending stays until it settles. */
export function queueClear(ctx: StoreContext, sessionId: string): void {
  ctx.db
    .prepare("DELETE FROM session_queue WHERE session_id = ? AND state = 'queued'")
    .run(sessionId);
  queueSetPause(ctx, sessionId, null);
}

/**
 * At start nothing is on its way any more: messages left `sending` wait again and every queue with messages is
 * paused, so a restart never sends by itself; the user resumes.
 */
export function recoverQueues(ctx: StoreContext): void {
  ctx.db.prepare("UPDATE session_queue SET state = 'queued' WHERE state = 'sending'").run();
  ctx.db
    .prepare(
      `INSERT INTO session_queue_state(session_id, paused, error)
       SELECT DISTINCT session_id, 'interrupted', NULL FROM session_queue WHERE true
       ON CONFLICT(session_id) DO NOTHING`,
    )
    .run();
}

/** Sessions that have queued messages, for the drain after a reconnect. */
export function sessionsWithQueue(ctx: StoreContext): string[] {
  return (
    ctx.db.prepare('SELECT DISTINCT session_id FROM session_queue').all() as {
      session_id: string;
    }[]
  ).map((r) => r.session_id);
}
