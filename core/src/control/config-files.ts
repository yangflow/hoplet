// The `ConfigFiles` port as the owner's agent answers it (ADR 0036): runtime.config.preview and
// runtime.config.apply on the control lane. The agent maps each kind and name to a path itself, checks the content
// and writes all or nothing; the core only carries the request and reads the answer.

import { randomUUID } from 'node:crypto';
import {
  type ConfigWriteFailure,
  PROTOCOL_VERSION,
  type Provider,
  type RuntimeConfigApplied,
  type RuntimeConfigApply,
  type RuntimeConfigFailed,
  type RuntimeConfigPreview,
  type RuntimeConfigPreviewed,
} from '@hoplet/shared';
import { askWaiter, matchesWaiter, settleWaiter } from './pending.ts';
import { type ControlState, onlineLink } from './state.ts';
import type { ConfigFileApply, ConfigFilePreview, ConfigFiles, DeviceLink } from './types.ts';

/** The reason of a refusal the port has no code of its own for: the agent's code, then its reason. */
function detail(msg: RuntimeConfigFailed): string {
  return msg.reason ? `${msg.code}: ${msg.reason}` : msg.code;
}

function previewFailure(msg: RuntimeConfigFailed): ConfigFilePreview {
  const kept: Partial<Record<ConfigWriteFailure, 'unsupported' | 'unsafe-content' | 'too-large'>> =
    {
      unsupported: 'unsupported',
      'unsafe-content': 'unsafe-content',
      'too-large': 'too-large',
    };
  const error = kept[msg.code];
  return error
    ? { ok: false, error, ...(msg.reason ? { reason: msg.reason } : {}) }
    : { ok: false, error: 'rejected', reason: detail(msg) };
}

function applyFailure(msg: RuntimeConfigFailed): ConfigFileApply {
  const kept: Partial<Record<ConfigWriteFailure, 'unsupported' | 'unsafe-content' | 'stale'>> = {
    unsupported: 'unsupported',
    'unsafe-content': 'unsafe-content',
    stale: 'stale',
  };
  const error = kept[msg.code];
  return error
    ? { ok: false, error, ...(msg.reason ? { reason: msg.reason } : {}) }
    : { ok: false, error: 'failed', reason: detail(msg) };
}

export function createAgentConfigFiles(
  state: ControlState,
  providerOf: (sessionId: string) => Provider | null,
): ConfigFiles {
  const { pendingConfigPreview, pendingConfigApply } = state;
  return {
    preview(deviceId, sessionId, objects) {
      const link = onlineLink(state, deviceId);
      if (!link) return Promise.resolve({ ok: false, error: 'owner-offline' });
      const provider = providerOf(sessionId);
      if (provider === null) return Promise.resolve({ ok: false, error: 'unsupported' });
      const requestId = randomUUID();
      const msg: RuntimeConfigPreview = {
        v: PROTOCOL_VERSION,
        type: 'runtime.config.preview',
        sessionId,
        requestId,
        provider,
        objects: objects.map((o) => ({ kind: o.kind, name: o.name, content: o.content })),
      };
      return askWaiter(
        pendingConfigPreview,
        requestId,
        state.workspaceTimeoutMs,
        { ok: false, error: 'timeout' },
        () => link.send(msg),
        { link, sessionId },
      );
    },
    apply(deviceId, input) {
      const link = onlineLink(state, deviceId);
      if (!link) return Promise.resolve({ ok: false, error: 'owner-offline' });
      const provider = providerOf(input.sessionId);
      if (provider === null) return Promise.resolve({ ok: false, error: 'unsupported' });
      const requestId = randomUUID();
      const msg: RuntimeConfigApply = {
        v: PROTOCOL_VERSION,
        type: 'runtime.config.apply',
        sessionId: input.sessionId,
        requestId,
        changeId: input.changeId,
        provider,
        restore: input.restore === true,
        objects: input.objects.map((o) => ({
          kind: o.kind,
          name: o.name,
          content: o.content,
          baseHash: o.baseHash,
          newHash: o.newHash,
        })),
      };
      // A timeout leaves it open whether the agent wrote: the change is then failed, and its files say what
      // happened; the agent writes all or nothing, so they are the old or the new set.
      return askWaiter(
        pendingConfigApply,
        requestId,
        state.workspaceTimeoutMs,
        { ok: false, error: 'timeout' },
        () => link.send(msg),
        { link, sessionId: input.sessionId },
      );
    },
  };
}

/** Settles the preview or apply an agent's answer belongs to; answers from another link are dropped. */
export function receiveConfigFiles(
  state: ControlState,
  link: DeviceLink,
  msg: RuntimeConfigPreviewed | RuntimeConfigApplied | RuntimeConfigFailed,
): void {
  const { pendingConfigPreview, pendingConfigApply } = state;
  switch (msg.type) {
    case 'runtime.config.previewed':
      if (!matchesWaiter(pendingConfigPreview, msg.requestId, link, msg.sessionId)) return;
      settleWaiter(pendingConfigPreview, msg.requestId, {
        ok: true,
        objects: msg.objects.map((o) => ({
          kind: o.kind,
          name: o.name,
          currentHash: o.currentHash,
          previous: o.previous,
          diff: o.diff,
          ...(o.newDirectories?.length ? { newDirectories: o.newDirectories } : {}),
        })),
      });
      return;
    case 'runtime.config.applied':
      if (!matchesWaiter(pendingConfigApply, msg.requestId, link, msg.sessionId)) return;
      settleWaiter(pendingConfigApply, msg.requestId, { ok: true });
      return;
    case 'runtime.config.failed':
      if (matchesWaiter(pendingConfigPreview, msg.requestId, link, msg.sessionId)) {
        settleWaiter(pendingConfigPreview, msg.requestId, previewFailure(msg));
      } else if (matchesWaiter(pendingConfigApply, msg.requestId, link, msg.sessionId)) {
        settleWaiter(pendingConfigApply, msg.requestId, applyFailure(msg));
      }
      return;
  }
}
