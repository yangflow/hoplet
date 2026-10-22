import type {
  DeviceInfo,
  HookDelivery,
  HookEventType,
  HookInfo,
  HookListResponse,
  ScriptHookInfo,
  WebhookHookInfo,
} from '@hoplet/shared';
import { type ReactNode, useRef, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { fill } from '../../components/fill.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { MachineIcon } from '../../components/MachineIcon.tsx';
import { Menu, type MenuEntry } from '../../components/Menu.tsx';
import { Segmented } from '../../components/Segmented.tsx';
import { Select } from '../../components/Select.tsx';
import { Switch } from '../../components/Switch.tsx';
import { TextField } from '../../components/TextField.tsx';
import { Toast, useToast } from '../../components/Toast.tsx';
import { copyText } from '../../lib/clipboard.ts';
import { clock } from '../../lib/format.ts';
import {
  ALL_EVENTS,
  checkHookForm,
  deliveryState,
  emptyHookForm,
  eventName,
  eventsSummary,
  formOfHook,
  type HookForm,
  hookDot,
  hostOf,
  lastText,
  responseText,
  scopeText,
  scriptProjects,
  shortUrl,
  splitHooks,
  testResult,
  timeoutText,
} from '../../lib/hooks.ts';
import { currentLanguage, t } from '../../lib/i18n.ts';
import { systemOf } from '../../lib/system.ts';
import { baseName, fill as fillText } from '../../lib/templates.ts';
import { useDevices } from '../../lib/use-devices.ts';
import { type HooksState, useHookDeliveries, useHooks } from '../../lib/use-hooks.ts';
import { useProjectChoices } from '../../lib/use-project-choices.ts';
import '../../components/hooks.css';

/** The secret a card or a dialog shows once. */
type SecretView = {
  title: string;
  secret: string;
  /** Set after a rotation: when the previous secret stops working. */
  until?: number;
  /** A rotation answers in a dialog, an added hook in a card. */
  dialog: boolean;
};

type TestNote = { id: string; ok: boolean; text: string; hint: string };

/** What the development fixtures hand to Settings > Hooks. */
export type HooksFixture = {
  list: HookListResponse;
  devices: DeviceInfo[];
  /** The directories each machine offers, by device ID. */
  projects: Record<string, string[]>;
  /** The recent deliveries of each hook, by hook ID. */
  deliveries?: Record<string, HookDelivery[]>;
  /** The ID of the hook that starts open. */
  open?: string;
  /** The form that starts open in the editor. */
  form?: HookForm;
  secret?: SecretView;
  /** The ID of the hook whose "rotate the secret" confirmation starts open. */
  confirm?: string;
  /** A test that has just run, with the delivery it made. */
  test?: { id: string; delivery: HookDelivery };
  toast?: string;
};

const mono = (s: string) => <span className="mono">{s}</span>;
const DAY_MS = 24 * 60 * 60 * 1000;

function machineOf(devices: readonly DeviceInfo[], deviceId: string): string {
  return devices.find((d) => d.deviceId === deviceId)?.name ?? deviceId;
}

function pairValue(deviceId: string, path: string): string {
  return `${deviceId}\u0000${path}`;
}

function dateText(at: number): string {
  const locale = currentLanguage() === 'zh-Hans' ? 'zh-Hans' : 'en-GB';
  return new Date(at).toLocaleDateString(locale, { day: 'numeric', month: 'short' });
}

function Field(props: { label: ReactNode; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="fld">
      <span className="l">{props.label}</span>
      {props.children}
      {props.hint ? <span className="meta">{props.hint}</span> : null}
    </div>
  );
}

function Chips(props: { events: readonly string[] }) {
  if (props.events.includes('*')) return <span className="hqc">{t('All 8 events')}</span>;
  return (
    <>
      {props.events.map((e) => (
        <span key={e} className="hqc">
          {e}
        </span>
      ))}
    </>
  );
}

function Note(props: {
  tone: 'ok' | 'bad' | 'snap';
  icon: ReactNode;
  role?: 'status' | 'alert' | 'note';
  children: ReactNode;
  testid?: string;
}) {
  return (
    <div className={`hknote ${props.tone}`} role={props.role ?? 'note'} data-testid={props.testid}>
      {props.icon}
      <span>{props.children}</span>
    </div>
  );
}

function Heading(props: { title: string; children?: ReactNode }) {
  return (
    <div className="hgh">
      <span className="lb">{props.title}</span>
      <span className="sp" />
      {props.children}
    </div>
  );
}

// -- The deliveries -------------------------------------------------------------------------------------------------

function Deliveries(props: { rows: HookDelivery[] }) {
  const now = Date.now();
  return (
    <div className="mp-list" data-testid="settings.hooks.deliveries">
      <div className="dv th2">
        <span>{t('Time')}</span>
        <span>{t('Event')}</span>
        <span>{t('State')}</span>
        <span className="n">{t('Tries')}</span>
        <span>{t('Response')}</span>
      </div>
      {props.rows.map((d) => {
        const s = deliveryState(d.state);
        return (
          <div className="dv" key={d.id}>
            <span className="mono num">{clock(d.createdAt)}</span>
            <span className="mono">{d.type}</span>
            <span className="stt">
              <span className={`hd ${s.dot}`} />
              {s.text}
            </span>
            <span className="n">{d.attempts}</span>
            <span className="r">{responseText(d, now)}</span>
          </div>
        );
      })}
      <div className="dvf">
        {t('The latest 100 deliveries, kept 14 days. The response body is not stored.')}
      </div>
    </div>
  );
}

// -- A webhook ------------------------------------------------------------------------------------------------------

function RowMenu(props: { label: string; entries: MenuEntry[]; testid: string }) {
  const [open, setOpen] = useState(false);
  const [byKey, setByKey] = useState(false);
  const ref = useRef<HTMLSpanElement>(null);
  return (
    <span className="more" ref={ref}>
      <IconButton
        label={props.label}
        icon={Icon.ellipsis()}
        on={open}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={(e) => {
          setByKey(e.detail === 0);
          setOpen((v) => !v);
        }}
        data-testid={props.testid}
      />
      {open ? (
        <Menu
          label={props.label}
          entries={props.entries}
          className="mn-top"
          keyboard={byKey}
          onClose={(restore) => {
            setOpen(false);
            if (restore) ref.current?.querySelector('button')?.focus();
          }}
        />
      ) : null}
    </span>
  );
}

function WebhookDetail(props: {
  hook: WebhookHookInfo;
  devices: readonly DeviceInfo[];
  state: HooksState;
  fixture: Record<string, HookDelivery[]> | undefined;
  version: number;
  note: TestNote | null;
  onEdit: () => void;
  onTest: () => void;
  onRotate: () => void;
  onDelete: () => void;
}) {
  const { hook, state } = props;
  const lost = hook.status === 'needs-secret';
  const deliveries = useHookDeliveries(
    lost ? null : hook.id,
    props.version,
    props.fixture ? (props.fixture[hook.id] ?? []) : undefined,
  );
  return (
    <div className="hxd" data-testid={`settings.hooks.detail.${hook.id}`}>
      <div className="hkv">
        <span className="k">{t('URL')}</span>
        <span className="v mono">{hook.url}</span>
        <span className="k">{t('Events')}</span>
        <span className="v">
          <Chips events={hook.events} />
        </span>
        <span className="k">{t('Projects')}</span>
        <span className="v">{scopeText(hook.scope, (id) => machineOf(props.devices, id))}</span>
        <span className="k">{t('Summary')}</span>
        <span className="v">{hook.summary ? t('Included') : t('Not included')}</span>
        <span className="k">{t('Secret')}</span>
        <span className="v">
          {lost
            ? t('Lost — rotate to send again')
            : fillText(t('Set · created {date}'), { date: dateText(hook.createdAt) })}
        </span>
      </div>
      {lost ? (
        <div className="hwarn" role="note">
          {Icon.alert()}
          <span>
            {t('The secret file lost this hook’s secret. Nothing is sent until you rotate it.')}
          </span>
          <Button
            icon={Icon.retry()}
            disabled={state.saving}
            onClick={props.onRotate}
            data-testid="settings.hooks.rotate-lost"
          >
            {t('Rotate secret')}
          </Button>
        </div>
      ) : null}
      {props.note ? (
        <Note
          tone={props.note.ok ? 'ok' : 'bad'}
          icon={props.note.ok ? Icon.check() : Icon.alert()}
          role="status"
          testid="settings.hooks.test-note"
        >
          {[props.note.text, props.note.hint].filter(Boolean).join(' ')}
        </Note>
      ) : null}
      <div className="hacts">
        <Button icon={Icon.pen()} onClick={props.onEdit} data-testid="settings.hooks.edit">
          {t('Edit')}
        </Button>
        <Button
          icon={Icon.play()}
          disabled={lost || state.saving}
          onClick={props.onTest}
          data-testid="settings.hooks.test"
        >
          {t('Send test event')}
        </Button>
        {lost ? null : (
          <Button
            icon={Icon.retry()}
            disabled={state.saving}
            onClick={props.onRotate}
            data-testid="settings.hooks.rotate"
          >
            {t('Rotate secret')}
          </Button>
        )}
        <Button
          variant="quiet"
          className="del"
          icon={Icon.trash()}
          disabled={state.saving}
          onClick={props.onDelete}
          data-testid="settings.hooks.delete"
        >
          {t('Delete')}
        </Button>
      </div>
      {lost ? null : (
        <>
          <div className="meta">{t('Deliveries')}</div>
          {deliveries.data ? (
            <Deliveries rows={deliveries.data.slice(0, 6)} />
          ) : (
            <p className="rmeta" role={deliveries.error ? 'alert' : 'status'}>
              {deliveries.error ?? t('Loading…')}
            </p>
          )}
        </>
      )}
    </div>
  );
}

function WebhookRow(props: {
  hook: WebhookHookInfo;
  devices: readonly DeviceInfo[];
  selected: boolean;
  busy: boolean;
  onOpen: () => void;
  onToggle: (enabled: boolean) => void;
  onEdit: () => void;
  onTest: () => void;
  onRotate: () => void;
  onDelete: () => void;
}) {
  const { hook, selected } = props;
  const dot = hookDot(hook);
  const last = lastText(hook);
  const entries: MenuEntry[] = [
    { kind: 'item', id: 'edit', label: t('Edit'), icon: Icon.pen(), onSelect: props.onEdit },
    ...(hook.status === 'needs-secret'
      ? []
      : [
          {
            kind: 'item' as const,
            id: 'test',
            label: t('Send test event'),
            icon: Icon.play(),
            onSelect: props.onTest,
          },
        ]),
    {
      kind: 'item',
      id: 'rotate',
      label: t('Rotate secret'),
      icon: Icon.retry(),
      onSelect: props.onRotate,
    },
    { kind: 'separator', id: 'sep' },
    {
      kind: 'item',
      id: 'delete',
      label: t('Delete'),
      icon: Icon.trash(),
      danger: true,
      onSelect: props.onDelete,
    },
  ];
  return (
    <div
      className={`hk${hook.enabled ? '' : ' off'}${selected ? ' sel' : ''}`}
      data-testid={`settings.hooks.row.${hook.id}`}
    >
      <span className={`hd ${dot.dot}`} role="img" aria-label={dot.text} title={dot.text} />
      <button
        type="button"
        className="hx"
        aria-expanded={selected}
        onClick={props.onOpen}
        data-testid={`settings.hooks.open.${hook.id}`}
      >
        <span className="h1">
          <b>{hook.label}</b>
          <span className="tag">{scopeText(hook.scope, (id) => machineOf(props.devices, id))}</span>
        </span>
        <span className="h2">
          <span className="mono">{shortUrl(hook.url)}</span>
          <i>·</i>
          <span>{eventsSummary(hook.events)}</span>
        </span>
      </button>
      <span className={`hl2${last.tone ? ` ${last.tone}` : ''}`}>{last.text}</span>
      <Switch
        label={hook.label}
        hideLabel
        checked={hook.enabled}
        disabled={props.busy}
        onChange={props.onToggle}
        data-testid={`settings.hooks.switch.${hook.id}`}
      />
      <RowMenu label={t('More')} entries={entries} testid={`settings.hooks.more.${hook.id}`} />
    </div>
  );
}

// -- A script -------------------------------------------------------------------------------------------------------

function ScriptStderr(props: {
  hook: ScriptHookInfo;
  fixture: Record<string, HookDelivery[]> | undefined;
  version: number;
}) {
  const { hook } = props;
  const deliveries = useHookDeliveries(
    hook.id,
    props.version,
    props.fixture ? (props.fixture[hook.id] ?? []) : undefined,
  );
  const stderr = deliveries.data?.find((d) => d.run?.stderr)?.run?.stderr;
  if (!stderr) return null;
  return (
    <div className="hxd" data-testid={`settings.hooks.stderr.${hook.id}`}>
      <div className="meta">{t('Standard error · first 1 KiB')}</div>
      <div className="hstderr">{stderr}</div>
    </div>
  );
}

function ScriptRow(props: {
  hook: ScriptHookInfo;
  open: boolean;
  busy: boolean;
  fixture: Record<string, HookDelivery[]> | undefined;
  version: number;
  note: TestNote | null;
  onOpen: () => void;
  onToggle: (enabled: boolean) => void;
  onTest: () => void;
}) {
  const { hook } = props;
  const dot = hookDot(hook);
  const last = lastText(hook);
  const failed = hook.lastDelivery?.state === 'failed';
  const body = (
    <>
      <span className="h1">
        <b>{hook.label}</b>
        <Chips events={hook.events} />
      </span>
      <span className="h2">
        <span className="mono">{hook.command}</span>
        <i>·</i>
        <span>{scriptProjects(hook.projects)}</span>
        <i>·</i>
        <span>{timeoutText(hook.timeoutMs)}</span>
      </span>
    </>
  );
  return (
    <>
      <div
        className={`hk sc${hook.enabled ? '' : ' off'}${hook.machine.online ? '' : ' gone'}${props.open ? ' sel' : ''}`}
        data-testid={`settings.hooks.script.${hook.id}`}
      >
        <span className="lead" role="img" aria-label={dot.text} title={dot.text}>
          {Icon.terminal()}
        </span>
        {failed ? (
          <button
            type="button"
            className="hx"
            aria-expanded={props.open}
            onClick={props.onOpen}
            data-testid={`settings.hooks.open.${hook.id}`}
          >
            {body}
          </button>
        ) : (
          <div className="hx">{body}</div>
        )}
        <span className={`hl2${last.tone ? ` ${last.tone}` : ''}`}>{last.text}</span>
        <Switch
          label={hook.label}
          hideLabel
          checked={hook.enabled}
          disabled={props.busy}
          onChange={props.onToggle}
          data-testid={`settings.hooks.switch.${hook.id}`}
        />
        <RowMenu
          label={t('More')}
          entries={[
            {
              kind: 'item',
              id: 'test',
              label: t('Send test event'),
              icon: Icon.play(),
              onSelect: props.onTest,
            },
          ]}
          testid={`settings.hooks.more.${hook.id}`}
        />
      </div>
      {props.note ? (
        <div className="hxd">
          <Note
            tone={props.note.ok ? 'ok' : 'bad'}
            icon={props.note.ok ? Icon.check() : Icon.alert()}
            role="status"
          >
            {[props.note.text, props.note.hint].filter(Boolean).join(' ')}
          </Note>
        </div>
      ) : null}
      {props.open && failed ? (
        <ScriptStderr hook={hook} fixture={props.fixture} version={props.version} />
      ) : null}
    </>
  );
}

// -- The editor -----------------------------------------------------------------------------------------------------

function EventBox(props: {
  type: HookEventType;
  checked: boolean;
  onChange: (on: boolean) => void;
}) {
  return (
    // biome-ignore lint/a11y/useSemanticElements: a drawn check box, as in the mockup
    <button
      type="button"
      role="checkbox"
      aria-checked={props.checked}
      className="hev"
      onClick={() => props.onChange(!props.checked)}
      data-testid={`settings.hooks.event.${props.type}`}
    >
      <span className={`hcb${props.checked ? ' on' : ''}`}>
        {props.checked ? Icon.check() : null}
      </span>
      <span className="tx">
        <span className="mono">{props.type}</span>
        <span className="meta">{eventName(props.type)}</span>
      </span>
    </button>
  );
}

function HookFields(props: {
  form: HookForm;
  devices: readonly DeviceInfo[];
  projects: { deviceId: string; path: string }[];
  onChange: (change: Partial<HookForm>) => void;
}) {
  const { form, devices } = props;
  const check = checkHookForm(form);
  const choices =
    form.deviceId &&
    form.projectPath &&
    !props.projects.some((p) => p.deviceId === form.deviceId && p.path === form.projectPath)
      ? [...props.projects, { deviceId: form.deviceId, path: form.projectPath }]
      : props.projects;
  const urlHint = t(
    'https only. The query string is stored but never shown again, so it can carry a token.',
  );
  const labelWrong = form.label !== '' && check.errors.label !== undefined;
  const urlWrong = form.url !== '' && check.errors.url !== undefined;
  return (
    <div className="hfq">
      <Field label={t('Label')} hint={labelWrong ? check.errors.label : undefined}>
        <TextField
          label={t('Label')}
          value={form.label}
          placeholder={t('Deploy notifier')}
          aria-invalid={labelWrong ? true : undefined}
          onChange={(e) => props.onChange({ label: e.target.value })}
          data-testid="settings.hooks.label"
        />
      </Field>
      <Field label={t('URL')} hint={urlWrong ? check.errors.url : urlHint}>
        <TextField
          label={t('URL')}
          type="url"
          value={form.url}
          placeholder="https://example.org/hoplet"
          aria-invalid={urlWrong ? true : undefined}
          onChange={(e) => props.onChange({ url: e.target.value })}
          data-testid="settings.hooks.url"
        />
      </Field>
      <Field label={t('Events')}>
        {/* biome-ignore lint/a11y/useSemanticElements: a group of check boxes, as in the mockup */}
        <div className="hevs" role="group" aria-label={t('Events')}>
          {ALL_EVENTS.map((type) => (
            <EventBox
              key={type}
              type={type}
              checked={form.events.includes(type)}
              onChange={(on) =>
                props.onChange({
                  events: on ? [...form.events, type] : form.events.filter((e) => e !== type),
                })
              }
            />
          ))}
        </div>
      </Field>
      <Field label={t('Projects')}>
        <div className="hscope">
          <Segmented
            label={t('Projects')}
            value={form.scope}
            options={[
              { value: 'all', label: t('All projects') },
              { value: 'project', label: t('One project') },
            ]}
            onChange={(scope) => props.onChange({ scope })}
            data-testid="settings.hooks.scope"
          />
          {form.scope === 'project' ? (
            <Select
              label={t('Project')}
              value={
                form.deviceId && form.projectPath ? pairValue(form.deviceId, form.projectPath) : ''
              }
              placeholder={t('Choose a project')}
              options={choices.map((p) => ({
                value: pairValue(p.deviceId, p.path),
                label: `${baseName(p.path)} · ${machineOf(devices, p.deviceId)}`,
                leading: Icon.folder(),
              }))}
              onChange={(v) => {
                const [deviceId = '', projectPath = ''] = v.split('\u0000');
                props.onChange({ deviceId, projectPath });
              }}
              data-testid="settings.hooks.project"
            />
          ) : null}
        </div>
      </Field>
      <div className="hsw">
        <div className="tx">
          <span>{t('Include a summary')}</span>
          <span className="meta">
            {t(
              'Adds the session title and usage to turn.completed, a redacted command or path to permission.requested and the reason to session.error. Off sends names, codes, counts and times only.',
            )}
          </span>
        </div>
        <Switch
          label={t('Include a summary')}
          hideLabel
          checked={form.summary}
          onChange={(summary) => props.onChange({ summary })}
          data-testid="settings.hooks.summary"
        />
      </div>
      <div className="hsw">
        <div className="tx">
          <span>{t('Enabled')}</span>
        </div>
        <Switch
          label={t('Enabled')}
          hideLabel
          checked={form.enabled}
          onChange={(enabled) => props.onChange({ enabled })}
          data-testid="settings.hooks.enabled"
        />
      </div>
    </div>
  );
}

function HookEditor(props: {
  form: HookForm;
  devices: readonly DeviceInfo[];
  projects: { deviceId: string; path: string }[];
  state: HooksState;
  note: TestNote | null;
  onChange: (form: HookForm) => void;
  onClose: () => void;
  onSaved: (text: string) => void;
  onAdded: (hook: WebhookHookInfo, secret: string) => void;
  onTest: () => void;
  onDelete: () => void;
}) {
  const { form, state } = props;
  const existing = form.id !== null;
  const title = existing ? fillText(t('Edit {name}'), { name: form.label }) : t('Add webhook');
  const check = checkHookForm(form);
  const set = (change: Partial<HookForm>) => {
    state.clearProblem();
    props.onChange({ ...form, ...change });
  };
  const save = async () => {
    if (!check.ok || state.saving) return;
    if (existing) {
      if (await state.update(form)) props.onSaved(t('Hook saved'));
      return;
    }
    const made = await state.create(form);
    if (made && made.hook.kind === 'webhook') props.onAdded(made.hook, made.secret);
  };
  return (
    // biome-ignore lint/a11y/useSemanticElements: a group of fields, as in the mockup
    <div
      className="mcard"
      role="group"
      aria-label={title}
      data-testid="settings.hooks.editor"
      onKeyDown={(e) => {
        if (e.key === 'Escape' && !e.defaultPrevented && !e.nativeEvent.isComposing) {
          e.preventDefault();
          e.stopPropagation();
          props.onClose();
        }
      }}
    >
      <div className="mcard-h">
        <b>{title}</b>
        <IconButton label={t('Close (Esc)')} icon={Icon.close()} onClick={props.onClose} />
      </div>
      <HookFields form={form} devices={props.devices} projects={props.projects} onChange={set} />
      {props.note ? (
        <Note
          tone={props.note.ok ? 'ok' : 'bad'}
          icon={props.note.ok ? Icon.check() : Icon.alert()}
          role="status"
        >
          {[props.note.text, props.note.hint].filter(Boolean).join(' ')}
        </Note>
      ) : null}
      {state.problem ? (
        <Note tone="bad" icon={Icon.alert()} role="alert" testid="settings.hooks.problem">
          {state.problem}
        </Note>
      ) : null}
      <div className="mcard-f">
        {existing ? (
          <Button
            variant="quiet"
            className="del"
            icon={Icon.trash()}
            disabled={state.saving}
            onClick={props.onDelete}
            data-testid="settings.hooks.editor-delete"
          >
            {t('Delete')}
          </Button>
        ) : null}
        {existing ? (
          <Button
            variant="quiet"
            icon={Icon.play()}
            disabled={state.saving}
            onClick={props.onTest}
            data-testid="settings.hooks.editor-test"
          >
            {t('Send test event')}
          </Button>
        ) : null}
        <Button variant="quiet" onClick={props.onClose}>
          {t('Cancel')}
        </Button>
        <Button
          variant="primary"
          disabled={!check.ok || state.saving}
          onClick={() => void save()}
          data-testid="settings.hooks.save"
        >
          {existing ? t('Save') : t('Add hook')}
        </Button>
      </div>
    </div>
  );
}

// -- The secret -----------------------------------------------------------------------------------------------------

function untilText(until: number): string {
  const day = new Date(until).getDate() === new Date().getDate() ? null : t('{time} tomorrow');
  return day ? fillText(day, { time: clock(until) }) : clock(until);
}

function SecretBody(props: { view: SecretView; onCopied: (ok: boolean) => void }) {
  const { view } = props;
  return (
    <>
      <div className="hwarn" role="note">
        {Icon.alert()}
        <span>
          {t('Copy this secret now. Hoplet keeps it to sign deliveries and never shows it again.')}
        </span>
      </div>
      <Field label={t('Signing secret')}>
        <div className="hsec">
          <span className="mono" data-testid="settings.hooks.secret">
            {view.secret}
          </span>
          <Button
            icon={Icon.copy()}
            onClick={() => void copyText(view.secret).then(props.onCopied)}
            data-testid="settings.hooks.copy"
          >
            {t('Copy')}
          </Button>
        </div>
      </Field>
      {view.until !== undefined ? (
        <Note tone="snap" icon={Icon.info()}>
          {fillText(
            t(
              'The previous secret keeps working until {time}. Until then each delivery carries both signatures, so you can switch the receiver without a gap.',
            ),
            { time: untilText(view.until) },
          )}
        </Note>
      ) : null}
      <div className="meta">
        {fill(
          `${t('A receiver checks')} {header} ${t('over the raw body and refuses a time more than 5 minutes off.')}`,
          { header: mono('Hoplet-Signature: t=<unix seconds>,v1=<hex>') },
        )}
      </div>
    </>
  );
}

function SecretCard(props: {
  view: SecretView;
  onDone: () => void;
  onCopied: (ok: boolean) => void;
}) {
  return (
    // biome-ignore lint/a11y/useSemanticElements: a group of fields, as in the mockup
    <div
      className="mcard"
      role="group"
      aria-label={props.view.title}
      data-testid="settings.hooks.secret-card"
    >
      <div className="mcard-h">
        <b>{props.view.title}</b>
      </div>
      <SecretBody view={props.view} onCopied={props.onCopied} />
      <div className="mcard-f">
        <Button variant="primary" onClick={props.onDone} data-testid="settings.hooks.done">
          {t('Done')}
        </Button>
      </div>
    </div>
  );
}

function SecretDialog(props: {
  view: SecretView;
  onDone: () => void;
  onCopied: (ok: boolean) => void;
}) {
  return (
    <Dialog
      label={props.view.title}
      onClose={props.onDone}
      className="dlg hd-dlg hd-secret"
      data-testid="settings.hooks.secret-dialog"
    >
      <div className="dlg-h">{props.view.title}</div>
      <div className="dlg-b">
        <SecretBody view={props.view} onCopied={props.onCopied} />
      </div>
      <div className="dlg-f">
        <Button variant="primary" onClick={props.onDone} data-testid="settings.hooks.done">
          {t('Done')}
        </Button>
      </div>
    </Dialog>
  );
}

function ConfirmRotate(props: {
  hook: WebhookHookInfo;
  busy: boolean;
  onCancel: () => void;
  onRotate: () => void;
}) {
  const title = fillText(t('Rotate the secret of {name}?'), { name: props.hook.label });
  return (
    <Dialog
      label={title}
      onClose={props.onCancel}
      className="dlg hd-dlg hd-confirm"
      data-testid="settings.hooks.confirm"
    >
      <div className="dlg-h">{title}</div>
      <div className="dlg-b">
        <p className="dlg-p">
          {t(
            'Hoplet makes a new secret and shows it once. The old one keeps working for 24 hours, so the receiver can switch without missing a delivery.',
          )}
        </p>
      </div>
      <div className="dlg-f">
        <Button variant="quiet" onClick={props.onCancel} data-autofocus>
          {t('Cancel')}
        </Button>
        <Button
          variant="primary"
          icon={Icon.retry()}
          disabled={props.busy}
          onClick={props.onRotate}
          data-testid="settings.hooks.confirm-rotate"
        >
          {t('Rotate secret')}
        </Button>
      </div>
    </Dialog>
  );
}

// -- The section ----------------------------------------------------------------------------------------------------

export function HooksSection(props: { fixture?: HooksFixture | undefined }) {
  const { fixture } = props;
  const devices = useDevices(fixture?.devices);
  const state = useHooks(fixture?.list);
  const projects = useProjectChoices(devices, fixture?.projects);
  const [toast, say] = useToast(fixture?.toast ? { text: fixture.toast } : null);
  const [form, setForm] = useState<HookForm | null>(fixture?.form ?? null);
  const [secret, setSecret] = useState<SecretView | null>(fixture?.secret ?? null);
  const [open, setOpen] = useState<string | null>(fixture?.open ?? null);
  const [confirm, setConfirm] = useState<string | null>(fixture?.confirm ?? null);
  const [version, setVersion] = useState(0);
  const [note, setNote] = useState<TestNote | null>(() => {
    const test = fixture?.test;
    const hook = test ? fixture?.list.hooks.find((h) => h.id === test.id) : undefined;
    if (!test || !hook) return null;
    return {
      id: test.id,
      ...testResult(test.delivery, hook.kind === 'webhook' ? hostOf(hook.url) : hook.label),
    };
  });

  if (state.loading) {
    return (
      <p className="rmeta" role="status">
        {t('Loading…')}
      </p>
    );
  }
  if (state.error || !state.data) {
    return (
      <p className="rmeta" role="alert" data-testid="settings.hooks.load-error">
        {state.error ?? t('Loading…')}
      </p>
    );
  }

  const { webhooks, machines } = splitHooks(state.data.hooks);
  const secretCard = secret && !secret.dialog ? secret : null;
  const secretDialog = secret?.dialog ? secret : null;
  const editing = form !== null || secretCard !== null;
  const confirming = webhooks.find((h) => h.id === confirm) ?? null;
  const dropDelay = (id: string) => {
    setNote((n) => (n?.id === id ? null : n));
  };

  const startAdd = () => {
    state.clearProblem();
    setSecret(null);
    setForm(emptyHookForm());
  };
  const startEdit = (hook: WebhookHookInfo) => {
    state.clearProblem();
    setSecret(null);
    setForm(formOfHook(hook));
  };
  const runTest = async (hook: HookInfo) => {
    const answer = await state.test(hook.id);
    if (!answer) {
      say(t('The test event was not sent'), true);
      return;
    }
    const target = hook.kind === 'webhook' ? hostOf(hook.url) : hook.label;
    setNote({ id: hook.id, ...testResult(answer.delivery, target) });
    setVersion((v) => v + 1);
  };
  const remove = async (hook: WebhookHookInfo) => {
    const ok = await state.remove(hook.id);
    if (ok) {
      setForm(null);
      setOpen((o) => (o === hook.id ? null : o));
      dropDelay(hook.id);
    }
    say(ok ? t('Hook deleted') : t('The change was not saved'), !ok);
  };
  const toggle = async (hook: HookInfo, enabled: boolean) => {
    const ok = await state.toggle(hook, enabled);
    if (!ok) say(t('The change was not saved'), true);
  };
  const rotate = async (hook: WebhookHookInfo) => {
    const made = await state.rotate(hook.id);
    setConfirm(null);
    if (!made) {
      say(t('The change was not saved'), true);
      return;
    }
    setSecret({
      title: fillText(t('New secret · {name}'), { name: hook.label }),
      secret: made.secret,
      until: Date.now() + DAY_MS,
      dialog: true,
    });
  };
  const copied = (ok: boolean) => say(ok ? t('Copied') : t('Could not copy'), !ok);
  const openRow = (id: string) => {
    dropDelay(id);
    setOpen((o) => (o === id ? null : id));
  };

  return (
    <div className="hkpage" data-testid="settings.hooks">
      <h1>{t('Hooks')}</h1>
      {editing ? null : (
        <p className="rmeta qlead">
          {t(
            'A hook tells another program when something happens in a session. It only observes: what a hook does or fails to do never changes a session, a permission or a handoff. Delivery is at least once, so a receiver keeps the event id.',
          )}
        </p>
      )}
      {secretCard ? (
        <SecretCard view={secretCard} onDone={() => setSecret(null)} onCopied={copied} />
      ) : null}
      {form ? (
        <HookEditor
          form={form}
          devices={devices}
          projects={projects}
          state={state}
          note={note && note.id === form.id ? note : null}
          onChange={setForm}
          onClose={() => {
            state.clearProblem();
            setForm(null);
          }}
          onSaved={(text) => {
            setForm(null);
            say(text);
          }}
          onAdded={(hook, value) => {
            setForm(null);
            setSecret({
              title: fillText(t('Hook added · {host}'), { host: hostOf(hook.url) }),
              secret: value,
              dialog: false,
            });
          }}
          onTest={() => {
            const hook = webhooks.find((h) => h.id === form.id);
            if (hook) void runTest(hook);
          }}
          onDelete={() => {
            const hook = webhooks.find((h) => h.id === form.id);
            if (hook) void remove(hook);
          }}
        />
      ) : null}
      <Heading title={t('Webhooks')}>
        {editing ? null : (
          <Button
            variant="primary"
            icon={Icon.plus()}
            onClick={startAdd}
            data-testid="settings.hooks.add"
          >
            {t('Add webhook')}
          </Button>
        )}
      </Heading>
      <div className="mp-list" data-testid="settings.hooks.webhooks">
        {webhooks.length === 0 ? (
          <div className="uempty" data-testid="settings.hooks.empty">
            <span className="tile">{Icon.webhook()}</span>
            <b>{t('No hooks yet')}</b>
            <p>
              {t(
                'Add a webhook to post session events to another service, such as a chat room or a CI job. A script on one of your machines is declared in that machine’s agent configuration and appears here.',
              )}
            </p>
            <div className="acts">
              <Button variant="primary" icon={Icon.plus()} onClick={startAdd}>
                {t('Add webhook')}
              </Button>
            </div>
          </div>
        ) : null}
        {webhooks.map((hook) => {
          const noteHere = note && note.id === hook.id ? note : null;
          const isOpen = open === hook.id;
          return (
            <div key={hook.id} className="hkw">
              <WebhookRow
                hook={hook}
                devices={devices}
                selected={isOpen || noteHere !== null}
                busy={state.saving}
                onOpen={() => openRow(hook.id)}
                onToggle={(on) => void toggle(hook, on)}
                onEdit={() => startEdit(hook)}
                onTest={() => {
                  setOpen(hook.id);
                  void runTest(hook);
                }}
                onRotate={() => setConfirm(hook.id)}
                onDelete={() => void remove(hook)}
              />
              {isOpen ? (
                <WebhookDetail
                  hook={hook}
                  devices={devices}
                  state={state}
                  fixture={fixture?.deliveries}
                  version={version}
                  note={noteHere}
                  onEdit={() => startEdit(hook)}
                  onTest={() => void runTest(hook)}
                  onRotate={() => setConfirm(hook.id)}
                  onDelete={() => void remove(hook)}
                />
              ) : noteHere ? (
                <div className="hxd">
                  <Note
                    tone={noteHere.ok ? 'ok' : 'bad'}
                    icon={noteHere.ok ? Icon.check() : Icon.alert()}
                    role="status"
                    testid="settings.hooks.test-note"
                  >
                    {[noteHere.text, noteHere.hint].filter(Boolean).join(' ')}
                  </Note>
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
      <div className="sgrp">
        <Heading title={t('Scripts on your machines')} />
        <Note tone="snap" icon={Icon.lock()}>
          {fill(
            t(
              'Declared in {hooks} in each machine’s agent configuration. Hoplet lists them and switches them on and off; it never adds, edits or points one. A change to the list takes an agent restart.',
            ),
            { hooks: mono('hooks') },
          )}
        </Note>
        {machines.length === 0 ? (
          <div style={{ marginTop: 'var(--space-3)' }}>
            <Note tone="snap" icon={Icon.info()}>
              {t('No machine declares a script hook.')}
            </Note>
          </div>
        ) : null}
        {machines.map((m) => {
          const device = devices.find((d) => d.deviceId === m.id);
          return (
            <div
              key={m.id}
              className="mp-list"
              style={{ marginTop: 'var(--space-3)' }}
              data-testid={`settings.hooks.machine.${m.id}`}
            >
              <div className="hm">
                <MachineIcon kind={systemOf(device?.system?.os)} online={m.online} />
                <span>{m.name}</span>
                <span className="meta">
                  {m.online ? t('online') : t('offline · events for it are dropped')}
                </span>
              </div>
              {m.scripts.map((s) => (
                <ScriptRow
                  key={s.id}
                  hook={s}
                  open={open === s.id}
                  busy={state.saving}
                  fixture={fixture?.deliveries}
                  version={version}
                  note={note && note.id === s.id ? note : null}
                  onOpen={() => openRow(s.id)}
                  onToggle={(on) => void toggle(s, on)}
                  onTest={() => void runTest(s)}
                />
              ))}
            </div>
          );
        })}
      </div>
      {confirming ? (
        <ConfirmRotate
          hook={confirming}
          busy={state.saving}
          onCancel={() => setConfirm(null)}
          onRotate={() => void rotate(confirming)}
        />
      ) : null}
      {secretDialog ? (
        <SecretDialog view={secretDialog} onDone={() => setSecret(null)} onCopied={copied} />
      ) : null}
      {toast ? <Toast {...(toast.failed ? { failed: true } : {})}>{toast.text}</Toast> : null}
    </div>
  );
}
