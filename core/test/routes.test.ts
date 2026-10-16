import { describe, expect, it } from 'vitest';
import { routes } from '../src/api/routes.ts';

describe('the route table', () => {
  it('declares each method and path once, under /api', () => {
    const keys = routes.map((r) => `${r.method} ${r.path}`);
    expect(new Set(keys).size).toBe(keys.length);
    for (const r of routes) expect(r.path.startsWith('/api/')).toBe(true);
  });

  it('gives every route authentication and a response schema, and every body a limit', () => {
    for (const r of routes) {
      expect(r.auth, r.path).toBe('host');
      expect(r.response, r.path).toBeDefined();
      if (r.body) expect(r.bodyLimit, r.path).toBeGreaterThan(0);
      if (r.method === 'GET') expect(r.body, r.path).toBeUndefined();
    }
  });

  it('keeps the specific session suffixes apart from the bare session path', () => {
    const match = (p: string) => routes.filter((r) => r.pattern.test(p)).map((r) => r.path);
    expect(match('/api/sessions/s1/workspace/file')).toEqual([
      '/api/sessions/:sessionId/workspace/file',
    ]);
    expect(match('/api/sessions/s1')).toEqual([
      '/api/sessions/:sessionId',
      '/api/sessions/:sessionId',
    ]);
    expect(routes.filter((r) => r.pattern.test('/api/sessions/s1')).map((r) => r.method)).toEqual([
      'GET',
      'PATCH',
    ]);
    expect(
      routes.filter((r) => r.pattern.test('/api/sessions/s1/queue/q1')).map((r) => r.method),
    ).toEqual(['PATCH', 'DELETE']);
    expect(match('/api/unknown')).toEqual([]);
  });
});
