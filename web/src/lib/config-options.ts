import type { ConfigOption } from '@hoplet/shared';
import { t } from './i18n.ts';

/** An option's name: the strings' entry for its ID when there is one, else the driver's name. */
export function optionName(o: ConfigOption): string {
  const key = `config.${o.id}`;
  const known = t(key);
  return known !== key ? known : o.name;
}

/** A value's name: the strings' entry for the option and value when there is one, else the driver's name. */
export function valueName(o: ConfigOption, value: string, name: string): string {
  const key = `config.${o.id}.${value}`;
  const known = t(key);
  return known !== key ? known : name;
}

/** The name of a select's current value. */
export function currentValueName(o: ConfigOption): string | undefined {
  if (o.type !== 'select') return undefined;
  const v = o.options.find((c) => c.value === o.currentValue);
  return v ? valueName(o, v.value, v.name) : undefined;
}

/** A value's description: the strings' entry for the option and value when there is one, else the driver's, else none. */
export function valueDescription(
  o: ConfigOption,
  value: string,
  description: string | undefined,
): string {
  const key = `config.${o.id}.${value}.description`;
  const known = t(key);
  return known !== key ? known : (description ?? '');
}
