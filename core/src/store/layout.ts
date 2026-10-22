import type { Hash } from 'node:crypto';
import { closeSync, fsyncSync, openSync } from 'node:fs';
import path from 'node:path';
import type { DatabaseSync } from 'node:sqlite';
import { type BudgetScope, isSessionId } from '@hoplet/shared';
import type { NotifySecrets } from './secrets.ts';
import type { SnapshotView } from './snapshot-read.ts';

/** Lines per chunk: a session is cut into 500-line files, so reading any page only decompresses the hit chunk. */
export const CHUNK_LINES = 500;

/** What every store concern works on: the index database and the data directory it sits beside. */
export interface StoreContext {
  db: DatabaseSync;
  dataDir: string;
  /** Chunks being compressed off the event loop, as `<sessionId>/<seq>`. */
  compressing: Set<string>;
  /** Compression runs in flight; `Store.idle` waits for them. */
  background: Set<Promise<void>>;
  /** Snapshot uploads in progress, by session ID. */
  uploads: Map<string, SnapshotUpload>;
  /** Parsed changes of recently read snapshots, by snapshot ID, least recently used first. */
  snapshotViews: Map<string, Promise<SnapshotView>>;
  /** The notification secrets once read from their file. */
  secrets: NotifySecrets | null;
  /** Called after usage is recorded; they must not throw or block. */
  usageListeners: Set<() => void>;
  /** The `pause` budgets as the prompt path sees them, by budget ID; kept by `store/budgets.ts`. */
  budgetBlocks: Map<string, BudgetBlock>;
  /** The pending refresh of the `budgetBlocks` that recorded usage made stale. */
  budgetBlockTimer: NodeJS.Timeout | null;
  /** Sessions whose tool calls the audit deriver is reading now, and those that got lines meanwhile. */
  auditDeriving: Set<string>;
  auditRerun: Set<string>;
}

/** Whether a `pause` budget refuses new prompts in the period [periodStart, periodEnd), as last computed. */
export interface BudgetBlock {
  scope: BudgetScope;
  createdAt: number;
  name: string;
  periodStart: number;
  periodEnd: number;
  paused: boolean;
  /** Usage was recorded since `paused` was computed. */
  stale: boolean;
}

/** A snapshot being received: where it is written and what the first chunk promised. */
export interface SnapshotUpload {
  snapshotId: string;
  part: string;
  nextSeq: number;
  received: number;
  hash: Hash;
  promised: SnapshotPromise;
}

export interface SnapshotPromise {
  createdAt: number;
  baseCommit: string;
  originUrl: string;
  treeHash: string;
  byteSize: number;
  sha256: string;
}

export function sessionDir(ctx: StoreContext, sessionId: string): string {
  if (!isSessionId(sessionId)) throw new Error('invalid-session-id');
  return path.join(ctx.dataDir, 'sessions', sessionId);
}

export function snapshotsRoot(ctx: StoreContext): string {
  return path.join(ctx.dataDir, 'snapshots');
}

export function snapshotDir(ctx: StoreContext, sessionId: string): string {
  if (!isSessionId(sessionId)) throw new Error('invalid-session-id');
  return path.join(snapshotsRoot(ctx), sessionId);
}

export function chunkPath(ctx: StoreContext, sessionId: string, seq: number): string {
  return path.join(sessionDir(ctx, sessionId), `c-${seq}.ndjson`);
}

export function syncDirectory(dir: string): void {
  const fd = openSync(dir, 'r');
  try {
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
}
