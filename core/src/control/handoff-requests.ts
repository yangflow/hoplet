// Hand-off requests of sessions (#67, ADR 0034): a session asks the user to move it to another machine. The request
// is answered by the user, or approved by the session's own settings (three an hour at most), and then waits for the
// end of the turn before `control.handoff` moves the session.
import { randomUUID } from 'node:crypto';
import {
  type AgentRuntimeState,
  HANDOFF_REQUEST_TTL_MS,
  MAX_SESSION_CHAIN,
  PROTOCOL_VERSION,
  type SessionHandoffRequest,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import type { AuditInput } from '../store/index.ts';
import type { ControlState } from './state.ts';
import type {
  AuditActor,
  Control,
  ControlEvent,
  ControlOptions,
  SessionHandoffRequests,
  SessionLimits,
  SessionMcp,
} from './types.ts';

const HOUR = 60 * 60_000;

/** What the module returns to control: the interface the API uses and the hooks control feeds. */
export interface HandoffRequestsModule {
  api: SessionHandoffRequests;
  onEvent(event: ControlEvent): void;
  /** A session's runtime state changed: an approved request runs once the session is `ready`. */
  runtimeChanged(sessionId: string, runtimeState: AgentRuntimeState): void;
  sessionGone(sessionId: string): void;
  /** The session moved by another hand-off: its open requests no longer apply. */
  sessionMoved(sessionId: string): void;
}

export function createHandoffRequests(
  state: ControlState,
  control: () => Pick<Control, 'ownerOf' | 'runtimeOf' | 'devices' | 'handoff'>,
  sessionMcp: SessionMcp,
  limits: SessionLimits,
  _options: ControlOptions,
): HandoffRequestsModule {
  const { store } = state;
  /** The requests whose hand-off is running, which the end of the old process must not expire. */
  const executing = new Set<string>();
  const timers = new Map<string, NodeJS.Timeout>();

  function publish(request: SessionHandoffRequest): void {
    state.publish({ v: PROTOCOL_VERSION, type: 'handoff.request', request });
  }

  function clearTimer(requestId: string): void {
    const timer = timers.get(requestId);
    if (timer) clearTimeout(timer);
    timers.delete(requestId);
  }

  function audit(
    type: 'handoff.requested' | 'handoff.decided',
    request: SessionHandoffRequest,
    actor: AuditActor | { kind: 'core' } | undefined,
    summary: Record<string, unknown>,
  ): AuditInput {
    return {
      type,
      at: state.now(),
      sessionId: request.sessionId,
      deviceId: request.targetDeviceId,
      ...(actor ? { actor } : {}),
      ref: request.requestId,
      summary: { requestId: request.requestId, ...summary },
    };
  }

  function arm(request: SessionHandoffRequest): void {
    clearTimer(request.requestId);
    const delay = Math.max(0, request.expiresAt - state.now());
    const timer = setTimeout(() => expire(request.requestId, ['pending']), delay);
    timer.unref();
    timers.set(request.requestId, timer);
  }

  /** Closes a request that nobody answered, or whose session stopped or moved. */
  function expire(requestId: string, from: readonly SessionHandoffRequest['state'][]): void {
    clearTimer(requestId);
    if (executing.has(requestId)) return;
    try {
      const closed = store.changeHandoffRequest(requestId, from, { state: 'expired' }, null);
      if (closed) publish(closed);
    } catch (err) {
      log('error', 'control', 'hand-off request expiry failed', { error: String(err) });
    }
  }

  function expireOpenOf(sessionId: string): void {
    for (const request of store.handoffRequests(sessionId)) {
      if (request.state === 'pending' || request.state === 'approved') {
        expire(request.requestId, ['pending', 'approved']);
      }
    }
  }

  /** Runs the approved request of the session when it is ready. */
  function settle(sessionId: string): void {
    const request = store
      .handoffRequests(sessionId)
      .find((r) => r.state === 'approved' && !executing.has(r.requestId));
    if (!request || control().runtimeOf(sessionId).state !== 'ready') return;
    void run(request);
  }

  async function run(request: SessionHandoffRequest): Promise<void> {
    executing.add(request.requestId);
    try {
      // A session's own request never copies project rules: that is the owner's choice (ADR 0037).
      const outcome = await control().handoff({
        sessionId: request.sessionId,
        targetDeviceId: request.targetDeviceId,
      });
      const closed = outcome.ok
        ? store.changeHandoffRequest(request.requestId, ['approved'], { state: 'done' }, null)
        : store.changeHandoffRequest(
            request.requestId,
            ['approved'],
            {
              state: 'failed',
              error: outcome.error,
              errorReason: outcome.reason ?? outcome.detail ?? null,
            },
            null,
          );
      if (closed) publish(closed);
    } catch (err) {
      log('error', 'control', 'hand-off request failed', { error: String(err) });
      try {
        const closed = store.changeHandoffRequest(
          request.requestId,
          ['approved'],
          { state: 'failed', error: 'internal' },
          null,
        );
        if (closed) publish(closed);
      } catch {
        // The request stays approved and runs again at the next ready.
      }
    } finally {
      executing.delete(request.requestId);
    }
  }

  function request(
    sessionId: string,
    targetDeviceId: string,
    reason: string,
  ): ReturnType<SessionHandoffRequests['request']> {
    const settings = sessionMcp.settingsOf(sessionId);
    if (!settings.requestHandoff) return { ok: false, error: 'forbidden', reason: 'not-allowed' };
    const owner = control().ownerOf(sessionId);
    if (owner === null) return { ok: false, error: 'not-found' };
    if (owner === targetDeviceId) return { ok: false, error: 'same-device' };
    const target = control()
      .devices()
      .find((d) => d.deviceId === targetDeviceId);
    if (!target) return { ok: false, error: 'not-found' };
    if (!target.connected) return { ok: false, error: 'target-offline' };
    if ([...store.sessionChain(sessionId), sessionId].length > MAX_SESSION_CHAIN) {
      return { ok: false, error: 'loop-limit' };
    }

    const at = state.now();
    const mine = store.handoffRequests(sessionId);
    if (mine.some((r) => r.state === 'pending' || r.state === 'approved')) {
      return { ok: false, error: 'forbidden', reason: 'request-open' };
    }
    const recent = mine.filter((r) => at - r.createdAt < HOUR);
    if (recent.length >= limits.handoffRequestsPerHour) {
      const oldest = recent[recent.length - 1]?.createdAt ?? at;
      return {
        ok: false,
        error: 'rate-limited',
        retryAfter: Math.max(1, Math.ceil((oldest + HOUR - at) / 1000)),
      };
    }

    const created: SessionHandoffRequest = {
      requestId: randomUUID(),
      sessionId,
      targetDeviceId,
      reason,
      state: 'pending',
      decision: null,
      createdAt: at,
      expiresAt: at + HANDOFF_REQUEST_TTL_MS,
      decidedAt: null,
    };
    let stored = store.insertHandoffRequest(
      created,
      audit(
        'handoff.requested',
        created,
        { kind: 'session', id: sessionId },
        {
          target: targetDeviceId,
        },
      ),
    );

    // The user stays in the loop unless the session's settings and the hourly limit say otherwise.
    const automatic =
      settings.autoApproveHandoff &&
      settings.autoApproveMachines.includes(targetDeviceId) &&
      store.autoApprovedHandoffs(sessionId, at - HOUR) < limits.autoApprovedHandoffsPerHour;
    if (automatic) {
      const approved = store.changeHandoffRequest(
        stored.requestId,
        ['pending'],
        { state: 'approved', decision: 'auto', decidedAt: at },
        audit('handoff.decided', stored, { kind: 'core' }, { decision: 'auto', approved: true }),
      );
      if (approved) stored = approved;
    }
    publish(stored);
    if (stored.state === 'pending') {
      arm(stored);
      state.emit({
        kind: 'handoff-requested',
        sessionId,
        requestId: stored.requestId,
        targetName: target.name,
      });
    } else {
      settle(sessionId);
    }
    return { ok: true, request: stored };
  }

  const api: SessionHandoffRequests = {
    request,
    list: (sessionId) => store.handoffRequests(sessionId),
    get: (requestId) => store.handoffRequest(requestId),
    decide(requestId, approve, actor) {
      const current = store.handoffRequest(requestId);
      if (!current) return { ok: false, error: 'not-found' };
      const at = state.now();
      if (current.state !== 'pending') return { ok: false, error: 'request-closed' };
      if (current.expiresAt <= at) {
        expire(requestId, ['pending']);
        return { ok: false, error: 'request-closed' };
      }
      const decided = store.changeHandoffRequest(
        requestId,
        ['pending'],
        { state: approve ? 'approved' : 'declined', decision: 'user', decidedAt: at },
        audit('handoff.decided', current, actor, { decision: 'user', approved: approve }),
      );
      if (!decided) return { ok: false, error: 'request-closed' };
      clearTimer(requestId);
      publish(decided);
      if (approve) settle(decided.sessionId);
      return { ok: true, request: decided };
    },
    close() {
      for (const timer of timers.values()) clearTimeout(timer);
      timers.clear();
    },
  };

  // Requests that were open when the core stopped keep their expiry.
  for (const open of store.openHandoffRequests()) {
    if (open.state === 'pending') arm(open);
  }

  return {
    api,
    onEvent(event) {
      if (event.kind === 'ended' || event.kind === 'runtime-error') expireOpenOf(event.sessionId);
    },
    runtimeChanged(sessionId, runtimeState) {
      if (runtimeState === 'ready') settle(sessionId);
    },
    sessionGone(sessionId) {
      for (const request of store.handoffRequests(sessionId)) clearTimer(request.requestId);
    },
    sessionMoved: expireOpenOf,
  };
}
