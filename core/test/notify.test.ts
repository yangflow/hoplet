import { createDecipheriv, createECDH, hkdfSync } from 'node:crypto';
import { existsSync, mkdtempSync, rmSync, statSync } from 'node:fs';
import { createServer, type IncomingMessage, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  type NotifyPayload,
  type NotifyPreferences,
  PROTOCOL_VERSION,
  type ServerMessage,
} from '@hoplet/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { type ControlReply, createControl, type DeviceLink } from '../src/control/index.ts';
import { createNotify, signBody } from '../src/notify/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const servers: Server[] = [];
const stops: Array<() => void> = [];

afterEach(async () => {
  for (const stop of stops.splice(0)) stop();
  for (const s of servers.splice(0)) {
    s.closeAllConnections();
    await new Promise<void>((r) => s.close(() => r()));
  }
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

const T0 = Date.UTC(2026, 9, 16, 12, 0);
const PROJECT = '/work/app';

interface Received {
  url: string;
  headers: IncomingMessage['headers'];
  body: Buffer;
}

/** A local receiver standing in for a push service or a webhook; `status` decides what it answers. */
async function receiver(statuses: number[] = [201]) {
  const got: Received[] = [];
  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (c: Buffer) => chunks.push(c));
    req.on('end', () => {
      got.push({ url: req.url ?? '', headers: req.headers, body: Buffer.concat(chunks) });
      const status = statuses[Math.min(got.length - 1, statuses.length - 1)] ?? 201;
      res.writeHead(status);
      res.end();
    });
  });
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;
  return { got, base: `http://127.0.0.1:${port}` };
}

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

const state = (sessionId: string, value: string) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'runtime.state',
    sessionId,
    state: value,
    capabilities: { permissions: true, interrupt: true, config: [] },
  }) as ControlReply;

const permission = (requestId: string, tool: string, input: unknown, planExit?: unknown) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'runtime.permission',
    sessionId: 's1',
    requestId,
    tool,
    input,
    ...(planExit ? { planExit } : {}),
  }) as ControlReply;

function setup(options: { publicUrl?: string | null } = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-notify-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.append(
    { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'dev-a' },
    [
      JSON.stringify({
        type: 'user',
        cwd: PROJECT,
        message: { role: 'user', content: 'Fix the login bug' },
      }),
    ],
  );
  const clock = { now: T0 };
  const control = createControl(store, { now: () => clock.now, startTimeoutMs: 100 });
  const link = fakeLink();
  control.attach('dev-a', link);
  const notify = createNotify(store, control, {
    publicUrl: options.publicUrl === undefined ? 'https://hoplet.example' : options.publicUrl,
    now: () => clock.now,
    retryDelayMs: 5,
    timeoutMs: 1_000,
  });
  notify.start();
  stops.push(() => notify.stop());
  return { dir, store, control, link, notify, clock };
}

const prefs = (over: Partial<NotifyPreferences> = {}): NotifyPreferences => ({
  preset: 'needs-me',
  events: {},
  projects: {},
  quietHours: null,
  ...over,
});

const json = (r: Received): NotifyPayload => JSON.parse(r.body.toString()) as NotifyPayload;
const tokenOf = (payload: NotifyPayload): string => (payload.link ?? '').split('#')[1] ?? '';

/** Decrypts a Web Push body the way the browser does. */
function decrypt(body: Buffer, ua: ReturnType<typeof createECDH>, auth: Buffer): string {
  const salt = body.subarray(0, 16);
  const idlen = body[20] as number;
  const sender = body.subarray(21, 21 + idlen);
  const record = body.subarray(21 + idlen);
  const ikm = Buffer.from(
    hkdfSync(
      'sha256',
      ua.computeSecret(sender),
      auth,
      Buffer.concat([Buffer.from('WebPush: info\0'), ua.getPublicKey(), sender]),
      32,
    ),
  );
  const cek = Buffer.from(
    hkdfSync('sha256', ikm, salt, Buffer.from('Content-Encoding: aes128gcm\0'), 16),
  );
  const nonce = Buffer.from(
    hkdfSync('sha256', ikm, salt, Buffer.from('Content-Encoding: nonce\0'), 12),
  );
  const d = createDecipheriv('aes-128-gcm', cek, nonce);
  d.setAuthTag(record.subarray(record.length - 16));
  const out = Buffer.concat([d.update(record.subarray(0, record.length - 16)), d.final()]);
  return out.subarray(0, out.length - 1).toString();
}

describe('delivery', () => {
  it('sends a permission request over Web Push, encrypted, signed with VAPID, with a redacted summary', async () => {
    const f = setup();
    const push = await receiver();
    const ua = createECDH('prime256v1');
    ua.generateKeys();
    const auth = Buffer.alloc(16, 9);
    f.store.addPushSubscription(
      {
        endpoint: `${push.base}/push/abc`,
        p256dh: ua.getPublicKey().toString('base64url'),
        auth: auth.toString('base64url'),
        label: 'Laptop',
      },
      T0,
    );
    f.control.receive(
      f.link,
      permission('r1', 'Bash', {
        command: 'curl -H "Authorization: Bearer abcdef0123456789" x.test',
      }),
    );
    await vi.waitFor(() => expect(push.got).toHaveLength(1));
    const req = push.got[0] as Received;
    expect(req.url).toBe('/push/abc');
    expect(req.headers['content-encoding']).toBe('aes128gcm');
    expect(req.headers.urgency).toBe('high');
    expect(req.headers.authorization).toMatch(/^vapid t=[\w-]+\.[\w-]+\.[\w-]+, k=[\w-]+$/);
    const payload = JSON.parse(decrypt(req.body, ua, auth)) as NotifyPayload;
    expect(payload).toMatchObject({
      kind: 'permission',
      sessionId: 's1',
      project: 'app',
      title: 'Fix the login bug',
      tool: 'Bash',
      requestId: 'r1',
    });
    expect(payload.summary).not.toContain('abcdef0123456789');
    expect([...(payload.summary ?? '')].length).toBeLessThanOrEqual(80);
    expect(payload.link).toMatch(/^https:\/\/hoplet\.example\/a#[\w-]+\.[\w-]+$/);
    await vi.waitFor(() =>
      expect(f.store.listPushSubscriptions()[0]?.lastDelivery).toMatchObject({ ok: true }),
    );
  });

  it('keeps the secrets in a file of mode 0600, not in the database', () => {
    const f = setup();
    const file = path.join(f.dir, 'notify-secrets.json');
    f.notify.pushKey();
    expect(existsSync(file)).toBe(true);
    expect(statSync(file).mode & 0o777).toBe(0o600);
  });

  it('posts a signed webhook and records the delivery', async () => {
    const f = setup();
    const hook = await receiver([200]);
    const added = f.store.addWebhook(
      { url: `${hook.base}/h`, label: 'Chat', secret: 's3cret' },
      T0,
    );
    f.control.receive(f.link, permission('r1', 'Edit', { file_path: `${PROJECT}/a.ts` }));
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    const req = hook.got[0] as Received;
    expect(req.headers['content-type']).toBe('application/json');
    expect(req.headers['x-hoplet-signature']).toBe(signBody('s3cret', req.body));
    expect(json(req)).toMatchObject({ kind: 'permission', summary: `${PROJECT}/a.ts` });
    await vi.waitFor(() => expect(f.store.getWebhook(added.id)?.lastDelivery?.ok).toBe(true));
  });

  it('omits the signature without a secret and skips disabled webhooks', async () => {
    const f = setup();
    const on = await receiver([200]);
    const off = await receiver([200]);
    f.store.addWebhook({ url: `${on.base}/h`, label: 'on' }, T0);
    const disabled = f.store.addWebhook({ url: `${off.base}/h`, label: 'off' }, T0);
    f.store.updateWebhook(disabled.id, { enabled: false });
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'ls' }));
    await vi.waitFor(() => expect(on.got).toHaveLength(1));
    expect(on.got[0]?.headers['x-hoplet-signature']).toBeUndefined();
    expect(off.got).toHaveLength(0);
  });

  it('sends a webhook only the events it chose', async () => {
    const f = setup();
    const planOnly = await receiver([200]);
    const defaults = await receiver([200]);
    const both = await receiver([200]);
    f.store.addWebhook({ url: `${planOnly.base}/h`, label: 'plan', events: ['plan'] }, T0);
    const d = f.store.addWebhook({ url: `${defaults.base}/h`, label: 'default' }, T0);
    f.store.addWebhook(
      { url: `${both.base}/h`, label: 'both', events: ['failure', 'permission'] },
      T0,
    );
    expect(d.events).toEqual(['permission', 'plan', 'failure', 'budget']);
    expect(Object.fromEntries(f.store.listWebhooks().map((w) => [w.label, w.events]))).toEqual({
      plan: ['plan'],
      default: ['permission', 'plan', 'failure', 'budget'],
      both: ['permission', 'failure'],
    });
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'ls' }));
    await vi.waitFor(() => expect(defaults.got).toHaveLength(1));
    await vi.waitFor(() => expect(both.got).toHaveLength(1));
    expect(planOnly.got).toHaveLength(0);
  });

  it('keeps a webhook that chose turn-done quiet while the preferences route the event nowhere', async () => {
    const f = setup();
    const hook = await receiver([200]);
    f.store.addWebhook({ url: `${hook.base}/h`, label: 'done', events: ['turn-done'] }, T0);
    // The default preset sends no turn-done, whatever the webhook chose.
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'ls' }));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(hook.got).toHaveLength(0);
  });

  it('retries a failing webhook once and records the status', async () => {
    const f = setup();
    const hook = await receiver([500]);
    const added = f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'ls' }));
    await vi.waitFor(() =>
      expect(f.store.getWebhook(added.id)?.lastDelivery).toMatchObject({
        ok: false,
        error: 'http-500',
      }),
    );
    expect(hook.got).toHaveLength(2);
  });

  it('does not retry a webhook that answers 4xx', async () => {
    const f = setup();
    const hook = await receiver([403]);
    const added = f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'ls' }));
    await vi.waitFor(() =>
      expect(f.store.getWebhook(added.id)?.lastDelivery?.error).toBe('http-403'),
    );
    expect(hook.got).toHaveLength(1);
  });

  it('times out a receiver that does not answer', async () => {
    const f = setup();
    const server = createServer(() => {});
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const port = (server.address() as AddressInfo).port;
    const slow = createNotify(f.store, f.control, {
      publicUrl: null,
      now: () => f.clock.now,
      retryDelayMs: 1,
      timeoutMs: 50,
    });
    const added = f.store.addWebhook({ url: `http://127.0.0.1:${port}/h`, label: 'slow' }, T0);
    f.notify.stop();
    slow.start();
    stops.push(() => slow.stop());
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'ls' }));
    await vi.waitFor(() =>
      expect(f.store.getWebhook(added.id)?.lastDelivery).toMatchObject({
        ok: false,
        error: 'timeout',
      }),
    );
  });

  it('deletes a push subscription the service reports gone', async () => {
    const f = setup();
    const push = await receiver([410]);
    const ua = createECDH('prime256v1');
    ua.generateKeys();
    f.store.addPushSubscription(
      {
        endpoint: `${push.base}/gone`,
        p256dh: ua.getPublicKey().toString('base64url'),
        auth: Buffer.alloc(16).toString('base64url'),
        label: 'Old phone',
      },
      T0,
    );
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'ls' }));
    await vi.waitFor(() => expect(f.store.listPushSubscriptions()).toHaveLength(0));
    expect(push.got).toHaveLength(1);
  });

  it('tests a webhook on demand and reports the delivery', async () => {
    const f = setup();
    const hook = await receiver([204]);
    const added = f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    const out = await f.notify.testWebhook(added.id);
    expect(out?.delivery.ok).toBe(true);
    expect(out?.webhook.lastDelivery?.ok).toBe(true);
    expect(await f.notify.testWebhook('missing')).toBeNull();
  });

  it('logs sent notifications without the token, a secret or the text', async () => {
    const lines: string[] = [];
    vi.spyOn(console, 'log').mockImplementation((line: string) => {
      lines.push(String(line));
    });
    const f = setup();
    const hook = await receiver([200]);
    f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat', secret: 'whsec-value-1' }, T0);
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'echo hidden-command-text' }));
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    await vi.waitFor(() => expect(lines.some((l) => l.includes('notification sent'))).toBe(true));
    const all = lines.join('\n');
    expect(all).toContain('"channel":"webhook"');
    expect(all).toContain('"requestId":"r1"');
    expect(all).not.toContain(tokenOf(json(hook.got[0] as Received)));
    expect(all).not.toContain('whsec-value-1');
    expect(all).not.toContain('hidden-command-text');
    vi.restoreAllMocks();
  });
});

describe('which events notify', () => {
  it('does not tell about a finished turn by default', async () => {
    const f = setup();
    const hook = await receiver([200]);
    f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    f.control.receive(f.link, state('s1', 'running'));
    f.control.receive(f.link, state('s1', 'ready'));
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'ls' }));
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    expect(json(hook.got[0] as Received).kind).toBe('permission');
  });

  it('tells about a finished turn when asked, unless a tab has the session open', async () => {
    const f = setup();
    const hook = await receiver([200]);
    const other = await receiver([200]);
    f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat', events: ['turn-done'] }, T0);
    f.store.addWebhook({ url: `${other.base}/h`, label: 'Needs me' }, T0);
    f.store.setNotifyPreferences(prefs({ preset: 'needs-me-and-done' }));

    f.control.viewing.add('s1');
    f.control.receive(f.link, state('s1', 'running'));
    f.control.receive(f.link, state('s1', 'ready'));
    await new Promise((r) => setTimeout(r, 40));
    expect(hook.got).toHaveLength(0);

    f.control.viewing.remove('s1');
    f.control.receive(f.link, state('s1', 'running'));
    f.control.receive(f.link, state('s1', 'ready'));
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    expect(json(hook.got[0] as Received)).toMatchObject({ kind: 'turn-done', sessionId: 's1' });
    expect(json(hook.got[0] as Received).link).toBeUndefined();
    // The webhook that did not choose turn-done is not told, though the preset routes the event to webhooks.
    expect(other.got).toHaveLength(0);
  });

  it('holds a finished turn back in quiet hours but not a permission request', async () => {
    const f = setup();
    const hook = await receiver([200]);
    f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    f.store.setNotifyPreferences(
      prefs({
        preset: 'everything',
        quietHours: { start: '11:00', end: '13:00', timeZone: 'UTC' },
      }),
    );
    f.control.receive(f.link, state('s1', 'running'));
    f.control.receive(f.link, state('s1', 'ready'));
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'ls' }));
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    await new Promise((r) => setTimeout(r, 40));
    expect(hook.got).toHaveLength(1);
    expect(json(hook.got[0] as Received).kind).toBe('permission');
  });

  it('marks a plan review as its own kind', async () => {
    const f = setup();
    const hook = await receiver([200]);
    f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    f.control.receive(
      f.link,
      permission(
        'p1',
        'ExitPlanMode',
        {},
        { plan: '# Plan', option: 'o', modes: ['default'], default: 'default' },
      ),
    );
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    const payload = json(hook.got[0] as Received);
    expect(payload.kind).toBe('plan');
    expect(payload.summary).toBeUndefined();
    expect(payload.link).toBeTruthy();
  });

  it('sends no link when there is no public URL', async () => {
    const f = setup({ publicUrl: null });
    const hook = await receiver([200]);
    f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    f.control.receive(f.link, permission('r1', 'Bash', { command: 'ls' }));
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    expect(json(hook.got[0] as Received).link).toBeUndefined();
  });

  it('tells about a session that failed to start', async () => {
    const f = setup();
    const hook = await receiver([200]);
    f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    const out = await f.control.startSession({
      deviceId: 'missing-device',
      projectPath: '/work/other',
      provider: 'claude',
      text: 'secret prompt text',
      promptId: 'p1',
    });
    expect(out).toMatchObject({ ok: false, error: 'target-offline' });
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    const payload = json(hook.got[0] as Received);
    expect(payload).toMatchObject({ kind: 'failure', project: 'other', reason: 'target-offline' });
    expect(payload.sessionId).toBeUndefined();
    expect(hook.got[0]?.body.toString()).not.toContain('secret prompt text');
  });

  it('tells about a handoff that failed, and not about one that was refused', async () => {
    const f = setup();
    const hook = await receiver([200]);
    f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    const same = await f.control.handoff({ sessionId: 's1', targetDeviceId: 'dev-a' });
    expect(same.ok).toBe(false);
    const failed = await f.control.handoff({ sessionId: 's1', targetDeviceId: 'nowhere' });
    expect(failed.ok).toBe(false);
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    expect(json(hook.got[0] as Received)).toMatchObject({
      kind: 'failure',
      sessionId: 's1',
      project: 'app',
    });
  });
});

describe('action links', () => {
  async function linked(tool = 'Bash', input: unknown = { command: 'npm test' }, plan?: unknown) {
    const f = setup();
    const hook = await receiver([200]);
    f.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    f.control.receive(f.link, permission('r1', tool, input, plan));
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    const token = tokenOf(json(hook.got[0] as Received));
    return { ...f, token };
  }

  const answers = (link: { sent: ServerMessage[] }) =>
    link.sent.filter((m) => m.type === 'runtime.permission.answer');

  it('shows an open request and answers it once', async () => {
    const f = await linked();
    expect(f.notify.inspect(f.token)).toMatchObject({
      state: 'open',
      kind: 'permission',
      sessionId: 's1',
      requestId: 'r1',
      project: 'app',
      tool: 'Bash',
      summary: 'npm test',
      expiresAt: T0 + 10 * 60_000,
    });
    expect(f.notify.decide(f.token, 'allow')).toEqual({ state: 'decided' });
    expect(answers(f.link)).toHaveLength(1);
    expect(answers(f.link)[0]).toMatchObject({ requestId: 'r1', decision: 'allow' });
    expect(f.notify.decide(f.token, 'allow')).toEqual({ state: 'used' });
    expect(f.notify.inspect(f.token)).toEqual({ state: 'used' });
    expect(answers(f.link)).toHaveLength(1);
  });

  it('denies on request', async () => {
    const f = await linked();
    expect(f.notify.decide(f.token, 'deny')).toEqual({ state: 'decided' });
    expect(answers(f.link)[0]).toMatchObject({ decision: 'deny' });
  });

  it('reports a request another window answered', async () => {
    const f = await linked();
    f.control.answerPermission('s1', 'dev-a', 'r1', { decision: 'deny' });
    expect(f.notify.inspect(f.token)).toEqual({ state: 'answered' });
    expect(f.notify.decide(f.token, 'allow')).toEqual({ state: 'answered' });
    expect(answers(f.link)).toHaveLength(1);
  });

  it('expires after the lifetime of the request', async () => {
    const f = await linked();
    f.clock.now = T0 + 10 * 60_000 + 1;
    expect(f.notify.inspect(f.token)).toEqual({ state: 'expired' });
    expect(f.notify.decide(f.token, 'allow')).toEqual({ state: 'expired' });
    expect(answers(f.link)).toHaveLength(0);
  });

  it('refuses what only the web UI may approve and does not spend the link', async () => {
    const f = await linked('Bash', { command: 'rm -rf build' });
    expect(f.notify.inspect(f.token)).toMatchObject({ state: 'webui-only', tool: 'Bash' });
    expect(f.notify.decide(f.token, 'allow')).toEqual({ state: 'webui-only' });
    expect(answers(f.link)).toHaveLength(0);
    const mcp = await linked('mcp__srv__do', {});
    expect(mcp.notify.inspect(mcp.token)).toMatchObject({ state: 'webui-only' });
  });

  it('approves a plan with its default mode and gives it ten minutes', async () => {
    const f = await linked(
      'ExitPlanMode',
      {},
      {
        plan: '# The plan',
        option: 'o',
        modes: ['default', 'acceptEdits'],
        default: 'acceptEdits',
      },
    );
    expect(f.notify.inspect(f.token)).toMatchObject({
      state: 'open',
      kind: 'plan',
      plan: '# The plan',
      planMode: 'acceptEdits',
      expiresAt: T0 + 10 * 60_000,
    });
    expect(f.notify.decide(f.token, 'allow')).toEqual({ state: 'decided' });
    expect(answers(f.link)[0]).toMatchObject({ decision: 'allow', mode: 'acceptEdits' });
  });

  it('rejects a token this core did not sign, also after the secrets file is replaced', async () => {
    const f = await linked();
    expect(f.notify.inspect('abcdefgh.abcdefgh')).toBeNull();
    expect(f.notify.decide('abcdefgh.abcdefgh', 'allow')).toEqual({ error: 'invalid-token' });
    const other = setup();
    expect(other.notify.inspect(f.token)).toBeNull();
  });

  it('reports a request closed by the owner going offline as answered', async () => {
    const f = await linked();
    f.control.detach(f.link);
    expect(f.notify.decide(f.token, 'allow')).toEqual({ state: 'answered' });
  });
});

describe('routes', () => {
  async function api() {
    const f = setup();
    const server = createServer();
    createApi(f.store, { port: 0, dataDir: f.dir, token: 'host-token', webDir: null }, f.control, {
      notify: f.notify,
    }).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const call = async (method: string, route: string, body?: unknown, host = true) => {
      const res = await fetch(`${base}${route}`, {
        method,
        headers: {
          ...(host ? { authorization: 'Bearer host-token' } : {}),
          ...(body === undefined ? {} : { 'content-type': 'application/json' }),
        },
        body: body === undefined ? null : JSON.stringify(body),
      });
      const text = await res.text();
      return {
        status: res.status,
        body: text ? (JSON.parse(text) as Record<string, unknown>) : {},
      };
    };
    return { ...f, call };
  }

  it('needs the host token for settings and none for the action routes', async () => {
    const a = await api();
    expect((await a.call('GET', '/api/notify/preferences', undefined, false)).status).toBe(401);
    expect((await a.call('GET', '/api/notify/preferences')).status).toBe(200);
    const bad = await a.call('POST', '/api/action/inspect', { token: 'abcdefgh.abcdefgh' }, false);
    expect(bad.status).toBe(400);
    expect(bad.body.code).toBe('invalid-token');
    const malformed = await a.call('POST', '/api/action/inspect', { token: 'x' }, false);
    expect(malformed.status).toBe(400);
  });

  it('inspects and decides through a link without the host token', async () => {
    const a = await api();
    const hook = await receiver([200]);
    a.store.addWebhook({ url: `${hook.base}/h`, label: 'Chat' }, T0);
    a.control.receive(a.link, permission('r1', 'Bash', { command: 'npm test' }));
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    const token = tokenOf(json(hook.got[0] as Received));

    const seen = await a.call('POST', '/api/action/inspect', { token }, false);
    expect(seen).toMatchObject({ status: 200, body: { state: 'open', tool: 'Bash' } });
    const done = await a.call('POST', '/api/action/decide', { token, decision: 'allow' }, false);
    expect(done).toMatchObject({ status: 200, body: { state: 'decided' } });
    const again = await a.call('POST', '/api/action/decide', { token, decision: 'allow' }, false);
    expect(again.body.state).toBe('used');
    expect((await a.call('POST', '/api/action/inspect', { token }, false)).body.state).toBe('used');
  });

  it('limits how often the action routes answer', async () => {
    const a = await api();
    const statuses: number[] = [];
    for (let i = 0; i < 40; i++) {
      statuses.push(
        (await a.call('POST', '/api/action/inspect', { token: 'abcdefgh.abcdefgh' }, false)).status,
      );
    }
    expect(statuses.slice(0, 30).every((s) => s === 400)).toBe(true);
    expect(statuses.slice(30).every((s) => s === 429)).toBe(true);
  });

  it('stores preferences', async () => {
    const a = await api();
    expect((await a.call('GET', '/api/notify/preferences')).body).toEqual(prefs());
    const next = prefs({
      preset: 'everything',
      projects: { '/work/app': { preset: 'off', events: {} } },
      quietHours: { start: '22:00', end: '07:00', timeZone: 'Europe/Berlin' },
    });
    expect((await a.call('PUT', '/api/notify/preferences', next)).body).toEqual(next);
    expect((await a.call('GET', '/api/notify/preferences')).body).toEqual(next);
    expect((await a.call('PUT', '/api/notify/preferences', { preset: 'nope' })).status).toBe(400);
  });

  it('manages push subscriptions without exposing the endpoint', async () => {
    const a = await api();
    const key = await a.call('GET', '/api/notify/push/key');
    expect(key.body.publicKey).toBe(a.notify.pushKey());
    const ua = createECDH('prime256v1');
    ua.generateKeys();
    const sub = {
      endpoint: 'https://push.example/send/secret-endpoint-id',
      keys: {
        p256dh: ua.getPublicKey().toString('base64url'),
        auth: Buffer.alloc(16).toString('base64url'),
      },
      label: 'Laptop',
    };
    const created = await a.call('POST', '/api/notify/push/subscriptions', sub);
    expect(created.status).toBe(201);
    expect(created.body).toMatchObject({ label: 'Laptop', service: 'push.example' });
    expect(JSON.stringify(created.body)).not.toContain('secret-endpoint-id');
    const again = await a.call('POST', '/api/notify/push/subscriptions', {
      ...sub,
      label: 'Renamed',
    });
    expect(again.body.id).toBe(created.body.id);
    expect((await a.call('GET', '/api/notify/push/subscriptions')).body.subscriptions).toHaveLength(
      1,
    );
    const gone = await a.call('POST', '/api/notify/push/unsubscribe', { endpoint: sub.endpoint });
    expect(gone.body.subscriptions).toEqual([]);
    const second = await a.call('POST', '/api/notify/push/subscriptions', sub);
    const removed = await a.call('DELETE', `/api/notify/push/subscriptions/${second.body.id}`);
    expect(removed.body.subscriptions).toEqual([]);
    expect((await a.call('DELETE', '/api/notify/push/subscriptions/missing')).status).toBe(404);
  });

  it('manages webhooks and never returns the secret', async () => {
    const a = await api();
    const hook = await receiver([200]);
    const created = await a.call('POST', '/api/notify/webhooks', {
      url: `${hook.base}/h`,
      label: 'Chat',
      secret: 'top-secret-value',
    });
    expect(created.status).toBe(201);
    expect(created.body).toMatchObject({
      label: 'Chat',
      hasSecret: true,
      enabled: true,
      events: ['permission', 'plan', 'failure', 'budget'],
    });
    expect(JSON.stringify(created.body)).not.toContain('top-secret-value');
    const id = created.body.id as string;

    const tested = await a.call('POST', `/api/notify/webhooks/${id}/test`);
    expect((tested.body.delivery as { ok: boolean }).ok).toBe(true);
    expect(hook.got[0]?.headers['x-hoplet-signature']).toBe(
      signBody('top-secret-value', hook.got[0]?.body ?? ''),
    );

    const patched = await a.call('PATCH', `/api/notify/webhooks/${id}`, {
      enabled: false,
      secret: null,
    });
    expect(patched.body).toMatchObject({ enabled: false, hasSecret: false });
    const chosen = await a.call('PATCH', `/api/notify/webhooks/${id}`, {
      events: ['turn-done', 'plan'],
    });
    expect(chosen.body.events).toEqual(['plan', 'turn-done']);
    for (const events of [[], ['plan', 'plan'], ['nope']])
      expect((await a.call('PATCH', `/api/notify/webhooks/${id}`, { events })).status).toBe(400);
    expect((await a.call('PATCH', '/api/notify/webhooks/missing', { enabled: true })).status).toBe(
      404,
    );
    expect(
      (await a.call('POST', '/api/notify/webhooks', { url: 'ftp://x.test', label: 'x' })).status,
    ).toBe(400);

    const list = await a.call('GET', '/api/notify/webhooks');
    expect(list.body.webhooks).toHaveLength(1);
    expect((await a.call('DELETE', `/api/notify/webhooks/${id}`)).body.webhooks).toEqual([]);
    expect((await a.call('POST', '/api/notify/webhooks/missing/test')).status).toBe(404);
  });

  it('limits the number of webhooks', async () => {
    const a = await api();
    for (let i = 0; i < 20; i++) {
      const r = await a.call('POST', '/api/notify/webhooks', {
        url: `http://127.0.0.1:9/${i}`,
        label: `h${i}`,
      });
      expect(r.status).toBe(201);
    }
    expect(
      (await a.call('POST', '/api/notify/webhooks', { url: 'http://127.0.0.1:9/x', label: 'x' }))
        .status,
    ).toBe(413);
  });
});
