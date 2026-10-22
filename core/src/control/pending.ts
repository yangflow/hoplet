import type { Provider } from '@hoplet/shared';
import type { DeviceLink } from './types.ts';

/** Request/reply waiters for control round-trips over a device link; each ends in a reply or its timeout. */

export type Waiter<T> = {
  resolve: (v: T) => void;
  link?: DeviceLink;
  sessionId?: string;
  /** Set on a new-session request: who was asked to start it, and with which provider. */
  start?: { deviceId: string; provider: Provider; projectPath: string } | undefined;
  /** Absent while the waiter waits without a deadline (see `waitFor`). */
  timer?: ReturnType<typeof setTimeout>;
};

export function settleWaiter<T>(map: Map<string, Waiter<T>>, id: string, outcome: T): void {
  const wait = map.get(id);
  if (!wait) return;
  map.delete(id);
  clearTimeout(wait.timer);
  wait.resolve(outcome);
}

export function askWaiter<T>(
  map: Map<string, Waiter<T>>,
  id: string,
  timeoutMs: number,
  onTimeout: T,
  send: () => void,
  binding?: { link: DeviceLink; sessionId: string; start?: Waiter<T>['start'] },
): Promise<T> {
  return new Promise((resolve) => {
    map.set(id, {
      ...binding,
      resolve,
      timer: setTimeout(() => settleWaiter(map, id, onTimeout), timeoutMs),
    });
    send();
  });
}

export function matchesWaiter<T>(
  map: Map<string, Waiter<T>>,
  id: string,
  link: DeviceLink,
  sessionId: string,
): boolean {
  const wait = map.get(id);
  return !!wait && wait.link === link && wait.sessionId === sessionId;
}

/**
 * A waiter that has no deadline until `arm` is called: the answer is awaited before the request that
 * provokes it is complete (a long transfer), and the clock starts when the request is.
 */
export function waitFor<T>(
  map: Map<string, Waiter<T>>,
  id: string,
  onTimeout: T,
  binding: { link: DeviceLink; sessionId: string },
): { promise: Promise<T>; arm(ms: number): void } {
  let waiter: Waiter<T> | undefined;
  const promise = new Promise<T>((resolve) => {
    waiter = { ...binding, resolve };
    map.set(id, waiter);
  });
  return {
    promise,
    arm(ms) {
      if (!waiter || map.get(id) !== waiter) return;
      clearTimeout(waiter.timer);
      waiter.timer = setTimeout(() => settleWaiter(map, id, onTimeout), ms);
    },
  };
}
