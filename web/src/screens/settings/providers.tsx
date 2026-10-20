import type {
  ConfigOption,
  ConfigSetting,
  DeviceInfo,
  LiveSessionMeta,
  ProviderInfo,
} from '@hoplet/shared';
import { type ReactNode, useId, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { ProviderMark, providerName } from '../../components/provider-marks.tsx';
import { Select } from '../../components/Select.tsx';
import { Switch } from '../../components/Switch.tsx';
import { TextField } from '../../components/TextField.tsx';
import { postDeviceProviders } from '../../lib/api.ts';
import { copyText } from '../../lib/clipboard.ts';
import { optionName, valueName } from '../../lib/config-options.ts';
import {
  addCommand,
  defaultsToSave,
  editableOptions,
  effectiveDefault,
  envCommand,
  groupByVendor,
  hasOwnLogin,
  isBuiltIn,
  missingVariables,
  notReady,
  PRESETS,
  runsCommand,
  SECRET_VARIABLE,
} from '../../lib/configurations.ts';
import { configurationErrorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { useDevices } from '../../lib/use-devices.ts';
import { lastSeenText } from './last-seen.ts';
import { MachineTabs, machinePanelId, machineTabId } from './machine-tabs.tsx';
import { defaultMachine, providerUse, versionBadge } from './providers-model.ts';

/** One configuration being edited: what the form holds and how the save went. */
export type ProviderEdit = {
  /** `<deviceId>/<configuration id>`, the key of the open row. */
  key: string;
  name: string;
  enabled: boolean;
  values: Record<string, ConfigSetting>;
  status?: 'idle' | 'saving' | 'error';
  error?: string;
};

/** What a state of the design starts from: the machines, the sessions they hold, the tab and the open rows. */
export type ProvidersFixture = {
  devices: DeviceInfo[];
  sessions: LiveSessionMeta[];
  selected?: string;
  open?: string[];
  edit?: ProviderEdit;
  /** The Add a configuration panel is open with this preset picked. */
  add?: string;
  now?: number;
};

const keyOf = (deviceId: string, id: string) => `${deviceId}/${id}`;

/** A sentence with nodes where it names their `{slots}`. */
function withSlots(text: string, slots: Record<string, ReactNode>): ReactNode[] {
  const out: ReactNode[] = [];
  for (const part of text.split(/(\{\w+\})/)) {
    if (part === '') continue;
    const slot = part.startsWith('{') ? slots[part.slice(1, -1)] : undefined;
    out.push(slot === undefined ? part : <span key={part}>{slot}</span>);
  }
  return out;
}

function settingName(o: ConfigOption, value: ConfigSetting): string {
  if (o.type === 'boolean') return value ? t('On') : t('Off');
  if (o.type !== 'select') return String(value);
  const v = o.options.find((c) => c.value === value);
  return v ? valueName(o, v.value, v.name) : String(value);
}

/** A command to run in a terminal on the machine, with a button that copies it. */
function CommandLine(props: { command: string; testid?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="cf-cmd" data-testid={props.testid}>
      <code>
        <span className="cf-pr" aria-hidden="true">
          $
        </span>
        {props.command}
      </code>
      <Button
        variant="quiet"
        icon={copied ? Icon.check() : Icon.copy()}
        aria-label={t('Copy command')}
        onClick={() => {
          void copyText(props.command).then((ok) => {
            if (!ok) return;
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          });
        }}
      >
        {copied ? t('Copied') : t('Copy')}
      </Button>
    </div>
  );
}

function Chip(props: { tone?: 'ok' | 'warn'; icon?: ReactNode; title?: string; children: string }) {
  return (
    <span className={`cf-chip${props.tone ? ` ${props.tone}` : ''}`} title={props.title}>
      {props.icon}
      {props.children}
    </span>
  );
}

function Variables(props: { info: ProviderInfo }) {
  const env = props.info.configuration.env;
  if (env.length === 0) return <span className="set-meta">{t('None')}</span>;
  return (
    <ul className="cf-vars">
      {env.map((e) => (
        <li key={e.name}>
          <span className="mono cf-var-n">{e.name}</span>
          {e.set ? (
            <Chip tone="ok" icon={Icon.check()}>
              {t('Set')}
            </Chip>
          ) : (
            <Chip tone="warn" icon={Icon.alert()}>
              {t('Not set')}
            </Chip>
          )}
        </li>
      ))}
    </ul>
  );
}

function RunsLine(props: { info: ProviderInfo }) {
  const command = runsCommand(props.info);
  return command ? (
    <span className="mono">{command}</span>
  ) : (
    t('default {vendor}').replace('{vendor}', props.info.configuration.extends)
  );
}

/** A configuration whose variable has no value cannot start a session: say so, with the command. */
function NotReadyWarning(props: { info: ProviderInfo; machine: string }) {
  const { info } = props;
  const missing = missingVariables(info)[0];
  if (!notReady(info) || missing === undefined) return null;
  return (
    <div className="cf-warn" data-testid="settings.providers.not-ready">
      <span className="cf-warn-i">{Icon.alert()}</span>
      <div className="bt">
        <span>
          <b className="cf-warn-t">
            {t('{name} cannot start until {variable} is set.')
              .replace('{name}', info.configuration.name)
              .replace('{variable}', missing)}
          </b>{' '}
          {t('Enter it on {machine}; the key stays there.').replace('{machine}', props.machine)}
        </span>
        <CommandLine command={envCommand(info.configuration.id, missing)} />
      </div>
    </div>
  );
}

function Detail(props: {
  info: ProviderInfo;
  machine: string;
  sessions: number;
  lastUsed: number | undefined;
  now: number;
  onEdit: () => void;
}) {
  const { info } = props;
  const c = info.configuration;
  const builtIn = isBuiltIn(info);
  const badge = versionBadge(info);
  const used =
    props.sessions > 0
      ? [
          t(props.sessions === 1 ? '{n} session' : '{n} sessions').replace(
            '{n}',
            `${props.sessions}`,
          ),
          props.lastUsed !== undefined
            ? t('used {time}').replace(
                '{time}',
                lastSeenText({ connected: false, lastSeen: props.lastUsed }, props.now),
              )
            : undefined,
        ]
          .filter(Boolean)
          .join(' · ')
      : t('None');
  return (
    <>
      <NotReadyWarning info={info} machine={props.machine} />
      <dl className="cf-dl">
        {builtIn ? (
          <>
            <dt>{t('Version')}</dt>
            <dd>
              {info.version ?? t('Unknown')}
              {badge === 'tested' ? (
                <>
                  {' · '}
                  <Chip tone="ok" icon={Icon.check()}>
                    {t('Tested')}
                  </Chip>
                </>
              ) : badge === 'newer' ? (
                <>
                  {' · '}
                  <Chip
                    tone="warn"
                    icon={Icon.alert()}
                    title={t('Newer than the versions the driver was tested against')}
                  >
                    {t('Newer than tested')}
                  </Chip>
                </>
              ) : null}
            </dd>
            <dt>{t('Sessions')}</dt>
            <dd>{used}</dd>
          </>
        ) : (
          <>
            <dt>{t('Command')}</dt>
            <dd>
              <RunsLine info={info} />
            </dd>
            <dt>{t('Variables')}</dt>
            <dd>
              <Variables info={info} />
            </dd>
          </>
        )}
        <dt>{t('New sessions')}</dt>
        <dd>
          <dl className="mp-defs">
            {editableOptions(info).map((o) => (
              <div key={o.id}>
                <dt>{optionName(o)}</dt>
                <dd>{settingName(o, effectiveDefault(info, o))}</dd>
              </div>
            ))}
          </dl>
        </dd>
      </dl>
      <div className="cf-foot">
        <p className="cf-hint">
          {builtIn
            ? t('Built-in: it can be turned off, not removed.')
            : withSlots(t('The command and the variables change on {machine}: {set} and {env}.'), {
                machine: props.machine,
                set: <span className="mono cf-hint-m">{`hoplet-agent provider set ${c.id}`}</span>,
                env: <span className="mono cf-hint-m">{`provider env ${c.id} NAME`}</span>,
              })}
        </p>
        <Button icon={Icon.pen()} onClick={props.onEdit} data-testid="settings.providers.edit">
          {t('Edit')}
        </Button>
      </div>
    </>
  );
}

function Form(props: {
  info: ProviderInfo;
  machine: string;
  edit: ProviderEdit;
  onChange: (edit: ProviderEdit) => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const { info, edit } = props;
  const busy = edit.status === 'saving';
  const bad = edit.status === 'error';
  const invalid = edit.name.trim() === '';
  return (
    <form
      className={`cf-form${busy ? ' busy' : ''}`}
      onSubmit={(e) => {
        e.preventDefault();
        if (!busy && !invalid) props.onSave();
      }}
      data-testid="settings.providers.form"
    >
      <TextField
        label={t('Name')}
        showLabel
        value={edit.name}
        maxLength={64}
        disabled={busy}
        aria-invalid={bad || invalid || undefined}
        className={bad || invalid ? 'bad' : undefined}
        onChange={(e) => props.onChange({ ...edit, name: e.target.value })}
        data-testid="settings.providers.name"
      />
      <div className="cf-en">
        <span className="cf-en-t">
          <span>{t('Enabled')}</span>
          <span className="cf-hint">{t('New sessions and handoffs can use it.')}</span>
        </span>
        <Switch
          label={t('Enabled')}
          hideLabel
          checked={edit.enabled}
          disabled={busy}
          onChange={(enabled) => props.onChange({ ...edit, enabled })}
          data-testid="settings.providers.enabled"
        />
      </div>
      <div className="field-group">
        <span className="field-label">{t('New sessions start with')}</span>
        <div className="cf-g2">
          {editableOptions(info).map((o) => {
            const value = edit.values[o.id] ?? effectiveDefault(info, o);
            return (
              <div key={o.id} className="field-group">
                <span className="cf-hint">{optionName(o)}</span>
                <Select
                  label={optionName(o)}
                  value={String(value)}
                  disabled={busy}
                  options={
                    o.type === 'boolean'
                      ? [
                          { value: 'true', label: t('On') },
                          { value: 'false', label: t('Off') },
                        ]
                      : o.type === 'select'
                        ? o.options.map((v) => ({
                            value: v.value,
                            label: valueName(o, v.value, v.name),
                          }))
                        : []
                  }
                  onChange={(v) =>
                    props.onChange({
                      ...edit,
                      values: {
                        ...edit.values,
                        [o.id]: o.type === 'boolean' ? v === 'true' : v,
                      },
                    })
                  }
                  data-testid={`settings.providers.default.${o.id}`}
                />
              </div>
            );
          })}
        </div>
      </div>
      <p className="cf-hint">{t('The command and the variables are not editable here.')}</p>
      {bad && edit.error ? (
        <div className="cf-err" role="alert" data-testid="settings.providers.error">
          <span className="cf-err-i">{Icon.alert()}</span>
          <span>{edit.error}</span>
        </div>
      ) : null}
      <div className="cf-act">
        <Button variant="quiet" disabled={busy} onClick={props.onCancel}>
          {t('Cancel')}
        </Button>
        <Button
          variant="primary"
          type="submit"
          disabled={busy || invalid}
          icon={busy ? <Spinner /> : undefined}
          data-testid="settings.providers.save"
        >
          {busy ? t('Saving…') : t('Save')}
        </Button>
      </div>
    </form>
  );
}

function ConfigurationRow(props: {
  info: ProviderInfo;
  machine: string;
  sessions: number;
  lastUsed: number | undefined;
  now: number;
  open: boolean;
  pending: boolean;
  edit: ProviderEdit | undefined;
  onToggle: () => void;
  onEnabled: (enabled: boolean) => void;
  onEdit: () => void;
  onEditChange: (edit: ProviderEdit) => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const { info, open } = props;
  const c = info.configuration;
  const id = useId();
  const missing = notReady(info);
  return (
    <div
      className={`mp-row${c.enabled ? '' : ' cf-off'}${open ? ' open' : ''}`}
      data-testid={`settings.providers.${c.id}`}
    >
      <div className="mp-rh">
        <button
          type="button"
          className="cf-main"
          aria-expanded={open}
          aria-controls={`${id}-body`}
          onClick={props.onToggle}
        >
          <ProviderMark provider={info.provider} />
          <span className="mp-tx">
            <span className="mp-nm">{c.name}</span>
            <span className="cf-sub">
              <span className="cf-id">{c.id}</span>
              <span className="set-meta">
                · <RunsLine info={info} />
              </span>
            </span>
          </span>
          {isBuiltIn(info) ? <Chip>{t('Built-in')}</Chip> : null}
          {hasOwnLogin(info) ? (
            <Chip
              icon={Icon.lock()}
              title={t(
                'It signs in separately from the vendor’s own tool: its config directory is set',
              )}
            >
              {t('Own login')}
            </Chip>
          ) : null}
          {missing ? (
            <Chip tone="warn" icon={Icon.alert()}>
              {t('Not ready')}
            </Chip>
          ) : null}
        </button>
        <Switch
          label={t('{name} enabled').replace('{name}', c.name)}
          hideLabel
          checked={c.enabled}
          disabled={props.pending}
          onChange={props.onEnabled}
          data-testid={`settings.providers.${c.id}.enabled`}
        />
        <span className="chev" aria-hidden="true">
          {open ? Icon.down() : Icon.chevron()}
        </span>
      </div>
      {open ? (
        <div className="mp-body" id={`${id}-body`}>
          {props.edit ? (
            <Form
              info={info}
              machine={props.machine}
              edit={props.edit}
              onChange={props.onEditChange}
              onSave={props.onSave}
              onCancel={props.onCancel}
            />
          ) : (
            <Detail
              info={info}
              machine={props.machine}
              sessions={props.sessions}
              lastUsed={props.lastUsed}
              now={props.now}
              onEdit={props.onEdit}
            />
          )}
        </div>
      ) : null}
    </div>
  );
}

/** Add a configuration: it is made on the machine, where its key stays; the page shows the commands and runs none. */
function AddPanel(props: {
  machine: string;
  preset: string;
  onPreset: (id: string) => void;
  onClose: () => void;
}) {
  const picked = PRESETS.find((p) => p.id === props.preset) ?? PRESETS[0];
  if (!picked) return null;
  return (
    <section
      className="cf-add"
      aria-label={t('Add a configuration')}
      data-testid="settings.providers.add"
    >
      <h2>{t('Add a configuration')}</h2>
      <p className="cf-hint">
        {t(
          'A configuration is made on {machine}, where its key stays. Pick a preset and run both commands in a terminal there.',
        ).replace('{machine}', props.machine)}
      </p>
      <fieldset className="cf-pre" aria-label={t('Preset')}>
        {PRESETS.map((p) => (
          <button
            key={p.id}
            type="button"
            aria-pressed={p.id === picked.id}
            className={p.id === picked.id ? 'on' : ''}
            onClick={() => props.onPreset(p.id)}
          >
            {p.name}
          </button>
        ))}
      </fieldset>
      <div className="cf-step">
        <span className="l">
          <b>1</b> · {t('Create {preset}').replace('{preset}', picked.name)}
        </span>
        <CommandLine command={addCommand(picked.id)} testid="settings.providers.add-command" />
      </div>
      <div className="cf-step">
        <span className="l">
          <b>2</b> · {t('Set its key; the command asks for the value and stores it on the machine')}
        </span>
        <CommandLine
          command={envCommand(picked.id, SECRET_VARIABLE)}
          testid="settings.providers.env-command"
        />
      </div>
      <div className="cf-foot">
        <p className="cf-hint">
          {withSlots(
            t(
              'The configuration appears here when the agent reports it. For your own command use {flag}.',
            ),
            { flag: <span className="mono cf-hint-m">--command</span> },
          )}
        </p>
        <Button variant="quiet" icon={Icon.close()} onClick={props.onClose}>
          {t('Close')}
        </Button>
      </div>
    </section>
  );
}

/**
 * Settings > Model providers: the provider configurations of one machine, a tab per machine, grouped by vendor.
 * The web changes a configuration's name, whether it is enabled and its new-session defaults; the command and the
 * variables are read-only (a variable as set or not set, never its value) and a new configuration is made on the
 * machine (ADR 0028).
 */
export function ProvidersSection(props: {
  fixture?: ProvidersFixture;
  sessions: readonly LiveSessionMeta[];
  onMachines: () => void;
}) {
  const { fixture } = props;
  const polled = useDevices(fixture?.devices);
  const sessions = fixture?.sessions ?? props.sessions;
  const now = fixture?.now ?? Date.now();
  const [picked, setPicked] = useState<string | undefined>(fixture?.selected);
  const [opened, setOpened] = useState<Set<string>>(new Set(fixture?.open));
  const [edit, setEdit] = useState<ProviderEdit | undefined>(fixture?.edit);
  const [adding, setAdding] = useState<string | undefined>(fixture?.add);
  const [pending, setPending] = useState<string | undefined>();
  const [notice, setNotice] = useState<string | undefined>();
  // The answer of a save, shown until the next poll brings the same change.
  const [saved, setSaved] = useState<Record<string, { base: string; providers: ProviderInfo[] }>>(
    {},
  );
  const id = useId();
  const devices = polled.map((d) => {
    const s = saved[d.deviceId];
    return s && s.base === JSON.stringify(d.providers) ? { ...d, providers: s.providers } : d;
  });
  const device =
    devices.find((d) => d.deviceId === picked) ??
    devices.find((d) => d.deviceId === defaultMachine(devices));

  const toggle = (key: string) =>
    setOpened((cur) => {
      const next = new Set(cur);
      if (!next.delete(key)) next.add(key);
      return next;
    });

  /** Sends one change; resolves true when the machine took it. */
  async function send(
    target: DeviceInfo,
    body: Parameters<typeof postDeviceProviders>[1],
  ): Promise<string | undefined> {
    if (fixture) return undefined;
    try {
      const answer = await postDeviceProviders(target.deviceId, body);
      const base = JSON.stringify(polled.find((d) => d.deviceId === target.deviceId)?.providers);
      setSaved((cur) => ({
        ...cur,
        [target.deviceId]: { base, providers: answer.providers },
      }));
      return undefined;
    } catch (err) {
      return configurationErrorText(err, target.name, body.id);
    }
  }

  async function setEnabled(target: DeviceInfo, configuration: string, enabled: boolean) {
    const key = keyOf(target.deviceId, configuration);
    setPending(key);
    setNotice(undefined);
    const failure = await send(target, { id: configuration, enabled });
    setPending(undefined);
    if (failure) setNotice(failure);
  }

  async function save(target: DeviceInfo, info: ProviderInfo, form: ProviderEdit) {
    const c = info.configuration;
    const defaults = defaultsToSave(info, form.values);
    const name = form.name.trim();
    const body: Parameters<typeof postDeviceProviders>[1] = {
      id: c.id,
      ...(name !== c.name ? { name } : {}),
      ...(form.enabled !== c.enabled ? { enabled: form.enabled } : {}),
      ...(JSON.stringify(sortKeys(defaults)) !== JSON.stringify(sortKeys(c.defaults))
        ? { defaults }
        : {}),
    };
    if (Object.keys(body).length === 1) {
      setEdit(undefined);
      return;
    }
    setEdit({ ...form, status: 'saving' });
    const failure = await send(target, body);
    if (failure) setEdit({ ...form, status: 'error', error: failure });
    else setEdit(undefined);
  }

  return (
    <>
      <h1>{t('Model providers')}</h1>
      <p className="mp-note">
        <span className="mp-note-i">{Icon.lock()}</span>
        <span>
          {t(
            'Keys are entered on the machine, never here. Hoplet shows which variables are set, not their values.',
          )}
        </span>
      </p>
      {device === undefined ? (
        <div className="mp-empty" data-testid="settings.providers.empty">
          <b className="mp-empty-t">{t('No machines yet')}</b>
          <p>
            {t(
              'Providers come from the agent on each machine. Go to Machines → Add a machine, and the providers it runs appear here.',
            )}
          </p>
          <Button icon={Icon.laptop()} onClick={props.onMachines}>
            {t('Go to Machines')}
          </Button>
        </div>
      ) : (
        <>
          <MachineTabs
            devices={devices}
            selected={device.deviceId}
            idPrefix={id}
            onSelect={(deviceId) => {
              setPicked(deviceId);
              setEdit(undefined);
              setNotice(undefined);
            }}
            testid="settings.providers.tabs"
          />
          <div
            role="tabpanel"
            id={machinePanelId(id)}
            aria-labelledby={machineTabId(id, device.deviceId)}
            data-testid="settings.providers.panel"
          >
            {!device.connected ? (
              <div className="mp-quiet" data-testid="settings.providers.offline">
                {t(
                  '{name} is offline · last seen {when}. Its configurations show when its agent connects.',
                )
                  .replace('{name}', device.name)
                  .replace('{when}', lastSeenText(device, now))}
              </div>
            ) : device.providers.length === 0 ? (
              <div className="mp-quiet" data-testid="settings.providers.none">
                {t('This machine’s agent announces no providers.')}
              </div>
            ) : (
              <div className="cf-a" data-testid="settings.providers.list">
                <div className="cf-top">
                  <span className="set-meta cf-top-m">
                    {t('Configurations on {name}').replace('{name}', device.name)}
                  </span>
                  <Button
                    icon={Icon.plus()}
                    aria-expanded={adding !== undefined}
                    onClick={() =>
                      setAdding((cur) => (cur === undefined ? PRESETS[0]?.id : undefined))
                    }
                    data-testid="settings.providers.add-button"
                  >
                    {t('Add a configuration')}
                  </Button>
                </div>
                {notice ? (
                  <div
                    className="cf-err cf-notice"
                    role="alert"
                    data-testid="settings.providers.notice"
                  >
                    <span className="cf-err-i">{Icon.alert()}</span>
                    <span>{notice}</span>
                  </div>
                ) : null}
                {adding !== undefined ? (
                  <div className="cf-add-wrap">
                    <AddPanel
                      machine={device.name}
                      preset={adding}
                      onPreset={setAdding}
                      onClose={() => setAdding(undefined)}
                    />
                  </div>
                ) : null}
                {groupByVendor(device.providers).map((group) => (
                  <div key={group.provider} className="cf-grp">
                    <div className="cf-gh">
                      <ProviderMark provider={group.provider} />
                      {providerName(group.provider)}
                      <span className="set-meta cf-gh-m">
                        {t(
                          group.items.length === 1 ? '{n} configuration' : '{n} configurations',
                        ).replace('{n}', `${group.items.length}`)}
                      </span>
                    </div>
                    <div className="mp-list">
                      {group.items.map((info) => {
                        const key = keyOf(device.deviceId, info.configuration.id);
                        const use = providerUse(
                          sessions,
                          device,
                          info.provider,
                          info.configuration.id,
                        );
                        const editing = edit?.key === key ? edit : undefined;
                        return (
                          <ConfigurationRow
                            key={key}
                            info={info}
                            machine={device.name}
                            sessions={use.sessions}
                            lastUsed={use.lastUsed}
                            now={now}
                            open={opened.has(key) || editing !== undefined}
                            pending={pending === key}
                            edit={editing}
                            onToggle={() => {
                              if (editing) return;
                              toggle(key);
                            }}
                            onEnabled={(enabled) =>
                              void setEnabled(device, info.configuration.id, enabled)
                            }
                            onEdit={() =>
                              setEdit({
                                key,
                                name: info.configuration.name,
                                enabled: info.configuration.enabled,
                                values: {},
                                status: 'idle',
                              })
                            }
                            onEditChange={setEdit}
                            onSave={() => editing && void save(device, info, editing)}
                            onCancel={() => setEdit(undefined)}
                          />
                        );
                      })}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </>
  );
}

function sortKeys(config: Record<string, ConfigSetting>): [string, ConfigSetting][] {
  return Object.entries(config).sort(([a], [b]) => a.localeCompare(b));
}
