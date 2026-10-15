/**
 * The REST route table (docs/spec.md#data-model). Each route declares its method, path, authentication,
 * body limit, request and response schemas from shared/src/rest/ and its handler; router.ts does the rest.
 */
import {
  AttachmentResultSchema,
  BlockParamsSchema,
  ConfigRequestSchema,
  DeviceCloneQuerySchema,
  DeviceCloneResultSchema,
  DeviceDirsQuerySchema,
  DeviceDirsResultSchema,
  DeviceListResponseSchema,
  DeviceMintRequestSchema,
  DeviceMintResultSchema,
  DeviceProjectsAddRequestSchema,
  DeviceProjectsRemoveRequestSchema,
  DeviceProjectsResultSchema,
  DeviceSessionsImportRequestSchema,
  DeviceSessionsRemoveRequestSchema,
  type Entry,
  FullBlockSchema,
  GlobalSearchQuerySchema,
  GlobalSearchResultSchema,
  HandoffRequestSchema,
  HandoffResultSchema,
  HealthResponseSchema,
  LinesPageSchema,
  LinesQuerySchema,
  type LiveSessionMeta,
  LiveSessionMetaSchema,
  NewSessionRequestSchema,
  NewSessionResultSchema,
  PermissionRequestSchema,
  PROTOCOL_VERSION,
  PromptImageSchema,
  PromptRequestSchema,
  PromptResultSchema,
  type Provider,
  type RuntimeCapabilities,
  RuntimeCommandResultSchema,
  SearchResultSchema,
  SessionListResponseSchema,
  type SessionMeta,
  SessionSearchQuerySchema,
  SnapshotChangesSchema,
  SnapshotDiffSchema,
  safeParse,
  undeclaredConfig,
  WorkspaceDiffQuerySchema,
  WorkspaceDiffSchema,
  WorkspaceFileQuerySchema,
  WorkspaceFileSchema,
  WorkspaceListingSchema,
  WsTicketResponseSchema,
} from '@hoplet/shared';
import corePackage from '../../package.json' with { type: 'json' };
import type { DeviceControlOutcome } from '../control/index.ts';
import { ApiError, relayError } from './http.ts';
import { checkPromptImage } from './images.ts';
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
  return device.providers.find((p) => p.provider === provider)?.capabilities ?? null;
}

/** The session's owner device, or the 404 when the session is unknown. */
function ownerOf(deps: ApiDeps, sessionId: string): string {
  const owner = deps.store.ownerDeviceId(sessionId);
  if (!owner) throw new ApiError('session-not-found');
  return owner;
}

/** The vendor of the session's lines, or the 404 when the session is unknown. */
function providerOf(store: ApiDeps['store'], sessionId: string): Provider {
  const meta = store.getSession(sessionId);
  if (!meta) throw new ApiError('session-not-found');
  return meta.provider;
}

/** The index entry plus whether the owner agent is connected. */
function withLiveness({ store, control }: ApiDeps, meta: SessionMeta): LiveSessionMeta {
  const owner = store.ownerDeviceId(meta.sessionId);
  return {
    ...meta,
    ownerConnected: !!owner && control.ownerOnline(owner),
    runtime: control.runtimeOf(meta.sessionId),
    ...(owner ? { ownerDeviceId: owner } : {}),
  };
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
    handler: (d) => ({ sessions: d.store.listSessions().map((meta) => withLiveness(d, meta)) }),
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
    bodyLimit: 4096,
    body: PermissionRequestSchema,
    response: RuntimeCommandResultSchema,
    handler: (d) => {
      const sessionId = d.params.sessionId ?? '';
      const outcome = d.control.answerPermission(
        sessionId,
        ownerOf(d, sessionId),
        d.body.requestId,
        d.body.decision,
      );
      if (outcome.ok) return { ok: true };
      throw new ApiError(outcome.error);
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
      const outcome = await d.control.handoff({
        sessionId: d.params.sessionId ?? '',
        targetDeviceId: d.body.targetDeviceId,
        targetPath: d.body.targetPath,
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
      throw relayError(outcome);
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
      if (outcome.ok) return { deviceId, path: outcome.path };
      throw relayError(outcome);
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
];
