// The notification settings as plain data (ADR 0021): the presets, the per-project and per-event overrides and
// quiet hours. The decision the core takes is in core/src/notify/rules.ts; this is the same table for display
// and for editing, with no React and no network.
import {
  NOTIFY_CHANNELS,
  NOTIFY_EVENT_KINDS,
  type NotifyChannel,
  type NotifyDelivery,
  type NotifyEventKind,
  type NotifyPreferences,
  type NotifyPreset,
  type QuietHours,
} from '@hoplet/shared';
import { t } from '../../lib/i18n.ts';

/** The level choices, in the order of the list; the text is an i18n key. */
export const PRESETS: readonly { id: NotifyPreset; title: string; detail: string }[] = [
  {
    id: 'needs-me',
    title: 'Only when Claude needs me',
    detail: 'A permission request, a plan to review, a failure. Nothing else.',
  },
  {
    id: 'needs-me-and-done',
    title: 'Needs me, and when a turn finishes',
    detail: 'Adds one notification when a turn ends while the session is not open in a tab.',
  },
  {
    id: 'everything',
    title: 'Everything',
    detail: 'Today the same as the level above; events added later arrive here first.',
  },
  { id: 'off', title: 'Off', detail: 'No notifications from any channel.' },
];

/** The events a preference can name, with the tone of their icon. */
export const EVENTS: readonly {
  kind: NotifyEventKind;
  tone: 'att' | 'ok' | 'bad';
  title: string;
  detail: string;
}[] = [
  {
    kind: 'permission',
    tone: 'att',
    title: 'Needs your permission',
    detail: 'Claude asks to run a tool',
  },
  {
    kind: 'plan',
    tone: 'att',
    title: 'Plan ready to review',
    detail: 'Claude left Plan mode',
  },
  {
    kind: 'turn-done',
    tone: 'ok',
    title: 'Turn finished',
    detail: 'Only when no tab has the session open',
  },
  {
    kind: 'failure',
    tone: 'bad',
    title: 'Failed',
    detail: 'A session did not start, or a handoff failed',
  },
];

/** The short names of the events on a webhook's chips and in its row; the text is an i18n key. */
export const WEBHOOK_EVENT_LABEL: Record<NotifyEventKind, string> = {
  permission: 'Permission',
  plan: 'Plan',
  'turn-done': 'Finished',
  failure: 'Failed',
  budget: 'Budget',
};

/** A webhook's events after switching one: the order of `NOTIFY_EVENT_KINDS`, and never empty (the last one stays on). */
export function toggleWebhookEvent(
  events: readonly NotifyEventKind[],
  kind: NotifyEventKind,
): NotifyEventKind[] {
  const next = events.includes(kind) ? events.filter((k) => k !== kind) : [...events, kind];
  if (next.length === 0) return NOTIFY_EVENT_KINDS.filter((k) => events.includes(k));
  return NOTIFY_EVENT_KINDS.filter((k) => next.includes(k));
}

/** The events of a webhook in words for its row: "Permission, Plan, Failed". */
export function webhookEventsText(events: readonly NotifyEventKind[]): string {
  return NOTIFY_EVENT_KINDS.filter((k) => events.includes(k))
    .map((k) => t(WEBHOOK_EVENT_LABEL[k]))
    .join(', ');
}

/** Whether two sets of events are the same set. */
export function sameEvents(a: readonly NotifyEventKind[], b: readonly NotifyEventKind[]): boolean {
  return a.length === b.length && a.every((k) => b.includes(k));
}

/** The channels the settings page lets a person choose; a native client has none to configure yet. */
export const CHOOSABLE_CHANNELS = ['push', 'webhook'] as const satisfies readonly NotifyChannel[];

const PRESET_EVENTS: Record<NotifyPreset, readonly NotifyEventKind[]> = {
  'needs-me': ['permission', 'plan', 'failure'],
  'needs-me-and-done': ['permission', 'plan', 'failure', 'turn-done'],
  everything: ['permission', 'plan', 'failure', 'turn-done'],
  off: [],
};

/** Where an override applies: every project, or the project with this path. */
export type Scope = { project: string | null };

export const EMPTY_PREFERENCES: NotifyPreferences = {
  preset: 'needs-me',
  events: {},
  projects: {},
  quietHours: null,
};

function sameChannels(a: readonly NotifyChannel[], b: readonly NotifyChannel[]): boolean {
  return NOTIFY_CHANNELS.every((c) => a.includes(c) === b.includes(c));
}

/** The channels an event uses with the global level and overrides only (what a project without its own follows). */
function globalChannels(prefs: NotifyPreferences, kind: NotifyEventKind): NotifyChannel[] {
  const override = prefs.events[kind];
  if (override) return NOTIFY_CHANNELS.filter((c) => override.channels.includes(c));
  return PRESET_EVENTS[prefs.preset].includes(kind) ? [...NOTIFY_CHANNELS] : [];
}

/** The channels an event uses in a scope, quiet hours aside. */
export function effectiveChannels(
  prefs: NotifyPreferences,
  kind: NotifyEventKind,
  scope: Scope,
): NotifyChannel[] {
  const project = scope.project === null ? undefined : prefs.projects[scope.project];
  if (!project) return globalChannels(prefs, kind);
  const override = project.events[kind];
  if (override) return NOTIFY_CHANNELS.filter((c) => override.channels.includes(c));
  if (project.preset)
    return PRESET_EVENTS[project.preset].includes(kind) ? [...NOTIFY_CHANNELS] : [];
  return globalChannels(prefs, kind);
}

/** Whether the event has an override of its own in the scope. */
export function isOverridden(
  prefs: NotifyPreferences,
  kind: NotifyEventKind,
  scope: Scope,
): boolean {
  if (scope.project === null) return prefs.events[kind] !== undefined;
  return prefs.projects[scope.project]?.events[kind] !== undefined;
}

/** How many overrides exist: events overridden for all projects, and per project its events and its own level. */
export function overrideCount(prefs: NotifyPreferences): number {
  let count = Object.keys(prefs.events).length;
  for (const project of Object.values(prefs.projects)) {
    count += Object.keys(project.events).length + (project.preset ? 1 : 0);
  }
  return count;
}

/** The preferences after the user turns one channel of one event on or off in a scope. */
export function toggleChannel(
  prefs: NotifyPreferences,
  kind: NotifyEventKind,
  channel: NotifyChannel,
  scope: Scope,
): NotifyPreferences {
  const current = effectiveChannels(prefs, kind, scope);
  const next = current.includes(channel)
    ? current.filter((c) => c !== channel)
    : [...current, channel];
  const channels = NOTIFY_CHANNELS.filter((c) => next.includes(c));
  if (scope.project === null) {
    const { [kind]: _dropped, ...rest } = prefs.events;
    const events = sameChannels(channels, defaultChannels(prefs.preset, kind))
      ? rest
      : { ...rest, [kind]: { channels } };
    return { ...prefs, events };
  }
  const project = prefs.projects[scope.project] ?? { events: {} };
  const { [kind]: _dropped, ...rest } = project.events;
  const baseline = effectiveChannels(
    { ...prefs, projects: { ...prefs.projects, [scope.project]: { ...project, events: rest } } },
    kind,
    scope,
  );
  const events = sameChannels(channels, baseline) ? rest : { ...rest, [kind]: { channels } };
  return withProject(prefs, scope.project, { ...project, events });
}

function defaultChannels(preset: NotifyPreset, kind: NotifyEventKind): NotifyChannel[] {
  return PRESET_EVENTS[preset].includes(kind) ? [...NOTIFY_CHANNELS] : [];
}

/** Stores a project's preferences, or drops the entry when it says nothing. */
function withProject(
  prefs: NotifyPreferences,
  path: string,
  project: NotifyPreferences['projects'][string],
): NotifyPreferences {
  const { [path]: _dropped, ...others } = prefs.projects;
  const empty = !project.preset && Object.keys(project.events).length === 0;
  return { ...prefs, projects: empty ? others : { ...others, [path]: project } };
}

/** The preferences with the overrides of one scope cleared (the level of the page stays). */
export function resetScope(prefs: NotifyPreferences, scope: Scope): NotifyPreferences {
  if (scope.project === null) return { ...prefs, events: {} };
  const { [scope.project]: _dropped, ...projects } = prefs.projects;
  return { ...prefs, projects };
}

/** The name of a project: the last segment of its path. */
export function projectName(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

/** The project paths the override panel offers: those with sessions or with preferences, by name. */
export function projectChoices(
  prefs: NotifyPreferences,
  sessionPaths: readonly string[],
): string[] {
  const all = new Set([...sessionPaths, ...Object.keys(prefs.projects)]);
  return [...all].sort(
    (a, b) => projectName(a).localeCompare(projectName(b)) || a.localeCompare(b),
  );
}

/** Default quiet hours when a person turns them on, on the clock of the browser's zone. */
export function defaultQuietHours(timeZone: string): QuietHours {
  return { start: '22:00', end: '08:00', timeZone };
}

/** The zone of this browser, or UTC where the runtime cannot tell. */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

/** A label for this browser's push subscription from its user agent: "Chrome on macOS". */
export function deviceLabel(userAgent: string): string {
  const ua = userAgent;
  const browser = /Edg\//.test(ua)
    ? 'Edge'
    : /OPR\/|Opera/.test(ua)
      ? 'Opera'
      : /Firefox\/|FxiOS/.test(ua)
        ? 'Firefox'
        : /Chrome\/|CriOS/.test(ua)
          ? 'Chrome'
          : /Safari\//.test(ua)
            ? 'Safari'
            : 'Browser';
  const system = /Android/.test(ua)
    ? 'Android'
    : /iPhone|iPad|iPod/.test(ua)
      ? 'iOS'
      : /Mac OS X|Macintosh/.test(ua)
        ? 'macOS'
        : /Windows/.test(ua)
          ? 'Windows'
          : /CrOS/.test(ua)
            ? 'ChromeOS'
            : /Linux|X11/.test(ua)
              ? 'Linux'
              : '';
  return system ? `${browser} on ${system}` : browser;
}

/** The unpadded base64url VAPID key as the bytes `pushManager.subscribe` takes. */
export function keyBytes(base64url: string): Uint8Array<ArrayBuffer> {
  const padded = base64url.replace(/-/g, '+').replace(/_/g, '/');
  const binary = atob(padded + '='.repeat((4 - (padded.length % 4)) % 4));
  const bytes = new Uint8Array(new ArrayBuffer(binary.length));
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/** What a webhook URL shows in a list: the host and a hint that a path follows, never the path (it may be a secret). */
export function hostHint(url: string): string {
  try {
    const u = new URL(url);
    return u.pathname.length > 1 || u.search ? `${u.host}/…` : u.host;
  } catch {
    return '…';
  }
}

/** The reason of a failed delivery in words: `timeout`, `gone`, `http-<status>` or `error`. */
function failureText(error: string | undefined): string {
  if (error === undefined) return t('error');
  if (error === 'timeout') return t('timeout');
  if (error === 'gone') return t('gone');
  const status = /^http-(\d+)$/.exec(error);
  if (status) return `HTTP ${status[1]}`;
  return t('error');
}

/** The last delivery of a subscription or webhook: "Delivered 2m ago", "Failed · timeout", or that none was sent. */
export function deliveryText(delivery: NotifyDelivery | undefined, now: number): string {
  if (!delivery) return t('Not sent yet');
  if (!delivery.ok) return `${t('Failed')} · ${failureText(delivery.error)}`;
  const minutes = Math.max(0, Math.floor((now - delivery.at) / 60_000));
  if (minutes < 1) return t('Delivered just now');
  if (minutes < 60) return `${t('Delivered')} ${minutes} ${t('min ago')}`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${t('Delivered')} ${hours} ${t('h ago')}`;
  return `${t('Delivered')} ${Math.floor(hours / 24)} ${t('d ago')}`;
}
