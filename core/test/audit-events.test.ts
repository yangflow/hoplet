import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import type { AuditEvent, Provider } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function open(dir?: string) {
  const d = dir ?? mkdtempSync(path.join(tmpdir(), 'hoplet-audit-events-'));
  if (!dir) dirs.push(d);
  const store = new Store(d);
  const raw = () => new DatabaseSync(path.join(d, 'hoplet.db'));
  return { dir: d, store, raw };
}

const all = (store: Store): AuditEvent[] =>
  store.listAudit({}, { order: 'asc', limit: 500 }).events;
const ofType = (store: Store, type: string): AuditEvent[] =>
  all(store).filter((e) => e.type === type);

function input(sessionId: string, provider: Provider = 'claude') {
  return { sessionId, provider, projectDir: 'p', filePath: `/x/${sessionId}`, deviceId: 'dev-a' };
}

const claudeLine = (
  tool: string,
  toolInput: Record<string, unknown>,
  id = 'toolu_1',
  timestamp = '2026-01-02T03:04:05.000Z',
) =>
  JSON.stringify({
    type: 'assistant',
    timestamp,
    message: {
      role: 'assistant',
      content: [{ type: 'tool_use', id, name: tool, input: toolInput }],
    },
  });
const userLine = JSON.stringify({ type: 'user', message: { role: 'user', content: 'hi' } });

const codexItem = (item: Record<string, unknown>) =>
  JSON.stringify({
    type: 'event_msg',
    timestamp: '2026-01-02T03:04:05.000Z',
    payload: { type: 'item_completed', item },
  });

describe('tool calls derived from session lines', () => {
  it('records a Claude shell call, edit calls and leaves other blocks out', async () => {
    const { store } = open();
    store.append(input('s1'), [
      userLine,
      claudeLine('Bash', { command: 'ls -la' }, 'toolu_a'),
      claudeLine('Edit', { file_path: '/work/a.ts', old_string: 'x', new_string: 'y' }, 'toolu_b'),
      claudeLine('Read', { file_path: '/work/b.ts' }, 'toolu_c'),
    ]);
    await store.idle();
    const calls = ofType(store, 'tool.call');
    expect(calls.map((e) => e.summary)).toEqual([
      { tool: 'Bash', line: 1, command: 'ls -la' },
      { tool: 'Edit', line: 2, paths: ['/work/a.ts'], pathCount: 1 },
      { tool: 'Read', line: 3 },
    ]);
    expect(calls[0]).toMatchObject({
      sessionId: 's1',
      deviceId: 'dev-a',
      actorKind: 'core',
      actorId: 'deriver',
      channel: 'core',
      ref: 'toolu_a',
      at: Date.parse('2026-01-02T03:04:05.000Z'),
    });
    expect(JSON.stringify(calls)).not.toContain('old_string');
  });

  it('records Codex commands, file changes and MCP calls', async () => {
    const { store } = open();
    store.append(input('c1', 'codex'), [
      codexItem({ type: 'CommandExecution', id: 'i1', command: ['bash', '-lc', 'git status'] }),
      codexItem({ type: 'FileChange', id: 'i2', changes: { '/w/a.go': {}, '/w/b.go': {} } }),
      codexItem({ type: 'McpToolCall', id: 'i3', server: 'fs', tool: 'read' }),
      codexItem({ type: 'AgentMessage', id: 'i4' }),
    ]);
    await store.idle();
    expect(ofType(store, 'tool.call').map((e) => [e.ref, e.summary])).toEqual([
      ['i1', { tool: 'exec_command', line: 0, command: 'git status' }],
      ['i2', { tool: 'apply_patch', line: 1, paths: ['/w/a.go', '/w/b.go'], pathCount: 2 }],
      ['i3', { tool: 'mcp__fs__read', line: 2 }],
    ]);
  });

  it('scrubs a secret out of the command before it is stored', async () => {
    const { store } = open();
    const token = `ghp_${'a1B2c3D4e5'.repeat(4)}`;
    store.append(input('s1'), [
      claudeLine('Bash', {
        command: `curl -H "Authorization: Bearer abcdefghij0123456789xyz" -u x:${token} https://h`,
      }),
    ]);
    await store.idle();
    const command = String(ofType(store, 'tool.call')[0]?.summary.command);
    expect(command).not.toContain(token);
    expect(command).not.toContain('abcdefghij0123456789xyz');
    expect(command).toContain('curl');
  });

  it('cuts a command to 500 characters', async () => {
    const { store } = open();
    store.append(input('s1'), [claudeLine('Bash', { command: `echo ${'x'.repeat(2000)}` })]);
    await store.idle();
    const command = String(ofType(store, 'tool.call')[0]?.summary.command);
    expect([...command].length).toBeLessThanOrEqual(500);
    expect(command.endsWith('...')).toBe(true);
  });

  it('names at most 20 paths and counts them all', async () => {
    const { store } = open();
    const changes = Object.fromEntries(Array.from({ length: 30 }, (_, i) => [`/w/f${i}`, {}]));
    store.append(input('c1', 'codex'), [codexItem({ type: 'FileChange', id: 'i', changes })]);
    await store.idle();
    const summary = ofType(store, 'tool.call')[0]?.summary as {
      paths: string[];
      pathCount: number;
    };
    expect(summary.paths).toHaveLength(20);
    expect(summary.pathCount).toBe(30);
  });

  it('does not derive twice and never changes the stored lines', async () => {
    const { store } = open();
    const lines = [
      claudeLine('Bash', { command: 'a' }, 't1'),
      claudeLine('Bash', { command: 'b' }, 't2'),
    ];
    store.append(input('s1'), lines.slice(0, 1));
    await store.idle();
    store.append(input('s1'), lines.slice(1));
    await store.idle();
    expect(ofType(store, 'tool.call').map((e) => e.ref)).toEqual(['t1', 't2']);
    expect(ofType(store, 'tool.call').map((e) => (e.summary as { line: number }).line)).toEqual([
      0, 1,
    ]);
  });

  it('resumes after a restart from the stored cursor, without duplicates', async () => {
    const first = open();
    const lines = [
      claudeLine('Bash', { command: 'one' }, 't1'),
      claudeLine('Bash', { command: 'two' }, 't2'),
      claudeLine('Bash', { command: 'three' }, 't3'),
    ];
    first.store.append(input('s1'), lines);
    await first.store.idle();
    expect(ofType(first.store, 'tool.call')).toHaveLength(3);

    // The core stopped after the first line: the cursor is behind.
    const db = first.raw();
    db.exec("UPDATE audit_cursor SET line = 1 WHERE session_id = 's1'");
    db.close();
    const restarted = open(first.dir);
    restarted.store.startAuditDerive();
    await restarted.store.idle();
    // Lines 1 and 2 are derived again from the cursor: the cursor was set back by hand, so they repeat.
    expect(ofType(restarted.store, 'tool.call').map((e) => e.ref)).toEqual([
      't1',
      't2',
      't3',
      't2',
      't3',
    ]);

    // A restart with the cursor at the end derives nothing.
    const again = open(first.dir);
    again.store.startAuditDerive();
    await again.store.idle();
    expect(ofType(again.store, 'tool.call')).toHaveLength(5);
    const db2 = again.raw();
    const cursor = db2.prepare('SELECT line FROM audit_cursor WHERE session_id = ?').get('s1');
    db2.close();
    expect(cursor).toEqual({ line: 3 });
  });

  it('derives the lines an interrupted run left behind', async () => {
    const first = open();
    first.store.append(input('s1'), [claudeLine('Bash', { command: 'one' }, 't1')]);
    await first.store.idle();
    // Lines reach the store while the deriver did not run (a crash between the append and the derive).
    const db = first.raw();
    db.exec("UPDATE audit_cursor SET line = 0 WHERE session_id = 's1'");
    db.exec("DELETE FROM audit_events WHERE type = 'tool.call'");
    db.close();
    const restarted = open(first.dir);
    restarted.store.startAuditDerive();
    await restarted.store.idle();
    expect(ofType(restarted.store, 'tool.call').map((e) => e.ref)).toEqual(['t1']);
  });

  it('does not back-fill a session that existed before the deriver', async () => {
    const first = open();
    first.store.append(input('s1'), [claudeLine('Bash', { command: 'old' }, 'old1')]);
    await first.store.idle();
    const db = first.raw();
    db.exec('DELETE FROM audit_cursor');
    db.exec("DELETE FROM audit_events WHERE type = 'tool.call'");
    db.close();
    const restarted = open(first.dir);
    restarted.store.append(input('s1'), [claudeLine('Bash', { command: 'new' }, 'new1')]);
    await restarted.store.idle();
    expect(ofType(restarted.store, 'tool.call').map((e) => e.ref)).toEqual(['new1']);
  });

  it('forgets the cursor when the session is purged', async () => {
    const { store, raw } = open();
    store.append(input('s1'), [claudeLine('Bash', { command: 'a' })]);
    await store.idle();
    store.purgeSession('s1');
    const db = raw();
    const rows = db.prepare('SELECT * FROM audit_cursor').all();
    db.close();
    expect(rows).toEqual([]);
  });

  it('keeps the chain intact', async () => {
    const { store } = open();
    store.append(input('s1'), [
      claudeLine('Bash', { command: 'a' }, 't1'),
      claudeLine('Write', { file_path: '/w/x', content: 'secret body' }, 't2'),
    ]);
    await store.idle();
    expect(await store.verifyAudit()).toMatchObject({ ok: true, firstBreak: null });
    expect(JSON.stringify(all(store))).not.toContain('secret body');
  });
});

describe('configuration changes', () => {
  it('records notification preferences with the fields that changed', async () => {
    const { store } = open();
    store.setNotifyPreferences({ ...store.notifyPreferences(), preset: 'everything' });
    const [event] = ofType(store, 'config.changed');
    expect(event).toMatchObject({ actorKind: 'operator', channel: 'rest' });
    expect(event?.summary).toMatchObject({
      area: 'notifications',
      action: 'updated',
      fields: ['preset'],
    });
    expect(await store.verifyAudit()).toMatchObject({ ok: true });
  });

  it('records a webhook without its URL or secret', () => {
    const { store } = open();
    const hook = store.addWebhook(
      { url: 'https://hooks.example/secret-path', label: 'ops', secret: 'shh-secret-value' },
      1,
    );
    store.updateWebhook(hook.id, { enabled: false });
    store.deleteWebhook(hook.id);
    const events = ofType(store, 'config.changed');
    expect(events.map((e) => [e.summary.area, e.summary.action])).toEqual([
      ['webhooks', 'created'],
      ['webhooks', 'updated'],
      ['webhooks', 'deleted'],
    ]);
    const text = JSON.stringify(events);
    expect(text).not.toContain('secret-path');
    expect(text).not.toContain('shh-secret-value');
  });

  it('records resource thresholds, price overrides and device labels', () => {
    const { store } = open();
    store.setResourceThresholds({ default: { memory: 70, disk: 80 }, machines: {} });
    store.setUsagePrices({
      overrides: { 'my-model': { input: 1, output: 2, cacheWrite: 3, cacheRead: 4 } },
    });
    store.mintDevice('Mac', 'dev-a');
    store.setDeviceLabels('dev-a', ['laptop']);
    const areas = ofType(store, 'config.changed').map((e) => e.summary.area);
    expect(areas).toEqual(
      expect.arrayContaining(['resource-thresholds', 'price-overrides', 'device-labels']),
    );
    const labels = ofType(store, 'config.changed').find((e) => e.summary.area === 'device-labels');
    expect(labels).toMatchObject({ deviceId: 'dev-a' });
    expect(labels?.summary).toMatchObject({ current: { labels: ['laptop'] } });
  });

  it('records nothing for a device the store does not know', () => {
    const { store } = open();
    expect(store.setDeviceLabels('nobody', ['x'])).toBe(false);
    expect(ofType(store, 'config.changed')).toHaveLength(0);
  });
});
