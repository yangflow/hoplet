// @vitest-environment jsdom
import type { LiveSessionMeta } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { AUDIT_EVENTS } from '../src/screens/settings/audit-fixtures.ts';
import { AuditTab, type AuditTabFixture } from '../src/screens/workspace/audit-tab.tsx';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const INGEST = '8f1c2d3e-4a5b-4c6d-8e7f-90a1b2c3d4e5';
const session = { sessionId: INGEST, title: 'fix-ingest' } as LiveSessionMeta;
const NOW = new Date(2026, 9, 22, 14, 40).getTime();

let root: Root;
let host: HTMLDivElement;
let shown = 0;

const q = (id: string) => host.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const rows = () => [...host.querySelectorAll('[data-testid^="workspace.audit.row."]')];
const click = async (el: Element | null | undefined) => {
  await act(async () => {
    (el as HTMLElement).click();
  });
};
const show = async (over: Partial<AuditTabFixture> & { events?: typeof AUDIT_EVENTS } = {}) => {
  const { events, ...rest } = over;
  const fixture: AuditTabFixture = {
    source: { events: events ?? AUDIT_EVENTS, next: 1271, now: NOW },
    devices: [],
    ...rest,
  };
  await act(async () => {
    root.render(createElement(AuditTab, { session, fixture, key: ++shown }));
  });
};

beforeEach(() => {
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe('Inspector > Audit', () => {
  const own = AUDIT_EVENTS.filter((e) => e.sessionId === INGEST);

  it('lists the records of this session only, under a heading per day', async () => {
    await show();
    expect(rows()).toHaveLength(own.length);
    expect(host.textContent).toContain('Today · ');
    expect(q('workspace.audit.more')).not.toBeNull();
  });

  it('keeps to the segment picked, and closes an open record when it changes', async () => {
    await show({ openSeq: 1281 });
    expect(q('workspace.audit.detail.1281')).not.toBeNull();
    await click(q('workspace.audit.kind.tools'));
    expect(q('workspace.audit.detail.1281')).toBeNull();
    const tools = own.filter((e) => e.type === 'tool.call');
    expect(rows()).toHaveLength(tools.length);
    await click(q('workspace.audit.kind.perm'));
    expect(rows()).toHaveLength(own.filter((e) => e.type.startsWith('permission.')).length);
  });

  it('opens a record in place and closes it again', async () => {
    await show();
    await click(q('workspace.audit.row.1281'));
    expect(q('workspace.audit.detail.1281')?.textContent).toContain('Bearer ***');
    await click(q('workspace.audit.row.1281'));
    expect(q('workspace.audit.detail.1281')).toBeNull();
  });

  it('says so when nothing was recorded, without a list or a Load older', async () => {
    await show({ events: [] });
    expect(q('workspace.audit.empty')).not.toBeNull();
    expect(q('workspace.audit.list')).toBeNull();
    expect(q('workspace.audit.more')).toBeNull();
  });
});
