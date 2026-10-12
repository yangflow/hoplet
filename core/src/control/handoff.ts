import { randomUUID } from 'node:crypto';
import { promisify } from 'node:util';
import { gzip as gzipCallback } from 'node:zlib';
import {
  type ChunkEncoding,
  type HandoffPart,
  type HandoffStep,
  isSessionId,
  PROTOCOL_VERSION,
  SNAPSHOT_CHUNK_BYTES,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import type { SnapshotRecord } from '../store/index.ts';
import { askWaiter, type Waiter, waitFor } from './pending.ts';
import type { ControlState } from './state.ts';
import type {
  Control,
  DeviceLink,
  HandoffError,
  HandoffOutcome,
  HandoffRequest,
  InstalledInfo,
  StepFailure,
  StepOutcome,
} from './types.ts';

const gzip = promisify(gzipCallback);

/** The refusal or failure of a handoff step; the handoff ends with it and the target removes what it has. */
class HandoffFailure extends Error {
  readonly code: HandoffError;
  readonly reason: string | undefined;
  constructor(code: HandoffError, reason?: string) {
    super(code);
    this.code = code;
    this.reason = reason;
  }
}

/** A handoff of a session: who moves it where, and the links the steps talk over. */
export interface ActiveHandoff {
  handoffId: string;
  ownerDeviceId: string;
  targetDeviceId: string;
  /** Set once the owner was found online; null for an offline handoff. */
  owner: DeviceLink | null;
  target: DeviceLink;
}

function failureOf(outcome: StepFailure, device: 'owner' | 'target'): HandoffFailure {
  switch (outcome.error) {
    case 'timeout':
      return new HandoffFailure('timeout');
    case 'offline':
      return new HandoffFailure(device === 'owner' ? 'owner-offline' : 'target-offline');
    case 'rejected':
      return outcome.reason === 'no-clone'
        ? new HandoffFailure('no-clone')
        : new HandoffFailure('rejected', outcome.reason);
  }
}

/** Transfer progress goes to browsers at most this often. */
const TRANSFER_PROGRESS_MS = 250;

/** What a progress message carries besides its step. */
type ProgressExtra = {
  snapshotAt?: number | undefined;
  sent?: number;
  total?: number;
  worktree?: string;
  branch?: string;
  reason?: string;
};

/** Resolves when `check` holds, or fails with `code` once `ms` have passed. */
async function eventually(
  check: () => boolean,
  ms: number,
  code: HandoffError,
  reason: string,
): Promise<void> {
  const deadline = Date.now() + ms;
  while (!check()) {
    if (Date.now() > deadline) throw new HandoffFailure(code, reason);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}

/** Settles with the failure when `promise` has not finished after `ms`. */
function within<T>(promise: Promise<T>, ms: number, code: HandoffError): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const expired = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new HandoffFailure(code)), ms);
  });
  return Promise.race([promise, expired]).finally(() => clearTimeout(timer));
}

export type HandoffControl = Pick<Control, 'handoff'> & {
  /** A device lost its connection: a handoff waiting for it fails. */
  detached(link: DeviceLink): void;
};

/**
 * The handoff state machine (docs/spec.md#handoff): prepare, transfer, install, verify, commit. Ownership
 * moves only in the commit, in one statement that checks owner, cursor and epoch; any failure before it
 * aborts the target and leaves owner and cursor as they were.
 */
export function createHandoff(state: ControlState): HandoffControl {
  const { store, byDevice, handoffs, pendingPrepared, pendingInstalled, pendingVerified } = state;

  const online = (device: string): DeviceLink | undefined => {
    const link = byDevice.get(device);
    return link?.isOpen() ? link : undefined;
  };

  function progress(
    sessionId: string,
    active: ActiveHandoff,
    step: HandoffStep,
    extra: ProgressExtra = {},
  ): void {
    const defined = Object.fromEntries(
      Object.entries(extra).filter(([, value]) => value !== undefined),
    ) as Omit<ProgressExtra, 'snapshotAt'> & { snapshotAt?: number };
    state.publish({
      v: PROTOCOL_VERSION,
      type: 'handoff.progress',
      sessionId,
      handoffId: active.handoffId,
      step,
      targetDeviceId: active.targetDeviceId,
      ...defined,
    });
  }

  async function ask<T>(
    map: Map<string, Waiter<StepOutcome<T>>>,
    link: DeviceLink,
    sessionId: string,
    handoffId: string,
    device: 'owner' | 'target',
    send: () => void,
  ): Promise<T> {
    const outcome = await askWaiter<StepOutcome<T>>(
      map,
      handoffId,
      state.handoffTimeoutMs,
      { ok: false, error: 'timeout' },
      send,
      { link, sessionId },
    );
    if (!outcome.ok) throw failureOf(outcome, device);
    return outcome.value;
  }

  /** Sends one part of the transfer as ordered chunks of at most 1 MiB; the last one says so. */
  async function sendPart(
    link: DeviceLink,
    transfer: { stopped: boolean; sent: (bytes: number) => void },
    sessionId: string,
    handoffId: string,
    part: HandoffPart,
    pieces: AsyncIterable<Buffer>,
    encoding: ChunkEncoding,
  ): Promise<void> {
    let seq = 0;
    const emit = async (piece: Buffer, last: boolean): Promise<void> => {
      if (transfer.stopped) throw new HandoffFailure('cancelled');
      const data = (encoding === 'gzip+base64' ? await gzip(piece) : piece).toString('base64');
      await link.sendAndWait({
        v: PROTOCOL_VERSION,
        type: 'handoff.data',
        sessionId,
        handoffId,
        part,
        seq: seq++,
        last,
        encoding,
        data,
      });
      transfer.sent(piece.length);
    };
    let held: Buffer | null = null;
    for await (const piece of pieces) {
      if (held) await emit(held, false);
      held = piece;
    }
    if (!held) throw new HandoffFailure('internal', `empty ${part}`);
    await emit(held, true);
  }

  async function run(
    sessionId: string,
    request: HandoffRequest,
    active: ActiveHandoff,
  ): Promise<Extract<HandoffOutcome, { ok: true }>> {
    const { handoffId, target } = active;
    const owner = active.ownerDeviceId;
    let snapshot: SnapshotRecord;
    let snapshotAt: number | undefined;

    // Prepare: the owner stops its process and takes a fresh snapshot, or the latest stored one is used.
    progress(sessionId, active, 'prepare');
    if (active.owner) {
      const prepared = await ask(pendingPrepared, active.owner, sessionId, handoffId, 'owner', () =>
        active.owner?.send({ v: PROTOCOL_VERSION, type: 'handoff.prepare', sessionId, handoffId }),
      );
      // The final append and the snapshot travel on the bulk lane; wait until the store holds both.
      await eventually(
        () =>
          store.getSession(sessionId)?.byteSize === prepared.byteSize &&
          store.latestSnapshot(sessionId)?.snapshotId === prepared.snapshotId,
        state.handoffTimeoutMs,
        'timeout',
        'the final append or the snapshot did not arrive',
      );
      const stored = store.latestSnapshot(sessionId);
      if (!stored) throw new HandoffFailure('no-snapshot');
      snapshot = stored;
    } else {
      const stored = store.latestSnapshot(sessionId);
      if (!stored) throw new HandoffFailure('no-snapshot');
      snapshot = stored;
      snapshotAt = stored.createdAt;
      progress(sessionId, active, 'prepare', { snapshotAt });
    }

    // The cursor, the epoch and the fingerprint of the stream the target must end up with.
    const meta = store.getSession(sessionId);
    if (!meta || meta.ownerDeviceId !== owner) throw new HandoffFailure('session-changed');
    const cursor = meta.byteSize;
    const epoch = meta.ownerEpoch;
    const fingerprint = await store.streamFingerprint(sessionId);
    if (!fingerprint || fingerprint.byteSize !== cursor)
      throw new HandoffFailure('session-changed');
    if (cursor === 0) throw new HandoffFailure('session-changed', 'the session is empty');

    // Transfer: the install message, then the snapshot archive and the session stream. The target answers
    // when the last chunk is in, so its answer is awaited from the start; its deadline starts at the end.
    // `total` counts the archive and the stream as stored, before the stream is compressed for the wire.
    const total = snapshot.byteSize + cursor;
    let sent = 0;
    let reported = 0;
    progress(sessionId, active, 'transfer', { snapshotAt, sent, total });
    const answer = waitFor<StepOutcome<InstalledInfo>>(
      pendingInstalled,
      handoffId,
      { ok: false, error: 'timeout' },
      { link: target, sessionId },
    );
    const transfer = {
      stopped: false,
      sent: (bytes: number) => {
        // A chunk that was on its way when the handoff failed reports nothing after the failure.
        if (transfer.stopped) return;
        sent += bytes;
        const now = state.now();
        if (sent < total && now - reported < TRANSFER_PROGRESS_MS) return;
        reported = now;
        progress(sessionId, active, 'transfer', { snapshotAt, sent, total });
      },
    };
    const sending = (async () => {
      target.send({
        v: PROTOCOL_VERSION,
        type: 'handoff.install',
        sessionId,
        handoffId,
        ...(request.targetPath !== undefined ? { targetPath: request.targetPath } : {}),
        snapshotBytes: snapshot.byteSize,
        snapshotSha256: snapshot.sha256,
        sessionBytes: cursor,
        sessionSha256: fingerprint.streamSha256,
      });
      await sendPart(
        target,
        transfer,
        sessionId,
        handoffId,
        'snapshot',
        store.readSnapshot(sessionId, snapshot.snapshotId),
        'base64',
      );
      await sendPart(
        target,
        transfer,
        sessionId,
        handoffId,
        'session',
        store.readStream(sessionId, cursor, SNAPSHOT_CHUNK_BYTES),
        'gzip+base64',
      );
    })();
    // A target that refuses early ends the transfer; a transfer that fails is reported as what it is.
    const early = answer.promise.then((outcome) => {
      if (!outcome.ok) throw failureOf(outcome, 'target');
    });
    sending.catch(() => {});
    early.catch(() => {});
    try {
      await within(Promise.race([sending, early]), state.transferTimeoutMs, 'timeout');
    } catch (err) {
      transfer.stopped = true;
      if (err instanceof HandoffFailure) throw err;
      if (!target.isOpen()) throw new HandoffFailure('target-offline');
      throw new HandoffFailure('session-changed', err instanceof Error ? err.message : String(err));
    }
    answer.arm(state.handoffTimeoutMs);

    progress(sessionId, active, 'install', { snapshotAt });
    const installedOutcome = await answer.promise;
    if (!installedOutcome.ok) throw failureOf(installedOutcome, 'target');
    const { worktree, branch } = installedOutcome.value;

    // Verify: the target proves its copy before ownership moves.
    progress(sessionId, active, 'verify');
    const verified = await ask(pendingVerified, target, sessionId, handoffId, 'target', () =>
      target.send({ v: PROTOCOL_VERSION, type: 'handoff.verify', sessionId, handoffId }),
    );
    if (
      verified.sessionBytes !== cursor ||
      verified.sessionSha256 !== fingerprint.streamSha256 ||
      verified.treeHash !== snapshot.treeHash
    ) {
      throw new HandoffFailure('verify-failed');
    }

    // Commit: no await from the last check through the owner update.
    progress(sessionId, active, 'commit');
    if (online(active.targetDeviceId) !== target) throw new HandoffFailure('target-offline');
    const newEpoch = store.handoffOwner(
      sessionId,
      owner,
      active.targetDeviceId,
      cursor,
      epoch,
      worktree,
    );
    if (newEpoch === null) throw new HandoffFailure('session-changed');
    const commit = {
      v: PROTOCOL_VERSION,
      type: 'handoff.commit',
      sessionId,
      handoffId,
      ownerDeviceId: active.targetDeviceId,
      ownerEpoch: newEpoch,
      byteSize: cursor,
    } as const;
    target.send(commit);
    online(owner)?.send(commit);
    return {
      ok: true,
      ownerDeviceId: active.targetDeviceId,
      ownerEpoch: newEpoch,
      worktree,
      branch,
      ...(snapshotAt !== undefined ? { snapshotAt } : {}),
    };
  }

  function failAll(link: DeviceLink): void {
    for (const map of [pendingPrepared, pendingInstalled, pendingVerified]) {
      for (const [id, waiter] of [...map]) {
        if (waiter.link !== link) continue;
        map.delete(id);
        clearTimeout(waiter.timer);
        waiter.resolve({ ok: false, error: 'offline' });
      }
    }
  }

  return {
    detached: failAll,

    async handoff(request) {
      const { sessionId, targetDeviceId } = request;
      if (!isSessionId(sessionId))
        return { ok: false, error: 'rejected', reason: 'invalid-session-id' };
      const meta = store.getSession(sessionId);
      const owner = meta?.ownerDeviceId;
      if (!meta || !owner) return { ok: false, error: 'session-not-found' };
      if (owner === targetDeviceId) return { ok: false, error: 'same-device' };
      if (handoffs.has(sessionId)) return { ok: false, error: 'handoff-in-progress' };
      const target = online(targetDeviceId);
      if (!target) return { ok: false, error: 'target-offline' };
      const ownerLink = online(owner);
      if (ownerLink) {
        const entry = state.runtime.get(sessionId);
        const running = entry?.deviceId === owner ? entry.state : 'idle';
        if (running === 'running' || running === 'waiting-permission') {
          return { ok: false, error: 'busy' };
        }
      }
      const active: ActiveHandoff = {
        handoffId: randomUUID(),
        ownerDeviceId: owner,
        targetDeviceId,
        owner: ownerLink ?? null,
        target,
      };
      handoffs.set(sessionId, active);
      log('info', 'control', 'handoff started', {
        sessionId,
        handoffId: active.handoffId,
        online: !!ownerLink,
      });
      try {
        const done = await run(sessionId, request, active);
        progress(sessionId, active, 'done', {
          snapshotAt: done.snapshotAt,
          worktree: done.worktree,
          branch: done.branch,
        });
        log('info', 'control', 'handoff committed', { sessionId, handoffId: active.handoffId });
        return done;
      } catch (err) {
        const failure =
          err instanceof HandoffFailure ? err : new HandoffFailure('internal', String(err));
        const reason = failure.reason ?? failure.code;
        log('warn', 'control', 'handoff failed', {
          sessionId,
          handoffId: active.handoffId,
          code: failure.code,
          reason,
        });
        const abort = {
          v: PROTOCOL_VERSION,
          type: 'handoff.abort',
          sessionId,
          handoffId: active.handoffId,
          reason,
        } as const;
        online(targetDeviceId)?.send(abort);
        online(owner)?.send(abort);
        progress(sessionId, active, 'failed', { reason });
        return {
          ok: false,
          error: failure.code,
          ...(failure.reason !== undefined ? { reason: failure.reason } : {}),
        };
      } finally {
        handoffs.delete(sessionId);
        for (const map of [pendingPrepared, pendingInstalled, pendingVerified]) {
          const waiter = map.get(active.handoffId);
          if (!waiter) continue;
          map.delete(active.handoffId);
          clearTimeout(waiter.timer);
        }
      }
    },
  };
}
