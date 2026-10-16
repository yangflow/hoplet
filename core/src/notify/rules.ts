// The decision (ADR 0021): event kind x project x preset x overrides x quiet hours -> the channels to use.

import {
  NOTIFY_CHANNELS,
  type NotifyChannel,
  type NotifyEventKind,
  type NotifyPreferences,
  type NotifyPreset,
  type QuietHours,
} from '@hoplet/shared';

/** The events that wait for the user; quiet hours never hold them back. */
export const NEEDS_YOU: ReadonlySet<NotifyEventKind> = new Set(['permission', 'plan', 'failure']);

const PRESET_EVENTS: Record<NotifyPreset, readonly NotifyEventKind[]> = {
  'needs-me': ['permission', 'plan', 'failure'],
  'needs-me-and-done': ['permission', 'plan', 'failure', 'turn-done'],
  // Equal to the one above until more events exist.
  everything: ['permission', 'plan', 'failure', 'turn-done'],
  off: [],
};

function minutesOf(clock: string): number {
  const [h, m] = clock.split(':');
  return Number(h) * 60 + Number(m);
}

/** The minutes since midnight on the clock of `timeZone`; null for a zone the runtime does not know. */
function localMinutes(now: number, timeZone: string): number | null {
  try {
    const parts = new Intl.DateTimeFormat('en-GB', {
      timeZone,
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).formatToParts(new Date(now));
    const hour = Number(parts.find((p) => p.type === 'hour')?.value);
    const minute = Number(parts.find((p) => p.type === 'minute')?.value);
    return Number.isFinite(hour) && Number.isFinite(minute) ? hour * 60 + minute : null;
  } catch {
    return null;
  }
}

/** Whether `now` is inside quiet hours; `start` after `end` spans midnight, `start` equal to `end` is empty. */
export function inQuietHours(quiet: QuietHours | null, now: number): boolean {
  if (!quiet) return false;
  const at = localMinutes(now, quiet.timeZone);
  if (at === null) return false;
  const start = minutesOf(quiet.start);
  const end = minutesOf(quiet.end);
  if (start === end) return false;
  return start < end ? at >= start && at < end : at >= start || at < end;
}

/** The channels an event goes to; none means no notification. */
export function channelsFor(
  prefs: NotifyPreferences,
  kind: NotifyEventKind,
  projectPath: string,
  now: number,
): NotifyChannel[] {
  if (!NEEDS_YOU.has(kind) && inQuietHours(prefs.quietHours, now)) return [];
  const project = prefs.projects[projectPath];
  const override = project?.events[kind] ?? prefs.events[kind];
  if (override) return NOTIFY_CHANNELS.filter((c) => override.channels.includes(c));
  const preset = project?.preset ?? prefs.preset;
  return PRESET_EVENTS[preset].includes(kind) ? [...NOTIFY_CHANNELS] : [];
}
