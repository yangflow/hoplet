// @vitest-environment jsdom
import type { Entry, LinesPage, LiveSessionMeta, SessionRuntimeInfo } from '@hoplet/shared';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ApiError } from '../src/lib/errors.ts';
import { emitLive } from '../src/lib/live.ts';
import { Conversation } from '../src/screens/cockpit/conversation/conversation.tsx';

const api = vi.hoisted(() => ({
  fetchLines: vi.fn(),
  fetchBlock: vi.fn(),
  postPrompt: vi.fn(),
  postConfig: vi.fn(),
  postAttachment: vi.fn(),
  postPermission: vi.fn(),
  postInterrupt: vi.fn(),
  postStop: vi.fn(),
}));
vi.mock('../src/lib/api.ts', async (original) => ({ ...(await original()), ...api }));

const SID = '11111111-2222-4333-8444-555555555555';
const session = {
  sessionId: SID,
  title: 'T',
  deviceName: 'MacBook Pro',
  projectPath: '/repo',
  provider: 'claude',
  ownerConnected: true,
  ownerDeviceId: 'dev-mac',
  lineCount: 4,
} as LiveSessionMeta;
const caps = { permissions: true, interrupt: true, config: [] };

const entry = (lineNo: number, role: Entry['role'], text: string): Entry => ({
  lineNo,
  role,
  blocks: [{ type: 'text', text }],
});
const stored = (entries: Entry[], total = entries.length): LinesPage => ({
  sessionId: SID,
  start: entries[0]?.lineNo ?? 0,
  end: (entries[entries.length - 1]?.lineNo ?? -1) + 1,
  total,
  entries,
});
const history = [entry(0, 'user', 'first prompt'), entry(1, 'assistant', 'first answer')];

let root: Root;
let host: HTMLDivElement;

async function show(runtime: SessionRuntimeInfo, over: Record<string, unknown> = {}) {
  await act(async () =>
    root.render(
      createElement(Conversation, {
        session,
        runtime,
        live: true,
        onHandoff: () => undefined,
        ...over,
      }),
    ),
  );
}
const q = (id: string) => host.querySelector(`[data-testid="${id}"]`);
const live = (msg: Record<string, unknown>) =>
  act(async () =>
    emitLive({ kind: 'message', msg: { v: PROTOCOL_VERSION, sessionId: SID, ...msg } as never }),
  );
function type(text: string) {
  const field = q('cockpit.composer-input') as HTMLTextAreaElement;
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(field, text);
  field.dispatchEvent(new Event('input', { bubbles: true }));
}

beforeEach(() => {
  vi.resetAllMocks();
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  Object.defineProperty(navigator, 'language', { configurable: true, value: 'en-US' });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  api.fetchLines.mockResolvedValue(stored(history));
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

it('opens on the last 80 lines and shows the turn with its prompt and answer', async () => {
  await show({ state: 'idle', capabilities: caps });
  expect(api.fetchLines).toHaveBeenCalledWith(SID, -80, 80);
  expect(q('cockpit.turn-head')?.textContent).toContain('first prompt');
  expect(q('cockpit.text')?.textContent).toBe('first answer');
  expect(q('cockpit.composer-input')?.getAttribute('placeholder')).toBe('Ask Claude Code…');
});

it('hides the microphone until transcription is available', async () => {
  await show({ state: 'idle' });
  expect(q('cockpit.mic')).toBeNull();
  await show({ state: 'idle' }, { voiceAvailable: true });
  expect(q('cockpit.mic')).not.toBeNull();
});

it('sends a prompt with a client id, shows its turn at once and maps errors to copy', async () => {
  await show({ state: 'idle', capabilities: caps });
  let refuse: ((e: unknown) => void) | undefined;
  api.postPrompt.mockReturnValue(
    new Promise((_, reject) => {
      refuse = reject;
    }),
  );
  await act(async () => type('run the tests'));
  await act(async () => {
    (q('cockpit.send') as HTMLButtonElement).click();
  });
  expect(api.postPrompt).toHaveBeenCalledWith(SID, 'run the tests', []);
  expect(q('cockpit.live-turn')?.textContent).toContain('run the tests');
  await act(async () => refuse?.(new ApiError(409, 'busy')));
  expect(q('cockpit.live-turn')).toBeNull();
  expect(q('cockpit.composer-error')?.textContent).toContain('busy');
  // The refused prompt is back in the field.
  expect((q('cockpit.composer-input') as HTMLTextAreaElement).value).toBe('run the tests');
});

const PNG_HEAD = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
/** A PNG file of `size` bytes: the signature, then zeros. */
function pngFile(name: string, size = 16): File {
  const bytes = new Uint8Array(size);
  bytes.set(PNG_HEAD);
  return new File([bytes], name, { type: 'image/png' });
}
async function attach(...files: File[]) {
  const input = q('cockpit.attach-input') as HTMLInputElement;
  Object.defineProperty(input, 'files', { configurable: true, value: files });
  await act(async () => {
    input.dispatchEvent(new Event('change', { bubbles: true }));
    await new Promise((r) => setTimeout(r, 0));
  });
}
const thumbs = () => host.querySelectorAll('[data-testid="cockpit.thumbs"] li').length;

it('uploads images to the owner as they are attached, sends their IDs, and gives them back when refused', async () => {
  await show({ state: 'idle', capabilities: caps }, { capabilities: caps });
  expect(q('cockpit.attach')).toBeNull();
  await show(
    { state: 'idle', capabilities: { ...caps, images: true } },
    { capabilities: { ...caps, images: true } },
  );
  expect(q('cockpit.attach')).not.toBeNull();

  const uploads: ((id: string) => void)[] = [];
  api.postAttachment.mockImplementation(
    () => new Promise<string>((resolve) => uploads.push(resolve)),
  );
  await attach(pngFile('one.png'), pngFile('two.png'));
  expect(thumbs()).toBe(2);
  const bytes = new Uint8Array(16);
  bytes.set(PNG_HEAD);
  expect(api.postAttachment).toHaveBeenCalledWith('dev-mac', {
    mediaType: 'image/png',
    data: btoa(String.fromCharCode(...bytes)),
  });
  // While an upload runs the prompt waits.
  await act(async () => type('what is wrong here'));
  expect((q('cockpit.send') as HTMLButtonElement).disabled).toBe(true);
  expect(host.querySelectorAll('[data-status="uploading"]')).toHaveLength(2);
  await act(async () => {
    uploads[0]?.('att-1');
    uploads[1]?.('att-2');
  });
  expect((q('cockpit.send') as HTMLButtonElement).disabled).toBe(false);
  await act(async () => (q('cockpit.thumb-remove') as HTMLButtonElement).click());
  expect(thumbs()).toBe(1);

  api.postPrompt.mockRejectedValue(new ApiError(409, 'busy'));
  await act(async () => (q('cockpit.send') as HTMLButtonElement).click());
  expect(api.postPrompt).toHaveBeenCalledWith(SID, 'what is wrong here', ['att-2']);
  // The refused prompt comes back with its image, still uploaded.
  expect(thumbs()).toBe(1);
  expect(api.postAttachment).toHaveBeenCalledTimes(2);
});

it('says when an upload failed and keeps the prompt from going', async () => {
  await show(
    { state: 'idle', capabilities: { ...caps, images: true } },
    { capabilities: { ...caps, images: true } },
  );
  api.postAttachment.mockRejectedValue(new ApiError(409, 'target-offline'));
  await attach(pngFile('one.png'));
  await act(async () => type('look'));
  expect(host.querySelector('[data-status="failed"]')).not.toBeNull();
  expect(q('cockpit.composer-error')).not.toBeNull();
  expect((q('cockpit.send') as HTMLButtonElement).disabled).toBe(true);
});

const configOptions = (model = 'opus') => [
  {
    type: 'select' as const,
    id: 'model',
    name: 'Model',
    category: 'model',
    currentValue: model,
    options: [
      { value: 'default', name: 'Default model' },
      { value: 'fable', name: 'fable' },
      { value: 'opus', name: 'opus' },
      { value: 'sonnet', name: 'sonnet' },
    ],
  },
  {
    type: 'boolean' as const,
    id: 'thinking',
    name: 'Thinking',
    category: 'thought_level',
    currentValue: true,
  },
  {
    type: 'boolean' as const,
    id: 'fast',
    name: 'Fast',
    category: 'model_config',
    currentValue: false,
  },
  // An option the web UI has never heard of is drawn from the driver's name alone.
  { type: 'boolean' as const, id: 'sandbox', name: 'Sandboxed', currentValue: false },
  {
    type: 'select' as const,
    id: 'mode',
    name: 'Mode',
    category: 'mode',
    currentValue: 'a',
    options: [{ value: 'a', name: 'Ask' }],
  },
];

it('offers the config options of a session and sets one as soon as it is picked', async () => {
  const fixed = { ...caps, config: [] };
  await show({ state: 'idle', capabilities: fixed }, { capabilities: fixed });
  expect(q('cockpit.model')).toBeNull();
  const all = { ...caps, config: configOptions() };
  await show({ state: 'idle', capabilities: all }, { capabilities: all });
  expect(q('cockpit.model')?.textContent).toContain('opus');
  await act(async () => (q('cockpit.model') as HTMLButtonElement).click());
  // The same menu as a draft: every option in the driver's order, the mode category left to its own control.
  const options = [...host.querySelectorAll('[data-testid="cockpit.model-option"]')];
  expect(options.map((o) => o.textContent)).toEqual(['Default model', 'fable', 'opus', 'sonnet']);
  expect(q('cockpit.config.thinking')?.getAttribute('aria-checked')).toBe('true');
  expect(q('cockpit.config.fast')?.getAttribute('aria-checked')).toBe('false');
  expect(q('cockpit.config.sandbox')?.textContent).toContain('Sandboxed');
  expect(q('cockpit.config.mode')).toBeNull();

  api.postConfig.mockResolvedValue({ ok: true });
  await act(async () => (options[3] as HTMLButtonElement).click());
  expect(api.postConfig).toHaveBeenLastCalledWith(SID, 'model', 'sonnet');
  // The pick shows at once, before the state reports it.
  expect(q('cockpit.model')?.textContent).toContain('sonnet');
  await act(async () => (q('cockpit.config.thinking') as HTMLButtonElement).click());
  expect(api.postConfig).toHaveBeenLastCalledWith(SID, 'thinking', false);
  expect(api.postPrompt).not.toHaveBeenCalled();

  // A state that reports a pick ends that pick only: thinking is not reported yet, so it still shows off.
  await show(
    { state: 'ready', capabilities: { ...caps, config: configOptions('sonnet') } },
    { capabilities: { ...caps, config: configOptions('sonnet') } },
  );
  expect(q('cockpit.model')?.textContent).toContain('sonnet');
  expect(q('cockpit.config.thinking')?.getAttribute('aria-checked')).toBe('false');
  // The menu stays usable while a turn runs, and a state that does not report the pick does not drop it.
  const running = { ...caps, config: configOptions('sonnet') };
  await show({ state: 'running', capabilities: running }, { capabilities: running });
  expect((q('cockpit.model') as HTMLButtonElement).disabled).toBe(false);
  expect(q('cockpit.config.thinking')?.getAttribute('aria-checked')).toBe('false');
});

it('takes a refused pick back and says why', async () => {
  const all = { ...caps, config: configOptions() };
  await show({ state: 'idle', capabilities: all }, { capabilities: all });
  await act(async () => (q('cockpit.model') as HTMLButtonElement).click());
  api.postConfig.mockRejectedValue(new ApiError(409, 'owner-offline'));
  await act(async () => (q('cockpit.config.fast') as HTMLButtonElement).click());
  expect(q('cockpit.config.fast')?.getAttribute('aria-checked')).toBe('false');
  expect(q('cockpit.composer-error')).not.toBeNull();
});

it('reads a session on the default model as Default model', async () => {
  const all = { ...caps, config: configOptions('default') };
  await show({ state: 'idle', capabilities: all }, { capabilities: all });
  expect(q('cockpit.model')?.textContent).toContain('Default model');
});

it('says why a file cannot be attached', async () => {
  api.postAttachment.mockResolvedValue('att');
  await show(
    { state: 'idle', capabilities: { ...caps, images: true } },
    { capabilities: { ...caps, images: true } },
  );
  await attach(new File(['hello'], 'notes.txt', { type: 'image/png' }));
  expect(q('cockpit.composer-error')?.textContent).toContain('Only PNG, JPEG, GIF and WebP images');
  await attach(pngFile('huge.png', 4 * 1024 * 1024 + 1));
  expect(q('cockpit.composer-error')?.textContent).toContain('huge.png is larger than 4 MiB');
  await attach(
    pngFile('1.png'),
    pngFile('2.png'),
    pngFile('3.png'),
    pngFile('4.png'),
    pngFile('5.png'),
  );
  expect(thumbs()).toBe(4);
  expect(q('cockpit.composer-error')?.textContent).toContain('At most 4 images');
});

it('streams a running turn into the live turn and holds the stored lines until it settles', async () => {
  const running: SessionRuntimeInfo = { state: 'running', capabilities: caps };
  await show({ state: 'idle', capabilities: caps });
  await show(running);
  expect(q('cockpit.live-turn')).not.toBeNull();
  await live({ type: 'session.output', kind: 'thinking', tokens: 40 });
  await live({ type: 'session.output', kind: 'text', text: 'Looking at ' });
  await live({ type: 'session.output', kind: 'text', text: 'the store' });
  await live({
    type: 'session.output',
    kind: 'tool-call',
    tool: 'Bash',
    toolKind: 'run',
    toolUseId: 't1',
    input: { command: 'pnpm check' },
  });
  expect(q('cockpit.live-text')?.textContent).toBe('Looking at the store');
  expect(q('cockpit.live-thinking')?.textContent).toContain('40 tokens');
  expect(q('cockpit.live-tool')?.textContent).toContain('pnpm check');
  expect(q('cockpit.live-state')?.getAttribute('aria-label')).toBe('Running');
  expect(q('cockpit.composer-input')?.getAttribute('placeholder')).toBe('Turn running');
  expect((q('cockpit.composer-input') as HTMLTextAreaElement).disabled).toBe(true);
});

it('asks for permission inside the live turn and answers once', async () => {
  await show({ state: 'idle', capabilities: caps });
  await show({ state: 'waiting-permission', capabilities: caps });
  await live({
    type: 'session.permission',
    requestId: 'r1',
    tool: 'Bash',
    toolKind: 'run',
    input: { command: 'pnpm check 2>&1 | tail -40' },
  });
  const card = q('cockpit.permission');
  expect(card?.textContent).toContain('pnpm check 2>&1 | tail -40');
  expect(card?.textContent).toMatch(/Auto-deny in (10:00|9:5\d)/);
  expect(q('cockpit.announce')?.textContent).toBe('Claude Code asks to use Bash');
  api.postPermission.mockResolvedValue({ ok: true });
  await act(async () => {
    (q('cockpit.permission-allow') as HTMLButtonElement).click();
  });
  expect(api.postPermission).toHaveBeenCalledWith(SID, 'r1', 'allow');
  expect(q('cockpit.permission')).toBeNull();
});

it('shows a plan without a countdown, approves into a mode and keeps the decision as a row', async () => {
  await show({ state: 'idle', capabilities: caps });
  await show({ state: 'waiting-permission', capabilities: caps });
  await live({
    type: 'session.permission',
    requestId: 'p1',
    tool: 'ExitPlanMode',
    toolKind: 'other',
    input: {},
    planExit: {
      plan: '## Fix the cursor\n\n1. Roll back\n2. Test',
      option: 'mode',
      modes: ['acceptEdits', 'default'],
      default: 'acceptEdits',
    },
  });
  expect(q('cockpit.plan')?.textContent).toContain('Fix the cursor');
  expect(q('cockpit.plan')?.textContent).not.toContain('Auto-deny');
  expect(q('cockpit.permission')).toBeNull();
  api.postPermission.mockResolvedValue({ ok: true });
  await act(async () => {
    (q('cockpit.plan-approve') as HTMLButtonElement).click();
  });
  expect(api.postPermission).toHaveBeenCalledWith(SID, 'p1', 'allow', { mode: 'acceptEdits' });
  expect(q('cockpit.plan')).toBeNull();
  expect(q('cockpit.plan-resolved')?.getAttribute('data-outcome')).toBe('approved');
});

it('drops a request another window answered first', async () => {
  await show({ state: 'idle', capabilities: caps });
  await show({ state: 'waiting-permission', capabilities: caps });
  await live({
    type: 'session.permission',
    requestId: 'r2',
    tool: 'Write',
    toolKind: 'write',
    input: { path: 'a' },
  });
  api.postPermission.mockRejectedValue(new ApiError(409, 'request-closed'));
  await act(async () => {
    (q('cockpit.permission-deny') as HTMLButtonElement).click();
  });
  expect(q('cockpit.permission')).toBeNull();
  expect(q('cockpit.permission-notice')?.textContent).toContain('already answered');
});

it('interrupts with Esc while a turn runs, only when the driver declares it', async () => {
  await show({ state: 'idle', capabilities: caps });
  await show({ state: 'running', capabilities: caps });
  api.postInterrupt.mockResolvedValue({ ok: true });
  await act(async () => {
    window.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }),
    );
  });
  expect(api.postInterrupt).toHaveBeenCalledWith(SID);
  expect(q('cockpit.interrupt')?.getAttribute('aria-label')).toBe('Interrupt');
  expect(q('cockpit.interrupt')?.getAttribute('title')).toBe('Interrupt · Esc');

  api.postInterrupt.mockClear();
  await show({ state: 'running', capabilities: { ...caps, interrupt: false } });
  expect(q('cockpit.interrupt')).toBeNull();
  await act(async () => {
    window.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }),
    );
  });
  expect(api.postInterrupt).not.toHaveBeenCalled();
});

it('names the runtime state in the composer only when it changes what the user can do', async () => {
  await show({ state: 'ready', capabilities: caps });
  expect(q('cockpit.stop')?.getAttribute('aria-label')).toBe('Stop Claude Code');
  await show({ state: 'external', capabilities: caps });
  expect(q('cockpit.composer')?.textContent).toContain('Read only');
  await show({ state: 'offline' });
  expect(q('cockpit.composer')?.textContent).toContain('MacBook Pro offline');
  expect(q('cockpit.handoff')?.getAttribute('aria-label')).toBe('Hand off to another machine');
  await show({ state: 'idle' });
  expect(q('cockpit.composer-state')).toBeNull();
});

it('reads new lines when they are appended while the reader follows the tail', async () => {
  await show({ state: 'idle', capabilities: caps });
  api.fetchLines.mockResolvedValue(stored([entry(2, 'user', 'second prompt')], 3));
  await act(async () =>
    emitLive({
      kind: 'message',
      msg: {
        v: PROTOCOL_VERSION,
        type: 'session.appended',
        sessionId: SID,
        appendedLines: 1,
        totalLines: 3,
      } as never,
    }),
  );
  expect(api.fetchLines).toHaveBeenLastCalledWith(SID, 2, 80);
  expect(host.querySelectorAll('[data-testid="cockpit.turn-head"]')).toHaveLength(2);
});

it('opens an elided block in place by fetching it', async () => {
  api.fetchLines.mockResolvedValue(
    stored([
      entry(0, 'user', 'read it'),
      {
        lineNo: 1,
        role: 'assistant',
        blocks: [
          {
            type: 'tool-call',
            id: 'a',
            name: 'Read',
            kind: 'read',
            input: { file_path: '/repo/x.ndjson' },
          },
        ],
      },
      {
        lineNo: 2,
        role: 'user',
        blocks: [
          { elided: true, type: 'tool-result', bytes: 20_000, preview: 'first rows', ref: '2/0' },
        ],
      },
    ]),
  );
  api.fetchBlock.mockResolvedValue({
    type: 'tool-result',
    id: 'a',
    content: 'row 1\nrow 2\nrow 3',
  });
  await show({ state: 'idle' });
  expect(q('cockpit.elided')?.textContent).toContain('first rows');
  expect(q('cockpit.block-open')?.textContent).toContain('20 KiB');
  await act(async () => {
    (q('cockpit.block-open') as HTMLButtonElement).click();
  });
  expect(api.fetchBlock).toHaveBeenCalledWith(SID, 2, 0);
  expect(q('cockpit.block-full')?.textContent).toContain('row 3');
});

it('replaces the streamed output with the stored lines once they have arrived', async () => {
  vi.useFakeTimers();
  try {
    await show({ state: 'idle', capabilities: caps });
    await show({ state: 'running', capabilities: caps });
    await live({ type: 'session.output', kind: 'text', text: 'working on it' });
    // The stored lines of the turn arrive while it runs: they stay hidden behind the live turn.
    api.fetchLines.mockResolvedValue(
      stored([entry(2, 'user', 'new prompt'), entry(3, 'assistant', 'final answer')], 4),
    );
    await act(async () =>
      emitLive({
        kind: 'message',
        msg: {
          v: PROTOCOL_VERSION,
          type: 'session.appended',
          sessionId: SID,
          appendedLines: 2,
          totalLines: 4,
        } as never,
      }),
    );
    expect(host.textContent).not.toContain('final answer');
    expect(q('cockpit.live-text')?.textContent).toBe('working on it');

    await show({ state: 'ready', capabilities: caps });
    // The turn is over, but its lines are only just in: the live turn waits until they are quiet.
    expect(q('cockpit.live-turn')).not.toBeNull();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(700);
    });
    expect(q('cockpit.live-turn')).toBeNull();
    expect(host.textContent).toContain('final answer');
    expect(host.querySelectorAll('[data-testid="cockpit.turn-head"]')).toHaveLength(2);
  } finally {
    vi.useRealTimers();
  }
});

it('leaves a turn that failed on screen with its error, and a failed start to the composer', async () => {
  await show({ state: 'idle', capabilities: caps });
  await show({ state: 'running', capabilities: caps });
  await show({ state: 'failed', capabilities: caps, error: 'claude exited with code 1' });
  expect(q('cockpit.live-turn')?.className).toContain('fail');
  expect(q('cockpit.live-error')?.textContent).toContain('claude exited with code 1');
  expect(q('cockpit.live-state')?.getAttribute('aria-label')).toBe('Failed');
  expect(q('cockpit.composer-input')?.getAttribute('placeholder')).toBe('Send again to retry');

  await act(async () => root.unmount());
  root = createRoot(host);
  await show({ state: 'failed', capabilities: caps, error: 'claude not found in PATH' });
  expect(q('cockpit.live-turn')).toBeNull();
  expect(q('cockpit.composer-state')?.textContent).toContain('Claude Code not found');
  expect(q('cockpit.retry')?.getAttribute('aria-label')).toBe('Retry start');
});

it('cycles the permission mode of an open session with Shift+Tab, also while a turn runs, never mid-composition', async () => {
  const modes = (current: string) => ({
    ...caps,
    config: [
      {
        type: 'select' as const,
        id: 'mode',
        name: 'Mode',
        category: 'mode',
        currentValue: current,
        options: [
          { value: 'default', name: 'Manual' },
          { value: 'plan', name: 'Plan' },
          { value: 'bypassPermissions', name: 'Bypass permissions', risk: 'high' as const },
        ],
      },
    ],
  });
  api.postConfig.mockResolvedValue({ ok: true });
  const open = modes('default');
  await show({ state: 'idle', capabilities: open }, { capabilities: open });
  expect(q('cockpit.mode')?.textContent).toBe('Manual');
  const shiftTab = (init: Record<string, unknown> = {}) => {
    const ev = new KeyboardEvent('keydown', {
      key: 'Tab',
      shiftKey: true,
      bubbles: true,
      cancelable: true,
      ...init,
    });
    act(async () => {
      q('cockpit.composer-input')?.dispatchEvent(ev);
    });
    return ev;
  };
  const ev = shiftTab();
  await act(async () => undefined);
  expect(ev.defaultPrevented).toBe(true);
  expect(api.postConfig).toHaveBeenLastCalledWith(SID, 'mode', 'plan');
  expect(q('cockpit.mode')?.textContent).toBe('Plan');
  shiftTab();
  await act(async () => undefined);
  expect(api.postConfig).toHaveBeenLastCalledWith(SID, 'mode', 'bypassPermissions');
  expect(q('cockpit.mode')?.getAttribute('data-risk')).toBe('high');
  // An input method composing a word owns the key.
  api.postConfig.mockClear();
  const composing = new KeyboardEvent('keydown', {
    key: 'Tab',
    shiftKey: true,
    bubbles: true,
    cancelable: true,
    isComposing: true,
  });
  await act(async () => {
    q('cockpit.composer-input')?.dispatchEvent(composing);
  });
  expect(composing.defaultPrevented).toBe(false);
  expect(api.postConfig).not.toHaveBeenCalled();
  // A turn running does not stop the switch; the pick not reported yet is where the cycle goes on from.
  const running = modes('plan');
  await show({ state: 'running', capabilities: running }, { capabilities: running });
  shiftTab();
  await act(async () => undefined);
  expect(api.postConfig).toHaveBeenLastCalledWith(SID, 'mode', 'default');
});
