import {
  ActionDecideResponseSchema,
  ActionInspectResponseSchema,
  AttachmentResultSchema,
  AuditEventsResponseSchema,
  AuditVerifyResponseSchema,
  type BudgetBody,
  BudgetListResponseSchema,
  BudgetSchema,
  CommandsResultSchema,
  type ConfigSetting,
  type DeleteSessionRequest,
  DeleteSessionResponseSchema,
  DeletionPreviewSchema,
  DeviceCloneResultSchema,
  DeviceDirsResultSchema,
  DeviceLabelsResultSchema,
  DeviceListResponseSchema,
  DeviceMintResultSchema,
  DeviceProjectsResultSchema,
  DeviceProvidersResultSchema,
  type DeviceProvidersUpdateRequest,
  DeviceResourcesResultSchema,
  ErrorResponseSchema,
  FullBlockSchema,
  type GenericSchema,
  GlobalSearchResultSchema,
  HandoffCancelResultSchema,
  HandoffResultSchema,
  type HookCreateRequest,
  HookDeliveriesResponseSchema,
  HookInfoSchema,
  HookListResponseSchema,
  HookSecretResponseSchema,
  HookTestResponseSchema,
  type HookUpdateRequest,
  type InferOutput,
  LinesPageSchema,
  LiveSessionMetaSchema,
  NewSessionResultSchema,
  type NotifyPreferences,
  NotifyPreferencesSchema,
  type PermissionDecision,
  type PermissionRuleBody,
  PermissionRuleEntrySchema,
  type PermissionRuleListQuery,
  PermissionRuleListResponseSchema,
  PermissionRuleMachinesResponseSchema,
  type PermissionRulePut,
  type PromptImage,
  PromptResultSchema,
  type ProviderInfo,
  PushKeyResponseSchema,
  type PushSubscribeRequest,
  PushSubscriptionInfoSchema,
  PushSubscriptionListResponseSchema,
  QueueSchema,
  type ResourceThresholds,
  ResourceThresholdsSchema,
  RuntimeCommandResultSchema,
  type SessionConfig,
  SessionHandoffRequestListSchema,
  SessionHandoffRequestSchema,
  SessionListResponseSchema,
  SessionMcpDefaultsResponseSchema,
  SessionMcpOkResponseSchema,
  SessionMcpResponseSchema,
  type SessionMcpSettings,
  type SessionPatchRequest,
  SessionPortsResultSchema,
  SessionUsageSchema,
  SnapshotChangesSchema,
  SnapshotDiffSchema,
  SteerResultSchema,
  safeParse,
  type TemplateBody,
  TemplateFileSchema,
  type TemplateImportRequest,
  TemplateImportResultSchema,
  TemplateListResponseSchema,
  TemplateSchema,
  TemplateStartResultSchema,
  TerminalCloseResultSchema,
  TerminalInfoSchema,
  TerminalStateSchema,
  type TokenCreateRequest,
  TokenCreateResponseSchema,
  TokenListResponseSchema,
  TokenRevokeResponseSchema,
  type UsagePricesBody,
  UsagePricesSchema,
  UsageResponseSchema,
  type WebhookCreateRequest,
  WebhookInfoSchema,
  WebhookListResponseSchema,
  WebhookTestResponseSchema,
  type WebhookUpdateRequest,
  WorkspaceDiffSchema,
  WorkspaceFileSchema,
  WorkspaceListingSchema,
  WsTicketResponseSchema,
} from '@hoplet/shared';
import { ApiError, UnauthorizedError } from './errors.ts';

export { ApiError, UnauthorizedError };

const TOKEN_KEY = 'hoplet_token';

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(t: string): void {
  localStorage.setItem(TOKEN_KEY, t);
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

/** Sends one request and parses the JSON response with its schema; a non-2xx response throws ApiError. */
async function call<S extends GenericSchema>(
  path: string,
  schema: S,
  body?: unknown,
  signal?: AbortSignal,
  method: 'POST' | 'PATCH' | 'PUT' | 'DELETE' = 'POST',
  auth = true,
): Promise<InferOutput<S>> {
  const headers: Record<string, string> = auth
    ? { Authorization: `Bearer ${getToken() ?? ''}` }
    : {};
  const init: RequestInit = { headers };
  if (signal) init.signal = signal;
  if (method === 'DELETE') init.method = method;
  if (body !== undefined) {
    init.method = method;
    headers['content-type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  const res = await fetch(path, init);
  if (res.status === 401 && auth) throw new UnauthorizedError();
  const type = res.headers.get('content-type') ?? '';
  if (!type.includes('application/json')) {
    if (!res.ok) throw new ApiError(res.status, null);
    throw new Error(`${path}: expected JSON, got ${type || 'unknown'}`);
  }
  const payload: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    const error = safeParse(ErrorResponseSchema, payload);
    throw error.success
      ? new ApiError(
          res.status,
          error.output.code,
          error.output.reason,
          error.output.steps,
          error.output.detail,
          error.output.budget,
        )
      : new ApiError(res.status, null);
  }
  const parsed = safeParse(schema, payload);
  if (!parsed.success) throw new Error(`${path}: unexpected response`);
  return parsed.output;
}

export function fetchSessions() {
  return call('/api/sessions', SessionListResponseSchema);
}

export function fetchLines(sessionId: string, start: number, limit: number) {
  return call(`/api/sessions/${sessionId}/lines?start=${start}&limit=${limit}`, LinesPageSchema);
}

/** The commands and skills a session offers; they work with the owner offline. */
export function fetchSessionCommands(sessionId: string) {
  return call(`/api/sessions/${sessionId}/commands`, CommandsResultSchema);
}

/** The commands a new session in this directory would offer. */
export function fetchDeviceCommands(deviceId: string, cwd: string) {
  return call(
    `/api/devices/${deviceId}/commands?cwd=${encodeURIComponent(cwd)}`,
    CommandsResultSchema,
  );
}

export function fetchWorkspace(sessionId: string) {
  return call(`/api/sessions/${sessionId}/workspace`, WorkspaceListingSchema);
}

/** One file of the owner's working tree; `at: 'head'` reads it as `HEAD` has it. */
export function fetchWorkspaceFile(
  sessionId: string,
  path: string,
  at?: 'head',
  signal?: AbortSignal,
) {
  const query = `path=${encodeURIComponent(path)}${at ? `&at=${at}` : ''}`;
  return call(
    `/api/sessions/${sessionId}/workspace/file?${query}`,
    WorkspaceFileSchema,
    undefined,
    signal,
  );
}

/** The unified diff of one changed path against `HEAD`; the owner must be online. */
export function fetchWorkspaceDiff(sessionId: string, path: string, signal?: AbortSignal) {
  return call(
    `/api/sessions/${sessionId}/workspace/diff?path=${encodeURIComponent(path)}`,
    WorkspaceDiffSchema,
    undefined,
    signal,
  );
}

/** The changed paths of the latest stored snapshot, owner online or not. */
export function fetchSnapshotChanges(sessionId: string) {
  return call(`/api/sessions/${sessionId}/snapshot/changes`, SnapshotChangesSchema);
}

/** One path's diff from the latest stored snapshot. */
export function fetchSnapshotDiff(sessionId: string, path: string, signal?: AbortSignal) {
  return call(
    `/api/sessions/${sessionId}/snapshot/diff?path=${encodeURIComponent(path)}`,
    SnapshotDiffSchema,
    undefined,
    signal,
  );
}

export function fetchDevices() {
  return call('/api/devices', DeviceListResponseSchema);
}

/** The default warning thresholds and the machines' own. */
export function fetchResourceThresholds() {
  return call('/api/resources/thresholds', ResourceThresholdsSchema);
}

/** Replaces all of them; a machine without an entry uses the default. */
export function putResourceThresholds(thresholds: ResourceThresholds) {
  return call('/api/resources/thresholds', ResourceThresholdsSchema, thresholds, undefined, 'PUT');
}

/** The last hour of a machine's resource readings and the sessions of the latest one. */
export function fetchDeviceResources(deviceId: string) {
  return call(
    `/api/devices/${encodeURIComponent(deviceId)}/resources`,
    DeviceResourcesResultSchema,
  );
}

/** Live agent catalog; `null` when the device is offline. */
export async function fetchDeviceProjects(deviceId: string) {
  try {
    return await call(
      `/api/devices/${encodeURIComponent(deviceId)}/projects`,
      DeviceProjectsResultSchema,
    );
  } catch (err) {
    if (err instanceof ApiError && err.code === 'owner-offline') return null;
    throw err;
  }
}

/** Makes the agent of a device watch an absolute path; resolves with the device's updated listing. */
export function postAddProject(deviceId: string, path: string) {
  return call(`/api/devices/${encodeURIComponent(deviceId)}/projects`, DeviceProjectsResultSchema, {
    path,
  });
}

/** The directories under `path` on a device (`~` or absent: its browse root); aborts with `signal`. */
export function fetchDeviceDirs(deviceId: string, path: string | undefined, signal?: AbortSignal) {
  const query = path === undefined ? '' : `?path=${encodeURIComponent(path)}`;
  return call(
    `/api/devices/${encodeURIComponent(deviceId)}/dirs${query}`,
    DeviceDirsResultSchema,
    undefined,
    signal,
  );
}

/** The clone among a device's watched projects whose remote `origin` is `origin`; aborts with `signal`. */
export function fetchDeviceClone(
  deviceId: string,
  origin: string,
  signal?: AbortSignal,
  cloneInto?: string,
) {
  const into = cloneInto === undefined ? '' : `&cloneInto=${encodeURIComponent(cloneInto)}`;
  return call(
    `/api/devices/${encodeURIComponent(deviceId)}/clone?origin=${encodeURIComponent(origin)}${into}`,
    DeviceCloneResultSchema,
    undefined,
    signal,
  );
}

/** Changes the name, the enabled flag or the defaults of one provider configuration on a machine. */
export function postDeviceProviders(deviceId: string, request: DeviceProvidersUpdateRequest) {
  return call(
    `/api/devices/${encodeURIComponent(deviceId)}/providers`,
    DeviceProvidersResultSchema,
    request,
  );
}

/** Replaces a machine's labels; works while it is offline. */
export function putDeviceLabels(deviceId: string, labels: string[]) {
  return call(
    `/api/devices/${encodeURIComponent(deviceId)}/labels`,
    DeviceLabelsResultSchema,
    { labels },
    undefined,
    'PUT',
  );
}

export function postMintDevice(name: string, deviceId?: string) {
  return call('/api/devices', DeviceMintResultSchema, deviceId ? { name, deviceId } : { name });
}

/** Hands the session off; `where` is the path of a clone on the target or the folder to clone into first. */
export function postHandoff(
  sessionId: string,
  targetDeviceId: string,
  where: {
    targetPath?: string | undefined;
    cloneInto?: string | undefined;
    /** The target's provider configuration to continue under; the vendor's own when absent. */
    configuration?: string | undefined;
  } = {},
) {
  return call(`/api/sessions/${sessionId}/handoff`, HandoffResultSchema, {
    targetDeviceId,
    ...(where.targetPath === undefined ? {} : { targetPath: where.targetPath }),
    ...(where.cloneInto === undefined ? {} : { cloneInto: where.cloneInto }),
    ...(where.configuration === undefined ? {} : { configuration: where.configuration }),
  });
}

/** Stops the clone step of the session's handoff. */
export function postHandoffCancel(sessionId: string) {
  return call(`/api/sessions/${sessionId}/handoff/cancel`, HandoffCancelResultSchema, {});
}

export function fetchSearch(q: string, start = 0) {
  return call(`/api/search?q=${encodeURIComponent(q)}&start=${start}`, GlobalSearchResultSchema);
}

/** The full block an elided one stands for. */
export function fetchBlock(sessionId: string, line: number, block: number) {
  return call(`/api/sessions/${sessionId}/blocks/${line}/${block}`, FullBlockSchema);
}

export function postPrompt(
  sessionId: string,
  text: string,
  attachments: string[] = [],
  promptId: string = crypto.randomUUID(),
) {
  return call(`/api/sessions/${sessionId}/prompt`, PromptResultSchema, {
    text,
    promptId,
    ...(attachments.length > 0 ? { attachments } : {}),
  });
}

/** Sets one config option of a session; the next runtime state reports it (ADR 0018). */
export function postConfig(sessionId: string, configId: string, value: ConfigSetting) {
  return call(`/api/sessions/${sessionId}/config`, RuntimeCommandResultSchema, {
    configId,
    value,
  });
}

/** Uploads an image for a coming prompt to a machine; the prompt names it by the ID (ADR 0017). */
export async function postAttachment(deviceId: string, image: PromptImage): Promise<string> {
  const result = await call(
    `/api/devices/${encodeURIComponent(deviceId)}/attachments`,
    AttachmentResultSchema,
    image,
  );
  return result.attachmentId;
}

/** Answers a permission request; a plan exit also takes the feedback of a denial and the mode of an approval. */
export function postPermission(
  sessionId: string,
  requestId: string,
  decision: PermissionDecision,
  extra: { message?: string; mode?: string } = {},
) {
  return call(`/api/sessions/${sessionId}/permission`, RuntimeCommandResultSchema, {
    requestId,
    decision,
    ...(extra.message ? { message: extra.message } : {}),
    ...(extra.mode ? { mode: extra.mode } : {}),
  });
}

/**
 * Approves an open plan into a new session on the same machine and project (ADR 0025). Resolves with the new
 * session once it has started; a failure leaves the plan waiting. `promptId` makes a repeat after a timeout the same start.
 */
export function postFreshPermission(
  sessionId: string,
  request: { requestId: string; mode: string; promptId: string },
) {
  return call(`/api/sessions/${sessionId}/permission/fresh`, NewSessionResultSchema, request);
}

export function postInterrupt(sessionId: string) {
  return call(`/api/sessions/${sessionId}/interrupt`, RuntimeCommandResultSchema, {});
}

/** Opens a shell on the session's owner machine (ADR 0026); the attach itself goes over the WebSocket. */
export function openTerminal(sessionId: string, size: { cols: number; rows: number }) {
  return call(`/api/sessions/${sessionId}/terminal`, TerminalInfoSchema, size);
}

/** The session's open terminal, or null. */
export function fetchTerminal(sessionId: string) {
  return call(`/api/sessions/${sessionId}/terminal`, TerminalStateSchema);
}

export function closeTerminal(terminalId: string) {
  return call(
    `/api/terminals/${terminalId}`,
    TerminalCloseResultSchema,
    undefined,
    undefined,
    'DELETE',
  );
}

/** The ports the session's dev servers listen on. */
export function fetchPorts(sessionId: string) {
  return call(`/api/sessions/${sessionId}/ports`, SessionPortsResultSchema);
}

/** What deleting a session would remove, read from the owner (ADR 0023). */
export function fetchDeletionPreview(sessionId: string) {
  return call(`/api/sessions/${sessionId}/deletion`, DeletionPreviewSchema);
}

/** Deletes a session and what was asked for; a half-done deletion rejects with an `ApiError` carrying `steps`. */
export function deleteSession(sessionId: string, request: DeleteSessionRequest) {
  return call(
    `/api/sessions/${sessionId}`,
    DeleteSessionResponseSchema,
    request,
    undefined,
    'DELETE',
  );
}

export function postStop(sessionId: string) {
  return call(`/api/sessions/${sessionId}/stop`, RuntimeCommandResultSchema, {});
}

/** Starts a session; the response comes when the agent has created and imported it (up to 75 s). */
export function postNewSession(request: {
  deviceId: string;
  projectPath: string;
  provider: ProviderInfo['provider'];
  /** The provider configuration to start with; the vendor's own when absent. */
  configuration?: string;
  config: SessionConfig;
  text: string;
  attachments?: string[];
  promptId?: string;
}) {
  const { attachments, ...rest } = request;
  return call('/api/sessions', NewSessionResultSchema, {
    ...rest,
    ...(attachments && attachments.length > 0 ? { attachments } : {}),
    promptId: request.promptId ?? crypto.randomUUID(),
  });
}

/**
 * The URL of the browser WebSocket. It carries a single-use ticket minted with the host token, valid for
 * 30 seconds, so the token itself never appears in a URL.
 */
export async function socketUrl(): Promise<string> {
  const { ticket } = await call('/api/ws-tickets', WsTicketResponseSchema, {});
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${location.host}/ws?ticket=${encodeURIComponent(ticket)}`;
}

/** Changes a session's title, pin or archive state (`PATCH /api/sessions/:id`); the core tells every browser. */
export function patchSession(sessionId: string, patch: SessionPatchRequest) {
  return call(
    `/api/sessions/${encodeURIComponent(sessionId)}`,
    LiveSessionMetaSchema,
    patch,
    undefined,
    'PATCH',
  );
}

const queuePath = (sessionId: string, tail = '') =>
  `/api/sessions/${encodeURIComponent(sessionId)}/queue${tail}`;

/** The queue of a session (ADR 0020). */
export function fetchQueue(sessionId: string, signal?: AbortSignal) {
  return call(queuePath(sessionId), QueueSchema, undefined, signal);
}

/** Appends a message to the queue; a repeated `id` changes nothing. */
export function postQueueAdd(
  sessionId: string,
  id: string,
  text: string,
  attachments: string[] = [],
) {
  return call(queuePath(sessionId), QueueSchema, {
    id,
    text,
    ...(attachments.length > 0 ? { attachments } : {}),
  });
}

/** Replaces a queued message; `version` is the one the client read, a newer one answers `queue-conflict`. */
export function patchQueueItem(
  sessionId: string,
  itemId: string,
  version: number,
  text: string,
  attachments: string[] = [],
) {
  return call(
    queuePath(sessionId, `/${encodeURIComponent(itemId)}`),
    QueueSchema,
    { version, text, ...(attachments.length > 0 ? { attachments } : {}) },
    undefined,
    'PATCH',
  );
}

export function deleteQueueItem(sessionId: string, itemId: string, version: number) {
  return call(
    queuePath(sessionId, `/${encodeURIComponent(itemId)}?version=${version}`),
    QueueSchema,
    undefined,
    undefined,
    'DELETE',
  );
}

export function postQueueOrder(sessionId: string, ids: string[]) {
  return call(queuePath(sessionId, '/order'), QueueSchema, { ids });
}

export function postQueueResume(sessionId: string) {
  return call(queuePath(sessionId, '/resume'), QueueSchema, {});
}

export function postQueueClear(sessionId: string) {
  return call(queuePath(sessionId, '/clear'), QueueSchema, {});
}

/** Sends a queued message into the running turn instead of waiting for its end. */
export function postQueueSteer(sessionId: string, itemId: string, version: number) {
  return call(queuePath(sessionId, `/${encodeURIComponent(itemId)}/steer`), QueueSchema, {
    version,
  });
}

/** Sends a message into the running turn at its next tool gap. */
export function postSteer(
  sessionId: string,
  text: string,
  attachments: string[] = [],
  steerId: string = crypto.randomUUID(),
) {
  return call(`/api/sessions/${encodeURIComponent(sessionId)}/steer`, SteerResultSchema, {
    steerId,
    text,
    ...(attachments.length > 0 ? { attachments } : {}),
  });
}

/** The notification preferences (ADR 0021). */
export function fetchNotifyPreferences() {
  return call('/api/notify/preferences', NotifyPreferencesSchema);
}

export function putNotifyPreferences(preferences: NotifyPreferences) {
  return call('/api/notify/preferences', NotifyPreferencesSchema, preferences, undefined, 'PUT');
}

/** The VAPID public key a push subscription is made against. */
export function fetchPushKey() {
  return call('/api/notify/push/key', PushKeyResponseSchema);
}

export function fetchPushSubscriptions() {
  return call('/api/notify/push/subscriptions', PushSubscriptionListResponseSchema);
}

export function postPushSubscription(request: PushSubscribeRequest) {
  return call('/api/notify/push/subscriptions', PushSubscriptionInfoSchema, request);
}

export function postPushUnsubscribe(endpoint: string) {
  return call('/api/notify/push/unsubscribe', PushSubscriptionListResponseSchema, { endpoint });
}

export function fetchWebhooks() {
  return call('/api/notify/webhooks', WebhookListResponseSchema);
}

export function postWebhook(request: WebhookCreateRequest) {
  return call('/api/notify/webhooks', WebhookInfoSchema, request);
}

export function patchWebhook(id: string, change: WebhookUpdateRequest) {
  return call(
    `/api/notify/webhooks/${encodeURIComponent(id)}`,
    WebhookInfoSchema,
    change,
    undefined,
    'PATCH',
  );
}

export function deleteWebhook(id: string) {
  return call(
    `/api/notify/webhooks/${encodeURIComponent(id)}`,
    WebhookListResponseSchema,
    undefined,
    undefined,
    'DELETE',
  );
}

export function postWebhookTest(id: string) {
  return call(`/api/notify/webhooks/${encodeURIComponent(id)}/test`, WebhookTestResponseSchema, {});
}

export function fetchHooks() {
  return call('/api/hooks', HookListResponseSchema);
}

export function postHook(request: HookCreateRequest) {
  return call('/api/hooks', HookSecretResponseSchema, request);
}

export function patchHook(id: string, change: HookUpdateRequest) {
  return call(`/api/hooks/${encodeURIComponent(id)}`, HookInfoSchema, change, undefined, 'PATCH');
}

export function deleteHook(id: string) {
  return call(
    `/api/hooks/${encodeURIComponent(id)}`,
    HookListResponseSchema,
    undefined,
    undefined,
    'DELETE',
  );
}

export function rotateHookSecret(id: string) {
  return call(`/api/hooks/${encodeURIComponent(id)}/rotate-secret`, HookSecretResponseSchema, {});
}

export function fetchHookDeliveries(id: string, signal?: AbortSignal) {
  return call(
    `/api/hooks/${encodeURIComponent(id)}/deliveries`,
    HookDeliveriesResponseSchema,
    undefined,
    signal,
  );
}

export function postHookTest(id: string) {
  return call(`/api/hooks/${encodeURIComponent(id)}/test`, HookTestResponseSchema, {});
}

/** The action page's calls: the signed token in the body is the credential, there is no host token. */
export function postActionInspect(token: string) {
  return call(
    '/api/action/inspect',
    ActionInspectResponseSchema,
    { token },
    undefined,
    'POST',
    false,
  );
}

export function postActionDecide(token: string, decision: 'allow' | 'deny') {
  return call(
    '/api/action/decide',
    ActionDecideResponseSchema,
    { token, decision },
    undefined,
    'POST',
    false,
  );
}

/** The usage in a range, grouped (docs/api.md#usage); the numbers are estimates. */
export function fetchUsage(
  query: { from?: number; to?: number; groupBy: string; utcOffset: number; limit?: number },
  signal?: AbortSignal,
) {
  const q = new URLSearchParams({ groupBy: query.groupBy, utcOffset: String(query.utcOffset) });
  if (query.from !== undefined) q.set('from', String(query.from));
  if (query.to !== undefined) q.set('to', String(query.to));
  if (query.limit !== undefined) q.set('limit', String(query.limit));
  return call(`/api/usage?${q}`, UsageResponseSchema, undefined, signal);
}

/** One session's usage in total and by model. */
export function fetchSessionUsage(sessionId: string, signal?: AbortSignal) {
  return call(
    `/api/sessions/${encodeURIComponent(sessionId)}/usage`,
    SessionUsageSchema,
    undefined,
    signal,
  );
}

/** The built-in prices and the host's own. */
export function fetchUsagePrices() {
  return call('/api/usage/prices', UsagePricesSchema);
}

/** Replaces all price overrides. */
export function putUsagePrices(body: UsagePricesBody) {
  return call('/api/usage/prices', UsagePricesSchema, body, undefined, 'PUT');
}

/** The budgets with the spend of their current periods (docs/api.md#budgets). */
export function fetchBudgets(signal?: AbortSignal) {
  return call('/api/budgets', BudgetListResponseSchema, undefined, signal);
}

export function postBudget(body: BudgetBody) {
  return call('/api/budgets', BudgetSchema, body);
}

export function putBudget(id: string, body: BudgetBody) {
  return call(`/api/budgets/${encodeURIComponent(id)}`, BudgetSchema, body, undefined, 'PUT');
}

/** Removes a budget; the answer is the list that remains. */
export function deleteBudget(id: string) {
  return call(
    `/api/budgets/${encodeURIComponent(id)}`,
    BudgetListResponseSchema,
    undefined,
    undefined,
    'DELETE',
  );
}

/** Lifts the pause of a budget until its period ends. */
export function postBudgetAllow(id: string) {
  return call(`/api/budgets/${encodeURIComponent(id)}/allow`, BudgetSchema, {});
}

/** Takes the lift back: the pause applies again at once. */
export function deleteBudgetAllow(id: string) {
  return call(
    `/api/budgets/${encodeURIComponent(id)}/allow`,
    BudgetSchema,
    undefined,
    undefined,
    'DELETE',
  );
}

/** The session templates of the host (docs/api.md#session-templates). */
export function fetchTemplates(signal?: AbortSignal) {
  return call('/api/templates', TemplateListResponseSchema, undefined, signal);
}

export function postTemplate(body: TemplateBody) {
  return call('/api/templates', TemplateSchema, body);
}

export function putTemplate(id: string, body: TemplateBody) {
  return call(`/api/templates/${encodeURIComponent(id)}`, TemplateSchema, body, undefined, 'PUT');
}

/** Removes a template; the answer is the list that remains. */
export function deleteTemplate(id: string) {
  return call(
    `/api/templates/${encodeURIComponent(id)}`,
    TemplateListResponseSchema,
    undefined,
    undefined,
    'DELETE',
  );
}

/** Starts a session from a template; the answer comes when the agent has created it, like `postNewSession`. */
export function postTemplateStart(
  id: string,
  request: {
    values: Record<string, string>;
    deviceId?: string;
    projectPath?: string;
    promptId?: string;
  },
) {
  const { promptId, ...rest } = request;
  return call(`/api/templates/${encodeURIComponent(id)}/start`, TemplateStartResultSchema, {
    ...rest,
    promptId: promptId ?? crypto.randomUUID(),
  });
}

/** Every template as an export file, machines by name. */
export function fetchTemplateExport() {
  return call('/api/templates/export', TemplateFileSchema);
}

/** Imports a file, or with `dryRun` says what would be added. */
export function postTemplateImport(request: TemplateImportRequest) {
  return call('/api/templates/import', TemplateImportResultSchema, request);
}

/** The audit log (docs/api.md#audit-log): a page of records; `query` holds the filters, `after` and `limit`. */
export function fetchAuditEvents(query: Record<string, string>, signal?: AbortSignal) {
  const qs = new URLSearchParams(query);
  return call(`/api/audit/events?${qs}`, AuditEventsResponseSchema, undefined, signal);
}

/** Walks the whole chain; it takes as long as the log is long. */
export function fetchAuditVerify(signal?: AbortSignal) {
  return call('/api/audit/verify', AuditVerifyResponseSchema, undefined, signal);
}

/**
 * The export of the records a filter matches, read whole: the stream needs the host token in a header, so it is
 * fetched and offered as a file by the caller rather than linked.
 */
export async function fetchAuditExport(
  query: Record<string, string>,
  format: 'jsonl' | 'csv',
  signal?: AbortSignal,
): Promise<Blob> {
  const qs = new URLSearchParams({ ...query, format });
  const init: RequestInit = { headers: { Authorization: `Bearer ${getToken() ?? ''}` } };
  if (signal) init.signal = signal;
  const res = await fetch(`/api/audit/export?${qs}`, init);
  if (res.status === 401) throw new UnauthorizedError();
  if (!res.ok) {
    const payload: unknown = await res.json().catch(() => null);
    const error = safeParse(ErrorResponseSchema, payload);
    throw error.success
      ? new ApiError(res.status, error.output.code, error.output.reason)
      : new ApiError(res.status, null);
  }
  return res.blob();
}

/** The permission rules (docs/api.md#permission-rules). */
export function fetchPermissionRules(query: PermissionRuleListQuery = {}, signal?: AbortSignal) {
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) if (v) qs.set(k, v);
  const suffix = qs.size > 0 ? `?${qs}` : '';
  return call(
    `/api/permission-rules${suffix}`,
    PermissionRuleListResponseSchema,
    undefined,
    signal,
  );
}

/** Which machines have applied the latest rule revision. */
export function fetchRuleMachines(signal?: AbortSignal) {
  return call(
    '/api/permission-rules/machines',
    PermissionRuleMachinesResponseSchema,
    undefined,
    signal,
  );
}

/** Sends the rules to one machine again. */
export function pushRulesAgain(deviceId: string) {
  return call(
    `/api/permission-rules/machines/${encodeURIComponent(deviceId)}/push`,
    PermissionRuleMachinesResponseSchema,
    {},
  );
}

export function postPermissionRule(body: PermissionRuleBody) {
  return call('/api/permission-rules', PermissionRuleEntrySchema, body);
}

export function putPermissionRule(id: string, body: PermissionRulePut) {
  return call(
    `/api/permission-rules/${encodeURIComponent(id)}`,
    PermissionRuleEntrySchema,
    body,
    undefined,
    'PUT',
  );
}

export function deletePermissionRule(id: string) {
  return call(
    `/api/permission-rules/${encodeURIComponent(id)}`,
    PermissionRuleListResponseSchema,
    undefined,
    undefined,
    'DELETE',
  );
}

/** The API tokens: the scoped ones, never their secrets. */
export function fetchTokens(signal?: AbortSignal) {
  return call('/api/tokens', TokenListResponseSchema, undefined, signal);
}

/** Issues a token. The answer carries its secret, which is shown once and kept nowhere. */
export function postToken(body: TokenCreateRequest) {
  return call('/api/tokens', TokenCreateResponseSchema, body);
}

/** Revokes a token at once. */
export function deleteToken(id: string) {
  return call(
    `/api/tokens/${encodeURIComponent(id)}`,
    TokenRevokeResponseSchema,
    undefined,
    undefined,
    'DELETE',
  );
}

/** What one session may do to other sessions (ADR 0034). */
export function fetchSessionMcp(sessionId: string, signal?: AbortSignal) {
  return call(
    `/api/sessions/${encodeURIComponent(sessionId)}/mcp`,
    SessionMcpResponseSchema,
    undefined,
    signal,
  );
}

export function putSessionMcp(sessionId: string, settings: SessionMcpSettings) {
  return call(
    `/api/sessions/${encodeURIComponent(sessionId)}/mcp`,
    SessionMcpResponseSchema,
    { settings },
    undefined,
    'PUT',
  );
}

/** The global default and the project defaults for sessions that begin afterwards. */
export function fetchMcpDefaults(signal?: AbortSignal) {
  return call('/api/mcp/defaults', SessionMcpDefaultsResponseSchema, undefined, signal);
}

/** Sets the global default, or one project's when `project` is given. */
export function putMcpDefault(
  settings: SessionMcpSettings,
  project?: { deviceId: string; path: string },
) {
  return call(
    '/api/mcp/defaults',
    SessionMcpOkResponseSchema,
    { settings, ...(project ?? {}) },
    undefined,
    'PUT',
  );
}

export function deleteMcpDefault(project: { deviceId: string; path: string }) {
  const q = new URLSearchParams(project);
  return call(
    `/api/mcp/defaults?${q.toString()}`,
    SessionMcpOkResponseSchema,
    undefined,
    undefined,
    'DELETE',
  );
}

/** The hand-off requests of a session, newest first (ADR 0034). */
export function fetchHandoffRequests(sessionId: string, signal?: AbortSignal) {
  return call(
    `/api/sessions/${encodeURIComponent(sessionId)}/handoff-requests`,
    SessionHandoffRequestListSchema,
    undefined,
    signal,
  );
}

/** Approves or declines a session's request to be handed off. */
export function postHandoffRequestDecision(requestId: string, approve: boolean) {
  return call(
    `/api/handoff-requests/${encodeURIComponent(requestId)}/${approve ? 'approve' : 'decline'}`,
    SessionHandoffRequestSchema,
    {},
  );
}
