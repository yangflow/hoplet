// @vitest-environment jsdom
import type { DeviceInfo, LinesPage, LiveSessionMeta } from '@hoplet/shared';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import App from '../src/App.tsx';

const api = vi.hoisted(() => ({
  fetchSessions: vi.fn(),
  fetchLines: vi.fn(),
  fetchDevices: vi.fn(),
  fetchDeviceProjects: vi.fn(),
  fetchWorkspace: vi.fn(),
  socketUrl: vi.fn(),
  postNewSession: vi.fn(),
  postPrompt: vi.fn(),
}));
vi.mock('../src/lib/api.ts', async (original) => ({ ...(await original()), ...api }));

const A = 'aaaaaaaa-2222-4333-8444-555555555555';
const B = 'bbbbbbbb-2222-4333-8444-555555555555';
const caps = {
  permissions: true,
  interrupt: true,
  thinking: true,
  fast: true,
  models: ['opus-5.5', 'haiku-5'],
};
const meta = (sessionId: string, title: string, over: Partial<LiveSessionMeta> = {}) =>
  ({
    sessionId,
    title,
    deviceName: 'MacBook Pro',
    ownerDeviceId: 'dev-mac',
    projectPath: '/repo/hoplet',
    projectDir: '-repo-hoplet',
    provider: 'claude',
    ownerConnected: true,
    createdAt: 1,
    updatedAt: 2,
    lineCount: 0,
    byteSize: 0,
    ownerEpoch: 0,
    runtime: { state: 'idle' },
    ...over,
  }) as LiveSessionMeta;
const devices: DeviceInfo[] = [
  {
    deviceId: 'dev-mac',
    name: 'MacBook Pro',
    connected: true,
    providers: [{ provider: 'claude', capabilities: caps }],
  },
];
const empty = (id: string): LinesPage => ({
  sessionId: id,
  start: 0,
  end: 0,
  total: 0,
  entries: [],
});

type FakeSocket = {
  onopen: (() => void) | null;
  onmessage: ((e: { data: string }) => void) | null;
  onclose: (() => void) | null;
  sent: string[];
};
let sockets: FakeSocket[] = [];
let root: Root;
let host: HTMLDivElement;

async function mount() {
  await act(async () => root.render(createElement(App)));
}
async function open() {
  const socket = sockets[sockets.length - 1] as FakeSocket;
  await act(async () => socket.onopen?.());
  return socket;
}
const q = (id: string) => host.querySelector(`[data-testid="${id}"]`);
const rows = () => [...host.querySelectorAll('[data-testid="cockpit.session"]')];

beforeEach(() => {
  vi.resetAllMocks();
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  Object.defineProperty(navigator, 'language', { configurable: true, value: 'en-US' });
  localStorage.setItem('hoplet_token', 'test-fixture');
  history.replaceState(null, '', '/');
  sockets = [];
  vi.stubGlobal(
    'WebSocket',
    class {
      onopen = null;
      onmessage = null;
      onclose = null;
      sent: string[] = [];
      constructor() {
        sockets.push(this as unknown as FakeSocket);
      }
      send(data: string) {
        this.sent.push(data);
      }
      close() {}
    },
  );
  api.socketUrl.mockResolvedValue('ws://localhost/ws?ticket=t');
  api.fetchSessions.mockResolvedValue({ sessions: [meta(A, 'first'), meta(B, 'second')] });
  api.fetchLines.mockImplementation(async (id: string) => empty(id));
  api.fetchDevices.mockResolvedValue({ devices });
  api.fetchDeviceProjects.mockResolvedValue({
    deviceId: 'dev-mac',
    candidates: [],
    projects: [
      { path: '/repo/hoplet', sessions: [] },
      { path: '/repo/other', sessions: [] },
    ],
  });
  api.fetchWorkspace.mockResolvedValue({
    sessionId: A,
    git: true,
    commit: 'c',
    paths: [],
    truncated: false,
    changes: [],
  });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  localStorage.clear();
});

it('keeps the selected session in the URL so a reload returns to it', async () => {
  history.replaceState(null, '', `/?session=${B}`);
  await mount();
  expect(rows().map((r) => r.getAttribute('aria-current'))).toEqual([null, 'true']);
  await act(async () => (rows()[0] as HTMLElement).click());
  expect(new URLSearchParams(location.search).get('session')).toBe(A);
  await act(async () => {
    history.replaceState(null, '', `/?session=${B}`);
    window.dispatchEvent(new PopStateEvent('popstate'));
  });
  expect(rows().map((r) => r.getAttribute('aria-current'))).toEqual([null, 'true']);
});

it('selects the first session when the URL names none, and writes it to the URL', async () => {
  await mount();
  expect(new URLSearchParams(location.search).get('session')).toBe(A);
  expect(rows().filter((r) => r.getAttribute('aria-current') === 'true')).toHaveLength(1);
});

it('shows the live socket in the sidebar foot and the runtime state of sessions as glyphs', async () => {
  await mount();
  expect(q('cockpit.live')?.textContent).toContain('Reconnecting');
  const socket = await open();
  expect(q('cockpit.live')?.textContent).not.toContain('Reconnecting');
  await act(async () =>
    socket.onmessage?.({
      data: JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'session.runtime',
        sessionId: B,
        state: 'waiting-permission',
        capabilities: caps,
      }),
    }),
  );
  const glyph = rows()[1]?.querySelector('[role="img"][aria-label="Waiting for permission"]');
  expect(glyph).not.toBeNull();
  await act(async () => socket.onclose?.());
  expect(q('cockpit.live')?.textContent).toContain('Reconnecting');
});

it('moves a session to its new owner as soon as a handoff is done', async () => {
  await mount();
  const socket = await open();
  const loads = api.fetchSessions.mock.calls.length;
  api.fetchSessions.mockResolvedValue({
    sessions: [
      meta(A, 'first', { ownerDeviceId: 'dev-studio', deviceName: 'studio' }),
      meta(B, 'second'),
    ],
  });
  // Within a second of the refresh on open: a done handoff is not held back by the refresh interval.
  await act(async () =>
    socket.onmessage?.({
      data: JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'handoff.progress',
        sessionId: A,
        handoffId: 'h1',
        step: 'done',
        targetDeviceId: 'dev-studio',
      }),
    }),
  );
  expect(api.fetchSessions.mock.calls.length).toBe(loads + 1);
  const studio = [...host.querySelectorAll('[data-testid="cockpit.device"]')].find((d) =>
    d.textContent?.includes('studio'),
  );
  expect(studio?.textContent).toContain('first');
});

it("reloads the target's watched projects when a handoff is done", async () => {
  await mount();
  const socket = await open();
  const loads = api.fetchDeviceProjects.mock.calls.filter((c) => c[0] === 'dev-studio').length;
  api.fetchDeviceProjects.mockResolvedValue({
    deviceId: 'dev-studio',
    candidates: [],
    projects: [{ path: '/work/hoplet-handoff', sessions: [] }],
  });
  await act(async () =>
    socket.onmessage?.({
      data: JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'handoff.progress',
        sessionId: A,
        handoffId: 'h1',
        step: 'done',
        targetDeviceId: 'dev-studio',
      }),
    }),
  );
  const after = api.fetchDeviceProjects.mock.calls.filter((c) => c[0] === 'dev-studio').length;
  expect(after).toBe(loads + 1);
});

it('refreshes once after a burst instead of dropping activity inside the refresh interval', async () => {
  vi.useFakeTimers();
  try {
    await mount();
    const socket = await open();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    const loads = api.fetchSessions.mock.calls.length;
    const send = (msg: object) =>
      act(async () =>
        socket.onmessage?.({ data: JSON.stringify({ v: PROTOCOL_VERSION, ...msg }) }),
      );
    // An owner going offline and back right after the refresh on open: one refresh, a second later.
    await send({ type: 'device.presence', deviceId: 'dev-mac', connected: false });
    await send({ type: 'device.presence', deviceId: 'dev-mac', connected: true });
    expect(api.fetchSessions.mock.calls.length).toBe(loads);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(api.fetchSessions.mock.calls.length).toBe(loads + 1);
  } finally {
    vi.useRealTimers();
  }
});

it('subscribes the selected session to the socket, again after a reconnect', async () => {
  vi.useFakeTimers();
  try {
    await mount();
    const socket = await open();
    const subscribes = (s: FakeSocket) =>
      s.sent.map((m) => JSON.parse(m)).filter((m) => m.type === 'subscribe');
    const selected = new URLSearchParams(location.search).get('session');
    expect(subscribes(socket).map((m) => m.sessionId)).toEqual([selected]);
    await act(async () => socket.onclose?.());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    const next = sockets[sockets.length - 1] as FakeSocket;
    expect(next).not.toBe(socket);
    await act(async () => next.onopen?.());
    expect(subscribes(next).map((m) => m.sessionId)).toEqual([selected]);
  } finally {
    vi.useRealTimers();
  }
});

it('starts a session from New session with the options its machine declares, then selects it', async () => {
  api.postNewSession.mockImplementation(async () => {
    api.fetchSessions.mockResolvedValue({
      sessions: [
        meta(A, 'first'),
        meta(B, 'second'),
        meta('cccccccc-2222-4333-8444-555555555555', 'new one'),
      ],
    });
    return { ok: true, sessionId: 'cccccccc-2222-4333-8444-555555555555', promptId: 'p' };
  });
  await mount();
  await open();
  await act(async () => (q('cockpit.new-session') as HTMLElement).click());
  // Only what the machine declares is offered: a model list, thinking and fast.
  expect(q('cockpit.new-model')).not.toBeNull();
  expect(q('cockpit.new-thinking')).not.toBeNull();
  expect(q('cockpit.new-fast')).not.toBeNull();
  expect(api.fetchDeviceProjects).toHaveBeenCalledWith('dev-mac');
  const prompt = q('cockpit.new-prompt') as HTMLTextAreaElement;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(
      prompt,
      'build it',
    );
    prompt.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await act(async () => (q('cockpit.new-start') as HTMLElement).click());
  expect(api.postNewSession).toHaveBeenCalledWith({
    deviceId: 'dev-mac',
    projectPath: expect.stringMatching(/^\/repo\//),
    provider: 'claude',
    options: { model: 'opus-5.5', thinking: true, fast: false },
    text: 'build it',
  });
  expect(new URLSearchParams(location.search).get('session')).toBe(
    'cccccccc-2222-4333-8444-555555555555',
  );
  expect(q('cockpit.new-dialog')).toBeNull();
});

it('keeps the dialog open and says why when the session could not start', async () => {
  const { ApiError } = await import('../src/lib/errors.ts');
  api.postNewSession.mockRejectedValue(new ApiError(502, 'rejected', 'claude not found in PATH'));
  await mount();
  await act(async () => (q('cockpit.new-session') as HTMLElement).click());
  const prompt = q('cockpit.new-prompt') as HTMLTextAreaElement;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(
      prompt,
      'go',
    );
    prompt.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await act(async () => (q('cockpit.new-start') as HTMLElement).click());
  expect(q('cockpit.new-error')?.textContent).toContain('claude not found in PATH');
  expect(q('cockpit.new-dialog')).not.toBeNull();
});

it('pauses polling while the tab is hidden', async () => {
  vi.useFakeTimers();
  try {
    await mount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    const loads = api.fetchSessions.mock.calls.length;
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    await act(async () => document.dispatchEvent(new Event('visibilitychange')));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(120_000);
    });
    expect(api.fetchSessions.mock.calls.length).toBe(loads);
    const devicePolls = api.fetchDevices.mock.calls.length;
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    await act(async () => document.dispatchEvent(new Event('visibilitychange')));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(api.fetchSessions.mock.calls.length).toBeGreaterThan(loads);
    expect(api.fetchDevices.mock.calls.length).toBeGreaterThan(devicePolls);
  } finally {
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
    vi.useRealTimers();
  }
});
