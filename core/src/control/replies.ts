import type {
  DeviceResources,
  SessionPort,
  SessionWorkspaceDiff,
  SessionWorkspaceFile,
  WorkspaceDiff,
  WorkspaceFile,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { matchesWaiter, settleWaiter } from './pending.ts';
import type { Runtime } from './runtime.ts';
import type { ControlState } from './state.ts';
import type { TerminalReply } from './terminals.ts';
import type { ControlReply, DeviceLink } from './types.ts';

/** Settles the waiter an agent's answer belongs to. Answers that no request is waiting for are dropped. */
export function receiveReply(
  state: ControlState,
  runtime: Runtime,
  terminals: { receive(link: DeviceLink, msg: TerminalReply): void },
  ports: { receive(link: DeviceLink, sessionId: string, ports: SessionPort[]): void },
  resources: { receive(link: DeviceLink, msg: DeviceResources): void },
  link: DeviceLink,
  msg: ControlReply,
): void {
  const {
    pendingWs,
    pendingFile,
    pendingDiff,
    pendingPrepared,
    pendingInstalled,
    pendingVerified,
    pendingDeviceControl,
    pendingProviders,
    pendingSession,
    pendingDirs,
    pendingClone,
    pendingCloneCheck,
    pendingCloneRun,
    pendingStart,
    pendingAttachments,
  } = state;
  switch (msg.type) {
    case 'runtime.state':
    case 'runtime.output':
    case 'runtime.permission':
    case 'runtime.permission.auto':
    case 'runtime.rules.unenforced':
    case 'runtime.rejected':
    case 'runtime.steered':
    case 'runtime.commands':
      runtime.receive(link, msg);
      return;
    case 'runtime.started': {
      const wait = pendingStart.get(msg.requestId);
      if (wait?.link === link) {
        // Recorded now, not when the caller continues: the agent reports the session's state right behind
        // this answer, possibly in the same read.
        if (wait.start) {
          state.startedSessions.set(msg.sessionId, { ...wait.start, at: state.now() });
        }
        settleWaiter(pendingStart, msg.requestId, {
          ok: true,
          sessionId: msg.sessionId,
          ...(msg.worktree ? { worktree: msg.worktree } : {}),
        });
      }
      return;
    }
    case 'runtime.start.rejected': {
      const wait = pendingStart.get(msg.requestId);
      if (wait?.link === link) {
        settleWaiter(pendingStart, msg.requestId, {
          ok: false,
          error: 'rejected',
          reason: msg.reason,
        });
      }
      return;
    }
    case 'session.workspace':
      settleWaiter(pendingWs, msg.requestId, {
        ok: true,
        listing: {
          sessionId: msg.sessionId,
          git: msg.git,
          commit: msg.commit,
          origin: msg.origin,
          paths: msg.paths,
          truncated: msg.truncated === true,
          changes: msg.changes,
        },
      });
      return;
    case 'session.workspace.file':
      settleWaiter(pendingFile, msg.requestId, { ok: true, file: fileOf(msg) });
      return;
    case 'session.workspace.diff':
      settleWaiter(pendingDiff, msg.requestId, { ok: true, diff: diffOf(msg) });
      return;
    case 'handoff.prepared':
      if (matchesWaiter(pendingPrepared, msg.handoffId, link, msg.sessionId)) {
        settleWaiter(pendingPrepared, msg.handoffId, {
          ok: true,
          value: { snapshotId: msg.snapshotId, byteSize: msg.byteSize },
        });
      }
      return;
    case 'handoff.installed':
      if (matchesWaiter(pendingInstalled, msg.handoffId, link, msg.sessionId)) {
        settleWaiter(pendingInstalled, msg.handoffId, {
          ok: true,
          value: { worktree: msg.worktree, branch: msg.branch, clone: msg.clone },
        });
      }
      return;
    case 'handoff.verified':
      if (matchesWaiter(pendingVerified, msg.handoffId, link, msg.sessionId)) {
        settleWaiter(pendingVerified, msg.handoffId, {
          ok: true,
          value: {
            sessionBytes: msg.sessionBytes,
            sessionSha256: msg.sessionSha256,
            treeHash: msg.treeHash,
          },
        });
      }
      return;
    case 'handoff.failed': {
      const failure = { ok: false, error: 'rejected', reason: msg.reason } as const;
      if (msg.step === 'prepare') {
        if (matchesWaiter(pendingPrepared, msg.handoffId, link, msg.sessionId)) {
          settleWaiter(pendingPrepared, msg.handoffId, failure);
        }
      } else if (msg.step === 'install') {
        if (matchesWaiter(pendingInstalled, msg.handoffId, link, msg.sessionId)) {
          settleWaiter(pendingInstalled, msg.handoffId, failure);
        }
      } else if (matchesWaiter(pendingVerified, msg.handoffId, link, msg.sessionId)) {
        settleWaiter(pendingVerified, msg.handoffId, failure);
      }
      return;
    }
    case 'device.projects':
      settleWaiter(pendingDeviceControl, msg.requestId, {
        ok: true,
        candidates: msg.candidates,
        projects: msg.projects,
      });
      return;
    case 'device.projects.rejected':
      settleWaiter(pendingDeviceControl, msg.requestId, {
        ok: false,
        error: 'rejected',
        reason: msg.reason,
      });
      return;
    case 'device.session.inspected':
    case 'device.session.deleted':
    case 'device.session.forgotten':
      if (pendingSession.get(msg.requestId)?.link === link) {
        settleWaiter(pendingSession, msg.requestId, { ok: true, reply: msg });
      }
      return;
    case 'device.session.rejected':
      if (pendingSession.get(msg.requestId)?.link === link) {
        settleWaiter(pendingSession, msg.requestId, {
          ok: false,
          error: 'rejected',
          reason: msg.reason,
        });
      }
      return;
    case 'device.dirs':
      settleWaiter(pendingDirs, msg.requestId, {
        ok: true,
        listing: {
          path: msg.path,
          parent: msg.parent,
          root: msg.root,
          entries: msg.entries,
          truncated: msg.truncated,
        },
      });
      return;
    case 'attachment.stored':
      settleWaiter(pendingAttachments, msg.requestId, {
        ok: true,
        attachmentId: msg.attachmentId,
      });
      return;
    case 'attachment.rejected':
      settleWaiter(pendingAttachments, msg.requestId, {
        ok: false,
        error: 'rejected',
        reason: msg.reason,
      });
      return;
    case 'device.clone':
      settleWaiter(pendingClone, msg.requestId, { ok: true, path: msg.path });
      return;
    case 'device.clone.checked':
      settleWaiter(pendingCloneCheck, msg.requestId, {
        ok: true,
        reason: msg.reason,
        cloneInto: msg.cloneInto,
        ...(msg.detail !== undefined ? { detail: msg.detail } : {}),
      });
      return;
    case 'device.clone.progress': {
      const wait = pendingCloneRun.get(msg.requestId);
      if (wait?.link === link) wait.progress(msg.received, msg.total);
      return;
    }
    case 'device.clone.done':
      if (pendingCloneRun.get(msg.requestId)?.link === link) {
        settleWaiter(pendingCloneRun, msg.requestId, { ok: true, path: msg.path });
      }
      return;
    case 'device.clone.failed':
      if (pendingCloneRun.get(msg.requestId)?.link === link) {
        settleWaiter(pendingCloneRun, msg.requestId, {
          ok: false,
          error: 'failed',
          reason: msg.reason,
          detail: msg.detail,
        });
      }
      return;
    case 'device.dirs.rejected':
      settleWaiter(pendingDirs, msg.requestId, {
        ok: false,
        error: 'rejected',
        reason: msg.reason,
      });
      return;
    case 'session.workspace.rejected':
      settleWaiter(pendingWs, msg.requestId, {
        ok: false,
        error: 'rejected',
        reason: msg.reason,
      });
      settleWaiter(pendingFile, msg.requestId, {
        ok: false,
        error: 'rejected',
        reason: msg.reason,
      });
      settleWaiter(pendingDiff, msg.requestId, {
        ok: false,
        error: 'rejected',
        reason: msg.reason,
      });
      return;
    case 'terminal.opened':
    case 'terminal.output':
    case 'terminal.closed':
    case 'terminal.rejected':
      terminals.receive(link, msg);
      return;
    case 'session.ports':
      ports.receive(link, msg.sessionId, msg.ports);
      return;
    case 'device.rules.applied':
      return; // the rules module takes it before the reply is routed
    case 'device.resources':
      resources.receive(link, msg);
      return;
    case 'device.inventory': {
      const { v: _v, type: _type, ...inventory } = msg;
      for (const [deviceId, attached] of state.byDevice) {
        if (attached === link) state.store.setDeviceInventory(deviceId, inventory);
      }
      return;
    }
    case 'device.providers': {
      // The list is the agent's own, whole: it replaces what the hello announced.
      for (const [deviceId, attached] of state.byDevice) {
        if (attached === link) state.providers.set(deviceId, msg.providers);
      }
      if (msg.requestId !== undefined) {
        settleWaiter(pendingProviders, msg.requestId, { ok: true, providers: msg.providers });
      }
      return;
    }
    case 'device.providers.rejected':
      log('warn', 'control', 'provider update refused by the agent', { reason: msg.reason });
      settleWaiter(pendingProviders, msg.requestId, {
        ok: false,
        error: 'rejected',
        reason: msg.reason,
      });
      return;
  }
}

/** The REST shape of a file answer; an image without its media type or data reads as binary. */
function fileOf(msg: SessionWorkspaceFile): WorkspaceFile {
  const base = { sessionId: msg.sessionId, path: msg.path, size: msg.size };
  if (msg.kind === 'text') {
    return { kind: 'text', ...base, text: msg.text ?? '', truncated: msg.truncated === true };
  }
  if (msg.kind === 'image' && msg.mediaType !== undefined && msg.data !== undefined) {
    return { kind: 'image', ...base, mediaType: msg.mediaType, data: msg.data };
  }
  return { kind: 'binary', ...base };
}

/** The REST shape of a diff answer. */
function diffOf(msg: SessionWorkspaceDiff): WorkspaceDiff {
  const base = { sessionId: msg.sessionId, path: msg.path, status: msg.status };
  if (msg.kind === 'text') {
    return {
      kind: 'text',
      ...base,
      diff: msg.diff ?? '',
      truncated: msg.truncated === true,
      size: msg.size ?? 0,
    };
  }
  return {
    kind: 'binary',
    ...base,
    ...(msg.oldSize !== undefined ? { oldSize: msg.oldSize } : {}),
    ...(msg.newSize !== undefined ? { newSize: msg.newSize } : {}),
  };
}
