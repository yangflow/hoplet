import { parseLine, readChunkBytes, splitLines, yieldToEventLoop } from './chunks.ts';
import { CHUNK_LINES, type StoreContext } from './layout.ts';
import { committedLines, getRow, type SessionRow } from './sessions.ts';

/** A matching line, parsed but not projected. */
export interface RawHit {
  lineNo: number;
  line: unknown;
}

export interface RawSearchResult {
  sessionId: string;
  query: string;
  start: number;
  total: number;
  hits: RawHit[];
}

export interface RawGlobalSearchResult {
  query: string;
  start: number;
  results: (RawHit & { sessionId: string })[];
  exhausted: boolean;
}

/**
 * The lines of one chunk that contain `needle` (lowercase), as [index, raw] pairs. A chunk without the
 * needle is rejected on its whole text, before it is cut into lines.
 */
async function matchesInChunk(
  ctx: StoreContext,
  row: SessionRow,
  seq: number,
  needle: string,
): Promise<{ index: number; raw: string }[]> {
  const bytes = await readChunkBytes(ctx, row.session_id, seq);
  if (!bytes) return [];
  const text = bytes.toString('utf8');
  if (!text.toLowerCase().includes(needle)) return [];
  const found: { index: number; raw: string }[] = [];
  const lines = splitLines(text, committedLines(row, seq));
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i] ?? '';
    if (raw.toLowerCase().includes(needle)) found.push({ index: i, raw });
  }
  return found;
}

/**
 * Scan every session newest-first for lines containing the query (case-insensitive) and return the
 * page [start, start+limit) across sessions. Scanning stops once the page is full; exhausted=false
 * then means more sessions or hits may exist (docs/spec.md#search). The scan reads one chunk at a time
 * and gives the event loop back after each, so other requests are served while it runs.
 */
export async function searchAll(
  ctx: StoreContext,
  query: string,
  start: number,
  limit: number,
): Promise<RawGlobalSearchResult> {
  const sessions = ctx.db
    .prepare('SELECT * FROM sessions ORDER BY updated_at DESC LIMIT 200')
    .all() as unknown as SessionRow[];
  const needle = query.toLowerCase();
  const results: RawGlobalSearchResult['results'] = [];
  let skipped = 0;
  let exhausted = true;
  outer: for (const row of sessions) {
    for (let seq = 0; seq < row.chunk_count; seq++) {
      for (const { index, raw } of await matchesInChunk(ctx, row, seq, needle)) {
        if (skipped < start) {
          skipped++;
          continue;
        }
        results.push({
          sessionId: row.session_id,
          lineNo: seq * CHUNK_LINES + index,
          line: parseLine(raw),
        });
        if (results.length >= limit) {
          exhausted = false;
          break outer;
        }
      }
      await yieldToEventLoop();
    }
  }
  return { query, start, results, exhausted };
}

/**
 * Scan the session's chunk files in order for lines containing the query (case-insensitive) and return
 * the hits [start, start+limit) with their line numbers. Keeps no index: it reads the same bytes the
 * store serves (docs/spec.md#search).
 */
export async function search(
  ctx: StoreContext,
  sessionId: string,
  query: string,
  start: number,
  limit: number,
): Promise<RawSearchResult | null> {
  const row = getRow(ctx, sessionId);
  if (!row) return null;
  const needle = query.toLowerCase();
  const hits: RawHit[] = [];
  let total = 0;
  for (let seq = 0; seq < row.chunk_count; seq++) {
    for (const { index, raw } of await matchesInChunk(ctx, row, seq, needle)) {
      if (total >= start && hits.length < limit) {
        hits.push({ lineNo: seq * CHUNK_LINES + index, line: parseLine(raw) });
      }
      total++;
    }
    await yieldToEventLoop();
  }
  return { sessionId, query, start, total, hits };
}
