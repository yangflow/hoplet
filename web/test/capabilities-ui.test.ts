// @vitest-environment jsdom
import type { DeviceInfo } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const api = vi.hoisted(() => ({ putDeviceLabels: vi.fn() }));
vi.mock('../src/lib/api.ts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/lib/api.ts')>()),
  ...api,
}));

import { CapabilitiesGroup } from '../src/screens/settings/machine-capabilities.tsx';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const NOW = 1_000_000_000;
const GIB = 1024 ** 3;
const device = (over: Partial<DeviceInfo> = {}): DeviceInfo => ({
  deviceId: 'dev-studio',
  name: 'studio',
  connected: true,
  system: { os: 'linux', arch: 'amd64' },
  providers: [],
  labels: ['gpu', 'release'],
  inventory: {
    at: NOW - 17 * 60_000,
    toolchains: [
      { name: 'git', version: '2.51.0' },
      { name: 'node', version: '24.9.0' },
    ],
    gpus: [{ name: 'NVIDIA GeForce RTX 4090', memory: 24 * GIB }],
    repositories: [
      { path: '/home/me/code/hoplet', origin: 'github.com/yangflow/hoplet' },
      { path: '/home/me/code/scratch', origin: null },
    ],
  },
  ...over,
});

let root: Root;
let host: HTMLDivElement;
const q = (id: string) =>
  host.querySelector<HTMLElement>(`[data-testid="settings.capabilities.${id}"]`);
const render = async (d: DeviceInfo, props: Record<string, unknown> = {}) => {
  await act(async () =>
    root.render(createElement(CapabilitiesGroup, { device: d, now: NOW, ...props })),
  );
};
const click = async (el: Element | null | undefined) => {
  await act(async () => (el as HTMLElement).click());
};
const type = async (value: string) => {
  const input = q('input') as HTMLInputElement;
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
  await act(async () => {
    set?.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
};
const key = async (k: string) => {
  await act(async () => {
    q('input')?.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }));
  });
};
const chips = () => [...host.querySelectorAll('.cp-lb')].map((c) => c.textContent);

beforeEach(() => {
  Object.defineProperty(navigator, 'language', { configurable: true, value: 'en-US' });
  api.putDeviceLabels
    .mockReset()
    .mockImplementation(async (_id: string, labels: string[]) => ({ labels }));
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

describe('the reported part', () => {
  it('shows tools, the missing ones, the GPU, and the origin of each project', async () => {
    await render(device());
    expect(q('at')?.textContent).toBe('Detected 17 min ago');
    expect(q('toolchains')?.textContent).toContain('git2.51.0');
    expect(q('toolchains')?.textContent).toContain('Not found: pnpm, npm, bun');
    expect(q('gpu')?.textContent).toContain('NVIDIA GeForce RTX 4090 · 24 GB');
    expect(q('gpu')?.textContent).not.toContain('unified');
    expect(q('repositories')?.textContent).toContain('hoplet');
    expect(q('repositories')?.textContent).toContain('github.com/yangflow/hoplet');
    expect(q('repositories')?.textContent).toContain('scratchno origin');
  });

  it('calls the Apple chip memory unified', async () => {
    await render(device({ system: { os: 'darwin', arch: 'arm64' } }));
    expect(q('gpu')?.textContent).toContain('24 GB unified');
  });

  it('leaves the GPU line out when there is none', async () => {
    const d = device();
    await render({ ...d, inventory: d.inventory && { ...d.inventory, gpus: [] } });
    expect(q('gpu')).toBeNull();
    expect(q('toolchains')).not.toBeNull();
  });

  it('greys an offline machine and says when it last reported; labels stay', async () => {
    await render(device({ connected: false }));
    expect(host.querySelector('.rcap.stale')).not.toBeNull();
    expect(q('at')?.textContent).toBe('Last reported 17 min ago');
    expect(q('add')).not.toBeNull();
  });

  it('says so for an agent that reports nothing, and still has the labels', async () => {
    const { inventory: _drop, ...bare } = device();
    await render({ ...bare, labels: ['lab'] });
    expect(q('none')?.textContent).toContain('studio’s agent does not report capabilities');
    expect(q('toolchains')).toBeNull();
    expect(q('at')).toBeNull();
    expect(chips()).toEqual(['lab']);
  });
});

describe('the labels', () => {
  it('adds a label with Enter, saving the whole list', async () => {
    await render(device());
    await click(q('add'));
    await type('ci-runner');
    await key('Enter');
    expect(api.putDeviceLabels).toHaveBeenCalledWith('dev-studio', ['gpu', 'release', 'ci-runner']);
    expect(chips()).toEqual(['gpu', 'release', 'ci-runner'].map((l) => l));
  });

  it('shows the rule for an invalid label and does not save it', async () => {
    await render(device());
    await click(q('add'));
    await type('GPU box');
    await key('Enter');
    expect(q('invalid')?.textContent).toContain('“GPU box” is not a label');
    expect(q('input')?.getAttribute('aria-invalid')).toBe('true');
    expect(api.putDeviceLabels).not.toHaveBeenCalled();
    await type('gpu-box');
    expect(q('invalid')).toBeNull();
  });

  it('cancels with Escape', async () => {
    await render(device());
    await click(q('add'));
    await type('x');
    await key('Escape');
    expect(q('input')).toBeNull();
    expect(api.putDeviceLabels).not.toHaveBeenCalled();
  });

  it('removes a label and puts it back with Undo', async () => {
    await render(device());
    await click(q('remove.release'));
    expect(api.putDeviceLabels).toHaveBeenLastCalledWith('dev-studio', ['gpu']);
    expect(q('removed')?.textContent).toContain('Removed release.');
    expect(chips()).toEqual(['gpu']);
    await click(q('undo'));
    expect(api.putDeviceLabels).toHaveBeenLastCalledWith('dev-studio', ['gpu', 'release']);
    expect(chips()).toEqual(['gpu', 'release']);
  });

  it('returns to the stored labels and says why when the save fails', async () => {
    api.putDeviceLabels.mockRejectedValueOnce(new Error('boom'));
    await render(device());
    await click(q('remove.gpu'));
    expect(q('error')).not.toBeNull();
    expect(chips()).toEqual(['gpu', 'release']);
  });

  it('keeps labels locally in a fixture', async () => {
    await render(device(), { local: true });
    await click(q('remove.gpu'));
    expect(api.putDeviceLabels).not.toHaveBeenCalled();
    expect(chips()).toEqual(['release']);
  });
});
