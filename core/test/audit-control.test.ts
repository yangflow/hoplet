import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { type AuditEvent, PROTOCOL_VERSION, type ServerMessage } from '@hoplet/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { type ControlReply, createControl, type DeviceLink } from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';

/** How long a permission request stays open (the core's ten minutes). */
const PERMISSION_OPEN_MS = 10 * 60_000;
const dirs: string[] = [];
afterEach(() => {
  vi.useRealTimers();
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function fakeLink(): DeviceLink & { sent: ServerMessage[] } {
  const link = {
    sent: [] as ServerMessage[],
    send(msg: ServerMessage) {
      link.sent.push(msg);
    },
    async sendAndWait(msg: ServerMessage) {
      link.sent.push(msg);
    },
    isOpen: () => true,
  };
  return link;
}

const state = (sessionId: string, s: string, steer = true) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'runtime.state',
    sessionId,
    state: s,
    capabilities: { permissions: true, interrupt: true, steer, config: [] },
  }) as unknown as ControlReply;

const permission = (requestId: string, tool = 'Bash', input: unknown = { command: 'ls' }) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'runtime.permission',
    sessionId: 's1',
    requestId,
    tool,
    toolUseId: 'tu1',
    input,
  }) as unknown as ControlReply;

function setup() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-audit-control-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.append(
    { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'dev-a' },
    ['{"type":"user"}'],
  );
  const control = createControl(store, {
    promptTimeoutMs: 100,
    terminalDetachMs: 60_000,
  });
  const link = fakeLink();
  control.attach('dev-a', link, [], { capabilities: { terminal: true }, previewHost: 'mac.local' });
  return { store, control, link };
}

const events = (store: Store, type: string): AuditEvent[] =>
  store.listAudit({ type }, { order: 'asc', limit: 100 }).events;

describe('permission requests', () => {
  it('records the request with the tool and the command, from the device', async () => {
    const { store, control, link } = setup();
    control.receive(link, state('s1', 'waiting-permission'));
    control.receive(link, permission('r1', 'Bash', { command: 'rm -rf build' }));
    control.receive(link, permission('r2', 'Edit', { file_path: '/w/a.ts', new_string: 'zzz' }));
    const [bash, edit] = events(store, 'permission.requested');
    expect(bash).toMatchObject({
      sessionId: 's1',
      deviceId: 'dev-a',
      actorKind: 'device',
      actorId: 'dev-a',
      channel: 'agent',
      ref: 'r1',
      summary: { tool: 'Bash', planExit: false, command: 'rm -rf build' },
    });
    expect(edit?.summary).toEqual({ tool: 'Edit', planExit: false, paths: ['/w/a.ts'] });
    expect(JSON.stringify(edit)).not.toContain('zzz');
    expect(await store.verifyAudit()).toMatchObject({ ok: true });
  });

  it('records a denial by the core when nobody answers in ten minutes', () => {
    vi.useFakeTimers();
    const { store, control, link } = setup();
    control.receive(link, state('s1', 'waiting-permission'));
    control.receive(link, permission('r1'));
    vi.advanceTimersByTime(PERMISSION_OPEN_MS + 1);
    expect(events(store, 'permission.answered')).toMatchObject([
      {
        sessionId: 's1',
        actorKind: 'core',
        actorId: 'permission-timeout',
        channel: 'core',
        ref: 'r1',
        summary: { decision: 'deny', tool: 'Bash' },
      },
    ]);
    expect(control.openPermissions('s1')).toEqual([]);
  });

  it('records a denial by the core when the session ends the request', () => {
    const { store, control, link } = setup();
    control.receive(link, state('s1', 'waiting-permission'));
    control.receive(link, permission('r1'));
    control.receive(link, state('s1', 'idle'));
    expect(events(store, 'permission.answered')).toMatchObject([
      { actorKind: 'core', actorId: 'session-end', ref: 'r1', summary: { decision: 'deny' } },
    ]);
  });

  it('records one answer an operator gives and none from the core after it', () => {
    vi.useFakeTimers();
    const { store, control, link } = setup();
    control.receive(link, state('s1', 'waiting-permission'));
    control.receive(link, permission('r1'));
    control.answerPermission('s1', 'dev-a', 'r1', { decision: 'allow' });
    vi.advanceTimersByTime(PERMISSION_OPEN_MS + 1);
    const answers = events(store, 'permission.answered');
    expect(answers).toHaveLength(1);
    expect(answers[0]).toMatchObject({ actorKind: 'operator', summary: { decision: 'allow' } });
  });
});

describe('prompts and steering', () => {
  it('records a prompt by its length and image count, never its text', async () => {
    const { store, control, link } = setup();
    const pending = control.prompt('s1', 'dev-a', 'SECRET-PROMPT-TEXT', 'p1', ['img-1']);
    control.receive(link, state('s1', 'running'));
    await pending;
    const [event] = events(store, 'prompt.sent');
    expect(event).toMatchObject({
      sessionId: 's1',
      deviceId: 'dev-a',
      ref: 'p1',
      summary: { promptId: 'p1', length: 18, images: 1 },
    });
    expect(JSON.stringify(event)).not.toContain('SECRET-PROMPT-TEXT');
  });

  it('records a steer as a prompt with the steer flag', async () => {
    const { store, control, link } = setup();
    control.receive(link, state('s1', 'running'));
    void control.steer('s1', 'dev-a', 'st1', 'änder das', []);
    const [event] = events(store, 'prompt.sent');
    expect(event).toMatchObject({
      ref: 'st1',
      summary: { steerId: 'st1', steer: true, length: 9, images: 0 },
    });
    expect(JSON.stringify(event)).not.toContain('änder');
  });

  it('records nothing for a prompt that was refused before it was sent', async () => {
    const { store, control } = setup();
    expect(await control.prompt('s1', 'nobody', 'x', 'p1')).toMatchObject({
      ok: false,
      error: 'owner-offline',
    });
    expect(events(store, 'prompt.sent')).toEqual([]);
  });
});

describe('terminals', () => {
  it('records the open and the close, without what was typed or shown', async () => {
    const { store, control, link } = setup();
    control.receive(link, state('s1', 'idle'));
    const pending = control.terminals.open('s1', 'dev-a', 80, 24);
    const msg = link.sent.find((m) => m.type === 'terminal.open') as unknown as {
      terminalId: string;
    };
    control.receive(link, {
      v: PROTOCOL_VERSION,
      type: 'terminal.opened',
      terminalId: msg.terminalId,
    } as unknown as ControlReply);
    expect((await pending).ok).toBe(true);
    control.receive(link, {
      v: PROTOCOL_VERSION,
      type: 'terminal.output',
      terminalId: msg.terminalId,
      seq: 0,
      data: Buffer.from('TOP-SECRET-OUTPUT').toString('base64'),
    } as unknown as ControlReply);
    expect(control.terminals.close(msg.terminalId)).toBe(true);

    expect(events(store, 'terminal.opened')).toMatchObject([
      { sessionId: 's1', deviceId: 'dev-a', ref: msg.terminalId, summary: { cols: 80, rows: 24 } },
    ]);
    const [closed] = events(store, 'terminal.closed');
    expect(closed).toMatchObject({ actorKind: 'core', channel: 'core', ref: msg.terminalId });
    expect(closed?.summary).toMatchObject({ bytesOut: 17, bytesIn: 0 });
    expect(typeof closed?.summary.reason).toBe('string');
    expect(JSON.stringify(closed)).not.toContain('TOP-SECRET');
  });
});
