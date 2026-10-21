import {
  DEFAULT_WEBHOOK_EVENTS,
  NOTIFY_EVENT_KINDS,
  type NotifyChannel,
  type NotifyEventKind,
  type NotifyPreferences,
  type NotifyPreset,
  type WebhookInfo,
} from '@hoplet/shared';
import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { Segmented } from '../../components/Segmented.tsx';
import { Switch } from '../../components/Switch.tsx';
import { TextField } from '../../components/TextField.tsx';
import {
  deleteWebhook,
  fetchNotifyPreferences,
  fetchPushKey,
  fetchPushSubscriptions,
  fetchSessions,
  fetchWebhooks,
  patchWebhook,
  postPushSubscription,
  postPushUnsubscribe,
  postWebhook,
  postWebhookTest,
  putNotifyPreferences,
} from '../../lib/api.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import {
  browserTimeZone,
  CHOOSABLE_CHANNELS,
  defaultQuietHours,
  deliveryText,
  deviceLabel,
  EMPTY_PREFERENCES,
  EVENTS,
  effectiveChannels,
  hostHint,
  isOverridden,
  keyBytes,
  overrideCount,
  PRESETS,
  projectChoices,
  projectName,
  resetScope,
  type Scope,
  sameEvents,
  toggleChannel,
  toggleWebhookEvent,
  WEBHOOK_EVENT_LABEL,
  webhookEventsText,
} from './notify-model.ts';

/** This browser's Web Push as the page shows it. */
export type PushState = 'checking' | 'unsupported' | 'blocked' | 'off' | 'on';

/** What a state of the design starts from: the data the page would load, and which parts are open. */
export type NotifyFixture = {
  preferences: NotifyPreferences;
  projects: string[];
  push: PushState;
  webhooks: WebhookInfo[];
  now: number;
  overridesOpen?: boolean;
  /** The project the override panel is on; the whole install when absent. */
  scope?: string;
  /** A webhook being edited, or `new` for the form of a new one. */
  editing?: string | 'new';
  /** The result of the last test send shown in the form. */
  testResult?: { ok: boolean; text: string };
};

const SUPPORTED =
  typeof window !== 'undefined' &&
  'serviceWorker' in navigator &&
  'PushManager' in window &&
  'Notification' in window;

/** The Web Push of this browser: whether it is subscribed, and the two gestures that change it. */
function usePush(fixture: PushState | undefined, onChanged: () => void) {
  const [state, setState] = useState<PushState>(fixture ?? 'checking');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (fixture !== undefined) return;
    if (!SUPPORTED) {
      setState('unsupported');
      return;
    }
    if (Notification.permission === 'denied') {
      setState('blocked');
      return;
    }
    let cancelled = false;
    void navigator.serviceWorker.ready
      .then((registration) => registration.pushManager.getSubscription())
      .then((subscription) => {
        if (cancelled) return;
        setState(subscription && Notification.permission === 'granted' ? 'on' : 'off');
      })
      .catch(() => {
        if (!cancelled) setState('off');
      });
    return () => {
      cancelled = true;
    };
  }, [fixture]);

  /** Asks for permission and subscribes: only ever called from a click. */
  const enable = async () => {
    if (!SUPPORTED || busy) return;
    setBusy(true);
    setError(null);
    try {
      const permission = await Notification.requestPermission();
      if (permission === 'denied') {
        setState('blocked');
        return;
      }
      if (permission !== 'granted') {
        setState('off');
        return;
      }
      const registration = await navigator.serviceWorker.ready;
      const { publicKey } = await fetchPushKey();
      const subscription =
        (await registration.pushManager.getSubscription()) ??
        (await registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: keyBytes(publicKey),
        }));
      const json = subscription.toJSON();
      const p256dh = json.keys?.p256dh;
      const auth = json.keys?.auth;
      if (!json.endpoint || !p256dh || !auth) throw new Error('subscription');
      await postPushSubscription({
        endpoint: json.endpoint,
        keys: { p256dh, auth },
        label: deviceLabel(navigator.userAgent),
      });
      setState('on');
      onChanged();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const disable = async () => {
    if (!SUPPORTED || busy) return;
    setBusy(true);
    setError(null);
    try {
      const registration = await navigator.serviceWorker.ready;
      const subscription = await registration.pushManager.getSubscription();
      if (subscription) {
        await postPushUnsubscribe(subscription.endpoint);
        await subscription.unsubscribe();
      }
      setState('off');
      onChanged();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return { state, busy, error, enable, disable };
}

function Check(props: { on: boolean }) {
  return props.on ? Icon.check() : null;
}

/** One level of the page, as a card that is one radio of the group. */
function PresetCard(props: {
  id: NotifyPreset;
  title: string;
  detail: string;
  on: boolean;
  onPick: () => void;
}) {
  return (
    // biome-ignore lint/a11y/useSemanticElements: a card that is one radio of the level group
    <button
      type="button"
      role="radio"
      aria-checked={props.on}
      tabIndex={props.on ? 0 : -1}
      className={`preset${props.on ? ' on' : ''}`}
      onClick={props.onPick}
      data-testid={`settings.notify.preset.${props.id}`}
    >
      <span className={`radio${props.on ? ' on' : ''}`} aria-hidden="true" />
      <span className="pt">
        <b>{t(props.title)}</b>
        <span className="pd">{t(props.detail)}</span>
      </span>
    </button>
  );
}

function Presets(props: { value: NotifyPreset; onChange: (preset: NotifyPreset) => void }) {
  const move = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (step === undefined) return;
    e.preventDefault();
    const at = PRESETS.findIndex((p) => p.id === props.value);
    const next = PRESETS[(at + step + PRESETS.length) % PRESETS.length];
    if (!next) return;
    props.onChange(next.id);
    e.currentTarget.querySelectorAll<HTMLElement>('[role="radio"]')[PRESETS.indexOf(next)]?.focus();
  };
  return (
    <div
      className="presets"
      role="radiogroup"
      aria-label={t('Notification level')}
      onKeyDown={move}
      data-testid="settings.notify.presets"
    >
      {PRESETS.map((p) => (
        <PresetCard
          key={p.id}
          id={p.id}
          title={p.title}
          detail={p.detail}
          on={p.id === props.value}
          onPick={() => props.onChange(p.id)}
        />
      ))}
    </div>
  );
}

const CHANNEL_LABEL: Record<NotifyChannel, string> = {
  push: 'Push',
  webhook: 'Webhook',
  native: 'App',
};

function eventIcon(kind: NotifyEventKind): ReactNode {
  switch (kind) {
    case 'permission':
      return Icon.lock();
    case 'plan':
      return Icon.listChecks();
    case 'turn-done':
      return Icon.check();
    case 'failure':
    case 'budget':
      return Icon.alert();
  }
}

/** The per-project and per-event panel: each event a row, its channels chips; a differing choice is marked. */
function Overrides(props: {
  prefs: NotifyPreferences;
  projects: string[];
  initialOpen: boolean;
  initialScope: string | null;
  onChange: (next: NotifyPreferences) => void;
}) {
  const { prefs, onChange } = props;
  const [open, setOpen] = useState(props.initialOpen);
  const [project, setProject] = useState<string | null>(props.initialScope);
  const scope: Scope = { project };
  const count = overrideCount(prefs);
  const choices = projectChoices(prefs, props.projects);
  const options = [
    { value: '', label: t('All projects') },
    ...choices.map((path) => ({ value: path, label: projectName(path) })),
  ];
  return (
    <div className="ovr" data-testid="settings.notify.overrides">
      <button
        type="button"
        className="pr ovr-h"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        data-testid="settings.notify.overrides.toggle"
      >
        <span className="nm">{t('Per project and per event')}</span>
        <span className="set-meta">
          {count === 0
            ? t('No overrides')
            : count === 1
              ? t('1 override')
              : t('{n} overrides').replace('{n}', String(count))}
        </span>
        <span className={`chev${open ? ' open' : ''}`} aria-hidden="true">
          {Icon.down()}
        </span>
      </button>
      {open ? (
        <>
          <div className="ovr-bar">
            <Segmented
              label={t('Project')}
              value={project ?? ''}
              options={options}
              onChange={(value) => setProject(value === '' ? null : value)}
              data-testid="settings.notify.scope"
            />
            <Button
              variant="quiet"
              disabled={
                project === null ? Object.keys(prefs.events).length === 0 : !prefs.projects[project]
              }
              onClick={() => onChange(resetScope(prefs, scope))}
              data-testid="settings.notify.reset"
            >
              {project === null ? t('Reset all projects') : t('Reset this project')}
            </Button>
          </div>
          <ul className="evlist" aria-label={t('Events')}>
            {EVENTS.map((ev) => {
              const channels = effectiveChannels(prefs, ev.kind, scope);
              const overridden = isOverridden(prefs, ev.kind, scope);
              return (
                <li
                  key={ev.kind}
                  className="pr tall"
                  data-testid={`settings.notify.event.${ev.kind}`}
                >
                  <span className={`lead evh ${ev.tone}`} aria-hidden="true">
                    {eventIcon(ev.kind)}
                  </span>
                  <span className="nm">
                    {t(ev.title)}
                    <span className="set-meta">{t(ev.detail)}</span>
                  </span>
                  <span className={`ovd set-meta${overridden ? ' on' : ''}`}>
                    {overridden ? t('Override') : ''}
                  </span>
                  <span className="nchips">
                    {CHOOSABLE_CHANNELS.map((channel) => {
                      const on = channels.includes(channel);
                      return (
                        <button
                          key={channel}
                          type="button"
                          role="switch"
                          aria-checked={on}
                          className={`nchip${on ? ' on' : ''}`}
                          onClick={() => onChange(toggleChannel(prefs, ev.kind, channel, scope))}
                          data-testid={`settings.notify.chip.${ev.kind}.${channel}`}
                        >
                          {channel === 'push' ? Icon.bell() : Icon.webhook()}
                          {t(CHANNEL_LABEL[channel])}
                        </button>
                      );
                    })}
                  </span>
                </li>
              );
            })}
          </ul>
        </>
      ) : null}
    </div>
  );
}

function QuietHoursGroup(props: {
  prefs: NotifyPreferences;
  onChange: (next: NotifyPreferences) => void;
}) {
  const { prefs, onChange } = props;
  const quiet = prefs.quietHours;
  const setTime = (key: 'start' | 'end', value: string) => {
    if (!quiet || !/^([01]\d|2[0-3]):[0-5]\d$/.test(value)) return;
    onChange({ ...prefs, quietHours: { ...quiet, [key]: value } });
  };
  return (
    <div className="sgrp">
      <div className="lb">{t('Quiet hours')}</div>
      <div className="plist">
        <div className="pr tall">
          <span className="lead ic" aria-hidden="true">
            {Icon.moon()}
          </span>
          <span className="nm">
            {quiet ? (
              <span className="times">
                <input
                  type="time"
                  className="field tm"
                  aria-label={t('Quiet hours start')}
                  value={quiet.start}
                  onChange={(e) => setTime('start', e.target.value)}
                  data-testid="settings.notify.quiet.start"
                />
                <span aria-hidden="true">–</span>
                <input
                  type="time"
                  className="field tm"
                  aria-label={t('Quiet hours end')}
                  value={quiet.end}
                  onChange={(e) => setTime('end', e.target.value)}
                  data-testid="settings.notify.quiet.end"
                />
              </span>
            ) : (
              t('Quiet hours')
            )}
            <span className="set-meta">
              {quiet
                ? `${t('Events where Claude needs you still arrive')} · ${quiet.timeZone}`
                : t('Hold back finished-turn notifications overnight')}
            </span>
          </span>
          <Switch
            label={t('Quiet hours')}
            hideLabel
            checked={quiet !== null}
            onChange={(on) =>
              onChange({
                ...prefs,
                quietHours: on ? defaultQuietHours(browserTimeZone()) : null,
              })
            }
            data-testid="settings.notify.quiet"
          />
        </div>
      </div>
    </div>
  );
}

function BrowserGroup(props: { push: ReturnType<typeof usePush> }) {
  const { state, busy, error, enable, disable } = props.push;
  const name =
    typeof navigator === 'undefined' ? t('This browser') : deviceLabel(navigator.userAgent);
  const sub = {
    checking: '',
    unsupported: t('This browser cannot receive Web Push here; it needs HTTPS or localhost'),
    off: t('permission not asked yet'),
    on: t('permission granted'),
    blocked: t('blocked in the browser'),
  }[state];
  return (
    <div className="sgrp">
      <div className="lb">{t('This browser')}</div>
      <div className="plist">
        <div className="pr tall" data-testid="settings.notify.push" data-state={state}>
          <span
            className={`lead ic${state === 'on' ? ' ok' : state === 'blocked' ? ' bad' : ''}`}
            aria-hidden="true"
          >
            {state === 'on' ? Icon.bell() : Icon.bellOff()}
          </span>
          <span className="nm">
            {t('Web Push')}
            <span className="set-meta">{sub ? `${name} · ${sub}` : name}</span>
          </span>
          {state === 'off' ? (
            <Button
              variant="primary"
              disabled={busy}
              onClick={() => void enable()}
              data-testid="settings.notify.push.enable"
            >
              {t('Enable')}
            </Button>
          ) : (
            <Switch
              label={t('Web Push on this browser')}
              hideLabel
              checked={state === 'on'}
              disabled={state !== 'on' || busy}
              onChange={() => void disable()}
              data-testid="settings.notify.push.switch"
            />
          )}
        </div>
      </div>
      {state === 'blocked' ? (
        <div className="nbanner" role="note" data-testid="settings.notify.blocked">
          {Icon.info()}
          <div className="bt">
            <b>{t('Notifications are blocked for this site.')}</b>
            <p>
              {t(
                'Hoplet cannot ask again. Allow notifications in the site settings of your browser (the icon left of the address bar), then reload this page.',
              )}
            </p>
          </div>
        </div>
      ) : null}
      {error ? (
        <p className="mrow-err" role="alert">
          {error}
        </p>
      ) : null}
      <p className="set-meta note">
        {t(
          'Delivered through your browser vendor’s push service. Other browsers and devices enable their own.',
        )}
      </p>
    </div>
  );
}

type Draft = {
  label: string;
  url: string;
  secret: string;
  clearSecret: boolean;
  events: NotifyEventKind[];
};

function WebhookForm(props: {
  existing: WebhookInfo | null;
  now: number;
  initialResult?: { ok: boolean; text: string } | undefined;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { existing } = props;
  const [draft, setDraft] = useState<Draft>({
    label: existing?.label ?? '',
    url: existing?.url ?? '',
    secret: '',
    clearSecret: false,
    events: existing?.events ?? [...DEFAULT_WEBHOOK_EVENTS],
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(
    props.initialResult ?? null,
  );
  const valid = draft.label.trim() !== '' && /^https?:\/\/\S+$/.test(draft.url.trim());

  /** Creates the webhook, or applies the changes to the one being edited; resolves with it. */
  const persist = async (): Promise<WebhookInfo> => {
    const label = draft.label.trim();
    const url = draft.url.trim();
    if (!existing)
      return postWebhook({
        label,
        url,
        events: draft.events,
        ...(draft.secret !== '' ? { secret: draft.secret } : {}),
      });
    const change: Parameters<typeof patchWebhook>[1] = {};
    if (label !== existing.label) change.label = label;
    if (url !== existing.url) change.url = url;
    if (!sameEvents(draft.events, existing.events)) change.events = draft.events;
    if (draft.secret !== '') change.secret = draft.secret;
    else if (draft.clearSecret) change.secret = null;
    return Object.keys(change).length > 0 ? patchWebhook(existing.id, change) : existing;
  };

  const run = async (test: boolean) => {
    if (!valid || busy) return;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const saved = await persist();
      if (test) {
        const { delivery } = await postWebhookTest(saved.id);
        setResult({
          ok: delivery.ok,
          text: delivery.ok
            ? t('Test delivered')
            : `${t('Test failed')} · ${deliveryText(delivery, props.now).replace(`${t('Failed')} · `, '')}`,
        });
        props.onSaved();
        // The form stays open on the saved webhook so the result can be read; a new one is saved once.
        if (!existing) props.onClose();
      } else {
        props.onSaved();
        props.onClose();
      }
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section
      className="wh-card"
      aria-label={existing ? t('Edit webhook') : t('Add a webhook')}
      data-testid="settings.notify.hook.form"
    >
      <div className="mcard-h">
        <b>{existing ? t('Edit webhook') : t('Add a webhook')}</b>
        <IconButton label={t('Close')} icon={Icon.close()} onClick={props.onClose} />
      </div>
      <div className="fl">
        <TextField
          label={t('Name')}
          showLabel
          value={draft.label}
          maxLength={80}
          autoComplete="off"
          onChange={(e) => setDraft({ ...draft, label: e.target.value })}
          data-testid="settings.notify.hook.label"
        />
        <TextField
          label={t('URL')}
          showLabel
          mono
          value={draft.url}
          placeholder="https://"
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => setDraft({ ...draft, url: e.target.value })}
          data-testid="settings.notify.hook.url"
        />
        <TextField
          label={t('Signing secret')}
          showLabel
          mono
          type="password"
          value={draft.secret}
          placeholder={
            existing?.hasSecret && !draft.clearSecret ? '••••••••••••••••' : t('Optional')
          }
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => setDraft({ ...draft, secret: e.target.value })}
          data-testid="settings.notify.hook.secret"
        />
        <div className="field-group">
          <span className="field-label">{t('Events')}</span>
          <div className="nchips" data-testid="settings.notify.hook.events">
            {NOTIFY_EVENT_KINDS.map((kind) => {
              const on = draft.events.includes(kind);
              return (
                <button
                  key={kind}
                  type="button"
                  role="switch"
                  aria-checked={on}
                  className={`nchip${on ? ' on' : ''}`}
                  onClick={() =>
                    setDraft({ ...draft, events: toggleWebhookEvent(draft.events, kind) })
                  }
                  data-testid={`settings.notify.hook.event.${kind}`}
                >
                  {on ? Icon.check() : null}
                  {t(WEBHOOK_EVENT_LABEL[kind])}
                </button>
              );
            })}
          </div>
        </div>
      </div>
      <p className="set-meta note">
        {t('Optional · HMAC-SHA256 in X-Hoplet-Signature')}
        {existing?.hasSecret ? (
          <>
            {' · '}
            <button
              type="button"
              className="link"
              onClick={() => setDraft({ ...draft, secret: '', clearSecret: !draft.clearSecret })}
              data-testid="settings.notify.hook.clear-secret"
            >
              {draft.clearSecret ? t('Keep the secret') : t('Remove the secret')}
            </button>
          </>
        ) : null}
      </p>
      {error ? (
        <p className="mrow-err" role="alert">
          {error}
        </p>
      ) : null}
      <div className="fx">
        {result ? (
          <span
            className={`res${result.ok ? '' : ' bad'}`}
            role="status"
            data-testid="settings.notify.hook.result"
          >
            {result.ok ? Icon.check() : Icon.alert()}
            {result.text}
          </span>
        ) : null}
        <span className="sp" />
        <Button
          icon={Icon.paperPlane()}
          disabled={!valid || busy}
          onClick={() => void run(true)}
          data-testid="settings.notify.hook.test"
        >
          {t('Send a test')}
        </Button>
        <Button
          variant="primary"
          disabled={!valid || busy}
          onClick={() => void run(false)}
          data-testid="settings.notify.hook.save"
        >
          {t('Save')}
        </Button>
      </div>
    </section>
  );
}

function WebhooksGroup(props: {
  webhooks: WebhookInfo[];
  now: number;
  initialEditing: string | undefined;
  initialResult: { ok: boolean; text: string } | undefined;
  offline: boolean;
  onChanged: () => void;
}) {
  const { webhooks, now } = props;
  const [editing, setEditing] = useState<string | undefined>(props.initialEditing);
  const [error, setError] = useState<string | null>(null);
  const existing =
    editing && editing !== 'new' ? webhooks.find((w) => w.id === editing) : undefined;

  const test = async (id: string) => {
    setError(null);
    try {
      await postWebhookTest(id);
    } catch (e) {
      setError(errorText(e));
    }
    props.onChanged();
  };
  const remove = async (id: string) => {
    setError(null);
    try {
      await deleteWebhook(id);
    } catch (e) {
      setError(errorText(e));
    }
    props.onChanged();
  };

  return (
    <div className="sgrp">
      <div className="lb">{t('Webhooks')}</div>
      {webhooks.length > 0 ? (
        <ul className="plist" aria-label={t('Webhooks')} data-testid="settings.notify.hooks">
          {webhooks.map((w) => {
            const last = w.lastDelivery;
            const bad = last !== undefined && !last.ok;
            return (
              <li key={w.id} className="pr tall" data-testid={`settings.notify.hook.${w.id}`}>
                <span className="lead ic" aria-hidden="true">
                  {Icon.webhook()}
                </span>
                <span className="nm">
                  {w.label}
                  <span className="set-meta mono">{hostHint(w.url)}</span>
                  <span className="set-meta" data-testid={`settings.notify.hook.${w.id}.events`}>
                    {webhookEventsText(w.events)}
                  </span>
                </span>
                <span className={`dst${last ? (bad ? ' bad' : ' ok') : ''}`}>
                  {last ? bad ? Icon.alert() : <Check on /> : null}
                  {deliveryText(last, now)}
                </span>
                <span className="acts">
                  <IconButton
                    label={t('Send a test')}
                    icon={Icon.paperPlane()}
                    onClick={() => void test(w.id)}
                    data-testid={`settings.notify.hook.${w.id}.test`}
                  />
                  <IconButton
                    label={t('Edit')}
                    icon={Icon.pen()}
                    onClick={() => setEditing(w.id)}
                    data-testid={`settings.notify.hook.${w.id}.edit`}
                  />
                  <IconButton
                    label={t('Remove')}
                    icon={Icon.trash()}
                    onClick={() => void remove(w.id)}
                    data-testid={`settings.notify.hook.${w.id}.remove`}
                  />
                </span>
              </li>
            );
          })}
        </ul>
      ) : null}
      {error ? (
        <p className="mrow-err" role="alert">
          {error}
        </p>
      ) : null}
      {editing !== undefined && (editing === 'new' || existing) ? (
        <WebhookForm
          key={editing}
          existing={existing ?? null}
          now={now}
          initialResult={props.initialResult}
          onClose={() => setEditing(undefined)}
          onSaved={props.onChanged}
        />
      ) : (
        <div className="sact">
          <Button
            icon={Icon.plus()}
            onClick={() => setEditing('new')}
            data-testid="settings.notify.hook.add"
          >
            {t('Add a webhook')}
          </Button>
        </div>
      )}
      <p className="set-meta note">
        {t(
          'The core sends JSON with the event, the project, the session title and a link. Never the conversation.',
        )}
      </p>
    </div>
  );
}

/** Settings > Notifications (ADR 0021): the level, per-project overrides, quiet hours, this browser and webhooks. */
export function NotificationsSection(props: { fixture?: NotifyFixture }) {
  const { fixture } = props;
  const [prefs, setPrefs] = useState<NotifyPreferences>(fixture?.preferences ?? EMPTY_PREFERENCES);
  const [projects, setProjects] = useState<string[]>(fixture?.projects ?? []);
  const [webhooks, setWebhooks] = useState<WebhookInfo[]>(fixture?.webhooks ?? []);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(fixture !== undefined);
  const saving = useRef(0);

  const loadWebhooks = useCallback(() => {
    if (fixture) return;
    void fetchWebhooks()
      .then((r) => setWebhooks(r.webhooks))
      .catch((e) => setError(errorText(e)));
  }, [fixture]);

  const push = usePush(fixture?.push, loadWebhooks);

  useEffect(() => {
    if (fixture) return;
    let cancelled = false;
    void Promise.all([fetchNotifyPreferences(), fetchWebhooks(), fetchSessions()])
      .then(([p, w, s]) => {
        if (cancelled) return;
        setPrefs(p);
        setWebhooks(w.webhooks);
        setProjects([...new Set(s.sessions.map((x) => x.projectPath.trim()).filter(Boolean))]);
        setLoaded(true);
      })
      .catch((e) => {
        if (!cancelled) setError(errorText(e));
      });
    void fetchPushSubscriptions().catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [fixture]);

  /** Applies a change at once and saves the whole document; the last save in flight decides what stays. */
  const change = (next: NotifyPreferences) => {
    setPrefs(next);
    if (fixture) return;
    setError(null);
    const mine = ++saving.current;
    void putNotifyPreferences(next)
      .then((saved) => {
        if (mine === saving.current) setPrefs(saved);
      })
      .catch((e) => {
        if (mine !== saving.current) return;
        setError(errorText(e));
        void fetchNotifyPreferences()
          .then(setPrefs)
          .catch(() => undefined);
      });
  };

  return (
    <>
      <h1>{t('Notifications')}</h1>
      {error ? (
        <p className="mrow-err page-err" role="alert" data-testid="settings.notify.error">
          {error}
        </p>
      ) : null}
      {loaded ? (
        <>
          <div className="sgrp">
            <div className="lb">{t('What to tell me about')}</div>
            <Presets value={prefs.preset} onChange={(preset) => change({ ...prefs, preset })} />
            <Overrides
              prefs={prefs}
              projects={projects}
              initialOpen={fixture?.overridesOpen ?? false}
              initialScope={fixture?.scope ?? null}
              onChange={change}
            />
          </div>
          <QuietHoursGroup prefs={prefs} onChange={change} />
          <BrowserGroup push={push} />
          <WebhooksGroup
            webhooks={webhooks}
            now={fixture?.now ?? Date.now()}
            initialEditing={fixture?.editing}
            initialResult={fixture?.testResult}
            offline={false}
            onChanged={loadWebhooks}
          />
        </>
      ) : null}
    </>
  );
}
