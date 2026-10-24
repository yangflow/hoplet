// @vitest-environment jsdom
import type { LiveSessionMeta } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/lib/errors.ts';
import {
  cutBytes,
  DEFAULT_EXPORT_OPTIONS,
  exportQuery,
  kindLabel,
  maskList,
  scanExport,
} from '../src/screens/cockpit/export.ts';
import { ExportDialog } from '../src/screens/cockpit/export-dialog.tsx';

const api = vi.hoisted(() => ({ fetchSessionExport: vi.fn() }));
vi.mock('../src/lib/api.ts', async (orig) => ({
  ...(await orig<typeof import('../src/lib/api.ts')>()),
  ...api,
}));
const audit = vi.hoisted(() => ({ downloadBlob: vi.fn(() => true) }));
vi.mock('../src/lib/audit.ts', async (orig) => ({
  ...(await orig<typeof import('../src/lib/audit.ts')>()),
  ...audit,
}));
const answer = vi.hoisted(() => ({ lastAnswerMarkdown: vi.fn() }));
vi.mock('../src/screens/cockpit/last-answer.ts', async (orig) => ({
  ...(await orig<typeof import('../src/screens/cockpit/last-answer.ts')>()),
  ...answer,
}));

const FILE = [
  '---',
  'title: "Fix the build"',
  'turns: 3',
  '---',
  '',
  '> shown as `[redacted:<kind>]`. Other secrets may remain in the text.',
  '',
  '### Turn 1 · MacBook Pro',
  '',
  '> **You:** HOPLET_TOKEN=[redacted:hoplet-token] and [redacted:custom]',
  '',
  '### Turn 3 · studio',
  '',
  '- Ran `make` · exit 0',
  '- Output elided: 2.0 MiB, see the session in Hoplet',
  '',
  'Token [redacted:hoplet-token].',
  '',
  '## Changes',
  '',
  'url https://[redacted:url-credential]@example.com',
  '',
].join('\n');

describe('the export helpers', () => {
  it('ask only for what is ticked, with the summary and masks cut to what the core takes', () => {
    expect(
      exportQuery({ ...DEFAULT_EXPORT_OPTIONS, summary: null, mask: '' }, 'Europe/Berlin'),
    ).toEqual({
      conversation: 'all',
      changes: 'true',
      commands: 'true',
      machines: 'true',
      tz: 'Europe/Berlin',
    });
    expect(
      exportQuery({
        conversation: 'none',
        summary: '  Done.  ',
        changes: false,
        commands: false,
        machines: false,
        mask: 'acme\n\nab\n  studio-7  ',
      }),
    ).toEqual({ conversation: 'none', summary: 'Done.', mask: 'acme\nstudio-7' });
  });

  it('cut text at a character, never inside one', () => {
    expect(cutBytes('ab', 5)).toBe('ab');
    expect(cutBytes('é漢字', 4)).toBe('é');
  });

  it('keep at most twenty strings to mask, each three characters or more', () => {
    const many = Array.from({ length: 25 }, (_, i) => `name-${i}`).join('\n');
    expect(maskList(many)).toHaveLength(20);
    expect(maskList('ab\nabc')).toEqual(['abc']);
  });

  it('read the turns, the masked values by kind and turn, and the elided output', () => {
    const scan = scanExport(FILE);
    expect(scan.total).toBe(3);
    expect(scan.turn).toBe(3);
    expect(scan.masked).toBe(4);
    expect(scan.elided).toBe(1);
    expect(scan.kinds).toEqual([
      { kind: 'hoplet-token', count: 2, turns: [1, 3] },
      { kind: 'custom', count: 1, turns: [1] },
      { kind: 'url-credential', count: 1, turns: [3] },
    ]);
    expect(scanExport('---\ntitle: "x"\n').total).toBeNull();
  });

  it('name each kind, and show a kind it does not know as it is', () => {
    expect(kindLabel('github-token')).toBe('GitHub token');
    expect(kindLabel('future-kind')).toBe('future-kind');
  });
});

const session = {
  sessionId: 's1',
  title: 'Fix the build',
  projectPath: '/w/app',
  projectDir: '-w-app',
  provider: 'claude',
  deviceName: 'MacBook Pro',
  ownerDeviceId: 'dev-mac',
  ownerConnected: true,
} as LiveSessionMeta;

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe('ExportDialog', () => {
  let host: HTMLDivElement;
  let root: Root;
  const onClose = vi.fn();
  const say = vi.fn();
  const mount = async () => {
    await act(async () => {
      root.render(createElement(ExportDialog, { session, onClose, say }));
    });
  };
  const q = (id: string) => document.querySelector<HTMLElement>(`[data-testid="${id}"]`);
  const click = async (id: string) => {
    const el = q(id);
    if (!el) throw new Error(id);
    await act(async () => el.click());
  };

  beforeEach(() => {
    host = document.createElement('div');
    document.body.append(host);
    root = createRoot(host);
    vi.clearAllMocks();
    answer.lastAnswerMarkdown.mockResolvedValue('The build passes.');
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
  });

  it('starts the summary from the last reply and sends it with the export', async () => {
    api.fetchSessionExport.mockResolvedValue({ name: 'fix-the-build.md', text: FILE });
    await mount();
    expect((q('cockpit.export-summary') as HTMLTextAreaElement).value).toBe('The build passes.');
    await click('cockpit.export-confirm');
    const query = api.fetchSessionExport.mock.calls[0]?.[1] as Record<string, string>;
    expect(query).toMatchObject({ conversation: 'all', summary: 'The build passes.' });
    expect(audit.downloadBlob).toHaveBeenCalledWith('fix-the-build.md', expect.any(Blob));
    expect(q('cockpit.export-name')?.textContent).toBe('fix-the-build.md');
    expect(q('cockpit.export-facts')?.textContent).toContain('4 values');
  });

  it('asks for the replies only when that is chosen', async () => {
    api.fetchSessionExport.mockResolvedValue({ name: 'f.md', text: FILE });
    await mount();
    await click('cockpit.export-opt-conversation-replies');
    await click('cockpit.export-opt-changes');
    await click('cockpit.export-confirm');
    const query = api.fetchSessionExport.mock.calls[0]?.[1] as Record<string, string>;
    expect(query.conversation).toBe('replies');
    expect(query.changes).toBeUndefined();
  });

  it('lists what a preview masked, and saves that preview without asking again', async () => {
    api.fetchSessionExport.mockResolvedValue({ name: 'f.md', text: FILE });
    await mount();
    await click('cockpit.export-preview-button');
    expect(q('cockpit.export-kinds')?.textContent).toContain('Hoplet token');
    expect(q('cockpit.export-preview')?.textContent).toContain('[redacted:custom]');
    await click('cockpit.export-confirm');
    expect(api.fetchSessionExport).toHaveBeenCalledTimes(1);
    expect(audit.downloadBlob).toHaveBeenCalledTimes(1);
  });

  it('saves nothing when the export fails, and tries again', async () => {
    api.fetchSessionExport.mockRejectedValueOnce(new ApiError(429, 'rate-limited'));
    await mount();
    await click('cockpit.export-confirm');
    expect(q('cockpit.export-error')).not.toBeNull();
    expect(audit.downloadBlob).not.toHaveBeenCalled();
    api.fetchSessionExport.mockResolvedValue({ name: 'f.md', text: FILE });
    await click('cockpit.export-retry');
    expect(audit.downloadBlob).toHaveBeenCalledTimes(1);
  });

  it('cancels a running export and goes back to the options', async () => {
    let signal: AbortSignal | undefined;
    api.fetchSessionExport.mockImplementation(
      (_id: string, _q: unknown, _on: unknown, s: AbortSignal) =>
        new Promise((_resolve, reject) => {
          signal = s;
          s.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }),
    );
    await mount();
    await click('cockpit.export-confirm');
    expect(q('cockpit.export-progress')).not.toBeNull();
    await click('cockpit.export-cancel-run');
    expect(signal?.aborted).toBe(true);
    expect(q('cockpit.export-confirm')).not.toBeNull();
    expect(audit.downloadBlob).not.toHaveBeenCalled();
  });
});
