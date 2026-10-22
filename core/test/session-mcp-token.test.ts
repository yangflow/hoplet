import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION, type ServerMessage } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { type ControlReply, createControl, type DeviceLink } from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];

afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

type Link = DeviceLink & { sent: ServerMessage[] };

function fakeLink(): Link {
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

function setup(sessionMcp: boolean) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-mcp-token-'));
  dirs.push(dir);
  const store = new Store(dir);
  for (const [id, device] of [
    ['s1', 'dev-a'],
    ['s2', 'dev-b'],
  ] as const) {
    store.append(
      {
        sessionId: id,
        provider: 'claude',
        projectDir: 'p',
        filePath: `/x/${id}`,
        deviceId: device,
      },
      ['{"type":"user"}'],
    );
  }
  const control = createControl(store, { promptTimeoutMs: 100 });
  const link = fakeLink();
  control.attach('dev-a', link, [], { capabilities: sessionMcp ? { sessionMcp: true } : {} });
  return { control, link };
}

const request = (sessionId: string) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'runtime.mcp.token.request',
    requestId: 'r1',
    sessionId,
  }) as unknown as ControlReply;

const lastReply = (link: Link) =>
  link.sent.filter((m) => m.type === 'runtime.mcp.token').at(-1) as unknown as Record<
    string,
    unknown
  >;

describe('the session MCP token request', () => {
  it('answers the owner of a session with a token that expires within a day', () => {
    const { control, link } = setup(true);
    control.receive(link, request('s1'));
    const reply = lastReply(link);
    expect(reply).toMatchObject({ requestId: 'r1', sessionId: 's1' });
    expect(String(reply.token)).toMatch(/^hpt_/);
    expect(Number(reply.expiresAt) - Date.now()).toBeLessThanOrEqual(24 * 3_600_000);
  });

  it('refuses a session of another device', () => {
    const { control, link } = setup(true);
    control.receive(link, request('s2'));
    expect(lastReply(link)).toMatchObject({ requestId: 'r1', reason: 'not-owner' });
    expect(lastReply(link)).not.toHaveProperty('token');
  });

  it('refuses a session that is not known', () => {
    const { control, link } = setup(true);
    control.receive(link, request('nope'));
    expect(lastReply(link)).toMatchObject({ reason: 'unknown-session' });
  });

  it('refuses a device that did not announce sessionMcp', () => {
    const { control, link } = setup(false);
    control.receive(link, request('s1'));
    expect(lastReply(link)).toMatchObject({ reason: 'not-allowed' });
    expect(lastReply(link)).not.toHaveProperty('token');
  });
});
