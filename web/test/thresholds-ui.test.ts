// @vitest-environment jsdom
import {
  DEFAULT_RESOURCE_THRESHOLDS,
  type DeviceInfo,
  type ResourceThresholds,
} from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const api = vi.hoisted(() => ({
  fetchResourceThresholds: vi.fn(),
  putResourceThresholds: vi.fn(),
}));
const devices = vi.hoisted(() => ({ refreshDevices: vi.fn() }));
vi.mock('../src/lib/api.ts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/lib/api.ts')>()),
  ...api,
}));

vi.mock('../src/lib/use-devices.ts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/lib/use-devices.ts')>()),
  refreshDevices: devices.refreshDevices,
}));

import {
  THRESHOLDS_SAVE_DELAY_MS,
  useResourceThresholds,
} from '../src/lib/use-resource-thresholds.ts';
import { ThresholdsGroup } from '../src/screens/settings/machine-thresholds.tsx';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const device = { deviceId: 'dev-studio', name: 'studio' } as DeviceInfo;
const stored: ResourceThresholds = { default: { memory: 90, disk: 10 }, machines: {} };

function Harness() {
  const state = useResourceThresholds();
  return createElement(ThresholdsGroup, { device, state });
}

let root: Root;
let host: HTMLDivElement;

const q = (id: string) =>
  host.querySelector<HTMLElement>(`[data-testid="settings.thresholds.${id}"]`);
const type = async (id: string, value: string) => {
  const input = q(id) as HTMLInputElement;
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
  await act(async () => {
    set?.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
};
const click = async (el: Element | null | undefined) => {
  await act(async () => {
    (el as HTMLElement).click();
  });
};
const wait = async (ms: number) => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
};
const mode = (label: string) =>
  [...host.querySelectorAll<HTMLElement>('[role="radio"]')].find((b) => b.textContent === label);

beforeEach(async () => {
  vi.useFakeTimers();
  api.fetchResourceThresholds.mockReset().mockResolvedValue(stored);
  api.putResourceThresholds.mockReset().mockImplementation(async (t: ResourceThresholds) => t);
  devices.refreshDevices.mockReset();
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  await act(async () => {
    root.render(createElement(Harness));
  });
  await wait(0);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

describe('the Warnings group', () => {
  it('summarises the default and opens its fields with Change default', async () => {
    expect(q('summary')?.textContent).toContain('90%');
    expect(q('summary')?.textContent).toContain('10%');
    await click(q('change-default'));
    expect((q('memory') as HTMLInputElement).value).toBe('90');
    expect((q('disk') as HTMLInputElement).value).toBe('10');
  });

  it('saves the default once, after the last keystroke, with the whole document', async () => {
    await click(q('change-default'));
    await type('memory', '9');
    await type('memory', '95');
    expect(api.putResourceThresholds).not.toHaveBeenCalled();
    await wait(THRESHOLDS_SAVE_DELAY_MS);
    expect(api.putResourceThresholds).toHaveBeenCalledTimes(1);
    expect(api.putResourceThresholds).toHaveBeenCalledWith({
      default: { memory: 95, disk: 10 },
      machines: {},
    });
    await wait(0);
    expect(devices.refreshDevices).toHaveBeenCalledTimes(1);
  });

  it('shows an invalid field and does not save it', async () => {
    await click(q('change-default'));
    await type('memory', '101');
    expect(q('memory')?.getAttribute('aria-invalid')).toBe('true');
    expect(q('invalid')).not.toBeNull();
    await wait(THRESHOLDS_SAVE_DELAY_MS * 2);
    expect(api.putResourceThresholds).not.toHaveBeenCalled();
    await type('disk', '100');
    expect(q('disk')?.getAttribute('aria-invalid')).toBe('true');
    await type('memory', '80');
    await type('disk', '5');
    await wait(THRESHOLDS_SAVE_DELAY_MS);
    expect(api.putResourceThresholds).toHaveBeenCalledWith({
      default: { memory: 80, disk: 5 },
      machines: {},
    });
  });

  it('creates an override from the default on Custom and removes it on Default', async () => {
    await click(mode('Custom for studio'));
    await wait(THRESHOLDS_SAVE_DELAY_MS);
    expect(api.putResourceThresholds).toHaveBeenLastCalledWith({
      default: { memory: 90, disk: 10 },
      machines: { 'dev-studio': { memory: 90, disk: 10 } },
    });
    await type('memory', '97');
    await wait(THRESHOLDS_SAVE_DELAY_MS);
    expect(api.putResourceThresholds).toHaveBeenLastCalledWith({
      default: { memory: 90, disk: 10 },
      machines: { 'dev-studio': { memory: 97, disk: 10 } },
    });
    await click(mode('Default'));
    await wait(THRESHOLDS_SAVE_DELAY_MS);
    expect(api.putResourceThresholds).toHaveBeenLastCalledWith(DEFAULT_RESOURCE_THRESHOLDS);
  });

  it('shows a failed save and returns to what the core holds', async () => {
    api.putResourceThresholds.mockRejectedValueOnce(new Error('nope'));
    await click(q('change-default'));
    await type('memory', '70');
    await wait(THRESHOLDS_SAVE_DELAY_MS);
    expect(q('error')).not.toBeNull();
    expect((q('memory') as HTMLInputElement).value).toBe('90');
  });
});
