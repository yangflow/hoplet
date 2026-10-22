import { randomUUID } from 'node:crypto';
import {
  findProvider,
  PROTOCOL_VERSION,
  type ProviderInfo,
  type RuntimeStart,
  undeclaredConfig,
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

/** What the provider's capabilities do not declare in the request (`images` or `config`), or null. */
function undeclared(req: StartRequest, info: ProviderInfo): 'images' | 'config' | null {
  if ((req.attachments?.length ?? 0) > 0 && !info.capabilities.images) return 'images';
  if (req.config && undeclaredConfig(req.config, info.capabilities) !== null) return 'config';
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
    const info = findProvider(
      state.providers.get(req.deviceId) ?? [],
      req.provider,
      req.configuration,
    );
    if (!info) return refuse(req.configuration === undefined ? 'provider' : 'configuration');
    if (!info.configuration.enabled) return refuse('configuration');
    const missing = undeclared(req, info);
    if (missing) return refuse(missing);

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

    // A budget at its limit with `pause` refuses new sessions in its scope as it refuses prompts.
    const budget = state.store.budgetBlockFor(project.path, req.deviceId, state.now());
    if (budget) {
      log('info', 'control', 'new session refused by a budget', {
        device: req.deviceId,
        projectPath: project.path,
        budget: budget.id,
      });
      return { ok: false, error: 'over-budget', promptId, budget };
    }

    // The audit trail: who was asked to start what where, never the text itself (docs/threat-model.md).
    log('info', 'control', 'new session forwarded', {
      device: req.deviceId,
      projectPath: project.path,
      promptId,
      images: req.attachments?.length ?? 0,
    });
    const requestId = randomUUID();
    const msg: RuntimeStart = {
      v: PROTOCOL_VERSION,
      type: 'runtime.start',
      requestId,
      projectPath: project.path,
      provider: req.provider,
      ...(req.configuration !== undefined ? { configuration: req.configuration } : {}),
      ...(req.config && Object.keys(req.config).length > 0 ? { config: req.config } : {}),
      text: req.text,
      ...(req.attachments && req.attachments.length > 0 ? { attachments: req.attachments } : {}),
      promptId,
    };
    const answer = await askWaiter<StartAnswer>(
      pendingStart,
      requestId,
      state.startTimeoutMs,
      { ok: false, error: 'timeout' },
      () => link.send(msg),
      { link, sessionId: '', start: { deviceId: req.deviceId, provider: req.provider } },
    );
    if (!answer.ok) {
      return {
        ok: false,
        error: answer.error,
        promptId,
        ...(answer.reason !== undefined ? { reason: answer.reason } : {}),
      };
    }
    // The session exists already, so a record the log cannot take is told and nothing else happens.
    try {
      state.store.audit({
        type: 'session.created',
        sessionId: answer.sessionId,
        deviceId: req.deviceId,
        ref: promptId,
        summary: {
          provider: req.provider,
          projectPath: project.path,
          promptId,
          length: [...req.text].length,
          images: req.attachments?.length ?? 0,
        },
      });
    } catch (err) {
      log('error', 'control', 'session start not recorded', {
        sessionId: answer.sessionId,
        error: err instanceof Error ? err.message : 'unknown',
      });
    }
    return { ok: true, sessionId: answer.sessionId, promptId };
  }

  return {
    startSession(req) {
      const now = state.now();
      sweep(now);
      const cached = startCache.get(req.promptId);
      if (cached) return cached.outcome;

      const failed = (error: string): void =>
        state.emit({
          kind: 'failure',
          source: 'start',
          projectPath: req.projectPath,
          deviceId: req.deviceId,
          error,
        });
      const link = onlineLink(state, req.deviceId);
      if (!link) {
        failed('target-offline');
        return Promise.resolve({ ok: false, error: 'target-offline', promptId: req.promptId });
      }
      const outcome = run(req, link);
      startCache.set(req.promptId, { at: now, outcome });
      // Only a result that may have reached the agent is repeated; a refusal can be retried.
      void outcome.then((result) => {
        if (!result.ok && result.error !== 'timeout') startCache.delete(req.promptId);
        if (result.ok) {
          state.emit({
            kind: 'started',
            sessionId: result.sessionId,
            deviceId: req.deviceId,
            projectPath: req.projectPath,
            provider: req.provider,
            ...(req.configuration !== undefined ? { configuration: req.configuration } : {}),
            origin: req.origin ?? 'new',
          });
        }
        // A request the core refused as invalid or over budget is the caller's matter, not a failure to tell about.
        if (!result.ok && result.error !== 'invalid-request' && result.error !== 'over-budget') {
          failed(result.error);
        }
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
