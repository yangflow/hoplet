// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { setToken, socketUrl } from '../src/lib/api.ts';

beforeEach(() => setToken('host-token-for-test'));
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

it('opens the live connection with a ticket minted by the token, never the token', async () => {
  const fetchMock = vi.fn(
    async () =>
      new Response(JSON.stringify({ ticket: 'abc123', expiresInSec: 30 }), {
        headers: { 'content-type': 'application/json' },
      }),
  );
  vi.stubGlobal('fetch', fetchMock);
  const url = await socketUrl();
  expect(url).toMatch(/^wss?:\/\/[^/]+\/ws\?ticket=abc123$/);
  expect(url).not.toContain('host-token-for-test');
  const [path, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
  expect(path).toBe('/api/ws-tickets');
  expect(init.method).toBe('POST');
  expect((init.headers as Record<string, string>).Authorization).toBe('Bearer host-token-for-test');
});
