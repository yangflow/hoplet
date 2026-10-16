// @vitest-environment jsdom
import type { DeletionPreview, LiveSessionMeta } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DeleteDialog, type DeleteFixture } from '../src/screens/cockpit/delete-dialog.tsx';

const api = vi.hoisted(() => ({ deleteSession: vi.fn(), fetchDeletionPreview: vi.fn() }));
vi.mock('../src/lib/api.ts', async (orig) => ({
  ...(await orig<typeof import('../src/lib/api.ts')>()),
  ...api,
}));

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

const preview = (over: Partial<DeletionPreview> = {}): DeletionPreview => ({
  owner: { deviceId: 'dev-mac', name: 'MacBook Pro', online: true },
  running: false,
  vendorFile: { path: '/home/u/.claude/projects/-w-app/s1.jsonl' },
  worktree: null,
  previousOwner: null,
  ...over,
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe('DeleteDialog', () => {
  let host: HTMLDivElement;
  let root: Root;
  const onDeleted = vi.fn();
  const onClose = vi.fn();
  const mount = async (fixture: DeleteFixture) => {
    await act(async () => {
      root.render(createElement(DeleteDialog, { session, fixture, onDeleted, onClose }));
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
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
  });

  it('asks for the vendor file only when it is ticked', async () => {
    api.deleteSession.mockResolvedValue({ steps: [] });
    await mount({ preview: preview() });
    await click('cockpit.delete-confirm');
    expect(api.deleteSession).toHaveBeenCalledWith('s1', {
      vendorFile: false,
      worktree: false,
      worktreeDirty: false,
    });
    expect(onDeleted).toHaveBeenCalledWith('s1');
  });

  it('sends the ticked vendor file', async () => {
    api.deleteSession.mockResolvedValue({ steps: [] });
    await mount({ preview: preview(), ticked: { vendorFile: true } });
    await click('cockpit.delete-confirm');
    expect(api.deleteSession.mock.calls[0]?.[1]).toMatchObject({ vendorFile: true });
  });

  it('shows the running note', async () => {
    await mount({ preview: preview({ running: true }) });
    expect(q('cockpit.delete-running')).not.toBeNull();
  });

  it('shows the offline note', async () => {
    await mount({
      preview: preview({
        owner: { deviceId: 'dev-mac', name: 'MacBook Pro', online: false },
        vendorFile: null,
      }),
    });
    expect(q('cockpit.delete-offline')).not.toBeNull();
  });
});
