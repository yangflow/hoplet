import * as v from 'valibot';
import { describe, expect, it } from 'vitest';
import { HTTP_ERROR_STATUS } from '../src/errors.ts';
import {
  DEFAULT_SESSION_MCP_SETTINGS,
  MAX_SESSION_CHAIN,
  SessionMcpDefaultsPutRequestSchema,
  SessionMcpPutRequestSchema,
  SessionMcpSettingsSchema,
} from '../src/rest/index.ts';

const ON = {
  send: true,
  create: false,
  requestHandoff: false,
  autoApproveHandoff: false,
  autoApproveMachines: [],
  visibility: 'machine',
  readConfig: true,
  writeConfig: false,
} as const;

describe('session MCP settings', () => {
  it('start with everything off and the project in view', () => {
    expect(DEFAULT_SESSION_MCP_SETTINGS).toEqual({
      send: false,
      create: false,
      requestHandoff: false,
      autoApproveHandoff: false,
      autoApproveMachines: [],
      visibility: 'project',
      readConfig: true,
      writeConfig: false,
    });
    expect(MAX_SESSION_CHAIN).toBe(3);
  });

  it('refuse an unknown visibility or an extra property', () => {
    expect(v.safeParse(SessionMcpSettingsSchema, ON).success).toBe(true);
    expect(v.safeParse(SessionMcpSettingsSchema, { ...ON, visibility: 'world' }).success).toBe(
      false,
    );
    expect(v.safeParse(SessionMcpSettingsSchema, { ...ON, answer: true }).success).toBe(false);
  });

  it('hold machines for automatic hand-off approval', () => {
    const ok = (autoApproveMachines: unknown) =>
      v.safeParse(SessionMcpSettingsSchema, { ...ON, autoApproveMachines }).success;
    expect(ok(['dev-a', 'dev-b'])).toBe(true);
    expect(ok([1])).toBe(false);
    expect(ok(Array.from({ length: 33 }, (_, i) => `m${i}`))).toBe(false);
  });

  it('are put as a whole', () => {
    expect(v.safeParse(SessionMcpPutRequestSchema, { settings: ON }).success).toBe(true);
    expect(v.safeParse(SessionMcpPutRequestSchema, {}).success).toBe(false);
  });

  it('take a default for everything or for a project', () => {
    const parse = (body: unknown) => v.safeParse(SessionMcpDefaultsPutRequestSchema, body).success;
    expect(parse({ settings: ON })).toBe(true);
    expect(parse({ deviceId: 'dev-a', path: '/work/a', settings: ON })).toBe(true);
    expect(parse({ deviceId: 'dev-a', path: '/work/a', settings: ON, extra: 1 })).toBe(false);
  });
});

describe('error codes of sessions as callers', () => {
  it('have an HTTP status', () => {
    expect(HTTP_ERROR_STATUS.self).toBe(400);
    expect(HTTP_ERROR_STATUS['loop-limit']).toBe(409);
  });
});
