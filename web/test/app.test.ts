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
      { path: '/repo/hoplet', branch: 'main', sessions: [] },
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

/** Types into the composer's field as a user would. */
async function type(text: string) {
  const field = q('cockpit.composer-input') as HTMLTextAreaElement;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(field, text);
    field.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
const click = async (el: Element | null) => act(async () => (el as HTMLElement).click());

it('starts a session from a draft with the options its machine declares, then selects it', async () => {
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
  await click(q('cockpit.new-session'));
  // New session is a draft in place of the conversation, with its own row and title.
  expect(q('cockpit.draft')).not.toBeNull();
  expect(q('cockpit.draft-row')?.textContent).toBe('New session');
  expect(q('cockpit.topbar')?.textContent).toContain('New session');
  expect(api.fetchDeviceProjects).toHaveBeenCalledWith('dev-mac');
  expect(q('cockpit.draft-directory')?.getAttribute('aria-label')).toBe('Directory, hoplet');
  // The branch is the one the catalog reads in the directory.
  expect(q('cockpit.where-branch')?.textContent).toBe('main');
  // Only what the machine declares is offered: a model list, thinking and fast.
  await click(q('cockpit.model'));
  const models = [...host.querySelectorAll('[data-testid="cockpit.model-option"]')];
  expect(models.map((m) => m.textContent)).toEqual(['opus-5.5', 'haiku-5']);
  await click(models[1] ?? null);
  await click(q('cockpit.fast'));
  expect(q('cockpit.thinking')?.getAttribute('aria-checked')).toBe('true');
  expect(q('cockpit.fast')?.getAttribute('aria-checked')).toBe('true');
  await type('build it');
  await click(q('cockpit.send'));
  expect(api.postNewSession).toHaveBeenCalledWith({
    deviceId: 'dev-mac',
    projectPath: '/repo/hoplet',
    provider: 'claude',
    options: { model: 'haiku-5', thinking: true, fast: true },
    text: 'build it',
    images: [],
  });
  expect(new URLSearchParams(location.search).get('session')).toBe(
    'cccccccc-2222-4333-8444-555555555555',
  );
  expect(q('cockpit.draft')).toBeNull();
  expect(q('cockpit.draft-row')).toBeNull();
});

it('keeps the draft and its text and says why when the session could not start', async () => {
  const { ApiError } = await import('../src/lib/errors.ts');
  api.postNewSession.mockRejectedValue(new ApiError(502, 'rejected', 'claude not found in PATH'));
  await mount();
  await click(q('cockpit.new-session'));
  await type('go');
  await click(q('cockpit.send'));
  expect(q('cockpit.composer-error')?.textContent).toContain('claude not found in PATH');
  expect(q('cockpit.draft')).not.toBeNull();
  expect((q('cockpit.composer-input') as HTMLTextAreaElement).value).toBe('go');
});

it('starts a draft where the open session runs, and selecting a session leaves it', async () => {
  history.replaceState(null, '', `/?session=${B}`);
  api.fetchSessions.mockResolvedValue({
    sessions: [
      meta(A, 'first'),
      meta(B, 'second', { projectPath: '/repo/other', gitBranch: 'feat/x' }),
    ],
  });
  await mount();
  await open();
  // The open session's composer says where it runs, read only.
  expect(q('cockpit.where')?.textContent).toBe('MacBook Pro/other/feat/x');
  await act(async () => {
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'n', metaKey: true }));
  });
  expect(q('cockpit.draft')).not.toBeNull();
  expect(q('cockpit.draft-directory')?.getAttribute('aria-label')).toBe('Directory, other');
  // A directory that is not in a repository has no branch, and no separator for one.
  expect(q('cockpit.where-branch')).toBeNull();
  expect(q('cockpit.where')?.textContent).toBe('MacBook Pro/other');
  await click(rows()[0] ?? null);
  expect(q('cockpit.draft')).toBeNull();
  expect(new URLSearchParams(location.search).get('session')).toBe(A);
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
