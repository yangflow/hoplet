import { expect, it } from 'vitest';
import { CONFIG_ICONS, ConfigOptionSchema, is } from '../src/index.ts';

const option = (icon?: string) => ({
  type: 'select',
  id: 'mode',
  name: 'Permission mode',
  category: 'mode',
  currentValue: 'a',
  options: [{ value: 'a', name: 'A', ...(icon === undefined ? {} : { icon }) }],
});

it('accepts a value without an icon and every name of the shared vocabulary', () => {
  expect(is(ConfigOptionSchema, option())).toBe(true);
  for (const name of CONFIG_ICONS) expect(is(ConfigOptionSchema, option(name))).toBe(true);
});

it('rejects a name outside the vocabulary, like any other field of the capabilities', () => {
  expect(is(ConfigOptionSchema, option('rocket'))).toBe(false);
  expect(is(ConfigOptionSchema, option(''))).toBe(false);
});
