import type { SessionMcpSettings } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  mcpOff,
  projectName,
  sendOn,
  withAutoApprove,
  withCreate,
  withHandoff,
  withMachine,
  withoutMachine,
  withSend,
} from '../src/lib/session-mcp.ts';

const base: SessionMcpSettings = {
  send: false,
  create: false,
  requestHandoff: false,
  autoApproveHandoff: false,
  autoApproveMachines: [],
  visibility: 'project',
};

describe('session MCP switches', () => {
  it('start implies send, and send off turns start off', () => {
    const started = withCreate(base, true);
    expect(started).toMatchObject({ create: true, send: true });
    expect(sendOn({ ...base, create: true })).toBe(true);
    expect(withSend(started, false)).toMatchObject({ create: false, send: false });
    expect(withCreate(started, false)).toMatchObject({ create: false, send: true });
  });

  it('hand-off off clears automatic approval and its machines', () => {
    const on = withMachine(withAutoApprove(withHandoff(base, true), true), 'm1');
    expect(on).toMatchObject({ autoApproveHandoff: true, autoApproveMachines: ['m1'] });
    expect(withHandoff(on, false)).toMatchObject({
      requestHandoff: false,
      autoApproveHandoff: false,
      autoApproveMachines: [],
    });
    expect(withAutoApprove(base, true).autoApproveHandoff).toBe(false);
  });

  it('lists a machine once and removes it', () => {
    const a = withMachine(base, 'm1');
    expect(withMachine(a, 'm1')).toBe(a);
    expect(withoutMachine(a, 'm1').autoApproveMachines).toEqual([]);
  });
});

describe('helpers', () => {
  it('knows only a connected machine without session_mcp is off', () => {
    expect(mcpOff({ connected: true })).toBe(true);
    expect(mcpOff({ connected: true, sessionMcp: true })).toBe(false);
    expect(mcpOff({ connected: false })).toBe(false);
    expect(mcpOff(undefined)).toBe(false);
  });

  it('names a project by the last path part', () => {
    expect(projectName('/Users/mark/work/hoplet/')).toBe('hoplet');
    expect(projectName('C:\\src\\app')).toBe('app');
  });
});
