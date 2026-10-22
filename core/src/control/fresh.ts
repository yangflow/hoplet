import { PROTOCOL_VERSION } from '@hoplet/shared';
import { log } from '../log/index.ts';
import { PROMPT_DEDUP_MS } from './runtime.ts';
import type { ControlState } from './state.ts';
import type { Control, FreshOutcome } from './types.ts';

/** What a refused plan tells the model in the old session. */
export const FRESH_REFUSAL = 'This plan is being implemented in a new session.';

/** The first prompt of the new session: a framing line, the plan and the file it lives in. */
function promptOf(plan: string, planFile: string | undefined): string {
  const lines = [
    'Implement the plan below. It was approved in an earlier session, so start from the plan alone.',
    '',
    plan,
  ];
  if (planFile) lines.push('', `Plan file: ${planFile}`);
  return lines.join('\n');
}

type Peer = Pick<
  Control,
  | 'answerPermission'
  | 'interrupt'
  | 'openPermissions'
  | 'ownerOf'
  | 'ownerOnline'
  | 'providerOf'
  | 'startSession'
>;

/**
 * A plan approved into a new session (ADR 0025): starts the session through `startSession`, and only when that
 * succeeds records the link, refuses the plan in the old session and interrupts its turn. `peer` is the finished
 * control, so the interrupt is the one that tells the queue.
 */
export function createFresh(
  state: ControlState,
  peer: () => Peer,
): Pick<Control, 'approvePlanFresh'> {
  const { store } = state;
  const cache = new Map<string, { at: number; outcome: Promise<FreshOutcome> }>();
  // Plan requests whose new session is starting: another promptId for the same plan must not start a second one.
  const starting = new Set<string>();

  function sweep(now: number): void {
    for (const [key, cached] of cache) if (now - cached.at > PROMPT_DEDUP_MS) cache.delete(key);
  }

  return {
    approvePlanFresh(req) {
      const { sessionId, requestId, mode, promptId } = req;
      const now = state.now();
      sweep(now);
      const cached = cache.get(promptId);
      if (cached) return cached.outcome;

      const control = peer();
      const refuse = (
        error: 'session-not-found' | 'owner-offline' | 'request-closed' | 'invalid-request',
        reason?: string,
      ): Promise<FreshOutcome> =>
        Promise.resolve({
          ok: false,
          error,
          promptId,
          ...(reason !== undefined ? { reason } : {}),
        });

      const owner = control.ownerOf(sessionId);
      const provider = control.providerOf(sessionId);
      const meta = store.getSession(sessionId);
      if (!owner || !provider || !meta) return refuse('session-not-found');
      if (!control.ownerOnline(owner)) return refuse('owner-offline');
      const planExit = control
        .openPermissions(sessionId)
        .find((p) => p.requestId === requestId)?.planExit;
      if (!planExit) return refuse('request-closed');
      if (planExit.freshContext !== true) return refuse('invalid-request', 'fresh-context');
      if (!planExit.modes.includes(mode)) return refuse('invalid-request', 'mode');
      const key = `${sessionId}\n${requestId}`;
      if (starting.has(key)) return refuse('request-closed');

      const run = async (): Promise<FreshOutcome> => {
        const started = await control.startSession({
          deviceId: owner,
          projectPath: meta.projectPath,
          provider,
          config: { [planExit.option]: mode },
          text: promptOf(planExit.plan, planExit.planFile),
          promptId,
          origin: 'plan',
          ...(req.actor ? { actor: req.actor } : {}),
        });
        if (!started.ok) {
          return {
            ok: false,
            error: started.error,
            promptId,
            ...(started.reason !== undefined ? { reason: started.reason } : {}),
            ...(started.budget !== undefined ? { budget: started.budget } : {}),
          };
        }
        store.recordContinuation(sessionId, started.sessionId, requestId, mode, state.now());
        // A plan answered elsewhere while the session started stays as it was answered, and its turn runs on; the
        // link is kept.
        const refused = control.answerPermission(sessionId, owner, requestId, {
          decision: 'deny',
          message: FRESH_REFUSAL,
          ...(req.actor ? { actor: req.actor } : {}),
        });
        // A turn that is not running is fine.
        if (refused.ok) control.interrupt(sessionId, owner, req.actor);
        log('info', 'control', 'plan approved into a new session', {
          from: sessionId,
          to: started.sessionId,
          refused: refused.ok,
        });
        for (const id of [sessionId, started.sessionId]) {
          state.publish({
            v: PROTOCOL_VERSION,
            type: 'session.updated',
            sessionId: id,
            ...store.sessionState(id),
          });
        }
        return { ok: true, sessionId: started.sessionId, promptId };
      };

      starting.add(key);
      const outcome = run().finally(() => starting.delete(key));
      cache.set(promptId, { at: now, outcome });
      // Only a result that may have reached the agent is repeated; a refusal can be retried.
      void outcome.then((result) => {
        if (!result.ok && result.error !== 'timeout') cache.delete(promptId);
      });
      return outcome;
    },
  };
}
