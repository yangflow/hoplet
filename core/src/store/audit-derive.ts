// The tool-call deriver of the audit log (#80, ADR 0030): reads a session's stored lines from its own cursor,
// never changes them and never runs on the append path. The events and the new cursor commit together, so a
// crash repeats at most one batch and no event is written twice.
import { log } from '../log/index.ts';
import { scrubText } from '../scrub/index.ts';
import { appendAudit } from './audit.ts';
import { extractToolCalls, type ToolCallRecord } from './audit-lines.ts';
import { readChunkLines, yieldToEventLoop } from './chunks.ts';
import type { StoreContext } from './layout.ts';
import { CHUNK_LINES } from './layout.ts';
import { getRow } from './sessions.ts';

/** The longest `command` of a record, in characters, the cut mark included. */
export const AUDIT_COMMAND_CHARS = 500;

/** Failures one run of a session survives, five seconds apart, before it waits for the next append. */
const MAX_RETRIES = 5;
const RETRY_MS = 5000;

/** Scrubbed first, so a secret cut in the middle cannot slip past the scrubber; then cut. */
export function auditCommand(command: string): string {
  const chars = [...scrubText(command)];
  return chars.length > AUDIT_COMMAND_CHARS
    ? `${chars.slice(0, AUDIT_COMMAND_CHARS - 3).join('')}...`
    : chars.join('');
}

function summaryOf(call: ToolCallRecord, line: number): Record<string, unknown> {
  return {
    tool: call.tool,
    line,
    ...(call.command !== undefined ? { command: auditCommand(call.command) } : {}),
    ...(call.paths ? { paths: call.paths, pathCount: call.pathCount ?? call.paths.length } : {}),
  };
}

/**
 * Derives what the session's lines hold beyond its cursor, one chunk at a time with the event loop handed back
 * between batches. A session with no cursor row is not derived.
 */
async function deriveSession(ctx: StoreContext, sessionId: string): Promise<void> {
  for (;;) {
    const row = getRow(ctx, sessionId);
    if (!row) return;
    const cursor = ctx.db
      .prepare('SELECT line FROM audit_cursor WHERE session_id = ?')
      .get(sessionId) as { line: number } | undefined;
    if (!cursor) return;
    let from = Number(cursor.line);
    if (from > row.line_count) {
      from = row.line_count;
      ctx.db.prepare('UPDATE audit_cursor SET line = ? WHERE session_id = ?').run(from, sessionId);
    }
    if (from >= row.line_count) return;
    const seq = Math.floor(from / CHUNK_LINES);
    const lines = (await readChunkLines(ctx, row, seq)).slice(from - seq * CHUNK_LINES);
    // A chunk that cannot be read now is tried again at the next append or start.
    if (lines.length === 0) return;
    const calls = extractToolCalls(
      row.provider === 'codex' ? 'codex' : 'claude',
      lines,
      row.created_at,
    );
    const next = from + lines.length;
    ctx.db.exec('BEGIN IMMEDIATE');
    try {
      const moved = ctx.db
        .prepare('UPDATE audit_cursor SET line = ? WHERE session_id = ? AND line = ?')
        .run(next, sessionId, from);
      // The session was deleted, or another run moved the cursor: write nothing.
      if (Number(moved.changes) === 0) {
        ctx.db.exec('ROLLBACK');
        return;
      }
      const owner = getRow(ctx, sessionId)?.device_id || row.device_id;
      for (const call of calls) {
        appendAudit(ctx, {
          type: 'tool.call',
          at: call.at,
          sessionId,
          deviceId: owner || null,
          actor: { kind: 'core', id: 'deriver' },
          channel: 'core',
          ref: call.id || null,
          summary: summaryOf(call, from + call.line),
        });
      }
      ctx.db.exec('COMMIT');
    } catch (err) {
      if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
      throw err;
    }
    await yieldToEventLoop();
  }
}

/**
 * Derives a session's new lines in the background. A session already being derived is run again when it
 * finishes, so a line appended meanwhile is not missed. A failure is tried again a few times and otherwise at
 * the next append or start; the cursor has not moved, so nothing is skipped.
 */
export function scheduleAuditDerive(ctx: StoreContext, sessionId: string, attempt = 0): void {
  if (ctx.auditDeriving.has(sessionId)) {
    ctx.auditRerun.add(sessionId);
    return;
  }
  ctx.auditDeriving.add(sessionId);
  let failed = false;
  const job: Promise<void> = deriveSession(ctx, sessionId)
    .catch((err: unknown) => {
      failed = true;
      log('warn', 'store', 'audit derive of a session failed', {
        sessionId,
        attempt,
        error: err instanceof Error ? err.message : String(err),
      });
    })
    .finally(() => {
      ctx.auditDeriving.delete(sessionId);
      ctx.background.delete(job);
      if (ctx.auditRerun.delete(sessionId)) {
        scheduleAuditDerive(ctx, sessionId);
      } else if (failed && attempt < MAX_RETRIES) {
        setTimeout(() => scheduleAuditDerive(ctx, sessionId, attempt + 1), RETRY_MS).unref();
      }
    });
  ctx.background.add(job);
}

/** Resumes every session whose cursor is behind its lines, after a restart. */
export function resumeAuditDerive(ctx: StoreContext): void {
  const ids = ctx.db
    .prepare(
      `SELECT c.session_id FROM audit_cursor c JOIN sessions s ON s.session_id = c.session_id
       WHERE c.line < s.line_count ORDER BY s.updated_at`,
    )
    .all() as { session_id: string }[];
  for (const { session_id: sessionId } of ids) scheduleAuditDerive(ctx, sessionId);
}
