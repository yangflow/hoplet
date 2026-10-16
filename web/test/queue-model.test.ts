import type { Queue, QueueItem } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  inOrder,
  movableIds,
  moveId,
  slotAt,
  trayPause,
} from '../src/screens/cockpit/queue/model.ts';

const item = (id: string, state: QueueItem['state'] = 'queued'): QueueItem => ({
  id,
  text: id,
  attachments: [],
  version: 1,
  state,
});

describe('queue model', () => {
  const items = [item('a', 'sending'), item('b'), item('c'), item('d')];

  it('keeps the sending item out of what can move', () => {
    expect(movableIds(items)).toEqual(['b', 'c', 'd']);
  });

  it('moves one id and ignores positions outside the list', () => {
    expect(moveId(['b', 'c', 'd'], 2, 0)).toEqual(['d', 'b', 'c']);
    expect(moveId(['b', 'c'], 0, 5)).toEqual(['b', 'c']);
    expect(moveId(['b', 'c'], 1, 1)).toEqual(['b', 'c']);
  });

  it('orders items by ids with the sending item first and unnamed items last', () => {
    expect(inOrder(items, ['d', 'b']).map((i) => i.id)).toEqual(['a', 'd', 'b', 'c']);
  });

  it('finds the row nearest to the pointer', () => {
    expect(slotAt([10, 30, 50], 34)).toBe(1);
    expect(slotAt([10, 30, 50], 100)).toBe(2);
  });

  it('says why the tray is paused', () => {
    const q: Queue = { items: [item('b')] };
    expect(trayPause(q, false)).toBeNull();
    expect(trayPause(q, true)).toBe('permission');
    expect(trayPause({ items: [] }, true)).toBeNull();
    expect(trayPause({ ...q, paused: 'error' }, true)).toBe('error');
    expect(trayPause({ ...q, paused: 'interrupted' }, false)).toBe('interrupted');
  });
});
