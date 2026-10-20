import type { ConfigOption, DeviceInfo, LiveSessionMeta, ProviderInfo } from '@hoplet/shared';
import { useId, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Icon } from '../../components/icons.tsx';
import { ProviderMark, providerName } from '../../components/provider-marks.tsx';
import { currentValueName, optionName } from '../../lib/config-options.ts';
import { t } from '../../lib/i18n.ts';
import { useDevices } from '../../lib/use-devices.ts';
import { lastSeenText } from './last-seen.ts';
import { MachineTabs, machinePanelId, machineTabId } from './machine-tabs.tsx';
import { defaultMachine, providerUse, versionBadge } from './providers-model.ts';

/** What a state of the design starts from: the machines, the sessions they hold, the tab and the open rows. */
export type ProvidersFixture = {
  devices: DeviceInfo[];
  sessions: LiveSessionMeta[];
  selected?: string;
  open?: string[];
  now?: number;
};

const CAPABILITIES: { key: 'permissions' | 'interrupt' | 'images' | 'steer'; label: string }[] = [
  { key: 'permissions', label: 'Permissions' },
  { key: 'interrupt', label: 'Interrupt' },
  { key: 'images', label: 'Images' },
  { key: 'steer', label: 'Steer' },
];

function defaultValue(o: ConfigOption): string {
  if (o.type === 'boolean') return o.currentValue ? t('On') : t('Off');
  return currentValueName(o) ?? o.currentValue;
}

function ProviderRow(props: {
  info: ProviderInfo;
  sessions: number;
  lastUsed: number | undefined;
  now: number;
  open: boolean;
  onToggle: () => void;
}) {
  const { info, open } = props;
  const id = useId();
  const badge = versionBadge(info);
  const meta = [
    info.version,
    props.sessions > 0
      ? t(props.sessions === 1 ? '{n} session' : '{n} sessions').replace('{n}', `${props.sessions}`)
      : undefined,
    props.lastUsed !== undefined
      ? t('used {time}').replace(
          '{time}',
          lastSeenText({ connected: false, lastSeen: props.lastUsed }, props.now),
        )
      : undefined,
  ].filter((s): s is string => Boolean(s));
  const defaults = info.capabilities.config.filter(
    (o) => o.type === 'select' || o.type === 'boolean',
  );
  const caps = CAPABILITIES.filter((c) => info.capabilities[c.key] === true);
  return (
    <div className="mp-row" data-testid={`settings.providers.${info.provider}`}>
      <button
        type="button"
        className="mp-rh"
        aria-expanded={open}
        aria-controls={`${id}-body`}
        onClick={props.onToggle}
      >
        <ProviderMark provider={info.provider} />
        <span className="mp-tx">
          <span className="mp-nm">{providerName(info.provider)}</span>
          {meta.length > 0 ? <span className="set-meta">{meta.join(' · ')}</span> : null}
        </span>
        {badge === 'tested' ? (
          <span className="mp-st">
            <span className="mp-st-i">{Icon.check()}</span>
            {t('Tested')}
          </span>
        ) : badge === 'newer' ? (
          <span
            className="mp-st new"
            title={t('Newer than the versions the driver was tested against')}
          >
            <span className="mp-st-i">{Icon.alert()}</span>
            {t('Newer than tested')}
          </span>
        ) : null}
        <span className="chev" aria-hidden="true">
          {open ? Icon.down() : Icon.chevron()}
        </span>
      </button>
      {open ? (
        <div className="mp-body" id={`${id}-body`}>
          {defaults.length > 0 ? (
            <dl className="mp-defs">
              {defaults.map((o) => (
                <div key={o.id}>
                  <dt>{optionName(o)}</dt>
                  <dd>{defaultValue(o)}</dd>
                </div>
              ))}
            </dl>
          ) : null}
          {caps.length > 0 ? (
            <div className="mp-caps">
              {caps.map((c) => (
                <span key={c.key} className="mp-cap">
                  {t(c.label)}
                </span>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/** Settings > Model providers: read-only, what each connected agent announces, a tab per machine. */
export function ProvidersSection(props: {
  fixture?: ProvidersFixture;
  sessions: readonly LiveSessionMeta[];
  onMachines: () => void;
}) {
  const { fixture } = props;
  const devices = useDevices(fixture?.devices);
  const sessions = fixture?.sessions ?? props.sessions;
  const now = fixture?.now ?? Date.now();
  const [picked, setPicked] = useState<string | undefined>(fixture?.selected);
  const [opened, setOpened] = useState<Set<string>>(new Set(fixture?.open));
  const id = useId();
  const device =
    devices.find((d) => d.deviceId === picked) ??
    devices.find((d) => d.deviceId === defaultMachine(devices));

  const toggle = (key: string) =>
    setOpened((cur) => {
      const next = new Set(cur);
      if (!next.delete(key)) next.add(key);
      return next;
    });

  return (
    <>
      <h1>{t('Model providers')}</h1>
      <p className="mp-note">
        <span className="mp-note-i">{Icon.lock()}</span>
        <span>
          {t(
            'Hoplet never asks for an API key. Each provider signs in with its own command-line tool on the machine.',
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
            onSelect={setPicked}
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
                  '{name} is offline · last seen {when}. Its providers show when its agent connects.',
                )
                  .replace('{name}', device.name)
                  .replace('{when}', lastSeenText(device, now))}
              </div>
            ) : device.providers.length === 0 ? (
              <div className="mp-quiet" data-testid="settings.providers.none">
                {t('This machine’s agent announces no providers.')}
              </div>
            ) : (
              <div className="mp-list" data-testid="settings.providers.list">
                {device.providers.map((info) => {
                  const use = providerUse(sessions, device, info.provider);
                  const key = `${device.deviceId}/${info.configuration.id}`;
                  return (
                    <ProviderRow
                      key={key}
                      info={info}
                      sessions={use.sessions}
                      lastUsed={use.lastUsed}
                      now={now}
                      open={opened.has(key)}
                      onToggle={() => toggle(key)}
                    />
                  );
                })}
              </div>
            )}
          </div>
        </>
      )}
    </>
  );
}
