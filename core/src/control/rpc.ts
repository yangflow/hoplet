import { randomUUID } from 'node:crypto';
import type {
  AttachmentPut,
  DeviceCloneCheck,
  DeviceCloneFind,
  DeviceDirsList,
  DeviceProjectsAdd,
  DeviceProjectsList,
  DeviceProjectsRemove,
  DeviceSessionsImport,
  DeviceSessionsRemove,
  SessionWorkspaceDiffRead,
  SessionWorkspaceFileRead,
  SessionWorkspaceList,
} from '@hoplet/shared';
import { PROTOCOL_VERSION, SNAPSHOT_CHUNK_BYTES } from '@hoplet/shared';
import { askWaiter } from './pending.ts';
import { type ControlState, onlineLink } from './state.ts';
import type { Control, DeviceControlOutcome } from './types.ts';

/** The agent bounds its reachability probe at 30 s; the core waits a little longer for the answer. */
const CLONE_CHECK_TIMEOUT_MS = 40_000;

type DeviceRequest =
  | DeviceProjectsList
  | DeviceProjectsAdd
  | DeviceProjectsRemove
  | DeviceSessionsImport
  | DeviceSessionsRemove;

type Rpc = Pick<
  Control,
  | 'workspace'
  | 'workspaceFile'
  | 'workspaceDiff'
  | 'listDirs'
  | 'putAttachment'
  | 'findClone'
  | 'checkClone'
  | 'listProjects'
  | 'addProject'
  | 'removeProject'
  | 'importSessions'
  | 'removeSessions'
>;

/** Workspace reads and device project control: one request out, one answer or a timeout. */
export function createRpc(state: ControlState): Rpc {
  const {
    pendingWs,
    pendingFile,
    pendingDiff,
    pendingDeviceControl,
    pendingDirs,
    pendingClone,
    pendingCloneCheck,
    pendingAttachments,
  } = state;

  function askDevice(
    deviceId: string,
    build: (requestId: string) => DeviceRequest,
  ): Promise<DeviceControlOutcome> {
    const link = onlineLink(state, deviceId);
    if (!link) return Promise.resolve({ ok: false, error: 'owner-offline' });
    const requestId = randomUUID();
    return askWaiter(
      pendingDeviceControl,
      requestId,
      state.workspaceTimeoutMs,
      { ok: false, error: 'timeout' },
      () => link.send(build(requestId)),
    );
  }

  return {
    workspace(sessionId, ownerDeviceId) {
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return Promise.resolve({ ok: false, error: 'owner-offline' });
      const requestId = randomUUID();
      const msg: SessionWorkspaceList = {
        v: PROTOCOL_VERSION,
        type: 'session.workspace.list',
        sessionId,
        requestId,
      };
      return askWaiter(
        pendingWs,
        requestId,
        state.workspaceTimeoutMs,
        { ok: false, error: 'timeout' },
        () => link.send(msg),
      );
    },
    workspaceFile(sessionId, ownerDeviceId, path, at) {
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return Promise.resolve({ ok: false, error: 'owner-offline' });
      const requestId = randomUUID();
      const msg: SessionWorkspaceFileRead = {
        v: PROTOCOL_VERSION,
        type: 'session.workspace.file.read',
        sessionId,
        requestId,
        path,
        ...(at !== undefined ? { at } : {}),
      };
      return askWaiter(
        pendingFile,
        requestId,
        state.workspaceTimeoutMs,
        { ok: false, error: 'timeout' },
        () => link.send(msg),
      );
    },
    workspaceDiff(sessionId, ownerDeviceId, path) {
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return Promise.resolve({ ok: false, error: 'owner-offline' });
      const requestId = randomUUID();
      const msg: SessionWorkspaceDiffRead = {
        v: PROTOCOL_VERSION,
        type: 'session.workspace.diff.read',
        sessionId,
        requestId,
        path,
      };
      return askWaiter(
        pendingDiff,
        requestId,
        state.workspaceTimeoutMs,
        { ok: false, error: 'timeout' },
        () => link.send(msg),
      );
    },
    putAttachment(deviceId, image) {
      const link = onlineLink(state, deviceId);
      if (!link) return Promise.resolve({ ok: false, error: 'target-offline' });
      const requestId = randomUUID();
      const attachmentId = randomUUID();
      // Chunks of at most SNAPSHOT_CHUNK_BYTES, as a snapshot travels, so no bulk message holds the
      // connection longer than one chunk; the bulk queue keeps their order.
      const bytes = Buffer.from(image.data, 'base64');
      const chunks = Math.max(1, Math.ceil(bytes.length / SNAPSHOT_CHUNK_BYTES));
      return askWaiter(
        pendingAttachments,
        requestId,
        state.attachmentTimeoutMs,
        { ok: false, error: 'timeout' },
        () => {
          for (let seq = 0; seq < chunks; seq++) {
            const msg: AttachmentPut = {
              v: PROTOCOL_VERSION,
              type: 'attachment.put',
              requestId,
              attachmentId,
              mediaType: image.mediaType,
              seq,
              last: seq === chunks - 1,
              data: bytes
                .subarray(seq * SNAPSHOT_CHUNK_BYTES, (seq + 1) * SNAPSHOT_CHUNK_BYTES)
                .toString('base64'),
            };
            link.send(msg);
          }
        },
      );
    },
    listDirs(deviceId, path) {
      const link = onlineLink(state, deviceId);
      if (!link) return Promise.resolve({ ok: false, error: 'target-offline' });
      const requestId = randomUUID();
      const msg: DeviceDirsList = {
        v: PROTOCOL_VERSION,
        type: 'device.dirs.list',
        requestId,
        path,
      };
      return askWaiter(
        pendingDirs,
        requestId,
        state.workspaceTimeoutMs,
        { ok: false, error: 'timeout' },
        () => link.send(msg),
      );
    },
    findClone(deviceId, origin) {
      const link = onlineLink(state, deviceId);
      if (!link) return Promise.resolve({ ok: false, error: 'target-offline' });
      const requestId = randomUUID();
      const msg: DeviceCloneFind = {
        v: PROTOCOL_VERSION,
        type: 'device.clone.find',
        requestId,
        origin,
      };
      return askWaiter(
        pendingClone,
        requestId,
        state.workspaceTimeoutMs,
        { ok: false, error: 'timeout' },
        () => link.send(msg),
      );
    },
    checkClone(deviceId, origin, cloneInto) {
      const link = onlineLink(state, deviceId);
      if (!link) return Promise.resolve({ ok: false, error: 'target-offline' });
      const requestId = randomUUID();
      const msg: DeviceCloneCheck = {
        v: PROTOCOL_VERSION,
        type: 'device.clone.check',
        requestId,
        origin,
        ...(cloneInto !== undefined ? { cloneInto } : {}),
      };
      // The agent probes the origin with `git ls-remote`, which may take a while.
      return askWaiter(
        pendingCloneCheck,
        requestId,
        CLONE_CHECK_TIMEOUT_MS,
        { ok: false, error: 'timeout' },
        () => link.send(msg),
      );
    },
    listProjects(deviceId) {
      return askDevice(deviceId, (requestId) => ({
        v: PROTOCOL_VERSION,
        type: 'device.projects.list',
        requestId,
      }));
    },
    addProject(deviceId, path) {
      return askDevice(deviceId, (requestId) => ({
        v: PROTOCOL_VERSION,
        type: 'device.projects.add',
        requestId,
        path,
      }));
    },
    removeProject(deviceId, path) {
      return askDevice(deviceId, (requestId) => ({
        v: PROTOCOL_VERSION,
        type: 'device.projects.remove',
        requestId,
        path,
      }));
    },
    async importSessions(deviceId, path, sessionIds) {
      // An import asks for the sessions back (ADR 0023): their tombstones go before the agent starts syncing.
      const lifted = sessionIds.flatMap((id) => {
        const tombstone = state.store.tombstoneOf(id);
        if (!tombstone) return [];
        state.store.clearTombstone(id);
        return [tombstone];
      });
      const outcome = await askDevice(deviceId, (requestId) => ({
        v: PROTOCOL_VERSION,
        type: 'device.sessions.import',
        requestId,
        path,
        sessionIds,
      }));
      if (!outcome.ok) {
        for (const t of lifted) state.store.tombstone(t.sessionId, t.deviceId, t.deletedAt);
      }
      return outcome;
    },
    removeSessions(deviceId, path, sessionIds) {
      return askDevice(deviceId, (requestId) => ({
        v: PROTOCOL_VERSION,
        type: 'device.sessions.remove',
        requestId,
        path,
        sessionIds,
      }));
    },
  };
}
