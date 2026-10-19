import type { DeviceInfo, DeviceMintResult } from '@hoplet/shared';
import { type ReactNode, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { MachineIcon } from '../../components/MachineIcon.tsx';
import { TextField } from '../../components/TextField.tsx';
import { postMintDevice } from '../../lib/api.ts';
import { copyText } from '../../lib/clipboard.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { machineLabel, systemName, systemOf } from '../../lib/system.ts';
import { useDevices } from '../../lib/use-devices.ts';
import { lastSeenText } from './last-seen.ts';

/** What a state of the design starts from: the machines, and a machine already minted. */
export type MachinesFixture = {
  devices: DeviceInfo[];
  mint?: { result: DeviceMintResult; copied: boolean };
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

/** A translated sentence with markup in place of its `{placeholders}`. */
function fill(template: string, parts: Record<string, ReactNode>): ReactNode[] {
  return template.split(/(\{\w+\})/).map((piece) => {
    const part = parts[piece];
    return part === undefined ? piece : <span key={piece}>{part}</span>;
  });
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

/** Settings > Machines: every machine with its presence and last connection, and the way to add one. */
export function MachinesSection(props: { fixture?: MachinesFixture }) {
  const { fixture } = props;
  const devices = useDevices(fixture?.devices);
  const [adding, setAdding] = useState(fixture?.mint !== undefined);
  const now = fixture?.now ?? Date.now();
  return (
    <>
      <h1>{t('Machines')}</h1>
      {devices.length > 0 ? (
        <>
          <div className="plh set-meta" aria-hidden="true">
            <span>{t('Machine')}</span>
            <span>{t('Last seen')}</span>
          </div>
          <ul className="plist" aria-label={t('Machines')} data-testid="settings.machines">
            {devices.map((d) => (
              <li
                key={d.deviceId}
                className={`pr${d.connected ? '' : ' off'}`}
                aria-label={machineLabel(d.name, d.system, d.connected)}
              >
                <span className="lead">
                  <MachineIcon kind={systemOf(d.system?.os)} online={d.connected} />
                </span>
                <span className="nm">{d.name}</span>
                <span className="set-meta">{systemName(d.system?.os)}</span>
                <span className="set-meta">{lastSeenText(d, now)}</span>
              </li>
            ))}
          </ul>
        </>
      ) : null}
      {adding ? (
        <MintCard devices={devices} initial={fixture?.mint} onClose={() => setAdding(false)} />
      ) : (
        <div className="sact">
          <Button
            icon={Icon.plus()}
            onClick={() => setAdding(true)}
            data-testid="settings.add-machine"
          >
            {t('Add a machine')}
          </Button>
        </div>
      )}
    </>
  );
}
