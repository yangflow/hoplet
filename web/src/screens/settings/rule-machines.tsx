import type { PermissionRuleMachine, PermissionRuleMachinesResponse } from '@hoplet/shared';
import { Button } from '../../components/Button.tsx';
import { Icon } from '../../components/icons.tsx';
import { t } from '../../lib/i18n.ts';
import { useRuleMachines } from '../../lib/use-rule-machines.ts';
import { agoText } from './last-seen.ts';

type Status = 'ok' | 'behind' | 'off';

/** A machine that confirmed the latest revision is applied; otherwise it is offline or behind. */
export function machineStatus(m: PermissionRuleMachine, latest: number): Status {
  if (m.applied !== null && m.applied >= latest) return 'ok';
  return m.online ? 'behind' : 'off';
}

function label(status: Status): string {
  return status === 'ok' ? t('Applied') : status === 'behind' ? t('Behind') : t('Offline');
}

/** The sentence after the status word: when it was confirmed, pushed or last seen. */
export function machineDetail(m: PermissionRuleMachine, status: Status, now: number): string {
  if (status === 'ok') {
    return m.appliedAt === null
      ? t('Confirmed')
      : t('Confirmed {ago}').replace('{ago}', agoText(m.appliedAt, now));
  }
  if (status === 'off') {
    return m.lastSeen === undefined
      ? t('Applies when it reconnects.')
      : t('Last seen {ago}. Applies when it reconnects.').replace(
          '{ago}',
          agoText(m.lastSeen, now),
        );
  }
  if (m.pushedAt === null) return t('No confirmation yet.');
  return (
    m.retrying
      ? t('Last push {ago}, no confirmation yet. Retrying.')
      : t('Last push {ago}, no confirmation yet.')
  ).replace('{ago}', agoText(m.pushedAt, now));
}

/**
 * Which machines have applied the latest permission rules (#98, ADR 0029): one row per machine with the revision it
 * confirmed, and "Push again" for a machine that is behind and online. Hidden until a machine has connected.
 */
export function RuleMachines(props: { fixture?: PermissionRuleMachinesResponse | undefined }) {
  const { data, pushing, push } = useRuleMachines(props.fixture);
  if (!data || data.machines.length === 0) return null;
  const now = Date.now();
  const applied = data.machines.filter((m) => machineStatus(m, data.latest) === 'ok').length;
  return (
    <section
      className="rv"
      aria-label={t('Rules on your machines')}
      data-testid="settings.rules.machines"
    >
      <div className="rv-h">
        <b>{t('On your machines')}</b>
        <span className="rmeta">
          {t('Latest revision')} <span className="mono">r{data.latest}</span>
        </span>
        <span className="sp" />
        <span className="rmeta">
          {t('{applied} of {total} applied')
            .replace('{applied}', String(applied))
            .replace('{total}', String(data.machines.length))}
        </span>
      </div>
      {data.machines.map((m) => {
        const status = machineStatus(m, data.latest);
        const name = label(status);
        return (
          <div
            key={m.deviceId}
            className={`rv-r${status === 'off' ? ' off' : ''}`}
            data-testid={`settings.rules.machine.${m.deviceId}`}
          >
            <span className={`rvs ${status}`} title={name}>
              {status === 'ok' ? Icon.check() : status === 'behind' ? Icon.clock() : Icon.laptop()}
            </span>
            <span className="rvn">
              {m.name}
              <span className="rvv">{m.applied === null ? '—' : `r${m.applied}`}</span>
            </span>
            <span className={`rvm${status === 'behind' ? ' att' : ''}`}>
              {name} · {machineDetail(m, status, now)}
            </span>
            {status === 'behind' ? (
              <Button
                variant="quiet"
                disabled={pushing === m.deviceId}
                onClick={() => void push(m.deviceId)}
                data-testid={`settings.rules.machine.${m.deviceId}.push`}
              >
                {t('Push again')}
              </Button>
            ) : (
              <span />
            )}
          </div>
        );
      })}
      <div className="rv-f">
        {t(
          'Rules reach a machine as a revision. A machine that is behind keeps deciding with the revision it has.',
        )}
      </div>
    </section>
  );
}
