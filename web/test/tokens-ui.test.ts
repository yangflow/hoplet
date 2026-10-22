// @vitest-environment jsdom
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setLanguage } from '../src/lib/i18n.ts';
import { TokensSection } from '../src/screens/settings/tokens.tsx';
import {
  filledForm,
  issuedFixture,
  tokensFixture,
} from '../src/screens/settings/tokens-fixtures.ts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const clip = vi.hoisted(() => ({ copyText: vi.fn(async () => true), copyLater: vi.fn() }));
vi.mock('../src/lib/clipboard.ts', () => clip);

let root: Root;
let host: HTMLDivElement;

const q = (id: string) => host.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const click = async (el: Element | null | undefined) => {
  await act(async () => {
    (el as HTMLElement).click();
  });
};
const type = async (el: HTMLElement | null, value: string) => {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
  await act(async () => {
    set?.call(el, value);
    el?.dispatchEvent(new Event('input', { bubbles: true }));
  });
};
const page = async (over: Parameters<typeof tokensFixture>[0] = {}) => {
  await act(async () => {
    root.render(createElement(TokensSection, { fixture: tokensFixture(over) }));
  });
};

beforeEach(() => {
  setLanguage('en');
  clip.copyText.mockClear();
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.querySelectorAll('[role="dialog"], .scrim').forEach((n) => {
    n.remove();
  });
});

describe('Settings > API tokens', () => {
  it('lists the active tokens, then the revoked and expired ones', async () => {
    await page();
    expect(q('settings.tokens.row.tok_release')?.textContent).toContain('CI release bot');
    expect(q('settings.tokens.row.tok_release')?.textContent).toContain('build-box');
    expect(q('settings.tokens.row.tok_old')?.textContent).toContain('Expired');
    expect(q('settings.tokens.row.tok_demo')?.textContent).toContain('Revoked');
    expect(q('settings.tokens.row.tok_grafana')?.textContent).toContain('Never used');
    expect(host.textContent).toContain('Revoked and expired');
    expect(q('settings.tokens.detail.tok_release')).toBeNull();
  });

  it('opens a token in place and closes it again', async () => {
    await page();
    await click(q('settings.tokens.toggle.tok_release'));
    expect(q('settings.tokens.detail.tok_release')?.textContent).toContain('tok_release');
    await click(q('settings.tokens.toggle.tok_release'));
    expect(q('settings.tokens.detail.tok_release')).toBeNull();
  });

  it('shows the empty state with an Issue button', async () => {
    await page({ empty: true });
    expect(q('settings.tokens.empty')).not.toBeNull();
    await click(q('settings.tokens.issue'));
    expect(q('settings.tokens.form')).not.toBeNull();
  });

  it('keeps answer off by default and warns when it is switched on', async () => {
    await page({ form: filledForm({ scope: 'all' }) });
    const answer = q('settings.tokens.answer');
    expect(answer?.getAttribute('aria-checked')).toBe('false');
    expect(q('settings.tokens.answer-warning')).toBeNull();
    await click(answer);
    expect(q('settings.tokens.answer-warning')?.textContent).toContain('answer');
  });

  it('refuses a form without a name and says why', async () => {
    await page({ form: filledForm({ name: '' }) });
    await click(q('settings.tokens.submit'));
    expect(q('settings.tokens.reveal')).toBeNull();
    expect(q('settings.tokens.form')?.textContent).toContain('Give the token a name');
  });

  it('shows the secret once, copies it, and drops it on Done', async () => {
    await page();
    await click(q('settings.tokens.issue'));
    await type(q('settings.tokens.name'), 'Bot');
    await click(q('settings.tokens.submit'));
    const secret = q('settings.tokens.secret');
    expect(secret?.textContent).toBe('hpt_placeholder');
    expect(q('settings.tokens.form')).toBeNull();
    await click(q('settings.tokens.copy'));
    expect(clip.copyText).toHaveBeenCalledWith('hpt_placeholder');
    await click(q('settings.tokens.done'));
    expect(q('settings.tokens.reveal')).toBeNull();
    expect(host.textContent).not.toContain('hpt_placeholder');
    expect(host.textContent).toContain('Bot');
  });

  it('starts the reveal from a fixture without keeping the secret in the list', async () => {
    await page({ issued: issuedFixture });
    expect(q('settings.tokens.secret')?.textContent).toBe(issuedFixture.token);
    expect(host.textContent).toContain('HOPLET_TOKEN');
    await click(q('settings.tokens.done'));
    expect(host.textContent).not.toContain(issuedFixture.token);
  });

  it('asks before it revokes, then moves the token to the revoked group', async () => {
    await page();
    await click(q('settings.tokens.toggle.tok_phone'));
    await click(q('settings.tokens.revoke.tok_phone'));
    const confirm = document.querySelector('[data-testid="settings.tokens.confirm"]');
    expect(confirm?.textContent).toContain('Phone shortcut');
    await click(document.querySelector('[data-testid="settings.tokens.confirm-revoke"]'));
    expect(document.querySelector('[data-testid="settings.tokens.confirm"]')).toBeNull();
    expect(q('settings.tokens.row.tok_phone')?.textContent).toContain('Revoked');
  });

  it('shows the core refusal inside the form', async () => {
    await page({ form: filledForm(), problem: 'Revoke one before issuing another.' });
    expect(q('settings.tokens.problem')?.textContent).toContain('Revoke one');
  });

  it('shows loading and a load failure', async () => {
    await page({ loading: true });
    expect(q('settings.tokens.loading')).not.toBeNull();
    await page({ error: 'Could not reach the core.' });
    expect(q('settings.tokens.load-error')?.textContent).toContain('Could not reach');
  });
});
