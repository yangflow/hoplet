import type { TokenCapability } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import { can, type Principal } from '../src/api/principal.ts';
import { routes } from '../src/api/routes.ts';

describe('the route table', () => {
  it('declares each method and path once, under /api', () => {
    const keys = routes.map((r) => `${r.method} ${r.path}`);
    expect(new Set(keys).size).toBe(keys.length);
    for (const r of routes) expect(r.path.startsWith('/api/')).toBe(true);
  });

  it('gives every route an access, a response schema, and every body a limit', () => {
    for (const r of routes) {
      expect(r.access, r.path).toBe(r.path.startsWith('/api/action/') ? 'link' : r.access);
      if (r.path.startsWith('/api/action/')) expect(r.access, r.path).toBe('link');
      expect(r.response, r.path).toBeDefined();
      if (r.body) expect(r.bodyLimit, r.path).toBeGreaterThan(0);
      if (r.method === 'GET') expect(r.body, r.path).toBeUndefined();
    }
  });

  // The table of ADR 0033, written out on its own: a route not listed here is the host's.
  const ABILITY_ROUTES: Record<TokenCapability, string[]> = {
    read: [
      'POST /api/ws-tickets',
      'GET /api/health',
      'GET /api/sessions',
      'GET /api/sessions/:sessionId',
      'GET /api/sessions/:sessionId/lines',
      'GET /api/sessions/:sessionId/blocks/:line/:block',
      'GET /api/sessions/:sessionId/search',
      'GET /api/search',
      'GET /api/sessions/:sessionId/queue',
      'GET /api/devices',
      'GET /api/devices/:deviceId/projects',
    ],
    send: [
      'POST /api/sessions/:sessionId/prompt',
      'POST /api/sessions/:sessionId/queue',
      'POST /api/sessions/:sessionId/queue/order',
      'POST /api/sessions/:sessionId/queue/resume',
      'POST /api/sessions/:sessionId/queue/clear',
      'PATCH /api/sessions/:sessionId/queue/:itemId',
      'DELETE /api/sessions/:sessionId/queue/:itemId',
      'POST /api/sessions/:sessionId/queue/:itemId/steer',
      'POST /api/sessions/:sessionId/steer',
      'POST /api/sessions/:sessionId/interrupt',
      'POST /api/devices/:deviceId/attachments',
    ],
    create: ['POST /api/sessions', 'GET /api/templates', 'POST /api/templates/:id/start'],
    answer: [
      'POST /api/sessions/:sessionId/permission',
      'POST /api/sessions/:sessionId/permission/fresh',
    ],
    // Only a session token holds it, and no route asks for it yet.
    'request-handoff': [],
  };
  const PRESETS: Record<string, TokenCapability[]> = {
    'read only': ['read'],
    'may send': ['read', 'send'],
    'may create': ['read', 'send', 'create'],
    'may create and answer': ['read', 'send', 'create', 'answer'],
    'read and answer': ['read', 'answer'],
  };

  it('lists no ability route twice and no unknown route', () => {
    const keys = new Set(routes.map((r) => `${r.method} ${r.path}`));
    const listed = Object.values(ABILITY_ROUTES).flat();
    expect(new Set(listed).size).toBe(listed.length);
    for (const key of listed) expect(keys.has(key), key).toBe(true);
  });

  for (const [preset, capabilities] of Object.entries(PRESETS)) {
    it(`lets a token with "${preset}" reach exactly the routes of its abilities`, () => {
      const grant = { capabilities };
      const principal = { kind: 'token', token: { grant } } as unknown as Principal;
      const reached = routes
        .filter(
          (r) => r.access !== 'link' && r.access !== 'host' && can(principal, r.access.capability),
        )
        .map((r) => `${r.method} ${r.path}`)
        .sort();
      const expected = capabilities.flatMap((c) => ABILITY_ROUTES[c]).sort();
      expect(reached).toEqual(expected);
    });
  }

  it('keeps every other route for the host, and the action routes for their own credential', () => {
    const listed = new Set(Object.values(ABILITY_ROUTES).flat());
    for (const r of routes) {
      const key = `${r.method} ${r.path}`;
      if (listed.has(key)) continue;
      expect(r.access, key).toBe(r.path.startsWith('/api/action/') ? 'link' : 'host');
    }
  });

  it('lets a session token call only the routes that read and direct other sessions', () => {
    const marked = routes
      .filter((r) => r.session)
      .map((r) => `${r.method} ${r.path}`)
      .sort();
    expect(marked).toEqual(
      [
        'GET /api/devices',
        'GET /api/sessions',
        'GET /api/sessions/:sessionId',
        'GET /api/sessions/:sessionId/lines',
        'GET /api/sessions/:sessionId/blocks/:line/:block',
        'POST /api/sessions',
        'POST /api/sessions/:sessionId/prompt',
        'POST /api/sessions/:sessionId/interrupt',
        'POST /api/sessions/:sessionId/queue',
      ].sort(),
    );
    for (const r of routes.filter((x) => x.session)) {
      expect(r.access, r.path).not.toBe('host');
      expect(r.access, r.path).not.toBe('link');
    }
  });

  it('keeps the settings of sessions that direct sessions with the host', () => {
    const access = (method: string, path: string) =>
      routes.find((r) => r.method === method && r.path === path)?.access;
    for (const [method, path] of [
      ['GET', '/api/sessions/:sessionId/mcp'],
      ['PUT', '/api/sessions/:sessionId/mcp'],
      ['GET', '/api/mcp/defaults'],
      ['PUT', '/api/mcp/defaults'],
      ['DELETE', '/api/mcp/defaults'],
    ] as const) {
      expect(access(method, path), `${method} ${path}`).toBe('host');
    }
  });

  it('keeps bypassed permissions, deletion and the token routes with the host', () => {
    const host = (method: string, path: string) =>
      routes.find((r) => r.method === method && r.path === path)?.access;
    expect(host('POST', '/api/sessions/:sessionId/config')).toBe('host');
    expect(host('DELETE', '/api/sessions/:sessionId')).toBe('host');
    expect(host('GET', '/api/tokens')).toBe('host');
    expect(host('POST', '/api/tokens')).toBe('host');
    expect(host('DELETE', '/api/tokens/:id')).toBe('host');
  });

  it('keeps the specific session suffixes apart from the bare session path', () => {
    const match = (p: string) => routes.filter((r) => r.pattern.test(p)).map((r) => r.path);
    expect(match('/api/sessions/s1/workspace/file')).toEqual([
      '/api/sessions/:sessionId/workspace/file',
    ]);
    expect(match('/api/sessions/s1')).toEqual([
      '/api/sessions/:sessionId',
      '/api/sessions/:sessionId',
      '/api/sessions/:sessionId',
    ]);
    expect(routes.filter((r) => r.pattern.test('/api/sessions/s1')).map((r) => r.method)).toEqual([
      'GET',
      'PATCH',
      'DELETE',
    ]);
    expect(
      routes.filter((r) => r.pattern.test('/api/sessions/s1/queue/q1')).map((r) => r.method),
    ).toEqual(['PATCH', 'DELETE']);
    expect(match('/api/unknown')).toEqual([]);
  });
});
