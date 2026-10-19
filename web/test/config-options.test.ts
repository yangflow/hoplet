import type { ConfigOption } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  currentValueName,
  optionName,
  valueDescription,
  valueName,
} from '../src/lib/config-options.ts';

const model: ConfigOption = {
  id: 'unlisted-option',
  name: 'Driver name',
  type: 'select',
  currentValue: 'a',
  options: [{ value: 'a', name: 'Driver A', description: 'Driver says' }],
};

describe('config option naming', () => {
  it('falls back to the driver when the strings have no entry', () => {
    expect(optionName(model)).toBe('Driver name');
    expect(valueName(model, 'a', 'Driver A')).toBe('Driver A');
    expect(valueDescription(model, 'a', 'Driver says')).toBe('Driver says');
    expect(valueDescription(model, 'a', undefined)).toBe('');
    expect(currentValueName(model)).toBe('Driver A');
  });

  it('has no current value name for a switch or an unknown value', () => {
    expect(
      currentValueName({ id: 'x', name: 'X', type: 'boolean', currentValue: true }),
    ).toBeUndefined();
    expect(currentValueName({ ...model, currentValue: 'zzz' })).toBeUndefined();
  });
});
