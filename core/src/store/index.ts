// The store is the only module that touches the data directory (docs/architecture.md#boundaries).
// One file per concern: chunks (append, reads), recovery, sessions, devices, search.
import { mkdirSync } from 'node:fs';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import type {
  AuditEvent,
  AuditFilter,
  AuditHead,
  AuditVerifyResponse,
  Budget,
  BudgetBody,
  BudgetRef,
  Command,
  DeviceInventory,
  DeviceSystem,
  NotifyDelivery,
  NotifyEventKind,
  NotifyPreferences,
  Queue,
  QueueItem,
  QueuePause,
  ResourceThresholds,
  SessionMeta,
  SessionUsage,
  SnapshotChanges,
  UsagePrices,
  UsagePricesBody,
  UsageQuery,
  UsageResponse,
} from '@hoplet/shared';
import { migrate } from '../migrations/index.ts';
import * as audit from './audit.ts';
import * as budgets from './budgets.ts';
import {
  type AppendInput,
  type AppendResult,
  append,
  NotOwnerError,
  type RawLinesPage,
  readLine,
  readLines,
  readStream,
  streamFingerprint,
} from './chunks.ts';
import * as commands from './commands.ts';
import * as continuations from './continuations.ts';
import * as deletion from './deletion.ts';
import * as devices from './devices.ts';
import type { StoreContext } from './layout.ts';
import * as notify from './notify.ts';
import * as queue from './queue.ts';
import { recoverOnStart } from './recovery.ts';
import * as resourceThresholds from './resource-thresholds.ts';
import { type RawGlobalSearchResult, type RawSearchResult, search, searchAll } from './search.ts';
import * as secrets from './secrets.ts';
import * as sessions from './sessions.ts';
import * as snapshotRead from './snapshot-read.ts';
import * as snapshots from './snapshots.ts';
import * as usage from './usage.ts';

export { type AuditInput, AuditUnavailableError } from './audit.ts';
export type { BudgetStatus } from './budgets.ts';
export type { ContinuationLinks } from './continuations.ts';
export type { Tombstone, WorktreeRecord } from './deletion.ts';
export type { PushSubscriptionRecord, WebhookRecord } from './notify.ts';
export type { QueueOutcome } from './queue.ts';
export type { NotifySecrets, VapidKeys } from './secrets.ts';
export type { SessionStateChange, SessionStateRecord } from './sessions.ts';
export { type SnapshotDiffResult, SnapshotReadError } from './snapshot-read.ts';
export { type SnapshotChunk, SnapshotError, type SnapshotRecord } from './snapshots.ts';
export { summarizeTitle } from './title.ts';
export type { AppendInput, AppendResult };
export { NotOwnerError };

export interface StoreOptions {
  /** Days the audit log keeps a record; 0 keeps everything. Default 180. */
  auditRetentionDays?: number;
}

export class Store {
  private ctx: StoreContext;
  private auditRetentionDays: number;

  constructor(dataDir: string, options: StoreOptions = {}) {
    this.auditRetentionDays = options.auditRetentionDays ?? 180;
    const dir = path.resolve(dataDir);
    mkdirSync(path.join(dir, 'sessions'), { recursive: true });
    const db = new DatabaseSync(path.join(dir, 'hoplet.db'));
    this.ctx = {
      db,
      dataDir: dir,
      compressing: new Set(),
      background: new Set(),
      uploads: new Map(),
      snapshotViews: new Map(),
      secrets: null,
      usageListeners: new Set(),
      budgetBlocks: new Map(),
      budgetBlockTimer: null,
    };
    try {
      db.exec('PRAGMA synchronous=FULL');
      migrate(db);
      audit.startAudit(this.ctx);
      recoverOnStart(this.ctx);
      snapshots.recoverSnapshots(this.ctx);
      queue.recoverQueues(this.ctx);
      budgets.loadBudgetBlocks(this.ctx, Date.now());
      this.ctx.usageListeners.add(() => budgets.budgetUsageRecorded(this.ctx));
    } catch (err) {
      db.close();
      throw err;
    }
  }

  /**
   * Records an action in the audit log. Inside a transaction it joins it; a failure throws
   * `AuditUnavailableError` and the action must not happen.
   */
  audit(input: audit.AuditInput, now?: number): AuditHead {
    return audit.appendAudit(this.ctx, input, now);
  }

  listAudit(
    filter: AuditFilter,
    options: audit.AuditPageOptions,
  ): { events: AuditEvent[]; next: number | null } {
    return audit.listAudit(this.ctx, filter, options);
  }

  countAudit(filter: AuditFilter, upTo: number): number {
    return audit.countAudit(this.ctx, filter, upTo);
  }

  auditHead(): AuditHead | null {
    return audit.auditHead(this.ctx);
  }

  verifyAudit(expected?: AuditHead): Promise<AuditVerifyResponse> {
    return audit.verifyAudit(this.ctx, expected);
  }

  /** Removes the audit records past the retention; returns how many. */
  pruneAudit(now?: number): Promise<number> {
    return audit.pruneAudit(this.ctx, this.auditRetentionDays, now);
  }

  /** Starts reading the usage of sessions recorded before usage was; `idle` waits for it. */
  startUsageBackfill(): void {
    const job = usage.backfillUsage(this.ctx).finally(() => this.ctx.background.delete(job));
    this.ctx.background.add(job);
  }

  usage(query: UsageQuery): UsageResponse {
    return usage.queryUsage(this.ctx, query);
  }

  sessionUsage(sessionId: string): SessionUsage | null {
    return usage.sessionUsage(this.ctx, sessionId);
  }

  usagePrices(): UsagePrices {
    return usage.usagePrices(this.ctx);
  }

  setUsagePrices(body: UsagePricesBody): void {
    usage.setUsagePrices(this.ctx, body);
  }

  /** Calls `listener` after usage has been recorded; returns how to stop. The listener must be quick. */
  onUsage(listener: () => void): () => void {
    this.ctx.usageListeners.add(listener);
    return () => this.ctx.usageListeners.delete(listener);
  }

  usageBackfilling(): boolean {
    return usage.usageBackfilling(this.ctx);
  }

  listBudgets(now: number): Budget[] {
    return budgets.listBudgets(this.ctx, now);
  }

  budgetStatuses(now: number) {
    return budgets.budgetStatuses(this.ctx, now);
  }

  getBudget(id: string, now: number): Budget | null {
    return budgets.getBudget(this.ctx, id, now);
  }

  budgetCount(): number {
    return budgets.budgetCount(this.ctx);
  }

  createBudget(body: BudgetBody, now: number): Budget {
    return budgets.createBudget(this.ctx, body, now);
  }

  updateBudget(id: string, body: BudgetBody, now: number): Budget | null {
    return budgets.updateBudget(this.ctx, id, body, now);
  }

  deleteBudget(id: string): boolean {
    return budgets.deleteBudget(this.ctx, id);
  }

  allowBudget(id: string, now: number): Budget | null {
    return budgets.allowBudget(this.ctx, id, now);
  }

  withdrawBudgetAllowance(id: string, now: number): Budget | null {
    return budgets.withdrawBudgetAllowance(this.ctx, id, now);
  }

  claimBudgetAlert(
    budgetId: string,
    periodStart: number,
    threshold: 'alert' | 'limit',
    now: number,
  ): boolean {
    return budgets.claimBudgetAlert(this.ctx, budgetId, periodStart, threshold, now);
  }

  pruneBudgetAlerts(): void {
    budgets.pruneBudgetAlerts(this.ctx);
  }

  /** The budget that refuses a new prompt in this session's project and on its owner; null when none does. */
  budgetBlockForSession(sessionId: string, now: number): BudgetRef | null {
    const row = sessions.getRow(this.ctx, sessionId);
    if (!row) return null;
    return budgets.blockingBudget(
      this.ctx,
      { projectPath: row.project_path || row.project_dir, deviceId: row.device_id },
      now,
    );
  }

  /** The budget that refuses a new session in this project on this machine; null when none does. */
  budgetBlockFor(projectPath: string, deviceId: string, now: number): BudgetRef | null {
    return budgets.blockingBudget(this.ctx, { projectPath, deviceId }, now);
  }

  /** Resolves when the compression an append started has finished. */
  async idle(): Promise<void> {
    while (this.ctx.background.size > 0) await Promise.all([...this.ctx.background]);
  }

  /** The VAPID keys, the action-link key and the webhook secrets; generated on first use (ADR 0021). */
  notifySecrets(): secrets.NotifySecrets {
    return secrets.notifySecrets(this.ctx);
  }

  notifyPreferences(): NotifyPreferences {
    return notify.notifyPreferences(this.ctx);
  }

  setNotifyPreferences(prefs: NotifyPreferences): void {
    notify.setNotifyPreferences(this.ctx, prefs);
  }

  resourceThresholds(): ResourceThresholds {
    return resourceThresholds.resourceThresholds(this.ctx);
  }

  setResourceThresholds(thresholds: ResourceThresholds): void {
    resourceThresholds.setResourceThresholds(this.ctx, thresholds);
  }

  /** Drops the push subscriptions made with a VAPID key other than the current one. */
  pruneForeignSubscriptions(): void {
    notify.pruneForeignSubscriptions(this.ctx);
  }

  addPushSubscription(
    input: { endpoint: string; p256dh: string; auth: string; label: string },
    now: number,
  ) {
    return notify.addPushSubscription(this.ctx, input, now);
  }

  listPushSubscriptions() {
    return notify.listPushSubscriptions(this.ctx);
  }

  deletePushSubscription(id: string): boolean {
    return notify.deletePushSubscription(this.ctx, id);
  }

  deletePushByEndpoint(endpoint: string): boolean {
    return notify.deletePushByEndpoint(this.ctx, endpoint);
  }

  recordPushDelivery(id: string, delivery: NotifyDelivery): void {
    notify.recordPushDelivery(this.ctx, id, delivery);
  }

  listWebhooks() {
    return notify.listWebhooks(this.ctx);
  }

  getWebhook(id: string) {
    return notify.getWebhook(this.ctx, id);
  }

  addWebhook(
    input: { url: string; label: string; secret?: string; events?: readonly NotifyEventKind[] },
    now: number,
  ) {
    return notify.addWebhook(this.ctx, input, now);
  }

  updateWebhook(
    id: string,
    patch: {
      url?: string;
      label?: string;
      enabled?: boolean;
      secret?: string | null;
      events?: readonly NotifyEventKind[];
    },
  ) {
    return notify.updateWebhook(this.ctx, id, patch);
  }

  deleteWebhook(id: string): boolean {
    return notify.deleteWebhook(this.ctx, id);
  }

  webhookSecret(id: string): string | null {
    return secrets.webhookSecret(this.ctx, id);
  }

  recordWebhookDelivery(id: string, delivery: NotifyDelivery): void {
    notify.recordWebhookDelivery(this.ctx, id, delivery);
  }

  /** Marks an action token used; false when it was used before. */
  useActionToken(tokenId: string, expiresAt: number, now: number): boolean {
    return notify.useActionToken(this.ctx, tokenId, expiresAt, now);
  }

  actionTokenUsed(tokenId: string): boolean {
    return notify.actionTokenUsed(this.ctx, tokenId);
  }

  /** Remembers the commands a session offered in `cwd` of `deviceId`, for drafts of that directory. */
  putDirectoryCommands(deviceId: string, cwd: string, list: Command[]): void {
    commands.putDirectoryCommands(this.ctx, deviceId, cwd, list);
  }

  directoryCommands(deviceId: string, cwd: string): Command[] | null {
    return commands.directoryCommands(this.ctx, deviceId, cwd);
  }

  /** The queue of messages held for a session (ADR 0020). */
  queueOf(sessionId: string): Queue {
    return queue.queueOf(this.ctx, sessionId);
  }

  queueAdd(sessionId: string, input: { id: string; text: string; attachments: string[] }) {
    return queue.queueAdd(this.ctx, sessionId, input);
  }

  queueEdit(
    sessionId: string,
    id: string,
    version: number,
    input: { text: string; attachments: string[] },
  ) {
    return queue.queueEdit(this.ctx, sessionId, id, version, input);
  }

  queueRemove(sessionId: string, id: string, version: number) {
    return queue.queueRemove(this.ctx, sessionId, id, version);
  }

  queueReorder(sessionId: string, ids: string[]) {
    return queue.queueReorder(this.ctx, sessionId, ids);
  }

  /** The head, when nothing of the session is sending. */
  queueNext(sessionId: string): QueueItem | null {
    return queue.queueNext(this.ctx, sessionId);
  }

  queueClaim(sessionId: string, id: string, version: number) {
    return queue.queueClaim(this.ctx, sessionId, id, version);
  }

  queueSettle(sessionId: string, id: string): void {
    queue.queueSettle(this.ctx, sessionId, id);
  }

  queueRestore(sessionId: string, id: string): void {
    queue.queueRestore(this.ctx, sessionId, id);
  }

  queueSetPause(sessionId: string, pause: QueuePause | null, error?: string): void {
    queue.queueSetPause(this.ctx, sessionId, pause, error);
  }

  queueClear(sessionId: string): void {
    queue.queueClear(this.ctx, sessionId);
  }

  sessionsWithQueue(): string[] {
    return queue.sessionsWithQueue(this.ctx);
  }

  sessionCount(): number {
    return sessions.sessionCount(this.ctx);
  }

  listSessions(limit = 200): SessionMeta[] {
    return sessions.listSessions(this.ctx, limit);
  }

  getSession(sessionId: string): SessionMeta | null {
    return sessions.getSession(this.ctx, sessionId);
  }

  /** The user's changes of every session that has any: a title of their own, a pin, an archive. */
  sessionStates(): Map<string, sessions.SessionStateRecord> {
    return sessions.sessionStates(this.ctx);
  }

  sessionState(sessionId: string): sessions.SessionStateRecord | null {
    return sessions.sessionState(this.ctx, sessionId);
  }

  /** Changes them and returns the state after; null when there is no such session. */
  updateSessionState(
    sessionId: string,
    change: sessions.SessionStateChange,
  ): sessions.SessionStateRecord | null {
    return sessions.updateSessionState(this.ctx, sessionId, change);
  }

  /** The provider configuration the session's owner announced; empty for the vendor's own (ADR 0028). */
  sessionConfiguration(sessionId: string): string {
    return sessions.sessionConfiguration(this.ctx, sessionId);
  }

  ownerDeviceId(sessionId: string): string | null {
    return sessions.ownerDeviceId(this.ctx, sessionId);
  }

  /**
   * The handoff commit: owner `from` becomes `to`, the session's project becomes `projectPath` (the target's
   * worktree) on `branch` and the epoch increases, provided the session still has the cursor `byteSize` and the epoch
   * `epoch`. Returns the new epoch, or null when anything differs.
   */
  handoffOwner(
    sessionId: string,
    from: string,
    to: string,
    byteSize: number,
    epoch: number,
    projectPath: string,
    branch: string,
    clone: string | null = null,
    configuration = '',
  ): number | null {
    return sessions.handoffOwner(
      this.ctx,
      sessionId,
      from,
      to,
      byteSize,
      epoch,
      projectPath,
      branch,
      clone,
      Date.now(),
      configuration,
    );
  }

  /** The worktree a handoff created for the session on its current owner, or null (ADR 0023). */
  sessionWorktree(sessionId: string): deletion.WorktreeRecord | null {
    return deletion.worktreeOf(this.ctx, sessionId);
  }

  /** The owner of a session including one being deleted; only the deletion reads it. */
  deletingOwner(sessionId: string): string | null {
    return sessions.getRowAny(this.ctx, sessionId)?.device_id || null;
  }

  tombstoneOf(sessionId: string): deletion.Tombstone | null {
    return deletion.tombstoneOf(this.ctx, sessionId);
  }

  isTombstoned(sessionId: string): boolean {
    return deletion.isTombstoned(this.ctx, sessionId);
  }

  /** Marks the session deleted: it leaves every list and ingest refuses it. A repeated call changes nothing. */
  tombstone(sessionId: string, deviceId: string, now: number = Date.now()): void {
    deletion.tombstone(this.ctx, sessionId, deviceId, now);
  }

  /** Records that the owner's agent stopped syncing a deleted session. */
  markForgotten(sessionId: string): void {
    deletion.markForgotten(this.ctx, sessionId);
  }

  /** Lifts a tombstone (an explicit import asked for the session back); true when there was one. */
  clearTombstone(sessionId: string): boolean {
    return deletion.clearTombstone(this.ctx, sessionId);
  }

  /** The deleted sessions whose agent has not confirmed it stopped syncing them. */
  unforgottenTombstones(deviceId: string): string[] {
    return deletion.unforgotten(this.ctx, deviceId);
  }

  /**
   * Records that the plan of `fromSessionId` was approved into `toSessionId` (ADR 0025). A session keeps its
   * latest continuation: a later one naming either session replaces the earlier row.
   */
  recordContinuation(
    fromSessionId: string,
    toSessionId: string,
    requestId: string,
    mode: string,
    now: number = Date.now(),
  ): void {
    continuations.recordContinuation(this.ctx, fromSessionId, toSessionId, requestId, mode, now);
  }

  /** The IDs of the sessions a session was continued in and from; either may not be in the index yet. */
  continuationLinks(sessionId: string): continuations.ContinuationLinks {
    return continuations.continuationLinks(this.ctx, sessionId);
  }

  /** Removes the core's rows, directories and queued messages of a session. */
  purgeSession(sessionId: string): void {
    deletion.purgeSession(this.ctx, sessionId);
  }

  /**
   * Receives one chunk of a snapshot upload; returns the record when it completed the snapshot, which then
   * replaces the session's previous one. Throws SnapshotError for anything it refuses.
   */
  putSnapshotChunk(chunk: snapshots.SnapshotChunk): snapshots.SnapshotRecord | null {
    return snapshots.putSnapshotChunk(this.ctx, chunk);
  }

  latestSnapshot(sessionId: string): snapshots.SnapshotRecord | null {
    return snapshots.latestSnapshot(this.ctx, sessionId);
  }

  /** The stored snapshot `snapshotId` in pieces of at most 1 MiB. */
  readSnapshot(sessionId: string, snapshotId: string): AsyncGenerator<Buffer> {
    return snapshots.readSnapshot(this.ctx, sessionId, snapshotId);
  }

  /**
   * The changes of the session's latest snapshot against its base commit, paths relative to the session's
   * working directory; null when the session has no snapshot. Throws SnapshotReadError for a damaged archive.
   */
  snapshotChanges(sessionId: string): Promise<SnapshotChanges | null> {
    return snapshotRead.snapshotChanges(this.ctx, sessionId);
  }

  /** The diff of one changed path of the session's latest snapshot. */
  snapshotDiff(sessionId: string, path: string): Promise<snapshotRead.SnapshotDiffResult> {
    return snapshotRead.snapshotDiff(this.ctx, sessionId, path);
  }

  upsertDevice(deviceId: string, name: string, version: string, system?: DeviceSystem): void {
    devices.upsertDevice(this.ctx, deviceId, name, version, system);
  }

  touchDevice(deviceId: string): void {
    devices.touchDevice(this.ctx, deviceId);
  }

  /** Keeps the latest inventory of a device; false when the store does not know the device. */
  setDeviceInventory(deviceId: string, inventory: DeviceInventory): boolean {
    return devices.setInventory(this.ctx, deviceId, inventory);
  }

  /** Replaces the labels of a device; false when the store does not know the device. */
  setDeviceLabels(deviceId: string, labels: string[]): boolean {
    return devices.setLabels(this.ctx, deviceId, labels);
  }

  listDevices(): devices.StoredDevice[] {
    return devices.listDevices(this.ctx);
  }

  mintDevice(name: string, deviceId?: string): { deviceId: string; token: string; name: string } {
    return devices.mintDevice(this.ctx, name, deviceId);
  }

  deviceIdForToken(token: string): string | null {
    return devices.deviceIdForToken(this.ctx, token);
  }

  append(input: AppendInput, rawLines: string[]): AppendResult {
    return append(this.ctx, input, rawLines);
  }

  streamFingerprint(sessionId: string): Promise<{ byteSize: number; streamSha256: string } | null> {
    return streamFingerprint(this.ctx, sessionId);
  }

  /** The first `byteSize` committed bytes of the session in pieces of at most `piece` bytes; `byteSize` must be the cursor. */
  readStream(sessionId: string, byteSize: number, piece: number): AsyncGenerator<Buffer> {
    return readStream(this.ctx, sessionId, byteSize, piece);
  }

  readLines(sessionId: string, start: number, limit: number): Promise<RawLinesPage | null> {
    return readLines(this.ctx, sessionId, start, limit);
  }

  readLine(sessionId: string, lineNo: number): Promise<unknown | null> {
    return readLine(this.ctx, sessionId, lineNo);
  }

  search(
    sessionId: string,
    query: string,
    start: number,
    limit: number,
  ): Promise<RawSearchResult | null> {
    return search(this.ctx, sessionId, query, start, limit);
  }

  searchAll(query: string, start: number, limit: number): Promise<RawGlobalSearchResult> {
    return searchAll(this.ctx, query, start, limit);
  }
}
