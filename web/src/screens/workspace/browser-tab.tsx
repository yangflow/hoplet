import type { SessionPort } from '@hoplet/shared';
import { Button } from '../../components/Button.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { copyText } from '../../lib/clipboard.ts';
import { t } from '../../lib/i18n.ts';
import { CodeText } from './code-text.tsx';
import { type PortsData, usePorts } from './use-ports.ts';
import { Quiet } from './viewer.tsx';

const named = (text: string, owner: string) => t(text).replace(/\{name\}/g, owner);

type Address = SessionPort['address'];

/** The host a link to a port uses: this machine's own name for a loopback port, else the preview host. */
export function addressOf(port: SessionPort, previewHost: string | undefined): string | null {
  const host = port.address === 'loopback' ? 'localhost' : previewHost;
  return host ? `${host}:${port.port}` : null;
}

const classText: Record<Address, string> = {
  loopback: 'loopback',
  any: 'all interfaces',
  other: 'other interface',
};

function PortCard(props: { port: SessionPort; previewHost: string | undefined; owner: string }) {
  const { port, previewHost, owner } = props;
  const address = addressOf(port, previewHost);
  const cls = t(classText[port.address]);
  const label = `${address ?? port.port} ${t('is running')} · ${port.process} · ${cls}`;
  return (
    <li className="ws-card" aria-label={label} data-testid="workspace.port">
      <div className="ws-card-top">
        <span className="ws-live" aria-hidden="true" />
        <span className="ws-port">{port.port}</span>
        <span className="ws-who">{port.process}</span>
        <span className="ws-cls">
          {port.address === 'loopback' ? Icon.lock() : Icon.globe()}
          {cls}
        </span>
      </div>
      <div className="ws-url ws-mono">{address ?? `:${port.port}`}</div>
      {port.address === 'loopback' ? (
        <div className="ws-meta">
          {named('Opens only on {name} or through your own tunnel.', owner)}
        </div>
      ) : null}
      <div className="ws-card-acts">
        {address ? (
          <a
            className="btn btn-default"
            href={`http://${address}`}
            target="_blank"
            rel="noopener noreferrer"
            title={t('Open {addr} in a new tab').replace('{addr}', address)}
            data-testid="workspace.port-open"
          >
            {Icon.external()}
            {t('Open')}
          </a>
        ) : (
          <Button
            icon={Icon.external()}
            disabled
            title={named('{name} has no preview host set, so there is no address to open.', owner)}
          >
            {t('Open')}
          </Button>
        )}
        <Button
          icon={Icon.copy()}
          disabled={!address}
          title={address ? t('Copy {addr}').replace('{addr}', address) : t('Copy host:port')}
          onClick={() => {
            if (address) void copyText(address);
          }}
          data-testid="workspace.port-copy"
        >
          {t('Copy host:port')}
        </Button>
      </div>
    </li>
  );
}

/**
 * The inspector's Browser tab (ADR 0026): a link to each dev server the session runs. Hoplet does not proxy and
 * does not frame them; a link opens in a new tab.
 */
export function BrowserTab(props: {
  sessionId: string;
  online: boolean;
  owner: string;
  /** A development state: nothing is fetched. */
  fixture?: PortsData;
}) {
  const { owner } = props;
  const data = usePorts(props.sessionId, props.online, props.fixture);

  if (!props.online)
    return (
      <Quiet
        icon={Icon.laptop()}
        title={named('{name} is offline', owner)}
        text={t('Ports are listed when it is back. Nothing is shown from before.')}
        testid="workspace.browser-offline"
      />
    );
  if (data.phase === 'loading')
    return <Quiet icon={<Spinner />} text={t('Looking for dev servers…')} status />;
  if (data.phase === 'error')
    return (
      <Quiet
        icon={Icon.alert()}
        title={t('Could not list the dev servers')}
        text={data.text}
        testid="workspace.browser-error"
      />
    );

  const host = data.previewHost;
  const loopbackOnly = data.ports.length > 0 && data.ports.every((p) => p.address === 'loopback');
  return (
    <div className="ws-tool" data-testid="workspace.browser">
      <div className="ws-hostbar">
        {host ? (
          <>
            <div className="ws-hostbar-l">
              {Icon.globe()}
              <span>{t('Links open at')}</span>
              <span className="ws-mono">{host}</span>
            </div>
            <div className="ws-meta">
              <CodeText text={t('from `preview_host`; `localhost` for loopback ports')} />
            </div>
          </>
        ) : (
          <>
            <div className="ws-hostbar-l">
              {Icon.globe()}
              <span>{t('No host for links')}</span>
            </div>
            <div className="ws-meta">
              <CodeText
                text={named('Set `preview_host` in the agent’s `config.json` on {name}.', owner)}
              />
            </div>
          </>
        )}
      </div>
      {data.ports.length === 0 ? (
        <div className="ws-es" data-testid="workspace.browser-empty">
          <span className="ws-tile">{Icon.globe()}</span>
          <b className="ws-es-t">{t('No dev servers yet')}</b>
          <p>
            <CodeText
              text={t(
                'Start one in the session or in a terminal, for example `pnpm dev`. Hoplet looks every 3 seconds.',
              )}
            />
          </p>
        </div>
      ) : (
        <div className="ws-ports">
          <ul className="ws-portlist" aria-label={t('Dev servers')}>
            {data.ports.map((port) => (
              <PortCard key={port.port} port={port} previewHost={host} owner={owner} />
            ))}
          </ul>
          {loopbackOnly ? (
            <p className="ws-snap ws-bnote" role="note" data-testid="workspace.browser-loopback">
              {Icon.info()}
              <span>
                <CodeText
                  text={named(
                    '`localhost` opens only on {name} or through your own tunnel.',
                    owner,
                  )}
                />
              </span>
            </p>
          ) : null}
        </div>
      )}
    </div>
  );
}
