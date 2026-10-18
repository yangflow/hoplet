import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION, type ServerMessage } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { type ControlReply, createControl } from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function fakeLink() {
  const link = {
    sent: [] as ServerMessage[],
    send(msg: ServerMessage) {
      link.sent.push(msg);
    },
    async sendAndWait(msg: ServerMessage) {
      link.sent.push(msg);
    },
    isOpen: () => true,
    watches: () =>
      link.sent
        .filter((m) => m.type === 'ports.watch')
        .map((m) => (m as unknown as { sessionIds: string[] }).sessionIds),
  };
  return link;
}

function setup() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-ports-'));
  dirs.push(dir);
  const store = new Store(dir);
  for (const [id, device] of [
    ['s1', 'dev-a'],
    ['s2', 'dev-a'],
    ['o1', 'dev-b'],
  ] as const) {
    store.append({ sessionId: id, projectDir: 'p', filePath: `/x/${id}`, deviceId: device }, [
      '{"type":"user"}',
    ]);
  }
  const control = createControl(store, {});
  const link = fakeLink();
  control.attach('dev-a', link, [], { previewHost: 'mac.local' });
  const events: ServerMessage[] = [];
  control.subscribe((m: ServerMessage) => events.push(m));
  return { control, link, events };
}

const report = (sessionId: string, ports: number[]) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'session.ports',
    sessionId,
    ports: ports.map((port) => ({ port, address: 'loopback', process: 'node' })),
  }) as unknown as ControlReply;

describe('ports.watch', () => {
  it('follows the sessions browsers have open on the device', () => {
    const { control, link } = setup();
    control.viewing.add('s2');
    control.viewing.add('s1');
    control.viewing.add('o1');
    expect(link.watches()).toEqual([['s2'], ['s1', 's2']]);
    control.viewing.remove('s1');
    expect(link.watches().at(-1)).toEqual(['s2']);
    control.viewing.remove('s2');
    expect(link.watches().at(-1)).toEqual([]);
  });

  it('is sent again to a reconnected agent', () => {
    const { control } = setup();
    control.viewing.add('s1');
    const second = fakeLink();
    control.attach('dev-a', second, []);
    expect(second.watches()).toEqual([['s1']]);
  });

  it('sends nothing to an agent with nothing to watch', () => {
    const { link } = setup();
    expect(link.watches()).toEqual([]);
  });
});

describe('session.ports', () => {
  it('is forwarded to the browsers when it changes and held in memory', () => {
    const { control, link, events } = setup();
    control.receive(link, report('s1', [3000]));
    control.receive(link, report('s1', [3000]));
    const listening = events.filter((e) => e.type === 'session.listening');
    expect(listening).toHaveLength(1);
    expect(listening[0]).toMatchObject({
      sessionId: 's1',
      previewHost: 'mac.local',
      ports: [{ port: 3000 }],
    });
    expect(control.portsOf('s1')).toMatchObject({
      previewHost: 'mac.local',
      ports: [{ port: 3000 }],
    });
    expect(control.portsOf('s2').ports).toEqual([]);
  });

  it('is ignored from a device that does not own the session', () => {
    const { control, events } = setup();
    const other = fakeLink();
    control.attach('dev-b', other, []);
    control.receive(other, report('s1', [1]));
    expect(events.filter((e) => e.type === 'session.listening')).toHaveLength(0);
    expect(control.portsOf('s1').ports).toEqual([]);
  });

  it('is cleared when the owner disconnects', () => {
    const { control, link, events } = setup();
    control.receive(link, report('s1', [3000]));
    control.detach(link);
    expect(events.filter((e) => e.type === 'session.listening').at(-1)).toMatchObject({
      sessionId: 's1',
      ports: [],
    });
    expect(control.portsOf('s1').ports).toEqual([]);
  });
});
