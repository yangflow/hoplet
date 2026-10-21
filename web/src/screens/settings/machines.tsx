import {
  DEFAULT_RESOURCE_THRESHOLDS,
  type DeviceInfo,
  type DeviceMintResult,
  type LiveSessionMeta,
  type ResourceThresholds,
  type SessionRuntimeInfo,
} from '@hoplet/shared';
import { type ReactNode, useEffect, useId, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { TextField } from '../../components/TextField.tsx';
import { postMintDevice } from '../../lib/api.ts';
import { copyText } from '../../lib/clipboard.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { systemName } from '../../lib/system.ts';
import { type DeviceResources, useDeviceResources } from '../../lib/use-device-resources.ts';
import { useDevices } from '../../lib/use-devices.ts';
import { useResourceThresholds } from '../../lib/use-resource-thresholds.ts';
import { fill } from './fill.tsx';
import { lastSeenText } from './last-seen.ts';
import { CapabilitiesGroup } from './machine-capabilities.tsx';
import { ResourcesBody } from './machine-resources.tsx';
import { MachineTabs, machinePanelId, machineTabId } from './machine-tabs.tsx';
import { ThresholdsGroup } from './machine-thresholds.tsx';
import { defaultMachine } from './providers-model.ts';
import { archName } from './resources-model.ts';
import { effectiveThresholds } from './thresholds-model.ts';

/** What a state of the design starts from: the machines, and a machine already minted. */
export type MachinesFixture = {
  devices: DeviceInfo[];
  mint?: { result: DeviceMintResult; copied: boolean };
  /** The tab selected, by device id. */
  selected?: string;
  /** What the resources endpoint answered for the selected machine, each session with its last hour. */
  resources?: DeviceResources;
  sessions?: LiveSessionMeta[];
  runtime?: Record<string, SessionRuntimeInfo>;
  /** A session row drawn as hovered, for the design's states. */
  hovered?: string;
  /** The warning thresholds; the defaults when left out. */
  thresholds?: ResourceThresholds;
  /** The default's fields open, as after Change default. */
  editDefault?: boolean;
  /** The label field of the selected machine open with this text. */
  addingLabel?: string;
  /** A label of the selected machine shown as just removed, with Undo. */
  removedLabel?: string;
  now?: number;
};

/** The config.json the agent reads: the server, and the identity the core minted. */
export function agentConfig(minted: DeviceMintResult, server = location.origin): string {
  return JSON.stringify(
    {
      server_url: server,
      token: minted.token,
      device_id: minted.deviceId,
      device_name: minted.name,
    },
    null,
    2,
  );
}

/** The same config as lines of a well, the token masked: the real one is only ever copied. */
function maskedLines(minted: DeviceMintResult, server: string): ReactNode[] {
  const rows: [string, string][] = [
    ['server_url', server],
    ['token', '••••••••••••••••••••••••'],
    ['device_id', minted.deviceId],
    ['device_name', minted.name],
  ];
  return [
    <div key="open" className="wl dim">
      <span className="c">{'{'}</span>
    </div>,
    ...rows.map(([k, v], i) => (
      <div key={k} className="wl">
        <span className="c">
          <span className="p">{`  "${k}": `}</span>
          {`"${v}"`}
          <span className="p">{i === rows.length - 1 ? '' : ','}</span>
        </span>
      </div>
    )),
    <div key="close" className="wl dim">
      <span className="c">{'}'}</span>
    </div>,
  ];
}

function Step(props: {
  state: 'ok' | 'todo' | 'go';
  n: number;
  testid?: string;
  children: ReactNode;
}) {
  return (
    <div className={`mstep ${props.state}`} data-testid={props.testid}>
      <span className="n" aria-hidden="true">
        {props.state === 'ok' ? Icon.check() : props.state === 'go' ? <Spinner /> : props.n}
      </span>
      {props.children}
    </div>
  );
}

function MintCard(props: {
  devices: DeviceInfo[];
  initial: MachinesFixture['mint'] | undefined;
  onClose: () => void;
}) {
  const [name, setName] = useState(props.initial?.result.name ?? '');
  const [minted, setMinted] = useState<DeviceMintResult | null>(props.initial?.result ?? null);
  const [copied, setCopied] = useState(props.initial?.copied ?? false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const online =
    minted !== null && props.devices.some((d) => d.deviceId === minted.deviceId && d.connected);
  const server = typeof location === 'undefined' ? '' : location.origin;

  const mint = async () => {
    const trimmed = name.trim();
    if (!trimmed || busy) return;
    setBusy(true);
    setError(null);
    try {
      // Minting for a machine that is already minted replaces its token.
      setMinted(await postMintDevice(trimmed, minted?.deviceId));
      setCopied(false);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    if (!minted) return;
    setError(null);
    if (await copyText(agentConfig(minted))) setCopied(true);
    else setError(t('Could not copy to the clipboard'));
  };

  return (
    <section className="mcard" aria-label={t('Add a machine')} data-testid="settings.mint">
      <div className="mcard-h">
        <b>{t('Add a machine')}</b>
        <IconButton label={t('Close')} icon={Icon.close()} onClick={props.onClose} />
      </div>
      <div className="mrow">
        <TextField
          label={t('Name')}
          showLabel
          value={name}
          placeholder="studio-2"
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void mint();
          }}
          data-testid="settings.mint.name"
        />
        <Button
          onClick={() => void mint()}
          disabled={busy || name.trim() === ''}
          data-testid="settings.mint.go"
        >
          {t('Mint')}
        </Button>
      </div>
      {error ? (
        <p className="mrow-err" role="alert">
          {error}
        </p>
      ) : null}
      {minted ? (
        <div className="ms-list">
          {online ? (
            <Step state="ok" n={1}>
              <span className="tx">
                {t('Token minted')} <span className="sub">· {t('shown once')}</span>
              </span>
            </Step>
          ) : (
            <Step state="ok" n={1}>
              <span className="tx">
                {t('Token minted')} <span className="sub">· {t('shown once')}</span>
              </span>
              <div className="well solo" data-testid="settings.mint.config">
                <div className="wb">{maskedLines(minted, server)}</div>
              </div>
              <div className="cfg-f">
                <Button
                  variant={copied ? 'default' : 'primary'}
                  icon={Icon.copy()}
                  onClick={() => void copy()}
                  data-testid="settings.mint.copy"
                >
                  {t('Copy config.json')}
                </Button>
                <span className="set-meta">{copied ? t('Copied') : t('Includes the token')}</span>
              </div>
            </Step>
          )}
          <Step state={copied ? 'ok' : 'todo'} n={2}>
            <span className="tx">
              {fill(t('Put it in {file} and run {command}'), {
                '{file}': <span className="mono">~/.hoplet/config.json</span>,
                '{command}': <span className="mono">hoplet-agent daemon</span>,
              })}
            </span>
          </Step>
          {online ? (
            <Step state="ok" n={3} testid="settings.mint.online">
              <span className="tx">{t('{name} is connected').replace('{name}', minted.name)}</span>
            </Step>
          ) : copied ? (
            <Step state="go" n={3} testid="settings.mint.waiting">
              <span className="tx" role="status">
                {t('Waiting for {name} to connect…').replace('{name}', minted.name)}
              </span>
              <span className="more set-meta">{t('Minting again replaces its token.')}</span>
            </Step>
          ) : (
            <Step state="todo" n={3}>
              <span className="tx">
                {t('Wait until {name} connects').replace('{name}', minted.name)}
              </span>
            </Step>
          )}
        </div>
      ) : null}
      {online ? (
        <div className="mcard-f">
          <Button variant="primary" onClick={props.onClose} data-testid="settings.mint.done">
            {t('Done')}
          </Button>
        </div>
      ) : null}
    </section>
  );
}

/** The facts line of a machine: system, architecture and presence; an offline machine says when it was last seen. */
function factsLine(device: DeviceInfo, now: number): string {
  const os = systemName(device.system?.os);
  const system = [os, archName(device.system?.arch)].filter((x): x is string => Boolean(x));
  const presence = device.connected ? t('Connected') : t('Offline');
  const parts = [...(system.length > 0 ? [system.join(' · ')] : []), presence];
  if (!device.connected && device.resources === undefined && device.lastSeen !== undefined)
    parts.push(t('last seen {when}').replace('{when}', lastSeenText(device, now)));
  return parts.join(' · ');
}

/** Settings > Machines: a tab per machine with its facts and resources, and the way to add one. */
export function MachinesSection(props: {
  fixture?: MachinesFixture;
  /** The machine the section opens on, from the address. */
  machine?: string | undefined;
  onMachine?: (deviceId: string) => void;
  sessions?: readonly LiveSessionMeta[];
  runtime?: Record<string, SessionRuntimeInfo> | undefined;
  onOpenSession?: (sessionId: string) => void;
}) {
  const { fixture } = props;
  const devices = useDevices(fixture?.devices);
  const [adding, setAdding] = useState(fixture?.mint !== undefined);
  const [picked, setPicked] = useState<string | undefined>(fixture?.selected ?? props.machine);
  useEffect(() => {
    if (props.machine !== undefined) setPicked(props.machine);
  }, [props.machine]);
  const id = useId();
  const now = fixture?.now ?? Date.now();
  const device =
    devices.find((d) => d.deviceId === picked) ??
    devices.find((d) => d.deviceId === defaultMachine(devices));
  const panelShown = device !== undefined && !adding;
  const live = useDeviceResources(
    device?.deviceId,
    panelShown && device.resources !== undefined,
    fixture?.resources,
  );
  const thresholds = useResourceThresholds(
    fixture ? (fixture.thresholds ?? DEFAULT_RESOURCE_THRESHOLDS) : undefined,
  );
  const select = (deviceId: string) => {
    setPicked(deviceId);
    props.onMachine?.(deviceId);
  };
  const sample = device?.resources;
  const capabilities = device ? (
    <CapabilitiesGroup
      key={device.deviceId}
      device={device}
      now={now}
      local={fixture !== undefined}
      adding={fixture?.addingLabel}
      removed={fixture?.removedLabel}
    />
  ) : null;
  return (
    <>
      <h1>{t('Machines')}</h1>
      <div className="mt-bar">
        {device ? (
          <MachineTabs
            devices={devices}
            selected={device.deviceId}
            idPrefix={id}
            panel={!adding}
            onSelect={select}
            testid="settings.machines.tabs"
          />
        ) : (
          <span className="mt-fill" />
        )}
        {adding ? null : (
          <Button
            variant="quiet"
            icon={Icon.plus()}
            onClick={() => setAdding(true)}
            data-testid="settings.add-machine"
          >
            {t('Add a machine')}
          </Button>
        )}
      </div>
      {adding ? (
        <MintCard devices={devices} initial={fixture?.mint} onClose={() => setAdding(false)} />
      ) : device ? (
        <div
          role="tabpanel"
          id={machinePanelId(id)}
          aria-labelledby={machineTabId(id, device.deviceId)}
          data-testid="settings.machines.panel"
        >
          <p className="rmeta mt-facts" data-testid="settings.machines.facts">
            {factsLine(device, now)}
          </p>
          {sample ? (
            <ResourcesBody
              device={device}
              sample={sample}
              result={live}
              sessions={fixture?.sessions ?? props.sessions ?? []}
              runtime={fixture?.runtime ?? props.runtime}
              now={now}
              hovered={fixture?.hovered}
              onOpenSession={props.onOpenSession ?? (() => undefined)}
              thresholds={
                thresholds.thresholds
                  ? effectiveThresholds(thresholds.thresholds, device.deviceId)
                  : null
              }
              warnings={
                <>
                  {capabilities}
                  <ThresholdsGroup
                    key={device.deviceId}
                    device={device}
                    state={thresholds}
                    editDefault={fixture?.editDefault ?? false}
                  />
                </>
              }
            />
          ) : (
            <>
              {device.connected ? (
                <p className="rmeta dim" data-testid="settings.resources.none">
                  {t('This machine’s agent does not report resources.')}
                </p>
              ) : null}
              {capabilities}
            </>
          )}
        </div>
      ) : null}
    </>
  );
}
