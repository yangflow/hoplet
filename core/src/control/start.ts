import { randomUUID } from 'node:crypto';
import {
  findProvider,
  isWorktreeReason,
  PROTOCOL_VERSION,
  type ProviderInfo,
  type RuntimeStart,
  undeclaredConfig,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { askWaiter } from './pending.ts';
import { body } from './rules.ts';
import { PROMPT_DEDUP_MS, senderTitle } from './runtime.ts';
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
export function createStart(
  state: ControlState,
  listProjects: Control['listProjects'],
  syncRules: (deviceIds?: readonly string[]) => void,
): Start {
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
    // A child obeys the deny and ask rules of the sessions that started it from its first tool call (ADR 0029).
    const ancestors = req.source ? [...req.source.lineage, req.source.sessionId] : [];
    const inherited = state.store.inheritedPermissionRules(ancestors).map(body);
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
      ...(req.worktree
        ? { worktree: req.worktree.branch !== undefined ? { branch: req.worktree.branch } : {} }
        : {}),
      // The new session's agent puts a first line naming the session that started it (ADR 0034).
      ...(req.source
        ? {
            source: {
              kind: 'session' as const,
              sessionId: req.source.sessionId,
              title: senderTitle(state.store.getSession(req.source.sessionId)?.title ?? ''),
              chain: req.source.chain,
            },
          }
        : {}),
      ...(inherited.length > 0 ? { inheritedRules: inherited } : {}),
    };
    const answer = await askWaiter<StartAnswer>(
      pendingStart,
      requestId,
      state.startTimeoutMs,
      { ok: false, error: 'timeout' },
      () => link.send(msg),
      {
        link,
        sessionId: '',
        start: { deviceId: req.deviceId, provider: req.provider, projectPath: project.path },
      },
    );
    if (!answer.ok) {
      // A worktree the agent could not make for a reason of the request is the caller's to fix.
      if (req.worktree && answer.error === 'rejected' && isWorktreeReason(answer.reason)) {
        return refuse(answer.reason);
      }
      return {
        ok: false,
        error: answer.error,
        promptId,
        ...(answer.reason !== undefined ? { reason: answer.reason } : {}),
      };
    }
    // The chain behind the new session's first prompt (ADR 0034).
    if (req.source) state.store.setSessionChain(answer.sessionId, req.source.chain);
    // The session is a child of the one that started it for as long as it lives (ADR 0035).
    if (req.source) {
      state.store.recordChild(
        answer.sessionId,
        [...req.source.lineage, req.source.sessionId],
        state.now(),
        req.source.timeLimitMs,
      );
      // The device's rules now include the child's inherited ones, and its revision counts the change.
      state.store.bumpPermissionRevision(req.deviceId);
      syncRules([req.deviceId]);
    }
    // The worktree is recorded first: deleting the session offers it (ADR 0023).
    if (answer.worktree) {
      try {
        state.store.recordNewWorktree(answer.sessionId, req.deviceId, answer.worktree);
      } catch (err) {
        log('error', 'control', 'session worktree not recorded', {
          sessionId: answer.sessionId,
          error: err instanceof Error ? err.message : 'unknown',
        });
      }
    }
    // The session exists already, so a record the log cannot take is told and nothing else happens.
    try {
      state.store.audit({
        type: 'session.created',
        ...(req.actor ? { actor: req.actor } : {}),
        sessionId: answer.sessionId,
        deviceId: req.deviceId,
        ref: promptId,
        summary: {
          provider: req.provider,
          projectPath: project.path,
          promptId,
          length: [...req.text].length,
          images: req.attachments?.length ?? 0,
          ...(req.source
            ? {
                source: {
                  kind: 'session',
                  sessionId: req.source.sessionId,
                  chain: req.source.chain,
                },
                // The rules the child obeys because of its parent, and the revision of the device's rules.
                rules: {
                  inherited: inherited.map((r) => r.id),
                  revision: state.store.permissionRevision(req.deviceId),
                },
              }
            : {}),
        },
      });
    } catch (err) {
      log('error', 'control', 'session start not recorded', {
        sessionId: answer.sessionId,
        error: err instanceof Error ? err.message : 'unknown',
      });
    }
    return {
      ok: true,
      sessionId: answer.sessionId,
      promptId,
      ...(answer.worktree ? { worktree: answer.worktree } : {}),
    };
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
