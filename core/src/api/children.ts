// What a parent session reads of its children (ADR 0035): where a child stands, its report and what it changed.
import {
  type ChildChanges,
  type ChildCollect,
  type ChildState,
  MAX_CHILD_COLLECT_BYTES,
  MAX_CHILD_COLLECT_FILES,
  MAX_CHILD_SUMMARY_BYTES,
  type Provider,
  type UsageTotals,
} from '@hoplet/shared';
import type { WorkspaceChangesOutcome } from '../control/index.ts';
import type { ChildRecord, Store } from '../store/index.ts';
import { fullEntry } from './projection/index.ts';

/** The marker that ends a report cut at the limit. */
export const SUMMARY_CUT_MARKER = '\n[The report is longer than 6 KiB and was cut here.]';

/**
 * Where a child stands: what the core did to it (a limit, a cancel) wins; otherwise the runtime decides. `done`
 * is a runtime that reached `ready`, `blocked` one that waits for a permission, `failed` one that failed;
 * anything else that is not running any more (offline, idle, external) is `ended`.
 */
export function childStateOf(record: ChildRecord, runtime: string): ChildState {
  if (record.ended) return record.ended;
  switch (runtime) {
    case 'starting':
    case 'running':
      return 'running';
    case 'ready':
      return 'done';
    case 'waiting-permission':
      return 'blocked';
    case 'failed':
      return 'failed';
    default:
      return 'ended';
  }
}

/** The text cut to at most `max` bytes of UTF-8 on a character boundary. */
function cutBytes(text: string, max: number): string {
  const bytes = Buffer.from(text, 'utf8');
  if (bytes.length <= max) return text;
  let end = max;
  // Back to the start of a character: continuation bytes are 10xxxxxx.
  while (end > 0 && ((bytes[end] ?? 0) & 0xc0) === 0x80) end--;
  return bytes.subarray(0, end).toString('utf8');
}

const REPORT_SCAN_LINES = 200;
const REPORT_PAGE_LINES = 40;

/**
 * The last text the assistant wrote in the child's last turn, read from the stored lines (nothing is appended and
 * the byte cursor is untouched), cut to 6 KiB with a marker. Empty when the last turn has no assistant text.
 */
export async function lastReport(
  store: Store,
  provider: Provider,
  sessionId: string,
): Promise<{ summary: string; truncated: boolean }> {
  let text = '';
  let end: number | null = null;
  scan: for (let scanned = 0; scanned < REPORT_SCAN_LINES; scanned += REPORT_PAGE_LINES) {
    const first = end === null ? -REPORT_PAGE_LINES : Math.max(0, end - REPORT_PAGE_LINES);
    const limit = end === null ? REPORT_PAGE_LINES : end - first;
    if (limit <= 0) break;
    const page = await store.readLines(sessionId, first, limit);
    if (!page || page.lines.length === 0) break;
    for (let i = page.lines.length - 1; i >= 0; i--) {
      const entry = fullEntry(provider, page.lines[i], page.start + i);
      if (!entry) continue;
      if (entry.role === 'assistant') {
        const blocks = entry.blocks.filter(
          (b): b is { type: 'text'; text: string } => 'type' in b && b.type === 'text',
        );
        const last = blocks.at(-1);
        if (last && last.text.trim() !== '') {
          text = last.text;
          break scan;
        }
      } else if (
        entry.role === 'user' &&
        entry.blocks.some((b) => 'type' in b && b.type === 'text')
      ) {
        // The prompt that began the turn: the turn wrote no text.
        break scan;
      }
    }
    if (page.start === 0) break;
    end = page.start;
  }
  const room = MAX_CHILD_SUMMARY_BYTES - Buffer.byteLength(SUMMARY_CUT_MARKER, 'utf8');
  if (Buffer.byteLength(text, 'utf8') <= MAX_CHILD_SUMMARY_BYTES) {
    return { summary: text, truncated: false };
  }
  return { summary: cutBytes(text, room) + SUMMARY_CUT_MARKER, truncated: true };
}

/** What a child changed, from the owner's answer. */
export function changesOf(outcome: WorkspaceChangesOutcome): ChildChanges {
  if (!outcome.ok)
    return { status: outcome.error === 'owner-offline' ? 'owner-offline' : 'unknown' };
  const c = outcome.changes;
  const files = c.files.slice(0, MAX_CHILD_COLLECT_FILES);
  return {
    status: 'ok',
    branch: c.branch,
    base: c.base,
    shared: c.shared,
    total: c.total,
    files,
    truncated: files.length < c.total,
  };
}

const bytesOf = (value: unknown): number => Buffer.byteLength(JSON.stringify(value), 'utf8');

/** The answer of `collect`, with the list of files cut until the whole fits in 8 KiB. */
export function fitCollect(collect: ChildCollect): ChildCollect {
  let result = collect;
  while (bytesOf(result) > MAX_CHILD_COLLECT_BYTES && result.changes.files?.length) {
    const files = result.changes.files.slice(0, Math.floor(result.changes.files.length * 0.8));
    result = { ...result, changes: { ...result.changes, files, truncated: true } };
  }
  return result;
}

const NO_USAGE: UsageTotals = {
  input: 0,
  output: 0,
  cacheWrite: 0,
  cacheRead: 0,
  messages: 0,
  estimatedCostUsd: null,
  costComplete: true,
};

/** The usage of a session in total, zero for one with none recorded. */
export function usageOf(store: Store, sessionId: string): UsageTotals {
  return store.sessionUsage(sessionId)?.total ?? NO_USAGE;
}
