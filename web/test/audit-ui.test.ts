// @vitest-environment jsdom
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { AuditSection } from '../src/screens/settings/audit.tsx';
import * as fx from '../src/screens/settings/audit-fixtures.ts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;
let shown = 0;

const q = (id: string) => host.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const all = (prefix: string) => [
  ...host.querySelectorAll<HTMLElement>(`[data-testid^="${prefix}"]`),
];
const click = async (el: Element | null | undefined) => {
  await act(async () => {
    (el as HTMLElement).click();
  });
};
const show = async (fixture: ReturnType<typeof fx.auditPage>) => {
  await act(async () => {
    root.render(createElement(AuditSection, { fixture, key: ++shown }));
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

describe('Settings > Audit', () => {
  it('lists the records under a heading per day, with the type in the attention colour for a request', async () => {
    await show(fx.auditPage());
    expect(all('settings.audit.row.')).toHaveLength(14);
    expect(host.textContent).toContain('Today · ');
    expect(host.textContent).toContain('Yesterday · ');
    expect(q('settings.audit.row.1283')?.querySelector('.ty')?.classList.contains('att')).toBe(
      true,
    );
    expect(q('settings.audit.row.1285')?.textContent).toContain('pnpm test --filter core');
    expect(q('settings.audit.more')).not.toBeNull();
  });

  it('opens a record in place and closes it again', async () => {
    await show(fx.auditPage());
    expect(q('settings.audit.detail.1281')).toBeNull();
    await click(q('settings.audit.row.1281'));
    const detail = q('settings.audit.detail.1281');
    expect(detail?.textContent).toContain('Bearer ***');
    expect(detail?.textContent).toContain('Never records prompts');
    await click(q('settings.audit.row.1281'));
    expect(q('settings.audit.detail.1281')).toBeNull();
  });

  it('starts a state with a record open', async () => {
    await show(fx.auditDetail());
    expect(q('settings.audit.detail.1281')).not.toBeNull();
  });

  it('shows only the records the filters leave, and clears them', async () => {
    await show(fx.auditFiltered());
    expect(all('settings.audit.row.')).toHaveLength(3);
    await show(fx.auditNoMatch());
    expect(all('settings.audit.row.')).toHaveLength(0);
    expect(q('settings.audit.no-match')).not.toBeNull();
    expect(q('settings.audit.clear')).not.toBeNull();
    await click(q('settings.audit.clear'));
    expect(all('settings.audit.row.')).toHaveLength(14);
  });

  it('says so when nothing was ever recorded', async () => {
    await show(fx.auditEmpty());
    expect(q('settings.audit.empty')).not.toBeNull();
    expect(q('settings.audit.no-match')).toBeNull();
  });

  it('shows loading and error instead of the list', async () => {
    await show(fx.auditLoading());
    expect(q('settings.audit.loading')).not.toBeNull();
    expect(q('settings.audit.list')).toBeNull();
    await show(fx.auditError());
    expect(q('settings.audit.error')?.textContent).toContain('could not be read');
    expect(q('settings.audit.retry')).not.toBeNull();
  });

  it('reports a verification that passed and one that broke', async () => {
    await show(fx.auditVerifyOk());
    expect(q('settings.audit.verify.ok')?.textContent).toContain('1,285 records checked');
    await show(fx.auditVerifyBroken());
    const bad = q('settings.audit.verify.broken');
    expect(bad?.textContent).toContain('#1,032');
    expect(bad?.textContent).toContain('does not match its content');
  });

  it('offers the two export formats in a menu', async () => {
    await show(fx.auditPage());
    expect(q('settings.audit.export.jsonl')).toBeNull();
    await click(q('settings.audit.export'));
    expect(q('settings.audit.export.jsonl')).not.toBeNull();
    expect(q('settings.audit.export.csv')).not.toBeNull();
  });
});
