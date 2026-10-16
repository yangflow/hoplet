import type { Queue, QueueItem } from '@hoplet/shared';

/** The IDs of the items that may still move, in order: the item being sent stays at the head. */
export function movableIds(items: readonly QueueItem[]): string[] {
  return items.filter((i) => i.state !== 'sending').map((i) => i.id);
}

/** `ids` with the one at `from` moved to `to`; a position outside the list changes nothing. */
export function moveId(ids: readonly string[], from: number, to: number): string[] {
  const out = [...ids];
  if (from < 0 || from >= out.length || to < 0 || to >= out.length || from === to) return out;
  const [moved] = out.splice(from, 1);
  out.splice(to, 0, moved as string);
  return out;
}

/** The items in the order of `ids`, the sending item first; an item `ids` does not name keeps its place at the end. */
export function inOrder(items: readonly QueueItem[], ids: readonly string[]): QueueItem[] {
  const sending = items.filter((i) => i.state === 'sending');
  const rest = items.filter((i) => i.state !== 'sending');
  const rank = (i: QueueItem) => {
    const at = ids.indexOf(i.id);
    return at < 0 ? ids.length : at;
  };
  return [...sending, ...[...rest].sort((a, b) => rank(a) - rank(b))];
}

/** Where the pointer is among rows: the index of the row whose vertical middle is nearest to `y`. */
export function slotAt(middles: readonly number[], y: number): number {
  let best = 0;
  for (const [i, m] of middles.entries()) {
    if (Math.abs(m - y) < Math.abs((middles[best] as number) - y)) best = i;
  }
  return best;
}

export const EMPTY_QUEUE: Queue = { items: [] };

/** Why the tray is paused, as the user sees it: a stored pause, or a pending permission request. */
export type TrayPause = 'permission' | 'error' | 'interrupted' | null;

export function trayPause(queue: Queue, waitingPermission: boolean): TrayPause {
  if (queue.paused) return queue.paused;
  return waitingPermission && queue.items.length > 0 ? 'permission' : null;
}
