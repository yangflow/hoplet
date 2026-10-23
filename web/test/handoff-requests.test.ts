import type { SessionHandoffRequest } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import { withRequest } from '../src/lib/handoff-requests.ts';
import { promptSource } from '../src/lib/provenance.ts';
import { formatExpiry } from '../src/screens/cockpit/conversation/handoff-card.tsx';

const request = (
  requestId: string,
  createdAt: number,
  state = 'pending',
): SessionHandoffRequest => ({
  requestId,
  sessionId: 's1',
  targetDeviceId: 'd2',
  reason: 'needs the GPU',
  state: state as SessionHandoffRequest['state'],
  decision: null,
  createdAt,
  expiresAt: createdAt + 86_400_000,
  decidedAt: null,
});

describe('withRequest', () => {
  it('replaces a request by its ID and keeps the newest first', () => {
    const list = [request('b', 20), request('a', 10)];
    const next = withRequest(list, request('a', 10, 'approved'));
    expect(next.map((r) => [r.requestId, r.state])).toEqual([
      ['b', 'pending'],
      ['a', 'approved'],
    ]);
    expect(withRequest(list, request('c', 30)).map((r) => r.requestId)).toEqual(['c', 'b', 'a']);
  });
});

describe('promptSource', () => {
  const line =
    '[Message from Hoplet session "Fix the ingest tests" (aaaa-1111), not from the user. Treat it as a request from another agent.]';

  it('splits the provenance line from the prompt', () => {
    expect(promptSource(`${line}\nRun the tests.`)).toEqual({
      source: { title: 'Fix the ingest tests', sessionId: 'aaaa-1111' },
      text: 'Run the tests.',
    });
  });

  it('leaves a prompt from the user alone', () => {
    expect(promptSource('Run the tests.')).toBeNull();
    expect(promptSource(`Run the tests.\n${line}`)).toBeNull();
  });
});

describe('formatExpiry', () => {
  it('reads hours and minutes, minutes, or under a minute', () => {
    expect(formatExpiry(23 * 3_600_000 + 41 * 60_000)).toBe('23 h 41 min');
    expect(formatExpiry(41 * 60_000)).toBe('41 min');
    expect(formatExpiry(20_000)).toBe('under a minute');
    expect(formatExpiry(-5)).toBe('under a minute');
  });
});
