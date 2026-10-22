import {
  HOOK_EVENT_TYPES,
  type HookCreateRequest,
  type HookDelivery,
  type HookEventType,
  type HookInfo,
  type HookUpdateRequest,
  type ScriptHookInfo,
  type WebhookHookInfo,
} from '@hoplet/shared';
import { clock } from './format.ts';
import { t } from './i18n.ts';
import { baseName, fill } from './templates.ts';

export const ALL_EVENTS: readonly HookEventType[] = HOOK_EVENT_TYPES;

/** What an event is, in words. */
export function eventName(type: HookEventType): string {
  switch (type) {
    case 'session.started':
      return t('A session starts');
    case 'turn.completed':
      return t('A turn completes');
    case 'session.ended':
      return t('A session ends');
    case 'permission.requested':
      return t('A permission is requested');
    case 'permission.resolved':
      return t('A permission is resolved');
    case 'session.error':
      return t('A session fails');
    case 'handoff.started':
      return t('A handoff starts');
    case 'handoff.completed':
      return t('A handoff completes');
  }
}

/** The events of a hook in a row: their names for one or two, else how many. */
export function eventsSummary(events: readonly string[]): string {
  if (events.includes('*') || events.length === ALL_EVENTS.length) return t('All 8 events');
  if (events.length <= 2) return events.join(', ');
  return fill(t('{count} events'), { count: events.length });
}

/** The URL of a webhook as a row shows it: without its scheme. */
export function shortUrl(url: string): string {
  return url.replace(/^https?:\/\//, '');
}

/** The host of a URL, or the text itself when it is not one. */
export function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return shortUrl(url).split('/')[0] ?? url;
  }
}

/** How long ago a moment was: "now", "5 min ago", "6 h ago", "2 d ago". */
function ago(at: number, now: number): string {
  const minutes = Math.max(0, Math.floor((now - at) / 60_000));
  if (minutes < 1) return t('now');
  if (minutes < 60) return `${minutes} ${t('min ago')}`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} ${t('h ago')}`;
  return `${Math.floor(hours / 24)} ${t('d ago')}`;
}

/** The look of the dot before a hook. `none`: nothing sent yet. */
export type HookDot = 'ok' | 'pend' | 'bad' | 'off' | 'att' | 'none' | 'gone';

/** The dot of a hook, and the words that name it for assistive technology and the tooltip. */
export function hookDot(hook: HookInfo): { dot: HookDot; text: string } {
  if (hook.kind === 'webhook' && hook.status === 'needs-secret') {
    return { dot: 'att', text: t('Needs a new secret') };
  }
  if (!hook.enabled || hook.status === 'disabled') return { dot: 'off', text: t('Off') };
  if (hook.kind === 'script' && !hook.machine.online) return { dot: 'gone', text: t('Offline') };
  switch (hook.lastDelivery?.state) {
    case 'delivered':
      return { dot: 'ok', text: t('Last delivery succeeded') };
    case 'pending':
      return { dot: 'pend', text: t('Retrying') };
    case 'failed':
    case 'dropped':
      return { dot: 'bad', text: t('Last delivery failed') };
    default:
      return { dot: 'none', text: t('Nothing sent yet') };
  }
}

/** `exit-1` as 1; null when the error is not an exit code. */
export function exitCodeOf(error: string | undefined): number | null {
  const m = error === undefined ? null : /^exit-(\d+)$/.exec(error);
  return m?.[1] === undefined ? null : Number(m[1]);
}

/** The last delivery of a hook in a row: "Delivered 2 min ago", "Retrying · http-503", "Exit 1 · 13:40". */
export function lastText(
  hook: WebhookHookInfo | ScriptHookInfo,
  now = Date.now(),
): { text: string; tone: 'bad' | 'att' | '' } {
  if (hook.kind === 'webhook' && hook.status === 'needs-secret') {
    return { text: t('Needs a new secret'), tone: 'att' };
  }
  if (!hook.enabled || hook.status === 'disabled') return { text: t('Off'), tone: '' };
  if (hook.kind === 'script' && !hook.machine.online) {
    return { text: t('Offline · events dropped'), tone: '' };
  }
  const d = hook.lastDelivery;
  if (!d) return { text: t('Not sent yet'), tone: '' };
  if (hook.kind === 'script') {
    const at = clock(d.at);
    if (d.state === 'delivered') {
      return { text: `${fill(t('Exit {code}'), { code: 0 })} · ${at}`, tone: '' };
    }
    if (d.state === 'pending') return { text: t('Running'), tone: '' };
    const code = exitCodeOf(d.error);
    const what =
      code !== null
        ? fill(t('Exit {code}'), { code })
        : [d.state === 'dropped' ? t('Dropped') : t('Failed'), d.error].filter(Boolean).join(' · ');
    return { text: `${what} · ${at}`, tone: 'bad' };
  }
  if (d.state === 'delivered') {
    const when = ago(d.at, now);
    return {
      text: when === t('now') ? t('Delivered just now') : `${t('Delivered')} ${when}`,
      tone: '',
    };
  }
  if (d.state === 'pending') {
    return { text: d.error ? `${t('Retrying')} · ${d.error}` : t('Retrying'), tone: 'att' };
  }
  const head = d.state === 'dropped' ? t('Dropped') : t('Failed');
  return {
    text: [head, d.error, ago(d.at, now)].filter((s) => s !== undefined && s !== '').join(' · '),
    tone: 'bad',
  };
}

/** What a webhook listens to, as its tag says it. */
export function scopeText(
  scope: WebhookHookInfo['scope'],
  machineName: (deviceId: string) => string,
): string {
  if (scope.kind === 'all') return t('All projects');
  return `${baseName(scope.projectPath)} · ${machineName(scope.deviceId)}`;
}

/** The projects of a script hook: its path, how many, or every project. */
export function scriptProjects(projects: readonly string[]): string {
  if (projects.length === 0) return t('Every project');
  if (projects.length === 1) return projects[0] ?? '';
  return fill(t('{count} projects'), { count: projects.length });
}

/** A timeout as a row writes it: `10 s`. */
export function timeoutText(ms: number): string {
  return `${Math.round(ms / 100) / 10} s`;
}

/** The state of a delivery in words, and the dot that goes with it. */
export function deliveryState(state: HookDelivery['state']): { text: string; dot: HookDot } {
  switch (state) {
    case 'delivered':
      return { text: t('Delivered'), dot: 'ok' };
    case 'pending':
      return { text: t('Retrying'), dot: 'pend' };
    case 'failed':
      return { text: t('Failed'), dot: 'bad' };
    case 'dropped':
      return { text: t('Dropped'), dot: 'off' };
  }
}

/** The response column of a delivery: `200`, the error, and when it will be tried again. */
export function responseText(d: HookDelivery, now = Date.now()): string {
  if (d.state === 'delivered') {
    return d.run ? fill(t('Exit {code}'), { code: d.run.exitCode ?? 0 }) : '200';
  }
  const parts: string[] = [];
  if (d.error) parts.push(d.error);
  if (d.state === 'pending' && d.nextAttemptAt !== undefined) {
    const minutes = Math.max(1, Math.round((d.nextAttemptAt - now) / 60_000));
    parts.push(fill(t('next in {minutes} min'), { minutes }));
  }
  return parts.join(' · ') || '—';
}

/** The result of a test, from the delivery the core answered with. */
export function testResult(
  delivery: HookDelivery,
  target: string,
): { ok: boolean; text: string; hint: string } {
  if (delivery.state === 'delivered') {
    return { ok: true, text: fill(t('Test event delivered to {target}'), { target }), hint: '' };
  }
  if (delivery.state === 'pending') {
    return { ok: true, text: fill(t('Test event sent to {target}'), { target }), hint: '' };
  }
  const error = delivery.error ?? 'error';
  return {
    ok: false,
    text: fill(t('Test event failed · {error}'), { error }),
    hint:
      error === 'blocked-address'
        ? t(
            'The host resolves to a private address; set HOPLET_HOOK_ALLOW_PRIVATE on the host to allow it.',
          )
        : '',
  };
}

// -- The form -------------------------------------------------------------------------------------------------------

export type HookForm = {
  /** null: a new webhook. */
  id: string | null;
  label: string;
  url: string;
  /** The URL the hook was loaded with; its query string is not known, so an unchanged URL is not sent. */
  loadedUrl: string;
  events: HookEventType[];
  scope: 'all' | 'project';
  deviceId: string;
  projectPath: string;
  summary: boolean;
  enabled: boolean;
};

export function emptyHookForm(): HookForm {
  return {
    id: null,
    label: '',
    url: '',
    loadedUrl: '',
    events: [],
    scope: 'all',
    deviceId: '',
    projectPath: '',
    summary: false,
    enabled: true,
  };
}

export function formOfHook(hook: WebhookHookInfo): HookForm {
  return {
    id: hook.id,
    label: hook.label,
    url: hook.url,
    loadedUrl: hook.url,
    events: ALL_EVENTS.filter((e) => hook.events.includes(e)),
    scope: hook.scope.kind === 'all' ? 'all' : 'project',
    deviceId: hook.scope.kind === 'project' ? hook.scope.deviceId : '',
    projectPath: hook.scope.kind === 'project' ? hook.scope.projectPath : '',
    summary: hook.summary,
    enabled: hook.enabled,
  };
}

export type HookFormCheck = {
  ok: boolean;
  errors: { label?: string; url?: string; events?: string; project?: string };
};

/** What the form says about itself before the core is asked. */
export function checkHookForm(form: HookForm): HookFormCheck {
  const errors: HookFormCheck['errors'] = {};
  const label = form.label.trim();
  if (label === '') errors.label = t('Name the hook.');
  else if (label.length > 80) errors.label = t('At most 80 characters.');
  const url = form.url.trim();
  if (!/^https?:\/\/\S+$/.test(url) || url.length > 2048) {
    errors.url = t('Enter a full address that starts with https://.');
  }
  if (form.events.length === 0) errors.events = t('Choose at least one event.');
  if (form.scope === 'project' && (form.deviceId === '' || form.projectPath === '')) {
    errors.project = t('Choose a project.');
  }
  return { ok: Object.keys(errors).length === 0, errors };
}

function scopeOf(form: HookForm): HookCreateRequest['scope'] {
  return form.scope === 'all'
    ? { kind: 'all' }
    : { kind: 'project', deviceId: form.deviceId, projectPath: form.projectPath };
}

export function toCreate(form: HookForm): HookCreateRequest {
  return {
    url: form.url.trim(),
    label: form.label.trim(),
    events: ALL_EVENTS.filter((e) => form.events.includes(e)),
    scope: scopeOf(form),
    summary: form.summary,
    enabled: form.enabled,
  };
}

/** The change an edit sends: the URL only when it was changed, since the stored one may carry a query string. */
export function toUpdate(form: HookForm): HookUpdateRequest {
  const body: HookUpdateRequest = {
    label: form.label.trim(),
    events: ALL_EVENTS.filter((e) => form.events.includes(e)),
    scope: scopeOf(form),
    summary: form.summary,
    enabled: form.enabled,
  };
  if (form.url.trim() !== form.loadedUrl) body.url = form.url.trim();
  return body;
}

export type ScriptMachine = {
  id: string;
  name: string;
  online: boolean;
  scripts: ScriptHookInfo[];
};

/** The webhooks and the scripts of a list, the scripts grouped by machine in the order they first appear. */
export function splitHooks(hooks: readonly HookInfo[]): {
  webhooks: WebhookHookInfo[];
  machines: ScriptMachine[];
} {
  const webhooks: WebhookHookInfo[] = [];
  const machines: ScriptMachine[] = [];
  for (const h of hooks) {
    if (h.kind === 'webhook') {
      webhooks.push(h);
      continue;
    }
    let group = machines.find((m) => m.id === h.machine.id);
    if (!group) {
      group = { ...h.machine, scripts: [] };
      machines.push(group);
    }
    group.scripts.push(h);
  }
  return { webhooks, machines };
}
