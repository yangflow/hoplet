import type { DeviceInfo, LiveSessionMeta, SessionMcpSettings } from '@hoplet/shared';
import { Icon, Spinner } from '../../components/icons.tsx';
import { Caps, LimitsNote, OffBox, Reach } from '../../components/mcp-parts.tsx';
import { t } from '../../lib/i18n.ts';
import { mcpOff } from '../../lib/session-mcp.ts';
import { useDevices } from '../../lib/use-devices.ts';
import { useSessionMcp } from '../../lib/use-session-mcp.ts';
import { Quiet } from './viewer.tsx';

/** Development fixture of the Other sessions tab: the settings, and the machines to name. */
export type SessionsFixture = {
  settings: SessionMcpSettings;
  devices?: DeviceInfo[];
};

/**
 * The Other sessions tab of the inspector (ADR 0034): what this session may do to other sessions. Only the
 * host token can change it, and the web UI always holds it.
 */
export function SessionsTab(props: { session: LiveSessionMeta; fixture?: SessionsFixture }) {
  const { session, fixture } = props;
  const devices = useDevices(fixture?.devices);
  const mcp = useSessionMcp(session.sessionId, fixture?.settings);
  const owner = devices.find((d) => d.deviceId === session.ownerDeviceId);
  const machine = session.deviceName || owner?.name || t('the owner');

  if (mcpOff(owner)) {
    return (
      <div className="mc-tab" data-testid="workspace.sessions">
        <OffBox machine={machine} testid="workspace.sessions.off" />
      </div>
    );
  }
  if (mcp.loading || !mcp.data) {
    return mcp.error ? (
      <Quiet
        icon={Icon.alert()}
        title={t('Could not load the settings')}
        text={mcp.error}
        testid="workspace.sessions.error"
      />
    ) : (
      <Quiet icon={<Spinner />} text={t('Loading…')} status testid="workspace.sessions.loading" />
    );
  }
  const s = mcp.data;
  return (
    <div className="mc-tab" data-testid="workspace.sessions">
      <div className="plist">
        <Caps
          settings={s}
          onChange={mcp.change}
          compact
          autoApprove={{ devices }}
          testid="workspace.sessions.caps"
        />
      </div>
      <Reach
        settings={s}
        onChange={mcp.change}
        machine={machine}
        testid="workspace.sessions.reach"
      />
      {s.visibility === 'all' ? <LimitsNote testid="workspace.sessions.limits" /> : null}
      {mcp.problem ? (
        <span className="rmeta" role="alert" data-testid="workspace.sessions.problem">
          {mcp.problem}
        </span>
      ) : null}
      <span className="mc-meta">
        {t('This session only. The defaults are in Settings › Other sessions.')}
      </span>
    </div>
  );
}
