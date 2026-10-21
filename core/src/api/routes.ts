/**
 * The REST route table (docs/spec.md#data-model). Each route declares its method, path, authentication,
 * body limit, request and response schemas from shared/src/rest/ and its handler; router.ts does the rest.
 */
import {
  ActionDecideRequestSchema,
  ActionDecideResponseSchema,
  ActionInspectRequestSchema,
  ActionInspectResponseSchema,
  AttachmentResultSchema,
  BlockParamsSchema,
  BUILTIN_COMMANDS,
  type BudgetBody,
  BudgetBodySchema,
  BudgetListResponseSchema,
  BudgetSchema,
  CommandsResultSchema,
  ConfigRequestSchema,
  DeleteSessionRequestSchema,
  DeleteSessionResponseSchema,
  DeletionPreviewSchema,
  DeviceCloneQuerySchema,
  DeviceCloneResultSchema,
  DeviceCommandsQuerySchema,
  DeviceDirsQuerySchema,
  DeviceDirsResultSchema,
  DeviceLabelsRequestSchema,
  DeviceLabelsResultSchema,
  DeviceListResponseSchema,
  DeviceMintRequestSchema,
  DeviceMintResultSchema,
  DeviceProjectsAddRequestSchema,
  DeviceProjectsRemoveRequestSchema,
  DeviceProjectsResultSchema,
  DeviceProvidersResultSchema,
  DeviceProvidersUpdateRequestSchema,
  DeviceResourcesResultSchema,
  DeviceSessionsImportRequestSchema,
  DeviceSessionsRemoveRequestSchema,
  type Entry,
  FreshPermissionRequestSchema,
  FullBlockSchema,
  findProvider,
  GlobalSearchQuerySchema,
  GlobalSearchResultSchema,
  HandoffCancelResultSchema,
  HandoffRequestSchema,
  HandoffResultSchema,
  HealthResponseSchema,
  LinesPageSchema,
  LinesQuerySchema,
  type LiveSessionMeta,
  LiveSessionMetaSchema,
  MAX_BUDGETS,
  MAX_NOTIFY_PROJECTS,
  MAX_RESOURCE_THRESHOLD_MACHINES,
  MAX_TITLE_OVERRIDE_CHARS,
  MAX_USAGE_PRICES,
  MAX_WEBHOOKS,
  NewSessionRequestSchema,
  NewSessionResultSchema,
  NotifyPreferencesSchema,
  PermissionRequestSchema,
  PROTOCOL_VERSION,
  PromptImageSchema,
  PromptRequestSchema,
  PromptResultSchema,
  type Provider,
  PushKeyResponseSchema,
  PushSubscribeRequestSchema,
  type PushSubscriptionInfo,
  PushSubscriptionInfoSchema,
  PushSubscriptionListResponseSchema,
  PushUnsubscribeRequestSchema,
  QueueAddRequestSchema,
  QueueEditRequestSchema,
  QueueOrderRequestSchema,
  QueueSchema,
  QueueSteerRequestSchema,
  QueueVersionQuerySchema,
  ResourceThresholdsSchema,
  type RuntimeCapabilities,
  RuntimeCommandResultSchema,
  SearchResultSchema,
  SessionListResponseSchema,
  type SessionMeta,
  SessionPatchRequestSchema,
  SessionPortsResultSchema,
  SessionSearchQuerySchema,
  SessionUsageSchema,
  SnapshotChangesSchema,
  SnapshotDiffSchema,
  SteerRequestSchema,
  SteerResultSchema,
  safeParse,
  TerminalCloseResultSchema,
  TerminalInfoSchema,
  TerminalOpenRequestSchema,
  TerminalStateSchema,
  UsagePricesBodySchema,
  UsagePricesSchema,
  UsageQuerySchema,
  UsageResponseSchema,
  undeclaredConfig,
  WebhookCreateRequestSchema,
  type WebhookInfo,
  WebhookInfoSchema,
  WebhookListResponseSchema,
  WebhookTestResponseSchema,
  WebhookUpdateRequestSchema,
  WorkspaceDiffQuerySchema,
  WorkspaceDiffSchema,
  WorkspaceFileQuerySchema,
  WorkspaceFileSchema,
  WorkspaceListingSchema,
  WsTicketResponseSchema,
} from '@hoplet/shared';
import corePackage from '../../package.json' with { type: 'json' };
import type { DeviceControlOutcome, QueueResult, SteerOutcome } from '../control/index.ts';
import { registerSecret } from '../log/index.ts';
import type { PushSubscriptionRecord, SessionStateRecord, Store } from '../store/index.ts';
import { ApiError, relayError } from './http.ts';
import { checkPromptImage } from './images.ts';
import { publicOrigin, repoNameOf } from './origin.ts';
import { blockOfLine, excerptOf, projectLine } from './projection/index.ts';
import { type ApiDeps, defineRoute, type Route } from './router.ts';

const MAX_PROMPT_CHARS = 8_000;
/** One uploaded image: 4 MiB as base64 and its media type (ADR 0017). */
const ATTACHMENT_BODY_LIMIT = 6 * 1024 * 1024;

/**
 * The capabilities of the provider the owner runs for a session: `undefined` while the owner is offline (the
 * prompt then answers `owner-offline`), `null` when the connected owner does not run that provider.
 */
function ownerCapabilities(
  deps: ApiDeps,
  owner: string,
  provider: Provider,
): RuntimeCapabilities | null | undefined {
  const device = deps.control.devices().find((d) => d.deviceId === owner);
  if (!device?.connected) return undefined;
  return findProvider(device.providers, provider)?.capabilities ?? null;
}

/** The session's owner device, also of one an agent just started, or the 404 when the session is unknown. */
function ownerOf(deps: ApiDeps, sessionId: string): string {
  const owner = deps.control.ownerOf(sessionId);
  if (!owner) throw new ApiError('session-not-found');
  return owner;
}

/** The most push subscriptions: one per browser of the owner. */
const MAX_PUSH_SUBSCRIPTIONS = 50;

/** A subscription as the API shows it: the host of its push service, never the endpoint (a credential). */
function pushInfo(record: PushSubscriptionRecord): PushSubscriptionInfo {
  let service = '';
  try {
    service = new URL(record.endpoint).host;
  } catch {
    // An endpoint the store accepted parses; nothing to show otherwise.
  }
  return {
    id: record.id,
    label: record.label,
    service,
    createdAt: record.createdAt,
    ...(record.lastDelivery ? { lastDelivery: record.lastDelivery } : {}),
  };
}

function webhookInfo(record: WebhookInfo): WebhookInfo {
  return {
    id: record.id,
    label: record.label,
    url: record.url,
    hasSecret: record.hasSecret,
    enabled: record.enabled,
    events: record.events,
    createdAt: record.createdAt,
    ...(record.lastDelivery ? { lastDelivery: record.lastDelivery } : {}),
  };
}

/** The vendor of the session's lines, or the 404 when the session is unknown. */
function providerOf(store: ApiDeps['store'], sessionId: string): Provider {
  const meta = store.getSession(sessionId);
  if (!meta) throw new ApiError('session-not-found');
  return meta.provider;
}

/** A message for the queue or a steer obeys the limits of a prompt: its length and the images its driver takes. */
function checkMessage(
  deps: ApiDeps,
  sessionId: string,
  owner: string,
  text: string,
  attachments: string[],
): void {
  if (text.length > MAX_PROMPT_CHARS) throw new ApiError('too-large');
  if (attachments.length === 0) return;
  const provider = deps.control.providerOf(sessionId);
  if (!provider) throw new ApiError('session-not-found');
  const caps = ownerCapabilities(deps, owner, provider);
  if (caps !== undefined && !caps?.images) {
    throw new ApiError('invalid-request', { reason: 'images' });
  }
}

function queueResult(result: QueueResult) {
  if (!result.ok) throw new ApiError(result.error === 'full' ? 'queue-full' : 'queue-conflict');
  return result.queue;
}

function steerResult(outcome: SteerOutcome, steerId: string) {
  if (outcome.ok) return { ok: true as const, steerId };
  throw relayError(outcome);
}

/** The index entry plus whether the owner agent is connected. */
function withLiveness(
  { store, control }: ApiDeps,
  meta: SessionMeta,
  state: SessionStateRecord = store.sessionState(meta.sessionId) ?? {},
): LiveSessionMeta {
  const owner = store.ownerDeviceId(meta.sessionId);
  const links = store.continuationLinks(meta.sessionId);
  // The title the list shows; empty while the session is not in the index, which the client shows as untitled.
  const end = (sessionId: string) => ({
    sessionId,
    title:
      store.sessionState(sessionId)?.titleOverride?.trim() ||
      store.getSession(sessionId)?.title ||
      '',
  });
  const configurationId = store.sessionConfiguration(meta.sessionId);
  const configuration = configurationId
    ? {
        id: configurationId,
        name:
          findProvider(
            control.devices().find((d) => d.deviceId === owner)?.providers ?? [],
            meta.provider,
            configurationId,
          )?.configuration.name ?? configurationId,
      }
    : undefined;
  return {
    ...(links.continuedIn ? { continuedIn: end(links.continuedIn) } : {}),
    ...(links.continuedFrom ? { continuedFrom: end(links.continuedFrom) } : {}),
    ...meta,
    ...state,
    ...(configuration ? { configuration } : {}),
    ownerConnected: !!owner && control.ownerOnline(owner),
    runtime: control.runtimeOf(meta.sessionId),
    ...(owner ? { ownerDeviceId: owner } : {}),
  };
}

function budgetList({ store }: { store: Store }) {
  return { budgets: store.listBudgets(Date.now()), backfilling: store.usageBackfilling() };
}

/** What the schema cannot say: a token amount is a whole number. */
function checkBudget(body: BudgetBody): void {
  if ((body.scope.kind === 'global') !== (body.scope.value === '')) {
    throw new ApiError('invalid-request', { reason: 'scope' });
  }
  if (body.unit === 'tokens' && !Number.isInteger(body.amount)) {
    throw new ApiError('invalid-request', { reason: 'amount' });
  }
}

function deviceResult(deviceId: string, outcome: DeviceControlOutcome) {
  if (!outcome.ok) {
    throw outcome.error === 'rejected' && outcome.reason === 'cancelled'
      ? new ApiError('cancelled')
      : relayError(outcome);
  }
  return { deviceId, candidates: outcome.candidates, projects: outcome.projects };
}

export const routes: readonly Route[] = [
  defineRoute({
    method: 'POST',
    path: '/api/ws-tickets',
    auth: 'host',
    lane: 'control',
    response: WsTicketResponseSchema,
    handler: ({ tickets }) => tickets.mint(),
  }),
  defineRoute({
    method: 'GET',
    path: '/api/health',
    auth: 'host',
    lane: 'control',
    response: HealthResponseSchema,
    handler: ({ store }) => ({
      ok: true,
      sessions: store.sessionCount(),
      uptimeSec: Math.round(process.uptime()),
      coreVersion: corePackage.version,
      protocolVersion: PROTOCOL_VERSION,
    }),
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions',
    auth: 'host',
    lane: 'control',
    response: SessionListResponseSchema,
    handler: (d) => {
      const states = d.store.sessionStates();
      return {
        sessions: d.store
          .listSessions()
          .map((meta) => withLiveness(d, meta, states.get(meta.sessionId) ?? {})),
      };
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions',
    auth: 'host',
    lane: 'control',
    bodyLimit: 32 * 1024,
    body: NewSessionRequestSchema,
    response: NewSessionResultSchema,
    handler: async (d) => {
      if (d.body.text.length > MAX_PROMPT_CHARS) throw new ApiError('too-large');
      const outcome = await d.control.startSession(d.body);
      if (outcome.ok) {
        return { ok: true, sessionId: outcome.sessionId, promptId: outcome.promptId };
      }
      // `reason` names the option or field that was refused, or carries the agent's diagnostic text.
      throw new ApiError(outcome.error, {
        promptId: outcome.promptId,
        ...(outcome.reason !== undefined ? { reason: outcome.reason } : {}),
        ...(outcome.budget !== undefined ? { budget: outcome.budget } : {}),
      });
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId',
    auth: 'host',
    lane: 'control',
    response: LiveSessionMetaSchema,
    handler: (d) => {
      const meta = d.store.getSession(d.params.sessionId ?? '');
      if (!meta) throw new ApiError('session-not-found');
      return withLiveness(d, meta);
    },
  }),
  defineRoute({
    method: 'PATCH',
    path: '/api/sessions/:sessionId',
    auth: 'host',
    lane: 'control',
    bodyLimit: 4 * 1024,
    body: SessionPatchRequestSchema,
    response: LiveSessionMetaSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      const { title, pinned, archived } = d.body;
      if (title === undefined && pinned === undefined && archived === undefined)
        throw new ApiError('invalid-request');
      const meta = d.store.getSession(sessionId);
      const current = d.store.sessionState(sessionId);
      if (!meta || !current) throw new ApiError('session-not-found');
      // The length counts the trimmed title; a title that is blank or the vendor's own is no title of its own.
      const own = title?.trim();
      if (own !== undefined && own.length > MAX_TITLE_OVERRIDE_CHARS)
        throw new ApiError('invalid-request');
      const now = Date.now();
      const state = d.store.updateSessionState(sessionId, {
        ...(title !== undefined
          ? { titleOverride: own && own !== meta.title.trim() ? own : null }
          : {}),
        // Pinning a pinned session, or archiving an archived one, keeps the time it has.
        ...(pinned !== undefined ? { pinnedAt: pinned ? (current.pinnedAt ?? now) : null } : {}),
        ...(archived !== undefined
          ? { archivedAt: archived ? (current.archivedAt ?? now) : null }
          : {}),
      });
      if (!state) throw new ApiError('session-not-found');
      d.control.publish({ v: PROTOCOL_VERSION, type: 'session.updated', sessionId, ...state });
      return withLiveness(d, meta, state);
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/lines',
    auth: 'host',
    lane: 'bulk',
    query: LinesQuerySchema,
    response: LinesPageSchema,
    handler: async ({ store, params, query }) => {
      const sessionId = params.sessionId ?? '';
      const provider = providerOf(store, sessionId);
      const page = await store.readLines(sessionId, query.start, query.limit);
      if (!page) throw new ApiError('session-not-found');
      const entries: Entry[] = [];
      page.lines.forEach((line, i) => {
        const entry = projectLine(provider, line, page.start + i);
        if (entry) entries.push(entry);
      });
      return { sessionId, start: page.start, end: page.end, total: page.total, entries };
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/blocks/:line/:block',
    auth: 'host',
    lane: 'bulk',
    response: FullBlockSchema,
    handler: async ({ store, params }) => {
      const sessionId = params.sessionId ?? '';
      const at = safeParse(BlockParamsSchema, params);
      if (!at.success) throw new ApiError('invalid-request');
      const provider = providerOf(store, sessionId);
      const line = await store.readLine(sessionId, at.output.line);
      const block =
        line === null ? null : blockOfLine(provider, line, at.output.line, at.output.block);
      if (!block) throw new ApiError('not-found');
      return block;
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/search',
    auth: 'host',
    lane: 'bulk',
    query: SessionSearchQuerySchema,
    response: SearchResultSchema,
    handler: async ({ store, params, query }) => {
      const sessionId = params.sessionId ?? '';
      const provider = providerOf(store, sessionId);
      const result = await store.search(sessionId, query.q, query.start, query.limit);
      if (!result) throw new ApiError('session-not-found');
      return {
        ...result,
        hits: result.hits.map((h) => ({
          lineNo: h.lineNo,
          excerpt: excerptOf(provider, h.line, h.lineNo, query.q),
        })),
      };
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/search',
    auth: 'host',
    lane: 'bulk',
    query: GlobalSearchQuerySchema,
    response: GlobalSearchResultSchema,
    handler: async ({ store, query }) => {
      const found = await store.searchAll(query.q, query.start, query.limit);
      return {
        ...found,
        results: found.results.map((h) => ({
          sessionId: h.sessionId,
          lineNo: h.lineNo,
          excerpt: excerptOf(providerOf(store, h.sessionId), h.line, h.lineNo, query.q),
        })),
      };
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/prompt',
    auth: 'host',
    lane: 'control',
    bodyLimit: 32 * 1024,
    body: PromptRequestSchema,
    status: 202,
    response: PromptResultSchema,
    handler: async (d) => {
      const sessionId = d.params.sessionId ?? '';
      const owner = ownerOf(d, sessionId);
      if (d.body.text.length > MAX_PROMPT_CHARS) throw new ApiError('too-large');
      const attachments = d.body.attachments ?? [];
      const caps = ownerCapabilities(d, owner, providerOf(d.store, sessionId));
      if (caps !== undefined) {
        if (attachments.length > 0 && !caps?.images) {
          throw new ApiError('invalid-request', { reason: 'images' });
        }
      }
      const outcome = await d.control.prompt(
        sessionId,
        owner,
        d.body.text,
        d.body.promptId,
        attachments,
      );
      if (outcome.ok) return { ok: true, promptId: outcome.promptId };
      throw relayError(
        outcome,
        outcome.error === 'owner-offline' ? {} : { promptId: outcome.promptId },
      );
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/queue',
    auth: 'host',
    lane: 'control',
    response: QueueSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      ownerOf(d, sessionId);
      return d.control.queueOf(sessionId);
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/queue',
    auth: 'host',
    lane: 'control',
    bodyLimit: 32 * 1024,
    body: QueueAddRequestSchema,
    status: 201,
    response: QueueSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      const owner = ownerOf(d, sessionId);
      const attachments = d.body.attachments ?? [];
      checkMessage(d, sessionId, owner, d.body.text, attachments);
      return queueResult(
        d.control.queueAdd(sessionId, { id: d.body.id, text: d.body.text, attachments }),
      );
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/queue/order',
    auth: 'host',
    lane: 'control',
    bodyLimit: 8 * 1024,
    body: QueueOrderRequestSchema,
    response: QueueSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      ownerOf(d, sessionId);
      return queueResult(d.control.queueReorder(sessionId, d.body.ids));
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/queue/resume',
    auth: 'host',
    lane: 'control',
    response: QueueSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      ownerOf(d, sessionId);
      return d.control.queueResume(sessionId);
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/queue/clear',
    auth: 'host',
    lane: 'control',
    response: QueueSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      ownerOf(d, sessionId);
      return d.control.queueClear(sessionId);
    },
  }),
  defineRoute({
    method: 'PATCH',
    path: '/api/sessions/:sessionId/queue/:itemId',
    auth: 'host',
    lane: 'control',
    bodyLimit: 32 * 1024,
    body: QueueEditRequestSchema,
    response: QueueSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      const owner = ownerOf(d, sessionId);
      const attachments = d.body.attachments ?? [];
      checkMessage(d, sessionId, owner, d.body.text, attachments);
      return queueResult(
        d.control.queueEdit(sessionId, d.params.itemId ?? '', d.body.version, {
          text: d.body.text,
          attachments,
        }),
      );
    },
  }),
  defineRoute({
    method: 'DELETE',
    path: '/api/sessions/:sessionId/queue/:itemId',
    auth: 'host',
    lane: 'control',
    query: QueueVersionQuerySchema,
    response: QueueSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      ownerOf(d, sessionId);
      return queueResult(d.control.queueRemove(sessionId, d.params.itemId ?? '', d.query.version));
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/queue/:itemId/steer',
    auth: 'host',
    lane: 'control',
    bodyLimit: 1024,
    body: QueueSteerRequestSchema,
    response: QueueSchema,
    handler: async (d) => {
      const sessionId = d.params.sessionId ?? '';
      const owner = ownerOf(d, sessionId);
      const outcome = await d.control.queueSteer(
        sessionId,
        owner,
        d.params.itemId ?? '',
        d.body.version,
      );
      if (!outcome.ok) throw new ApiError('queue-conflict');
      if (!outcome.steer.ok) throw relayError(outcome.steer);
      return outcome.queue;
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/steer',
    auth: 'host',
    lane: 'control',
    bodyLimit: 32 * 1024,
    body: SteerRequestSchema,
    status: 202,
    response: SteerResultSchema,
    handler: async (d) => {
      const sessionId = d.params.sessionId ?? '';
      const owner = ownerOf(d, sessionId);
      const attachments = d.body.attachments ?? [];
      checkMessage(d, sessionId, owner, d.body.text, attachments);
      return steerResult(
        await d.control.steer(sessionId, owner, d.body.steerId, d.body.text, attachments),
        d.body.steerId,
      );
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/config',
    auth: 'host',
    lane: 'control',
    bodyLimit: 4096,
    body: ConfigRequestSchema,
    response: RuntimeCommandResultSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      const owner = ownerOf(d, sessionId);
      const online = ownerCapabilities(d, owner, providerOf(d.store, sessionId));
      if (online !== undefined) {
        // The session's own capabilities carry its values; before it reported, the provider's declare the options.
        const caps = d.control.runtimeOf(sessionId).capabilities ?? online;
        if (!caps || undeclaredConfig({ [d.body.configId]: d.body.value }, caps) !== null) {
          throw new ApiError('invalid-request', { reason: 'config' });
        }
      }
      const outcome = d.control.config(sessionId, owner, d.body.configId, d.body.value);
      if (outcome.ok) return { ok: true };
      throw new ApiError(outcome.error);
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/permission',
    auth: 'host',
    lane: 'control',
    bodyLimit: 40_000,
    body: PermissionRequestSchema,
    response: RuntimeCommandResultSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      const outcome = d.control.answerPermission(
        sessionId,
        ownerOf(d, sessionId),
        d.body.requestId,
        { decision: d.body.decision, message: d.body.message, mode: d.body.mode },
      );
      if (outcome.ok) return { ok: true };
      throw new ApiError(outcome.error);
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/permission/fresh',
    auth: 'host',
    lane: 'control',
    bodyLimit: 4096,
    body: FreshPermissionRequestSchema,
    response: NewSessionResultSchema,
    handler: async (d) => {
      const outcome = await d.control.approvePlanFresh({
        sessionId: d.params.sessionId ?? '',
        requestId: d.body.requestId,
        mode: d.body.mode,
        promptId: d.body.promptId,
      });
      if (outcome.ok) {
        return { ok: true, sessionId: outcome.sessionId, promptId: outcome.promptId };
      }
      throw new ApiError(outcome.error, {
        promptId: outcome.promptId,
        ...(outcome.reason !== undefined ? { reason: outcome.reason } : {}),
        ...(outcome.budget !== undefined ? { budget: outcome.budget } : {}),
      });
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/interrupt',
    auth: 'host',
    lane: 'control',
    response: RuntimeCommandResultSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      const outcome = d.control.interrupt(sessionId, ownerOf(d, sessionId));
      if (outcome.ok) return { ok: true };
      throw new ApiError(outcome.error);
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/stop',
    auth: 'host',
    lane: 'control',
    response: RuntimeCommandResultSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      const outcome = d.control.stop(sessionId, ownerOf(d, sessionId));
      if (outcome.ok) return { ok: true };
      throw new ApiError(outcome.error);
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/terminal',
    auth: 'host',
    lane: 'control',
    bodyLimit: 256,
    body: TerminalOpenRequestSchema,
    response: TerminalInfoSchema,
    handler: async (d) => {
      const sessionId = d.params.sessionId ?? '';
      const owner = ownerOf(d, sessionId);
      const outcome = await d.control.terminals.open(sessionId, owner, d.body.cols, d.body.rows);
      if (outcome.ok) return outcome.terminal;
      throw new ApiError(
        outcome.error,
        outcome.reason !== undefined ? { reason: outcome.reason } : {},
      );
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/terminal',
    auth: 'host',
    lane: 'control',
    response: TerminalStateSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      ownerOf(d, sessionId);
      return { terminal: d.control.terminals.ofSession(sessionId) };
    },
  }),
  defineRoute({
    method: 'DELETE',
    path: '/api/terminals/:terminalId',
    auth: 'host',
    lane: 'control',
    response: TerminalCloseResultSchema,
    handler: (d) => {
      if (!d.control.terminals.close(d.params.terminalId ?? '')) throw new ApiError('not-found');
      return { ok: true };
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/ports',
    auth: 'host',
    lane: 'control',
    response: SessionPortsResultSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      ownerOf(d, sessionId);
      return d.control.portsOf(sessionId);
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/deletion',
    auth: 'host',
    lane: 'control',
    response: DeletionPreviewSchema,
    handler: async (d) => {
      const outcome = await d.control.deletionPreview(d.params.sessionId ?? '');
      if (outcome.ok) return outcome.preview;
      throw relayError(outcome);
    },
  }),
  defineRoute({
    method: 'DELETE',
    path: '/api/sessions/:sessionId',
    auth: 'host',
    lane: 'control',
    bodyLimit: 1024,
    body: DeleteSessionRequestSchema,
    response: DeleteSessionResponseSchema,
    handler: async (d) => {
      const outcome = await d.control.deleteSession(d.params.sessionId ?? '', d.body);
      if (outcome.ok) return { steps: outcome.steps };
      if (outcome.error === 'delete-partial') {
        throw new ApiError('delete-partial', { steps: outcome.steps });
      }
      throw relayError(outcome);
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/workspace',
    auth: 'host',
    lane: 'control',
    response: WorkspaceListingSchema,
    handler: async (d) => {
      const sessionId = d.params.sessionId ?? '';
      const outcome = await d.control.workspace(sessionId, ownerOf(d, sessionId));
      if (outcome.ok) return outcome.listing;
      throw relayError(outcome);
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/workspace/file',
    auth: 'host',
    lane: 'control',
    query: WorkspaceFileQuerySchema,
    response: WorkspaceFileSchema,
    handler: async (d) => {
      const sessionId = d.params.sessionId ?? '';
      const outcome = await d.control.workspaceFile(
        sessionId,
        ownerOf(d, sessionId),
        d.query.path,
        d.query.at,
      );
      if (outcome.ok) return outcome.file;
      throw relayError(outcome);
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/workspace/diff',
    auth: 'host',
    lane: 'control',
    query: WorkspaceDiffQuerySchema,
    response: WorkspaceDiffSchema,
    handler: async (d) => {
      const sessionId = d.params.sessionId ?? '';
      const outcome = await d.control.workspaceDiff(sessionId, ownerOf(d, sessionId), d.query.path);
      if (outcome.ok) return outcome.diff;
      throw relayError(outcome);
    },
  }),
  // The stored snapshot answers whether or not the owner is online. Reading it decompresses the archive, so
  // it is bulk work like a history page.
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/snapshot/changes',
    auth: 'host',
    lane: 'bulk',
    response: SnapshotChangesSchema,
    handler: async ({ store, params }) => {
      const sessionId = params.sessionId ?? '';
      if (!store.getSession(sessionId)) throw new ApiError('session-not-found');
      const changes = await store.snapshotChanges(sessionId);
      if (!changes) throw new ApiError('no-snapshot');
      return changes;
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/snapshot/diff',
    auth: 'host',
    lane: 'bulk',
    query: WorkspaceDiffQuerySchema,
    response: SnapshotDiffSchema,
    handler: async ({ store, params, query }) => {
      const sessionId = params.sessionId ?? '';
      if (!store.getSession(sessionId)) throw new ApiError('session-not-found');
      const found = await store.snapshotDiff(sessionId, query.path);
      if (!found.ok) throw new ApiError(found.error);
      return found.diff;
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/handoff',
    auth: 'host',
    lane: 'control',
    bodyLimit: 8192,
    body: HandoffRequestSchema,
    response: HandoffResultSchema,
    handler: async (d) => {
      if (d.body.targetPath !== undefined && d.body.cloneInto !== undefined) {
        throw new ApiError('invalid-request', { reason: 'targetPath and cloneInto are exclusive' });
      }
      if (d.body.configuration !== undefined) {
        const provider = d.store.getSession(d.params.sessionId ?? '')?.provider;
        const target = d.control.devices().find((x) => x.deviceId === d.body.targetDeviceId);
        // A target that is offline or unknown is the handoff's own error; a connected one must offer the configuration.
        if (provider !== undefined && target?.connected) {
          const info = findProvider(target.providers, provider, d.body.configuration);
          if (!info?.configuration.enabled) {
            throw new ApiError('invalid-request', { reason: 'configuration' });
          }
        }
      }
      const outcome = await d.control.handoff({
        sessionId: d.params.sessionId ?? '',
        targetDeviceId: d.body.targetDeviceId,
        targetPath: d.body.targetPath,
        cloneInto: d.body.cloneInto,
        configuration: d.body.configuration,
      });
      if (outcome.ok) {
        return {
          ok: true,
          ownerDeviceId: outcome.ownerDeviceId,
          ownerEpoch: outcome.ownerEpoch,
          worktree: outcome.worktree,
          branch: outcome.branch,
          ...(outcome.snapshotAt !== undefined ? { snapshotAt: outcome.snapshotAt } : {}),
        };
      }
      // A handoff that cannot complete leaves the owner where it was.
      if (outcome.error === 'clone-failed') {
        throw new ApiError('clone-failed', {
          ...(outcome.reason !== undefined ? { reason: outcome.reason } : {}),
          ...(outcome.detail !== undefined ? { detail: outcome.detail } : {}),
        });
      }
      throw relayError(outcome);
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/sessions/:sessionId/handoff/cancel',
    auth: 'host',
    lane: 'control',
    response: HandoffCancelResultSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      if (!d.store.getSession(sessionId)) throw new ApiError('session-not-found');
      const outcome = d.control.cancelHandoff(sessionId);
      if (outcome.ok) return { ok: true };
      throw new ApiError(outcome.error);
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/devices',
    auth: 'host',
    lane: 'control',
    response: DeviceListResponseSchema,
    handler: ({ control }) => ({ devices: control.devices() }),
  }),
  defineRoute({
    method: 'PUT',
    path: '/api/devices/:deviceId/labels',
    auth: 'host',
    lane: 'control',
    bodyLimit: 16 * 1024,
    body: DeviceLabelsRequestSchema,
    response: DeviceLabelsResultSchema,
    handler: ({ store, params, body }) => {
      const labels = [...new Set(body.labels)];
      if (!store.setDeviceLabels(params.deviceId ?? '', labels)) throw new ApiError('not-found');
      return { labels };
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/devices/:deviceId/resources',
    auth: 'host',
    lane: 'control',
    response: DeviceResourcesResultSchema,
    handler: ({ store, control, params }) => {
      const deviceId = params.deviceId ?? '';
      if (!control.devices().some((d) => d.deviceId === deviceId)) throw new ApiError('not-found');
      const { samples, sessions, history } = control.resourcesOf(deviceId);
      const latest = samples.at(-1);
      return {
        samples,
        sessions: sessions
          .map((s) => {
            const meta = store.getSession(s.sessionId);
            return {
              sessionId: s.sessionId,
              title: meta
                ? store.sessionState(s.sessionId)?.titleOverride?.trim() || meta.title
                : null,
              projectPath: meta?.projectPath ?? null,
              cpu: s.cpu,
              rss: s.rss,
              children: s.children,
              history: history.get(s.sessionId) ?? [],
            };
          })
          .sort((a, b) => b.rss - a.rss),
        ...(latest ? { at: latest.at } : {}),
      };
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/devices',
    auth: 'host',
    lane: 'control',
    bodyLimit: 4096,
    body: DeviceMintRequestSchema,
    response: DeviceMintResultSchema,
    handler: ({ store, body }) => store.mintDevice(body.name, body.deviceId),
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/commands',
    auth: 'host',
    lane: 'control',
    response: CommandsResultSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      ownerOf(d, sessionId);
      const commands = d.control.commandsOf(sessionId);
      return commands
        ? { commands, source: 'session' as const }
        : { commands: [...BUILTIN_COMMANDS], source: 'builtin' as const };
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/devices/:deviceId/commands',
    auth: 'host',
    lane: 'control',
    query: DeviceCommandsQuerySchema,
    response: CommandsResultSchema,
    handler: ({ store, params, query }) => {
      const commands = store.directoryCommands(params.deviceId ?? '', query.cwd);
      return commands
        ? { commands, source: 'directory' as const }
        : { commands: [...BUILTIN_COMMANDS], source: 'builtin' as const };
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/devices/:deviceId/dirs',
    auth: 'host',
    lane: 'control',
    query: DeviceDirsQuerySchema,
    response: DeviceDirsResultSchema,
    handler: async ({ control, params, query }) => {
      const outcome = await control.listDirs(params.deviceId ?? '', query.path ?? '~');
      if (outcome.ok) return outcome.listing;
      if (outcome.error === 'rejected') {
        throw new ApiError('invalid-request', { reason: outcome.reason ?? 'rejected' });
      }
      throw relayError(outcome);
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/devices/:deviceId/attachments',
    auth: 'host',
    lane: 'bulk',
    bodyLimit: ATTACHMENT_BODY_LIMIT,
    body: PromptImageSchema,
    response: AttachmentResultSchema,
    handler: async (d) => {
      const deviceId = d.params.deviceId ?? '';
      checkPromptImage(d.body);
      const device = d.control.devices().find((x) => x.deviceId === deviceId);
      if (!device?.connected) throw new ApiError('target-offline');
      if (!device.providers.some((p) => p.capabilities.images === true)) {
        throw new ApiError('invalid-request', { reason: 'images' });
      }
      const outcome = await d.control.putAttachment(deviceId, d.body);
      if (outcome.ok) return { attachmentId: outcome.attachmentId };
      if (outcome.error === 'rejected') {
        throw new ApiError('invalid-request', { reason: outcome.reason ?? 'images' });
      }
      throw relayError(outcome);
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/devices/:deviceId/clone',
    auth: 'host',
    lane: 'control',
    query: DeviceCloneQuerySchema,
    response: DeviceCloneResultSchema,
    handler: async ({ control, params, query }) => {
      const deviceId = params.deviceId ?? '';
      const outcome = await control.findClone(deviceId, query.origin);
      if (!outcome.ok) throw relayError(outcome);
      if (outcome.path !== null) return { deviceId, path: outcome.path };
      // No clone: say whether one can be made, and where (ADR 0024).
      const check = await control.checkClone(deviceId, query.origin, query.cloneInto);
      if (!check.ok) throw relayError(check);
      return {
        deviceId,
        path: null,
        clone: {
          origin: publicOrigin(query.origin),
          repoName: repoNameOf(query.origin),
          cloneInto: check.cloneInto,
          reason: check.reason,
          ...(check.detail !== undefined ? { detail: check.detail } : {}),
        },
      };
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/devices/:deviceId/projects',
    auth: 'host',
    lane: 'control',
    response: DeviceProjectsResultSchema,
    handler: async ({ control, params }) => {
      const deviceId = params.deviceId ?? '';
      return deviceResult(deviceId, await control.listProjects(deviceId));
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/devices/:deviceId/projects',
    auth: 'host',
    lane: 'control',
    bodyLimit: 16 * 1024,
    body: DeviceProjectsAddRequestSchema,
    response: DeviceProjectsResultSchema,
    handler: async ({ control, params, body }) => {
      const deviceId = params.deviceId ?? '';
      return deviceResult(deviceId, await control.addProject(deviceId, body.path));
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/devices/:deviceId/providers',
    auth: 'host',
    lane: 'control',
    bodyLimit: 16 * 1024,
    body: DeviceProvidersUpdateRequestSchema,
    response: DeviceProvidersResultSchema,
    handler: async ({ control, params, body }) => {
      const deviceId = params.deviceId ?? '';
      const device = control.devices().find((x) => x.deviceId === deviceId);
      if (!device) throw new ApiError('not-found');
      if (!device.connected) throw new ApiError('owner-offline');
      const info = device.providers.find((p) => p.configuration.id === body.id);
      if (!info) throw new ApiError('invalid-request', { reason: 'configuration' });
      // The defaults are checked against what the configuration's driver declares (ADR 0018).
      if (
        body.defaults !== undefined &&
        undeclaredConfig(body.defaults, info.capabilities) !== null
      ) {
        throw new ApiError('invalid-request', { reason: 'config' });
      }
      const outcome = await control.updateProvider(deviceId, body);
      if (!outcome.ok) throw relayError(outcome);
      return { deviceId, providers: outcome.providers };
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/devices/:deviceId/projects/remove',
    auth: 'host',
    lane: 'control',
    bodyLimit: 16 * 1024,
    body: DeviceProjectsRemoveRequestSchema,
    response: DeviceProjectsResultSchema,
    handler: async ({ control, params, body }) => {
      const deviceId = params.deviceId ?? '';
      return deviceResult(deviceId, await control.removeProject(deviceId, body.path));
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/devices/:deviceId/sessions/import',
    auth: 'host',
    lane: 'control',
    bodyLimit: 64 * 1024,
    body: DeviceSessionsImportRequestSchema,
    response: DeviceProjectsResultSchema,
    handler: async ({ control, params, body }) => {
      const deviceId = params.deviceId ?? '';
      return deviceResult(
        deviceId,
        await control.importSessions(deviceId, body.path, body.sessionIds),
      );
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/devices/:deviceId/sessions/remove',
    auth: 'host',
    lane: 'control',
    bodyLimit: 64 * 1024,
    body: DeviceSessionsRemoveRequestSchema,
    response: DeviceProjectsResultSchema,
    handler: async ({ control, params, body }) => {
      const deviceId = params.deviceId ?? '';
      return deviceResult(
        deviceId,
        await control.removeSessions(deviceId, body.path, body.sessionIds),
      );
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/notify/preferences',
    auth: 'host',
    lane: 'control',
    response: NotifyPreferencesSchema,
    handler: ({ store }) => store.notifyPreferences(),
  }),
  defineRoute({
    method: 'PUT',
    path: '/api/notify/preferences',
    auth: 'host',
    lane: 'control',
    bodyLimit: 256 * 1024,
    body: NotifyPreferencesSchema,
    response: NotifyPreferencesSchema,
    handler: ({ store, body }) => {
      if (Object.keys(body.projects).length > MAX_NOTIFY_PROJECTS) {
        throw new ApiError('too-large');
      }
      store.setNotifyPreferences(body);
      return store.notifyPreferences();
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/resources/thresholds',
    auth: 'host',
    lane: 'control',
    response: ResourceThresholdsSchema,
    handler: ({ store }) => store.resourceThresholds(),
  }),
  defineRoute({
    method: 'PUT',
    path: '/api/resources/thresholds',
    auth: 'host',
    lane: 'control',
    bodyLimit: 64 * 1024,
    body: ResourceThresholdsSchema,
    response: ResourceThresholdsSchema,
    handler: ({ store, body }) => {
      if (Object.keys(body.machines).length > MAX_RESOURCE_THRESHOLD_MACHINES) {
        throw new ApiError('too-large');
      }
      store.setResourceThresholds(body);
      return store.resourceThresholds();
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/usage',
    auth: 'host',
    lane: 'bulk',
    query: UsageQuerySchema,
    response: UsageResponseSchema,
    handler: ({ store, query }) => store.usage(query),
  }),
  defineRoute({
    method: 'GET',
    path: '/api/sessions/:sessionId/usage',
    auth: 'host',
    lane: 'bulk',
    response: SessionUsageSchema,
    handler: ({ store, params }) => {
      const usage = store.sessionUsage(params.sessionId ?? '');
      if (!usage) throw new ApiError('session-not-found');
      return usage;
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/usage/prices',
    auth: 'host',
    lane: 'control',
    response: UsagePricesSchema,
    handler: ({ store }) => store.usagePrices(),
  }),
  defineRoute({
    method: 'PUT',
    path: '/api/usage/prices',
    auth: 'host',
    lane: 'control',
    bodyLimit: 256 * 1024,
    body: UsagePricesBodySchema,
    response: UsagePricesSchema,
    handler: ({ store, body }) => {
      if (Object.keys(body.overrides).length > MAX_USAGE_PRICES) throw new ApiError('too-large');
      store.setUsagePrices(body);
      return store.usagePrices();
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/budgets',
    auth: 'host',
    lane: 'control',
    response: BudgetListResponseSchema,
    handler: budgetList,
  }),
  defineRoute({
    method: 'POST',
    path: '/api/budgets',
    auth: 'host',
    lane: 'control',
    bodyLimit: 16 * 1024,
    body: BudgetBodySchema,
    status: 201,
    response: BudgetSchema,
    handler: ({ store, notify, body }) => {
      if (store.budgetCount() >= MAX_BUDGETS) throw new ApiError('too-large');
      checkBudget(body);
      const created = store.createBudget(body, Date.now());
      notify.budgetsChanged();
      return created;
    },
  }),
  defineRoute({
    method: 'PUT',
    path: '/api/budgets/:id',
    auth: 'host',
    lane: 'control',
    bodyLimit: 16 * 1024,
    body: BudgetBodySchema,
    response: BudgetSchema,
    handler: ({ store, notify, params, body }) => {
      checkBudget(body);
      const updated = store.updateBudget(params.id ?? '', body, Date.now());
      if (!updated) throw new ApiError('not-found');
      notify.budgetsChanged();
      return updated;
    },
  }),
  defineRoute({
    method: 'DELETE',
    path: '/api/budgets/:id',
    auth: 'host',
    lane: 'control',
    response: BudgetListResponseSchema,
    handler: (d) => {
      if (!d.store.deleteBudget(d.params.id ?? '')) throw new ApiError('not-found');
      return budgetList(d);
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/budgets/:id/allow',
    auth: 'host',
    lane: 'control',
    response: BudgetSchema,
    handler: ({ store, params }) => {
      const budget = store.getBudget(params.id ?? '', Date.now());
      if (!budget) throw new ApiError('not-found');
      // A budget that only warns has no pause to lift.
      if (budget.onLimit !== 'pause') throw new ApiError('invalid-request', { reason: 'onLimit' });
      const allowed = store.allowBudget(budget.id, Date.now());
      if (!allowed) throw new ApiError('not-found');
      return allowed;
    },
  }),
  defineRoute({
    method: 'DELETE',
    path: '/api/budgets/:id/allow',
    auth: 'host',
    lane: 'control',
    response: BudgetSchema,
    handler: ({ store, params }) => {
      const budget = store.getBudget(params.id ?? '', Date.now());
      if (!budget) throw new ApiError('not-found');
      if (budget.onLimit !== 'pause') throw new ApiError('invalid-request', { reason: 'onLimit' });
      const withdrawn = store.withdrawBudgetAllowance(budget.id, Date.now());
      if (!withdrawn) throw new ApiError('not-found');
      return withdrawn;
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/notify/push/key',
    auth: 'host',
    lane: 'control',
    response: PushKeyResponseSchema,
    handler: ({ notify }) => ({ publicKey: notify.pushKey() }),
  }),
  defineRoute({
    method: 'GET',
    path: '/api/notify/push/subscriptions',
    auth: 'host',
    lane: 'control',
    response: PushSubscriptionListResponseSchema,
    handler: ({ store }) => ({ subscriptions: store.listPushSubscriptions().map(pushInfo) }),
  }),
  defineRoute({
    method: 'POST',
    path: '/api/notify/push/subscriptions',
    auth: 'host',
    lane: 'control',
    bodyLimit: 8 * 1024,
    body: PushSubscribeRequestSchema,
    status: 201,
    response: PushSubscriptionInfoSchema,
    handler: ({ store, body }) => {
      const known = store.listPushSubscriptions();
      if (
        known.length >= MAX_PUSH_SUBSCRIPTIONS &&
        !known.some((s) => s.endpoint === body.endpoint)
      ) {
        throw new ApiError('too-large');
      }
      return pushInfo(
        store.addPushSubscription(
          {
            endpoint: body.endpoint,
            p256dh: body.keys.p256dh,
            auth: body.keys.auth,
            label: body.label,
          },
          Date.now(),
        ),
      );
    },
  }),
  defineRoute({
    method: 'DELETE',
    path: '/api/notify/push/subscriptions/:id',
    auth: 'host',
    lane: 'control',
    response: PushSubscriptionListResponseSchema,
    handler: ({ store, params }) => {
      if (!store.deletePushSubscription(params.id ?? '')) throw new ApiError('not-found');
      return { subscriptions: store.listPushSubscriptions().map(pushInfo) };
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/notify/push/unsubscribe',
    auth: 'host',
    lane: 'control',
    bodyLimit: 4 * 1024,
    body: PushUnsubscribeRequestSchema,
    response: PushSubscriptionListResponseSchema,
    handler: ({ store, body }) => {
      store.deletePushByEndpoint(body.endpoint);
      return { subscriptions: store.listPushSubscriptions().map(pushInfo) };
    },
  }),
  defineRoute({
    method: 'GET',
    path: '/api/notify/webhooks',
    auth: 'host',
    lane: 'control',
    response: WebhookListResponseSchema,
    handler: ({ store }) => ({ webhooks: store.listWebhooks().map(webhookInfo) }),
  }),
  defineRoute({
    method: 'POST',
    path: '/api/notify/webhooks',
    auth: 'host',
    lane: 'control',
    bodyLimit: 8 * 1024,
    body: WebhookCreateRequestSchema,
    status: 201,
    response: WebhookInfoSchema,
    handler: ({ store, body }) => {
      if (store.listWebhooks().length >= MAX_WEBHOOKS) throw new ApiError('too-large');
      if (body.secret) registerSecret(body.secret);
      return webhookInfo(
        store.addWebhook(
          {
            url: body.url,
            label: body.label,
            ...(body.secret ? { secret: body.secret } : {}),
            ...(body.events ? { events: body.events } : {}),
          },
          Date.now(),
        ),
      );
    },
  }),
  defineRoute({
    method: 'PATCH',
    path: '/api/notify/webhooks/:id',
    auth: 'host',
    lane: 'control',
    bodyLimit: 8 * 1024,
    body: WebhookUpdateRequestSchema,
    response: WebhookInfoSchema,
    handler: ({ store, params, body }) => {
      if (body.secret) registerSecret(body.secret);
      const updated = store.updateWebhook(params.id ?? '', {
        ...(body.url === undefined ? {} : { url: body.url }),
        ...(body.label === undefined ? {} : { label: body.label }),
        ...(body.enabled === undefined ? {} : { enabled: body.enabled }),
        ...(body.events === undefined ? {} : { events: body.events }),
        ...(body.secret === undefined ? {} : { secret: body.secret }),
      });
      if (!updated) throw new ApiError('not-found');
      return webhookInfo(updated);
    },
  }),
  defineRoute({
    method: 'DELETE',
    path: '/api/notify/webhooks/:id',
    auth: 'host',
    lane: 'control',
    response: WebhookListResponseSchema,
    handler: ({ store, params }) => {
      if (!store.deleteWebhook(params.id ?? '')) throw new ApiError('not-found');
      return { webhooks: store.listWebhooks().map(webhookInfo) };
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/notify/webhooks/:id/test',
    auth: 'host',
    lane: 'control',
    response: WebhookTestResponseSchema,
    handler: async ({ notify, params }) => {
      const result = await notify.testWebhook(params.id ?? '');
      if (!result) throw new ApiError('not-found');
      return { webhook: webhookInfo(result.webhook), delivery: result.delivery };
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/action/inspect',
    auth: 'token',
    lane: 'control',
    bodyLimit: 2 * 1024,
    body: ActionInspectRequestSchema,
    response: ActionInspectResponseSchema,
    handler: ({ notify, body }) => {
      const view = notify.inspect(body.token);
      if (!view) throw new ApiError('invalid-token');
      return view;
    },
  }),
  defineRoute({
    method: 'POST',
    path: '/api/action/decide',
    auth: 'token',
    lane: 'control',
    bodyLimit: 2 * 1024,
    body: ActionDecideRequestSchema,
    response: ActionDecideResponseSchema,
    handler: ({ notify, body }) => {
      const result = notify.decide(body.token, body.decision);
      if ('error' in result) throw new ApiError(result.error);
      return result;
    },
  }),
];
