import {
  AttachmentResultSchema,
  DeviceCloneResultSchema,
  DeviceDirsResultSchema,
  DeviceListResponseSchema,
  DeviceMintResultSchema,
  DeviceProjectsResultSchema,
  ErrorResponseSchema,
  FullBlockSchema,
  type GenericSchema,
  GlobalSearchResultSchema,
  HandoffResultSchema,
  type InferOutput,
  LinesPageSchema,
  NewSessionResultSchema,
  type PermissionDecision,
  type PromptImage,
  PromptResultSchema,
  type ProviderInfo,
  RuntimeCommandResultSchema,
  SessionListResponseSchema,
  SnapshotChangesSchema,
  SnapshotDiffSchema,
  type StartOptions,
  safeParse,
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
): Promise<InferOutput<S>> {
  const headers: Record<string, string> = { Authorization: `Bearer ${getToken() ?? ''}` };
  const init: RequestInit = { headers };
  if (signal) init.signal = signal;
  if (body !== undefined) {
    init.method = 'POST';
    headers['content-type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  const res = await fetch(path, init);
  if (res.status === 401) throw new UnauthorizedError();
  const type = res.headers.get('content-type') ?? '';
  if (!type.includes('application/json')) {
    if (!res.ok) throw new ApiError(res.status, null);
    throw new Error(`${path}: expected JSON, got ${type || 'unknown'}`);
  }
  const payload: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    const error = safeParse(ErrorResponseSchema, payload);
    throw error.success
      ? new ApiError(res.status, error.output.code, error.output.reason)
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
export function fetchDeviceClone(deviceId: string, origin: string, signal?: AbortSignal) {
  return call(
    `/api/devices/${encodeURIComponent(deviceId)}/clone?origin=${encodeURIComponent(origin)}`,
    DeviceCloneResultSchema,
    undefined,
    signal,
  );
}

export function postMintDevice(name: string, deviceId?: string) {
  return call('/api/devices', DeviceMintResultSchema, deviceId ? { name, deviceId } : { name });
}

export function postHandoff(sessionId: string, targetDeviceId: string, targetPath?: string) {
  return call(
    `/api/sessions/${sessionId}/handoff`,
    HandoffResultSchema,
    targetPath === undefined ? { targetDeviceId } : { targetDeviceId, targetPath },
  );
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

/** Uploads an image for a coming prompt to a machine; the prompt names it by the ID (ADR 0017). */
export async function postAttachment(deviceId: string, image: PromptImage): Promise<string> {
  const result = await call(
    `/api/devices/${encodeURIComponent(deviceId)}/attachments`,
    AttachmentResultSchema,
    image,
  );
  return result.attachmentId;
}

export function postPermission(sessionId: string, requestId: string, decision: PermissionDecision) {
  return call(`/api/sessions/${sessionId}/permission`, RuntimeCommandResultSchema, {
    requestId,
    decision,
  });
}

export function postInterrupt(sessionId: string) {
  return call(`/api/sessions/${sessionId}/interrupt`, RuntimeCommandResultSchema, {});
}

export function postStop(sessionId: string) {
  return call(`/api/sessions/${sessionId}/stop`, RuntimeCommandResultSchema, {});
}

/** Starts a session; the response comes when the agent has created and imported it (up to 75 s). */
export function postNewSession(request: {
  deviceId: string;
  projectPath: string;
  provider: ProviderInfo['provider'];
  options: StartOptions;
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
