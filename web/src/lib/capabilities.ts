import {
  DEVICE_LABEL_MAX,
  type DeviceInfo,
  type DeviceInventory,
  DeviceLabelSchema,
  is,
  MAX_DEVICE_LABELS,
} from '@hoplet/shared';

/** The tools the agent looks for, in the order it lists them; the ones it did not find are named as missing. */
export const KNOWN_TOOLCHAINS = [
  'git',
  'node',
  'pnpm',
  'npm',
  'bun',
  'python3',
  'go',
  'cargo',
  'rustc',
  'java',
  'docker',
  'gh',
] as const;

/** The tools found, in the agent's order (an unknown name last), and the known ones that were not. */
export function toolchainsOf(inventory: Pick<DeviceInventory, 'toolchains'>): {
  found: { name: string; version: string }[];
  missing: string[];
} {
  const order = (name: string) => {
    const i = (KNOWN_TOOLCHAINS as readonly string[]).indexOf(name);
    return i < 0 ? KNOWN_TOOLCHAINS.length : i;
  };
  const found = [...inventory.toolchains].sort((a, b) => order(a.name) - order(b.name));
  const names = new Set(found.map((x) => x.name));
  return { found, missing: KNOWN_TOOLCHAINS.filter((name) => !names.has(name)) };
}

/** The last segment of a project path, whichever separator the machine uses. */
export function projectName(path: string): string {
  const parts = path.split(/[\\/]+/).filter((x) => x !== '');
  return parts[parts.length - 1] ?? path;
}

/** A memory size in bytes as the GPU line writes it: `24 GB`, `32 GB`; the unit is a GiB, which is how cards are sold. */
export function gpuMemoryText(bytes: number): string {
  const gib = bytes / 1024 ** 3;
  return `${gib < 10 ? Number(gib.toFixed(1)) : Math.round(gib)} GB`;
}

/** Why a label was not accepted. */
export type LabelProblem = 'invalid' | 'full';

export type LabelCheck = { ok: true; label: string } | { ok: false; problem: LabelProblem };

/** Whether text is a label that can be added to `current`: the schema's rule, and at most 20 labels in all. */
export function checkLabel(text: string, current: readonly string[]): LabelCheck {
  const label = text.trim();
  if (!is(DeviceLabelSchema, label)) return { ok: false, problem: 'invalid' };
  if (current.includes(label)) return { ok: true, label };
  if (current.length >= MAX_DEVICE_LABELS) return { ok: false, problem: 'full' };
  return { ok: true, label };
}

/** The rule in words, for a label that is not accepted. */
export const LABEL_RULE = { max: DEVICE_LABEL_MAX, count: MAX_DEVICE_LABELS };

/** The labels with one added at the end; unchanged when it is already there. */
export function withLabel(labels: readonly string[], label: string): string[] {
  return labels.includes(label) ? [...labels] : [...labels, label];
}

/** The labels without one. */
export function withoutLabel(labels: readonly string[], label: string): string[] {
  return labels.filter((x) => x !== label);
}

/** The labels with one put back at `index` (an undo); unchanged when it is there or the machine is full. */
export function withLabelAt(labels: readonly string[], label: string, index: number): string[] {
  if (labels.includes(label) || labels.length >= MAX_DEVICE_LABELS) return [...labels];
  const next = [...labels];
  next.splice(Math.min(Math.max(index, 0), next.length), 0, label);
  return next;
}

/** Whether the device list changed in what the capabilities group shows. */
export function sameCapabilities(a: DeviceInfo, b: DeviceInfo | undefined): boolean {
  return (
    JSON.stringify(a.inventory) === JSON.stringify(b?.inventory) &&
    JSON.stringify(a.labels) === JSON.stringify(b?.labels)
  );
}
