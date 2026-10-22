// @vitest-environment jsdom
import type { PermissionRuleMachine, PermissionRuleMachinesResponse } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  CodexPromptsNote,
  RulesUnenforcedNote,
  ToolRule,
} from '../src/screens/cockpit/conversation/rule-line.tsx';
import { ViewContext } from '../src/screens/cockpit/conversation/view-context.tsx';
import { machineStatus, RuleMachines } from '../src/screens/settings/rule-machines.tsx';

let root: Root;
let host: HTMLDivElement;

beforeEach(() => {
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const MIN = 60_000;
const machine = (over: Partial<PermissionRuleMachine>): PermissionRuleMachine => ({
  deviceId: 'dev',
  name: 'dev',
  online: true,
  revision: 14,
  applied: null,
  appliedAt: null,
  pushedAt: null,
  retrying: false,
  ...over,
});

function data(): PermissionRuleMachinesResponse {
  const now = Date.now();
  return {
    latest: 14,
    machines: [
      machine({ deviceId: 'a', name: 'MacBook Pro', applied: 14, appliedAt: now - 2 * MIN }),
      machine({
        deviceId: 'b',
        name: 'studio',
        applied: 13,
        pushedAt: now - 6 * MIN,
        retrying: true,
      }),
      machine({
        deviceId: 'c',
        name: 'mini',
        online: false,
        applied: 12,
        lastSeen: now - 3 * 86_400_000,
      }),
    ],
  };
}

describe('machineStatus', () => {
  it('is applied once the latest revision is confirmed, even while offline', () => {
    expect(machineStatus(machine({ applied: 14 }), 14)).toBe('ok');
    expect(machineStatus(machine({ applied: 14, online: false }), 14)).toBe('ok');
  });
  it('is behind when online without the latest, and offline otherwise', () => {
    expect(machineStatus(machine({ applied: 13 }), 14)).toBe('behind');
    expect(machineStatus(machine({ applied: null }), 14)).toBe('behind');
    expect(machineStatus(machine({ applied: 12, online: false }), 14)).toBe('off');
  });
});

describe('the machines panel', () => {
  it('counts the applied machines and words each row', () => {
    act(() => root.render(createElement(RuleMachines, { fixture: data() })));
    const text = host.textContent ?? '';
    expect(text).toContain('Latest revision r14');
    expect(text).toContain('1 of 3 applied');
    expect(host.querySelector('[data-testid="settings.rules.machine.a"]')?.textContent).toContain(
      'Applied · Confirmed 2 min ago',
    );
    expect(host.querySelector('[data-testid="settings.rules.machine.b"]')?.textContent).toContain(
      'Behind · Last push 6 min ago, no confirmation yet. Retrying.',
    );
    expect(host.querySelector('[data-testid="settings.rules.machine.c"]')?.textContent).toContain(
      'Offline · Last seen 3 d ago. Applies when it reconnects.',
    );
  });

  it('offers "Push again" only on a machine that is behind', () => {
    act(() => root.render(createElement(RuleMachines, { fixture: data() })));
    expect(host.querySelector('[data-testid="settings.rules.machine.a.push"]')).toBeNull();
    expect(host.querySelector('[data-testid="settings.rules.machine.b.push"]')).not.toBeNull();
    expect(host.querySelector('[data-testid="settings.rules.machine.c.push"]')).toBeNull();
  });

  it('is hidden with no machine', () => {
    act(() => root.render(createElement(RuleMachines, { fixture: { latest: 0, machines: [] } })));
    expect(host.querySelector('[data-testid="settings.rules.machines"]')).toBeNull();
  });
});

describe('the Codex notes', () => {
  it('says what the rules cover when Codex only asks', () => {
    act(() => root.render(createElement(CodexPromptsNote)));
    expect(host.textContent).toContain('In Full access Codex asks for nothing');
    expect(host.querySelector('a')?.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('links to the rules from the unenforced note', () => {
    let opened = 0;
    act(() =>
      root.render(
        createElement(RulesUnenforcedNote, {
          onView: () => {
            opened += 1;
          },
        }),
      ),
    );
    expect(host.textContent).toContain('Rules were not enforced');
    act(() => host.querySelector('button')?.click());
    expect(opened).toBe(1);
  });

  it('puts the unenforced note under the call it names only', () => {
    const view = {
      sessionId: 's',
      isOpen: () => false,
      setOpen: () => undefined,
      unenforcedAt: (id: string) => id === 'lt1',
    };
    const render = (toolUseId: string | undefined) =>
      act(() =>
        root.render(
          createElement(
            ViewContext.Provider,
            { value: view },
            createElement(ToolRule, { toolUseId }),
          ),
        ),
      );
    render('lt1');
    expect(host.querySelector('[data-testid="cockpit.rules-unenforced-note"]')).not.toBeNull();
    render('lt2');
    expect(host.querySelector('[data-testid="cockpit.rules-unenforced-note"]')).toBeNull();
    render(undefined);
    expect(host.querySelector('[data-testid="cockpit.rules-unenforced-note"]')).toBeNull();
  });
});
