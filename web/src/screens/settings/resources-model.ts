import {
  type DeviceResourceSample,
  type DeviceSessionResources,
  RESOURCES_DISK_WARN_DEFAULT,
  RESOURCES_MEMORY_WARN_DEFAULT,
} from '@hoplet/shared';

const GIB = 1024 ** 3;

/** Memory in GB with one decimal and no trailing zero: 21688000000 bytes is 20.2. */
export function memoryGb(bytes: number): number {
  return +(bytes / GIB).toFixed(1);
}

/** Disk space the way the system reports it (decimal units): "184 GB", "1 TB". */
export function diskText(bytes: number): string {
  return bytes >= 1e12 ? `${+(bytes / 1e12).toFixed(1)} TB` : `${Math.round(bytes / 1e9)} GB`;
}

/** A share as a whole percent; 0 for an empty whole. */
export function percent(part: number, whole: number): number {
  return whole > 0 ? Math.round((part / whole) * 100) : 0;
}

/** Which thresholds a reading crosses: memory above 90 % used, disk under 10 % free. */
export function readingWarn(s: DeviceResourceSample): { memory: boolean; disk: boolean } {
  return {
    memory: s.memTotal > 0 && s.memUsed / s.memTotal > RESOURCES_MEMORY_WARN_DEFAULT / 100,
    disk: s.diskTotal > 0 && s.diskFree / s.diskTotal < RESOURCES_DISK_WARN_DEFAULT / 100,
  };
}

/** The sessions largest in memory first; equal ones keep the order they came in. */
export function sortSessions(
  sessions: readonly DeviceSessionResources[],
): DeviceSessionResources[] {
  return sessions
    .map((s, i) => ({ s, i }))
    .sort((a, b) => b.s.rss - a.s.rss || a.i - b.i)
    .map((x) => x.s);
}

/** The colour step of the n-th session in a bar and its list: four steps, the rest share the last. */
export function shade(index: number): 0 | 1 | 2 | 3 {
  return Math.min(index, 3) as 0 | 1 | 2 | 3;
}

export type Segment = {
  sessionId: string;
  title: string | null;
  value: number;
  /** Width in percent of the whole bar. */
  width: number;
  shade: 0 | 1 | 2 | 3;
};

export type Stack = {
  segments: Segment[];
  /** What the machine uses beyond the sessions: used minus their sum, never negative. */
  other: number;
  /** Width of the other processes in percent of the whole bar. */
  otherWidth: number;
};

/**
 * One stacked bar: a segment per session, then the other processes, the rest being free. `values` are the sessions'
 * amounts in the order of `sessions`, `total` the bar's full scale and `used` what the machine uses in all. The widths
 * never add up to more than the bar.
 */
export function stack(
  sessions: readonly Pick<DeviceSessionResources, 'sessionId' | 'title'>[],
  values: readonly number[],
  total: number,
  used: number,
): Stack {
  if (total <= 0) return { segments: [], other: 0, otherWidth: 0 };
  let room = 100;
  const segments = sessions.map((s, i): Segment => {
    const value = Math.max(0, values[i] ?? 0);
    const width = Math.min(room, (value / total) * 100);
    room -= width;
    return { sessionId: s.sessionId, title: s.title, value, width, shade: shade(i) };
  });
  const sum = segments.reduce((a, s) => a + s.value, 0);
  const other = Math.max(0, used - sum);
  return { segments, other, otherWidth: Math.min(room, (other / total) * 100) };
}

/** The SVG points of a trend line in a `w` by `h` box: the newest value at the right edge, 2 px kept free above and below. */
export function sparkPoints(values: readonly number[], w: number, h: number): string {
  if (values.length < 2) return '';
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const span = Math.max(hi - lo, 8);
  return values
    .map((v, i) => {
      const x = (i / (values.length - 1)) * w;
      const y = h - 2 - ((v - lo) / span) * (h - 4);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(' ');
}

/** The CPU percent of every reading that had one. */
export function cpuSeries(samples: readonly DeviceResourceSample[]): number[] {
  return samples.flatMap((s) => (s.cpu === null ? [] : [s.cpu]));
}

/** The memory percent of every reading. */
export function memorySeries(samples: readonly DeviceResourceSample[]): number[] {
  return samples.map((s) => percent(s.memUsed, s.memTotal));
}

/** The architecture as people name it: the Go name `amd64` is x86_64. */
export function archName(arch: string | undefined): string | undefined {
  if (!arch) return undefined;
  return arch === 'amd64' ? 'x86_64' : arch;
}
