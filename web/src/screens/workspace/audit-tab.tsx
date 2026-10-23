import type { DeviceInfo, LiveSessionMeta } from '@hoplet/shared';
import { type ReactNode, useEffect, useMemo, useState } from 'react';
import { Detail, SummaryView } from '../../components/audit-parts.tsx';
import { Button } from '../../components/Button.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { Segmented } from '../../components/Segmented.tsx';
import {
  type AuditNames,
  actorLabel,
  clock,
  formatCount,
  groupByDay,
  inKind,
  sessionFilters,
  TAB_KINDS,
  type TabKind,
} from '../../lib/audit.ts';
import { t } from '../../lib/i18n.ts';
import { fill } from '../../lib/templates.ts';
import { type AuditSource, useAudit } from '../../lib/use-audit.ts';
import { useDevices } from '../../lib/use-devices.ts';
import './audit-tab.css';
import { Quiet } from './viewer.tsx';

/** Development fixture of the Audit tab: the records of the log, and where the tab starts. */
export type AuditTabFixture = {
  source: AuditSource;
  devices?: DeviceInfo[];
  kind?: TabKind;
  /** The record that starts open, by seq. */
  openSeq?: number;
};

const kindLabel = (kind: TabKind) =>
  ({
    all: t('All'),
    tools: t('Tools'),
    perm: t('Permissions'),
    other: t('Other'),
  })[kind];

/** Opens Settings > Audit, the way the URL of the settings does. */
function openSettingsAudit() {
  const url = new URL(location.href);
  url.searchParams.set('settings', 'audit');
  history.pushState(null, '', url);
  window.dispatchEvent(new PopStateEvent('popstate'));
}

/** Keeps the whole of an opened record, row and detail, inside the scrolled list. */
const keepInView = (el: HTMLElement | null) => {
  el?.scrollIntoView?.({ block: 'nearest' });
};

/** Records of the Other segment to gather before it stops reading older pages by itself. */
const OTHER_FILL = 20;

/**
 * The Audit tab of the inspector (issue #80): the records of this session, newest first in a group per day, a
 * record opening in place, and older ones on request.
 */
export function AuditTab(props: { session: LiveSessionMeta; fixture?: AuditTabFixture }) {
  const { session, fixture } = props;
  const devices = useDevices(fixture?.devices);
  const [kind, setKind] = useState<TabKind>(fixture?.kind ?? 'all');
  const [openSeq, setOpenSeq] = useState<number | null>(fixture?.openSeq ?? null);
  const audit = useAudit(sessionFilters(session.sessionId, kind), fixture?.source);

  const names = useMemo<AuditNames>(
    () => ({
      device: (id) => devices.find((d) => d.deviceId === id)?.name ?? id,
      session: (id) =>
        id === session.sessionId
          ? session.titleOverride?.trim() || session.title || id.slice(0, 8)
          : id.slice(0, 8),
    }),
    [devices, session],
  );
  const shown = useMemo(
    () => audit.events.filter((e) => inKind(e.type, kind)),
    [audit.events, kind],
  );
  const days = useMemo(() => groupByDay(shown, audit.now), [shown, audit.now]);

  // Other has no type of its own to ask the core for: it reads on until enough of its records are in.
  const { next, loadingMore, loadMore, error } = audit;
  const count = shown.length;
  useEffect(() => {
    if (kind === 'other' && next !== null && !loadingMore && !error && count < OTHER_FILL)
      loadMore();
  }, [kind, next, loadingMore, error, count, loadMore]);

  const pick = (value: TabKind) => {
    setKind(value);
    setOpenSeq(null);
  };

  let body: ReactNode;
  if (audit.loading && audit.events.length === 0) {
    body = (
      <Quiet icon={<Spinner />} text={t('Loading…')} status testid="workspace.audit.loading" />
    );
  } else if (error && audit.events.length === 0) {
    body = (
      <Quiet
        icon={Icon.alert()}
        title={t('The audit log could not be read')}
        text={error}
        action={
          <Button onClick={audit.retry} data-testid="workspace.audit.retry">
            {t('Try again')}
          </Button>
        }
        testid="workspace.audit.error"
      />
    );
  } else if (count === 0 && (kind !== 'other' || next === null)) {
    body = (
      <Quiet
        icon={Icon.listChecks()}
        title={t('Nothing recorded yet')}
        text={t('What this session does, and what you do in it, appears here as it happens.')}
        testid="workspace.audit.empty"
      />
    );
  } else {
    body = (
      <div className="ait-body" data-testid="workspace.audit.list">
        {days.map((day) => (
          <div key={day.key}>
            <div className="ait-dh">{day.label}</div>
            {day.events.map((event) => {
              const open = openSeq === event.seq;
              return (
                <div key={event.seq} className="ait-item" ref={open ? keepInView : undefined}>
                  <button
                    type="button"
                    className={`ait-row${open ? ' open' : ''}`}
                    aria-expanded={open}
                    onClick={() => setOpenSeq(open ? null : event.seq)}
                    data-testid={`workspace.audit.row.${event.seq}`}
                  >
                    <span className="l1">
                      <SummaryView event={event} names={names} />
                    </span>
                    <span className="l2">
                      <span>{clock(event.at)}</span>
                      <i>·</i>
                      <span
                        className={`mono${event.type === 'permission.requested' ? ' att' : ''}`}
                      >
                        {event.type}
                      </span>
                      <i>·</i>
                      <span>
                        {event.actorKind === 'session' && event.actorId === session.sessionId
                          ? t('Session')
                          : actorLabel(event, names)}
                      </span>
                    </span>
                  </button>
                  {open ? (
                    <Detail event={event} names={names} testid="workspace.audit.detail" />
                  ) : null}
                </div>
              );
            })}
          </div>
        ))}
        <div className="ait-more">
          <span data-testid="workspace.audit.count">
            {fill(t('Showing {count} records'), { count: formatCount(count) })}
          </span>
          {error ? (
            <span className="ait-err" role="alert">
              {error}
            </span>
          ) : null}
          {next !== null ? (
            <Button onClick={loadMore} disabled={loadingMore} data-testid="workspace.audit.more">
              {loadingMore ? t('Loading…') : t('Load older')}
            </Button>
          ) : null}
        </div>
      </div>
    );
  }

  return (
    <div className="ait" data-testid="workspace.audit">
      <div className="ait-fb">
        <Segmented
          label={t('Type')}
          value={kind}
          options={TAB_KINDS.map((value) => ({ value, label: kindLabel(value) }))}
          onChange={pick}
          data-testid="workspace.audit.kind"
        />
      </div>
      {body}
      <div className="ait-ft">
        <span>
          {t(
            'This session only. Never prompts, assistant text, tool output, file contents or diffs.',
          )}
        </span>
        <button
          type="button"
          className="ait-lnk"
          onClick={openSettingsAudit}
          data-testid="workspace.audit.all"
        >
          {t('All records in Settings › Audit')}
          {Icon.chevron()}
        </button>
      </div>
    </div>
  );
}
