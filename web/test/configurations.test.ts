import type { ProviderInfo } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  addCommand,
  defaultsToSave,
  effectiveDefault,
  enabledConfigurations,
  envCommand,
  groupByVendor,
  handoffChoice,
  hasOwnLogin,
  isBuiltIn,
  missingVariables,
  notReady,
} from '../src/lib/configurations.ts';
import { showsConfigurationChip } from '../src/screens/cockpit/composer/configuration-menu.tsx';
import { buildTree } from '../src/screens/cockpit/group.ts';

const model = {
  type: 'select' as const,
  id: 'model',
  name: 'Model',
  currentValue: 'default',
  options: [
    { value: 'default', name: 'Default' },
    { value: 'opus', name: 'Opus' },
  ],
};
const thinking = { type: 'boolean' as const, id: 'thinking', name: 'Thinking', currentValue: true };

function info(
  id: string,
  over: {
    vendor?: 'claude' | 'codex';
    enabled?: boolean;
    env?: { name: string; set: boolean }[];
    defaults?: Record<string, string | boolean>;
  } = {},
): ProviderInfo {
  const vendor = over.vendor ?? 'claude';
  return {
    provider: vendor,
    configuration: {
      id,
      name: id,
      extends: vendor,
      enabled: over.enabled ?? true,
      env: over.env ?? [],
      defaults: over.defaults ?? {},
    },
    capabilities: { permissions: true, interrupt: true, config: [model, thinking] },
  } as ProviderInfo;
}

describe('configuration readiness', () => {
  const glm = info('glm', {
    env: [
      { name: 'ANTHROPIC_AUTH_TOKEN', set: false },
      { name: 'CLAUDE_CONFIG_DIR', set: true },
    ],
  });

  it('knows the built-in by its ID', () => {
    expect(isBuiltIn(info('claude'))).toBe(true);
    expect(isBuiltIn(glm)).toBe(false);
  });

  it('lists the variables without a value and is not ready while one is missing', () => {
    expect(missingVariables(glm)).toEqual(['ANTHROPIC_AUTH_TOKEN']);
    expect(notReady(glm)).toBe(true);
    expect(notReady(info('glm', { enabled: false, env: [{ name: 'X', set: false }] }))).toBe(false);
    expect(notReady(info('claude'))).toBe(false);
  });

  it('reads a separate login from the config directory variable', () => {
    expect(hasOwnLogin(glm)).toBe(true);
    expect(hasOwnLogin(info('claude'))).toBe(false);
  });

  it('writes the commands the web shows and never runs', () => {
    expect(addCommand('glm')).toBe('hoplet-agent provider add glm --extends claude --preset glm');
    expect(envCommand('glm', 'ANTHROPIC_AUTH_TOKEN')).toBe(
      'hoplet-agent provider env glm ANTHROPIC_AUTH_TOKEN',
    );
  });
});

describe('grouping and enabling', () => {
  const all = [
    info('glm', { enabled: false }),
    info('codex', { vendor: 'codex' }),
    info('claude'),
    info('deepseek'),
  ];

  it('groups by vendor in the vendors order and keeps the machine order within one', () => {
    const groups = groupByVendor(all);
    expect(groups.map((g) => g.provider)).toEqual(['claude', 'codex']);
    expect(groups[0]?.items.map((p) => p.configuration.id)).toEqual(['glm', 'claude', 'deepseek']);
    expect(groupByVendor([info('codex', { vendor: 'codex' })]).map((g) => g.provider)).toEqual([
      'codex',
    ]);
  });

  it('keeps only the enabled configurations', () => {
    expect(enabledConfigurations(all).map((p) => p.configuration.id)).toEqual([
      'codex',
      'claude',
      'deepseek',
    ]);
  });

  it('shows the chip for more than one, or for a single one that is not the built-in', () => {
    expect(showsConfigurationChip([])).toBe(false);
    expect(showsConfigurationChip([info('claude')])).toBe(false);
    expect(showsConfigurationChip([info('glm')])).toBe(true);
    expect(showsConfigurationChip([info('claude'), info('glm')])).toBe(true);
  });

  it('starts a new session only from the enabled configurations of a machine', () => {
    const tree = buildTree(
      [],
      [
        {
          deviceId: 'd1',
          name: 'mac',
          connected: true,
          system: null,
          providers: all,
        },
      ],
    );
    expect(tree[0]?.providers.map((p) => p.configuration.id)).toEqual([
      'codex',
      'claude',
      'deepseek',
    ]);
  });
});

describe('defaults', () => {
  const glm = info('glm', { defaults: { model: 'opus', hidden: 'kept' } });

  it("takes the configuration's default before the driver's value", () => {
    expect(effectiveDefault(glm, model)).toBe('opus');
    expect(effectiveDefault(glm, thinking)).toBe(true);
  });

  it('saves only what differs from the driver and keeps entries the form cannot show', () => {
    expect(defaultsToSave(glm, { model: 'default', thinking: false })).toEqual({
      thinking: false,
      hidden: 'kept',
    });
    expect(defaultsToSave(glm, {})).toEqual({ model: 'opus', hidden: 'kept' });
  });
});

describe('handoff target', () => {
  it('asks for nothing when the vendor continues in its own, or the target announced nothing', () => {
    expect(handoffChoice([info('claude')], 'claude', 'claude')).toEqual({ state: 'builtin' });
    expect(handoffChoice([], 'claude', 'glm')).toEqual({ state: 'builtin' });
  });

  it('preselects the same configuration when the target has it enabled', () => {
    const target = [info('claude'), info('glm')];
    const got = handoffChoice(target, 'claude', 'glm');
    expect(got.state).toBe('same');
    if (got.state === 'same') {
      expect(got.value).toBe('glm');
      expect(got.options.map((p) => p.configuration.id)).toEqual(['claude', 'glm']);
    }
  });

  it('asks to pick among the enabled configurations of the vendor when the target lacks it', () => {
    const target = [
      info('claude'),
      info('glm', { enabled: false }),
      info('codex', { vendor: 'codex' }),
    ];
    const got = handoffChoice(target, 'claude', 'glm');
    expect(got.state).toBe('pick');
    if (got.state === 'pick')
      expect(got.options.map((p) => p.configuration.id)).toEqual(['claude']);
  });

  it('offers the disabled configurations of the vendor when none is enabled', () => {
    const target = [
      info('claude', { enabled: false }),
      info('deepseek', { enabled: false }),
      info('codex', { vendor: 'codex' }),
    ];
    const got = handoffChoice(target, 'claude', 'glm');
    expect(got.state).toBe('disabled');
    if (got.state === 'disabled')
      expect(got.options.map((p) => p.configuration.id)).toEqual(['claude', 'deepseek']);
    expect(handoffChoice([info('claude', { enabled: false })], 'claude', 'claude').state).toBe(
      'disabled',
    );
  });

  it('says none when the target has nothing of the vendor', () => {
    expect(handoffChoice([info('codex', { vendor: 'codex' })], 'claude', 'glm')).toEqual({
      state: 'none',
    });
  });
});
