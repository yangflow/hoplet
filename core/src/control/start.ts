import { randomUUID } from 'node:crypto';
import {
  PROTOCOL_VERSION,
  type ProviderInfo,
  type RuntimeStart,
  type StartOptions,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { askWaiter } from './pending.ts';
import { PROMPT_DEDUP_MS } from './runtime.ts';
import { type ControlState, onlineLink } from './state.ts';
import type { Control, DeviceLink, StartAnswer, StartOutcome, StartRequest } from './types.ts';

type Start = Pick<Control, 'startSession'> & {
  /** The link was lost: new sessions it was starting will not be answered. */
  detached(link: DeviceLink): void;
};

function sameDir(a: string, b: string): boolean {
  const trim = (p: string) => (p.length > 1 ? p.replace(/\/+$/, '') : p);
  return trim(a) === trim(b);
}

/** The first option the provider's capabilities do not declare, or null when all are. */
function undeclared(options: StartOptions, info: ProviderInfo, images: number): string | null {
  const caps = info.capabilities;
  if (images > 0 && !caps.images) return 'images';
  if (options.model !== undefined && !caps.models.includes(options.model)) return 'model';
  if (options.thinking !== undefined && !caps.thinking) return 'thinking';
  if (options.fast !== undefined && !caps.fast) return 'fast';
  return null;
}

/**
 * New sessions (docs/spec.md#new-session): checks the request against what the device announced and its
 * watched projects, forwards `runtime.start`, and waits for the agent to answer with the session's ID.
 */
export function createStart(state: ControlState, listProjects: Control['listProjects']): Start {
  const { pendingStart, startCache, startedSessions } = state;

  function sweep(now: number): void {
    for (const [key, cached] of startCache) {
      if (now - cached.at > PROMPT_DEDUP_MS) startCache.delete(key);
    }
    for (const [key, started] of startedSessions) {
      if (now - started.at > PROMPT_DEDUP_MS) startedSessions.delete(key);
    }
  }

  async function run(req: StartRequest, link: DeviceLink): Promise<StartOutcome> {
    const { promptId } = req;
    const refuse = (reason: string): StartOutcome => ({
      ok: false,
      error: 'invalid-request',
      promptId,
      reason,
    });
    const info = state.providers.get(req.deviceId)?.find((p) => p.provider === req.provider);
    if (!info) return refuse('provider');
    const option = undeclared(req.options, info, req.images?.length ?? 0);
    if (option) return refuse(option);

    const listing = await listProjects(req.deviceId);
    if (!listing.ok) {
      if (listing.error === 'owner-offline')
        return { ok: false, error: 'target-offline', promptId };
      return {
        ok: false,
        error: listing.error,
        promptId,
        ...(listing.reason !== undefined ? { reason: listing.reason } : {}),
      };
    }
    const project = listing.projects.find((p) => sameDir(p.path, req.projectPath));
    if (!project) return refuse('projectPath');

    // The audit trail: who was asked to start what where, never the text itself (docs/threat-model.md).
    log('info', 'control', 'new session forwarded', {
      device: req.deviceId,
      projectPath: project.path,
      promptId,
      images: req.images?.length ?? 0,
    });
    const requestId = randomUUID();
    const msg: RuntimeStart = {
      v: PROTOCOL_VERSION,
      type: 'runtime.start',
      requestId,
      projectPath: project.path,
      provider: req.provider,
      options: req.options,
      text: req.text,
      ...(req.images && req.images.length > 0 ? { images: req.images } : {}),
      promptId,
    };
    const answer = await askWaiter<StartAnswer>(
      pendingStart,
      requestId,
      state.startTimeoutMs,
      { ok: false, error: 'timeout' },
      () => link.send(msg),
      { link, sessionId: '' },
    );
    if (!answer.ok) {
      return {
        ok: false,
        error: answer.error,
        promptId,
        ...(answer.reason !== undefined ? { reason: answer.reason } : {}),
      };
    }
    startedSessions.set(answer.sessionId, {
      deviceId: req.deviceId,
      provider: req.provider,
      at: state.now(),
    });
    return { ok: true, sessionId: answer.sessionId, promptId };
  }

  return {
    startSession(req) {
      const now = state.now();
      sweep(now);
      const cached = startCache.get(req.promptId);
      if (cached) return cached.outcome;

      const link = onlineLink(state, req.deviceId);
      if (!link) {
        return Promise.resolve({ ok: false, error: 'target-offline', promptId: req.promptId });
      }
      const outcome = run(req, link);
      startCache.set(req.promptId, { at: now, outcome });
      // Only a result that may have reached the agent is repeated; a refusal can be retried.
      void outcome.then((result) => {
        if (!result.ok && result.error !== 'timeout') startCache.delete(req.promptId);
      });
      return outcome;
    },

    detached(link) {
      for (const [requestId, waiter] of [...pendingStart]) {
        if (waiter.link !== link) continue;
        pendingStart.delete(requestId);
        clearTimeout(waiter.timer);
        waiter.resolve({ ok: false, error: 'target-offline' });
      }
    },
  };
}
