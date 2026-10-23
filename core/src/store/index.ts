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
  HandoffRequestState,
  HookAnnounce,
  LocalPermissionRule,
  LocalPermissionRules,
  NotifyDelivery,
  NotifyEventKind,
  NotifyPreferences,
  PermissionRuleBody,
  PermissionRuleEntry,
  PermissionRuleListQuery,
  Queue,
  QueueItem,
  QueuePause,
  ResourceThresholds,
  SessionHandoffRequest,
  SessionMcpProjectDefault,
  SessionMcpSettings,
  SessionMeta,
  SessionUsage,
  SnapshotChanges,
  Template,
  TemplateBody,
  TokenInfo,
  UsagePrices,
  UsagePricesBody,
  UsageQuery,
  UsageResponse,
} from '@hoplet/shared';
import { migrate } from '../migrations/index.ts';
import * as audit from './audit.ts';
import { resumeAuditDerive, scheduleAuditDerive } from './audit-derive.ts';
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
import * as handoffRequests from './handoff-requests.ts';
import * as hooks from './hooks.ts';
import type { StoreContext } from './layout.ts';
import * as notify from './notify.ts';
import * as permissionRules from './permission-rules.ts';
import * as queue from './queue.ts';
import { recoverOnStart } from './recovery.ts';
import * as resourceThresholds from './resource-thresholds.ts';
import * as scriptHooks from './script-hooks.ts';
import { type RawGlobalSearchResult, type RawSearchResult, search, searchAll } from './search.ts';
import * as secrets from './secrets.ts';
import * as sessionMcp from './session-mcp.ts';
import * as sessions from './sessions.ts';
import * as snapshotRead from './snapshot-read.ts';
import * as snapshots from './snapshots.ts';
import * as templates from './templates.ts';
import * as tokens from './tokens.ts';
import * as usage from './usage.ts';

export { type AuditInput, AuditUnavailableError } from './audit.ts';
export { toolInputSummary } from './audit-lines.ts';
export type { BudgetStatus } from './budgets.ts';
export type { ContinuationLinks } from './continuations.ts';
export type { Tombstone, WorktreeRecord } from './deletion.ts';
export type {
  DeliveryOutcome,
  DeliveryRecord,
  HookInput,
  HookPatch,
  HookRecord,
  ScriptRun,
} from './hooks.ts';
export type { PushSubscriptionRecord, WebhookRecord } from './notify.ts';
export type { QueueOutcome } from './queue.ts';
export { type ScriptHookRecord, scriptHookId } from './script-hooks.ts';
export type { HookSecret, NotifySecrets, VapidKeys } from './secrets.ts';
export type { SessionStateChange, SessionStateRecord } from './sessions.ts';
export { type SnapshotDiffResult, SnapshotReadError } from './snapshot-read.ts';
export { type SnapshotChunk, SnapshotError, type SnapshotRecord } from './snapshots.ts';
export { freeName } from './templates.ts';
export { summarizeTitle } from './title.ts';
export { type TokenRecord, TooManyTokensError } from './tokens.ts';
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
      auditDeriving: new Set(),
      auditRerun: new Set(),
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

  /** Resumes the tool-call deriver of the audit log where a restart cut it; `idle` waits for it. */
  startAuditDerive(): void {
    resumeAuditDerive(this.ctx);
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

  listPermissionRules(query: PermissionRuleListQuery = {}): PermissionRuleEntry[] {
    return permissionRules.listPermissionRules(this.ctx, query);
  }

  getPermissionRule(id: string): PermissionRuleEntry | null {
    return permissionRules.getPermissionRule(this.ctx, id);
  }

  /** The deny rules the agents announced from their own configuration; only `deviceId`'s when given. Read-only. */
  localPermissionDeny(deviceId?: string): LocalPermissionRules[] {
    return devices.localPermissionDeny(this.ctx, deviceId);
  }

  permissionRuleCount(): number {
    return permissionRules.permissionRuleCount(this.ctx);
  }

  /** The revision of the rules that apply to a machine; it grows with every change that reaches it. */
  permissionRevision(deviceId: string): number {
    return permissionRules.permissionRevision(this.ctx, deviceId);
  }

  createPermissionRule(body: PermissionRuleBody, now: number): PermissionRuleEntry {
    return permissionRules.createPermissionRule(this.ctx, body, now);
  }

  updatePermissionRule(
    id: string,
    body: PermissionRuleBody,
    now: number,
  ): PermissionRuleEntry | null {
    return permissionRules.updatePermissionRule(this.ctx, id, body, now);
  }

  setBuiltinPermissionRule(id: string, enabled: boolean, now: number): PermissionRuleEntry | null {
    return permissionRules.setBuiltinEnabled(this.ctx, id, enabled, now);
  }

  deletePermissionRule(id: string): boolean {
    return permissionRules.deletePermissionRule(this.ctx, id);
  }

  hasDevice(deviceId: string): boolean {
    return devices.listDevices(this.ctx).some((d) => d.deviceId === deviceId);
  }

  listTemplates(): Template[] {
    return templates.listTemplates(this.ctx);
  }

  getTemplate(id: string): Template | null {
    return templates.getTemplate(this.ctx, id);
  }

  templateCount(): number {
    return templates.templateCount(this.ctx);
  }

  templateNames(): Set<string> {
    return templates.templateNames(this.ctx);
  }

  createTemplate(body: TemplateBody, now: number): Template | 'name-taken' {
    return templates.createTemplate(this.ctx, body, now);
  }

  createTemplates(bodies: readonly TemplateBody[], now: number): Template[] {
    return templates.createTemplates(this.ctx, bodies, now);
  }

  updateTemplate(id: string, body: TemplateBody, now: number): Template | null | 'name-taken' {
    return templates.updateTemplate(this.ctx, id, body, now);
  }

  deleteTemplate(id: string): boolean {
    return templates.deleteTemplate(this.ctx, id);
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

  listHooks(): hooks.HookRecord[] {
    return hooks.listHooks(this.ctx);
  }

  getHook(id: string): hooks.HookRecord | null {
    return hooks.getHook(this.ctx, id);
  }

  hookCount(): number {
    return hooks.hookCount(this.ctx);
  }

  /** Adds a hook with its signing secret and records it in the audit log. */
  createHook(input: hooks.HookInput, secret: string, now: number): hooks.HookRecord {
    return hooks.createHook(this.ctx, input, secret, now);
  }

  updateHook(id: string, patch: hooks.HookPatch, now: number): hooks.HookRecord | null {
    return hooks.updateHook(this.ctx, id, patch, now);
  }

  /** Removes a hook, its deliveries and its secret. */
  deleteHook(id: string): boolean {
    return hooks.deleteHook(this.ctx, id);
  }

  auditHookAction(id: string, action: 'rotated' | 'tested'): void {
    hooks.auditHookAction(this.ctx, id, action);
  }

  hookSecret(id: string): secrets.HookSecret | null {
    return secrets.hookSecret(this.ctx, id);
  }

  /** Replaces a hook's secret; the old one stays valid until `keepUntil` when given. */
  setHookSecret(id: string, secret: string | null, keepUntil?: number): void {
    secrets.setHookSecret(this.ctx, id, secret, keepUntil);
  }

  enqueueHookDelivery(
    input: Parameters<typeof hooks.enqueueDelivery>[1],
    now: number,
  ): hooks.DeliveryRecord {
    return hooks.enqueueDelivery(this.ctx, input, now);
  }

  findPendingHookDelivery(hookId: string, eventId: string): hooks.DeliveryRecord | null {
    return hooks.findPendingDelivery(this.ctx, hookId, eventId);
  }

  /** Takes the script hooks a device announced in its hello. */
  replaceScriptHooks(deviceId: string, announced: readonly HookAnnounce[], now: number): void {
    scriptHooks.replaceScriptHooks(this.ctx, deviceId, announced, now);
  }

  listScriptHooks(): scriptHooks.ScriptHookRecord[] {
    return scriptHooks.listScriptHooks(this.ctx);
  }

  getScriptHook(deviceId: string, slug: string): scriptHooks.ScriptHookRecord | null {
    return scriptHooks.getScriptHook(this.ctx, deviceId, slug);
  }

  setScriptHookEnabled(
    deviceId: string,
    slug: string,
    enabled: boolean,
  ): scriptHooks.ScriptHookRecord | null {
    return scriptHooks.setScriptHookEnabled(this.ctx, deviceId, slug, enabled);
  }

  auditScriptHookTest(deviceId: string, slug: string): void {
    scriptHooks.auditScriptHookTest(this.ctx, deviceId, slug);
  }

  settlePendingScriptDeliveries(deviceId: string | null, error: string, now: number): void {
    scriptHooks.settlePendingScriptDeliveries(this.ctx, deviceId, error, now);
  }

  getHookDelivery(id: string): hooks.DeliveryRecord | null {
    return hooks.getDelivery(this.ctx, id);
  }

  dueHookDeliveries(now: number, limit: number): hooks.DeliveryRecord[] {
    return hooks.dueDeliveries(this.ctx, now, limit);
  }

  nextHookDeliveryAt(): number | null {
    return hooks.nextDeliveryAt(this.ctx);
  }

  pendingHookDeliveries(hookId: string): number {
    return hooks.pendingCount(this.ctx, hookId);
  }

  recordHookDelivery(id: string, outcome: hooks.DeliveryOutcome): void {
    hooks.recordDelivery(this.ctx, id, outcome);
  }

  listHookDeliveries(hookId: string, limit: number): hooks.DeliveryRecord[] {
    return hooks.listDeliveries(this.ctx, hookId, limit);
  }

  lastSettledHookDelivery(hookId: string): hooks.DeliveryRecord | null {
    return hooks.lastSettled(this.ctx, hookId);
  }

  pruneHookDeliveries(now: number): void {
    hooks.pruneDeliveries(this.ctx, now);
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

  queueAdd(
    sessionId: string,
    input: { id: string; text: string; attachments: string[]; actorId?: string },
  ) {
    return queue.queueAdd(this.ctx, sessionId, input);
  }

  queueEdit(
    sessionId: string,
    id: string,
    version: number,
    input: { text: string; attachments: string[]; actorId?: string },
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

  /** The token that queued the message, or "" for the host. */
  queueAuthor(sessionId: string, id: string): string {
    return queue.queueAuthor(this.ctx, sessionId, id);
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

  /** Records the worktree an agent made for a session it started, for its deletion later (ADR 0023). */
  recordNewWorktree(
    sessionId: string,
    deviceId: string,
    worktree: { clone: string; path: string; branch: string },
  ): void {
    deletion.recordNewWorktree(this.ctx, sessionId, deviceId, worktree, Date.now());
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

  upsertDevice(
    deviceId: string,
    name: string,
    version: string,
    system?: DeviceSystem,
    permissionDeny?: readonly LocalPermissionRule[],
  ): void {
    devices.upsertDevice(this.ctx, deviceId, name, version, system, permissionDeny);
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

  listTokens(now: number = Date.now()): TokenInfo[] {
    return tokens.listTokens(this.ctx, now);
  }

  issueToken(
    input: Parameters<typeof tokens.issueToken>[1],
    now: number = Date.now(),
  ): { info: TokenInfo; secret: string } {
    return tokens.issueToken(this.ctx, input, now);
  }

  revokeToken(id: string, now: number = Date.now()): 'revoked' | 'removed' | 'unknown' {
    return tokens.revokeToken(this.ctx, id, now);
  }

  tokenForSecret(secret: string, now: number = Date.now()): tokens.TokenRecord | null {
    return tokens.tokenForSecret(this.ctx, secret, now);
  }

  tokenActive(id: string, now: number = Date.now()): boolean {
    return tokens.tokenActive(this.ctx, id, now);
  }

  mintSessionToken(
    sessionId: string,
    now: number = Date.now(),
  ): { record: tokens.TokenRecord; secret: string } {
    return tokens.mintSessionToken(this.ctx, sessionId, now);
  }

  revokeSessionTokens(sessionId: string): number {
    return tokens.revokeSessionTokens(this.ctx, sessionId);
  }

  /** Removes expired session tokens; returns the sessions that still hold one. */
  sweepSessionTokens(now: number = Date.now()): string[] {
    return tokens.sweepSessionTokens(this.ctx, now);
  }

  sessionMcpSettings(sessionId: string): SessionMcpSettings | null {
    return sessionMcp.getSessionSettings(this.ctx, sessionId);
  }

  seedSessionMcpSettings(
    sessionId: string,
    settings: SessionMcpSettings,
    now: number = Date.now(),
  ): void {
    sessionMcp.seedSessionSettings(this.ctx, sessionId, settings, now);
  }

  putSessionMcpSettings(
    sessionId: string,
    settings: SessionMcpSettings,
    previous: SessionMcpSettings,
    now: number = Date.now(),
  ): SessionMcpSettings {
    return sessionMcp.putSessionSettings(this.ctx, sessionId, settings, previous, now);
  }

  /** The default of a project, or the global one with '' for both; null when none is set. */
  sessionMcpDefault(deviceId: string, path: string): SessionMcpSettings | null {
    return sessionMcp.getSessionDefault(this.ctx, deviceId, path);
  }

  putSessionMcpDefault(
    deviceId: string,
    path: string,
    settings: SessionMcpSettings,
    now: number = Date.now(),
  ): SessionMcpSettings {
    return sessionMcp.putSessionDefault(this.ctx, deviceId, path, settings, now);
  }

  deleteSessionMcpDefault(deviceId: string, path: string): boolean {
    return sessionMcp.deleteSessionDefault(this.ctx, deviceId, path);
  }

  sessionMcpProjectDefaults(): SessionMcpProjectDefault[] {
    return sessionMcp.listProjectDefaults(this.ctx);
  }

  sessionChain(sessionId: string): string[] {
    return sessionMcp.getChain(this.ctx, sessionId);
  }

  /** The sessions a session started, by id, with their creation chain and time limit. */
  sessionChildren(): Map<string, sessionMcp.ChildRecord> {
    return sessionMcp.allChildren(this.ctx);
  }

  /** The creation chain of every session a session started (root first, parent last), by id. */
  childChains(): Map<string, string[]> {
    return new Map([...sessionMcp.allChildren(this.ctx)].map(([id, r]) => [id, r.chain]));
  }

  recordChild(sessionId: string, chain: string[], startedAt: number, timeLimitMs: number): void {
    sessionMcp.recordChild(this.ctx, sessionId, chain, startedAt, timeLimitMs);
  }

  setSessionChain(sessionId: string, chain: string[]): void {
    sessionMcp.setChain(this.ctx, sessionId, chain);
  }

  forgetSessionMcp(sessionId: string): void {
    sessionMcp.forgetSession(this.ctx, sessionId);
    handoffRequests.forgetRequests(this.ctx, sessionId);
  }

  insertHandoffRequest(
    request: SessionHandoffRequest,
    record: audit.AuditInput,
  ): SessionHandoffRequest {
    return handoffRequests.insertRequest(this.ctx, request, record);
  }

  handoffRequest(requestId: string): SessionHandoffRequest | null {
    return handoffRequests.getRequest(this.ctx, requestId);
  }

  handoffRequests(sessionId: string): SessionHandoffRequest[] {
    return handoffRequests.listRequests(this.ctx, sessionId);
  }

  openHandoffRequests(): SessionHandoffRequest[] {
    return handoffRequests.listOpenRequests(this.ctx);
  }

  /** Moves a request out of one of the states in `from`; null when it had left them already. */
  changeHandoffRequest(
    requestId: string,
    from: readonly HandoffRequestState[],
    change: handoffRequests.RequestChange,
    record: audit.AuditInput | null,
  ): SessionHandoffRequest | null {
    return handoffRequests.changeRequest(this.ctx, requestId, from, change, record);
  }

  autoApprovedHandoffs(sessionId: string, since: number): number {
    return handoffRequests.countAutoApproved(this.ctx, sessionId, since);
  }

  append(input: AppendInput, rawLines: string[]): AppendResult {
    const result = append(this.ctx, input, rawLines);
    if (result.appendedLines > 0) scheduleAuditDerive(this.ctx, input.sessionId);
    return result;
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

  searchAll(
    query: string,
    start: number,
    limit: number,
    visible?: (sessionId: string) => boolean,
  ): Promise<RawGlobalSearchResult> {
    return searchAll(this.ctx, query, start, limit, visible);
  }
}
