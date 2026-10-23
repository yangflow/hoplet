import type { DeviceInfo, LiveSessionMeta } from '@hoplet/shared';
import { type ReactNode, useCallback, useEffect, useMemo, useState } from 'react';
import { Detail, SummaryView } from '../../components/audit-parts.tsx';
import { Button } from '../../components/Button.tsx';
import { fill } from '../../components/fill.tsx';
import { Icon } from '../../components/icons.tsx';
import { Popover } from '../../components/Popover.tsx';
import { Select } from '../../components/Select.tsx';
import { TextField } from '../../components/TextField.tsx';
import { Toast, useToast } from '../../components/Toast.tsx';
import {
  type AuditFilters,
  type AuditNames,
  actorLabel,
  actorOptions,
  breakText,
  clock,
  DEFAULT_FILTERS,
  formatCount,
  groupByDay,
  isFiltered,
  timeOptions,
  typeOptions,
  verifiedLine,
} from '../../lib/audit.ts';
import { t } from '../../lib/i18n.ts';
import { fill as fillText } from '../../lib/templates.ts';
import { type AuditSource, useAudit, type VerifyState } from '../../lib/use-audit.ts';
import { useDevices } from '../../lib/use-devices.ts';
import './audit.css';

/** What the development fixtures hand to Settings > Audit. */
export type AuditFixture = {
  source: AuditSource;
  devices: DeviceInfo[];
  sessions: LiveSessionMeta[];
  /** The filters the bar starts with. */
  filters?: Partial<AuditFilters>;
  /** The record that starts open, by seq. */
  openSeq?: number;
  /** The Export menu starts open. */
  menuOpen?: boolean;
  toast?: string;
};

const mono = (s: string) => <span className="mono">{s}</span>;

function sessionTitle(s: LiveSessionMeta): string {
  return s.titleOverride?.trim() || s.title || s.sessionId.slice(0, 8);
}

function Banner(props: { verify: VerifyState; onAgain: () => void }) {
  const { verify } = props;
  if (verify.status === 'idle') return null;
  if (verify.status === 'running') {
    return (
      <div className="avr" role="status" data-testid="settings.audit.verify.running">
        {Icon.shield()}
        <div className="tx">
          <b>{t('Checking the chain…')}</b>
          <span>{t('Every record is hashed again and compared with the one after it.')}</span>
        </div>
      </div>
    );
  }
  if (verify.status === 'error') {
    return (
      <div className="avr bad" role="alert" data-testid="settings.audit.verify.error">
        {Icon.alert()}
        <div className="tx">
          <b>{t('The chain could not be checked')}</b>
          <span>{verify.message}</span>
        </div>
        <Button variant="quiet" onClick={props.onAgain}>
          {t('Verify again')}
        </Button>
      </div>
    );
  }
  const { result } = verify;
  if (result.ok) {
    return (
      <div className="avr ok" role="status" data-testid="settings.audit.verify.ok">
        {Icon.check()}
        <div className="tx">
          <b>{t('Chain verified')}</b>
          <span>{verifiedLine(result)}</span>
        </div>
        <Button variant="quiet" onClick={props.onAgain}>
          {t('Verify again')}
        </Button>
      </div>
    );
  }
  const brk = result.firstBreak;
  return (
    <div className="avr bad" role="alert" data-testid="settings.audit.verify.broken">
      {Icon.alert()}
      <div className="tx">
        <b>
          {brk
            ? fillText(t('Chain broken at #{seq}'), { seq: formatCount(brk.seq) })
            : t('Chain broken')}
        </b>
        {brk ? (
          <span>
            {mono(brk.reason)} · {breakText(brk.reason)}{' '}
            {t('Records from there on cannot be trusted until the host is checked.')}
          </span>
        ) : null}
      </div>
      <Button variant="quiet" onClick={props.onAgain}>
        {t('Verify again')}
      </Button>
    </div>
  );
}

/** Settings > Audit (issue #80): search, inspect, verify and export the hash-chained log of actions. */
export function AuditSection(props: {
  fixture?: AuditFixture | undefined;
  sessions?: readonly LiveSessionMeta[];
}) {
  const { fixture } = props;
  const devices = useDevices(fixture?.devices);
  const sessions = fixture?.sessions ?? props.sessions ?? [];
  const [filters, setFilters] = useState<AuditFilters>({ ...DEFAULT_FILTERS, ...fixture?.filters });
  const [search, setSearch] = useState(filters.q);
  const [openSeq, setOpenSeq] = useState<number | null>(fixture?.openSeq ?? null);
  const [menu, setMenu] = useState(fixture?.menuOpen ?? false);
  const [toast, say] = useToast(fixture?.toast ? { text: fixture.toast } : null);
  const audit = useAudit(filters, fixture?.source);

  // The search box waits for a pause in the typing before the list is asked again.
  useEffect(() => {
    if (search === filters.q) return;
    const timer = setTimeout(() => setFilters((f) => ({ ...f, q: search })), 300);
    return () => clearTimeout(timer);
  }, [search, filters.q]);

  const names = useMemo<AuditNames>(
    () => ({
      device: (id) => devices.find((d) => d.deviceId === id)?.name ?? id,
      session: (id) => {
        const s = sessions.find((x) => x.sessionId === id);
        return s ? sessionTitle(s) : id.slice(0, 8);
      },
    }),
    [devices, sessions],
  );
  const set = useCallback(
    (key: keyof AuditFilters) => (value: string) => setFilters((f) => ({ ...f, [key]: value })),
    [],
  );
  const days = useMemo(() => groupByDay(audit.events, audit.now), [audit.events, audit.now]);
  const filtered = isFiltered(filters);
  const clear = () => {
    setSearch('');
    setFilters({ ...DEFAULT_FILTERS });
  };
  const pick = async (format: 'jsonl' | 'csv') => {
    setMenu(false);
    const problem = await audit.exportAs(format);
    if (problem) say(problem, true);
    else say(t('Export saved'));
  };

  const all = t('All');
  const sessionOptions = [
    { value: 'all', label: all },
    ...sessions.map((s) => ({ value: s.sessionId, label: sessionTitle(s) })),
  ];
  const machineOptions = [
    { value: 'all', label: all },
    ...devices.map((d) => ({ value: d.deviceId, label: d.name })),
  ];

  let body: ReactNode;
  if (audit.loading && audit.events.length === 0) {
    body = (
      <p className="rmeta aload" role="status" data-testid="settings.audit.loading">
        {t('Loading…')}
      </p>
    );
  } else if (audit.error && audit.events.length === 0) {
    body = (
      <div className="mp-list">
        <div className="uempty" role="alert" data-testid="settings.audit.error">
          <span className="tile">{Icon.alert()}</span>
          <b>{t('The audit log could not be read')}</b>
          <p>{audit.error}</p>
          <div className="acts">
            <Button onClick={audit.retry} data-testid="settings.audit.retry">
              {t('Try again')}
            </Button>
          </div>
        </div>
      </div>
    );
  } else if (audit.events.length === 0) {
    body = (
      <div className="mp-list">
        <div
          className="uempty"
          data-testid={filtered ? 'settings.audit.no-match' : 'settings.audit.empty'}
        >
          <span className="tile">{Icon.search()}</span>
          <b>{filtered ? t('No records match') : t('Nothing is recorded yet')}</b>
          <p>
            {filtered
              ? t('Nothing in this time matches the filters. Widen the time or clear a filter.')
              : t('Records appear here as sessions run and settings change.')}
          </p>
          {filtered ? (
            <div className="acts">
              <Button onClick={clear} data-testid="settings.audit.clear">
                {t('Clear filters')}
              </Button>
            </div>
          ) : null}
        </div>
      </div>
    );
  } else {
    body = (
      <>
        <div className="mp-list" data-testid="settings.audit.list">
          {days.map((day) => (
            <div key={day.key} className="aday">
              <div className="adh">
                <span>{day.label}</span>
                <span className="meta">
                  {fillText(day.events.length === 1 ? t('{count} record') : t('{count} records'), {
                    count: formatCount(day.events.length),
                  })}
                </span>
              </div>
              {day.events.map((event) => {
                const open = openSeq === event.seq;
                return (
                  <div key={event.seq}>
                    <button
                      type="button"
                      className={`arr${open ? ' sel' : ''}`}
                      aria-expanded={open}
                      onClick={() => setOpenSeq(open ? null : event.seq)}
                      data-testid={`settings.audit.row.${event.seq}`}
                    >
                      <span className="tm2">{clock(event.at)}</span>
                      <span className={`ty${event.type === 'permission.requested' ? ' att' : ''}`}>
                        {event.type}
                      </span>
                      <span className="sm">
                        <SummaryView event={event} names={names} />
                      </span>
                      <span className="ac">{actorLabel(event, names)}</span>
                      <span className="chv">{open ? Icon.down() : Icon.chevron()}</span>
                    </button>
                    {open ? (
                      <Detail event={event} names={names} testid="settings.audit.detail" />
                    ) : null}
                  </div>
                );
              })}
            </div>
          ))}
        </div>
        <div className="aft">
          <span data-testid="settings.audit.count">
            {fillText(t('Showing {count} records'), { count: formatCount(audit.events.length) })}
          </span>
          <span className="sp" />
          {audit.error ? (
            <span className="aerr" role="alert">
              {audit.error}
            </span>
          ) : null}
          {audit.next !== null ? (
            <Button
              onClick={audit.loadMore}
              disabled={audit.loadingMore}
              data-testid="settings.audit.more"
            >
              {audit.loadingMore ? t('Loading…') : t('Load older')}
            </Button>
          ) : null}
        </div>
      </>
    );
  }

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: Escape closes the open Export menu from anywhere on the page
    <div
      className="aupage"
      data-testid="settings.audit"
      onKeyDown={(e) => {
        if (e.key === 'Escape' && menu) {
          e.stopPropagation();
          e.preventDefault();
          setMenu(false);
        }
      }}
    >
      <div className="ahead">
        <h1>{t('Audit')}</h1>
        <span className="sp" />
        <Button
          icon={Icon.rules()}
          onClick={audit.runVerify}
          disabled={audit.verify.status === 'running'}
          data-testid="settings.audit.verify"
        >
          {t('Verify chain')}
        </Button>
        <span className="amenu">
          <Button
            icon={Icon.download()}
            aria-haspopup="menu"
            aria-expanded={menu}
            disabled={audit.exporting}
            onClick={() => setMenu((m) => !m)}
            data-testid="settings.audit.export"
          >
            {t('Export')}
          </Button>
          <Popover open={menu} onClose={() => setMenu(false)} className="apop">
            <div role="menu" aria-label={t('Export')}>
              <span className="meta">
                {t('Exports the records that match the filters. The export is itself recorded.')}
              </span>
              <button
                type="button"
                role="menuitem"
                className="mi"
                onClick={() => void pick('jsonl')}
                data-testid="settings.audit.export.jsonl"
              >
                {Icon.file()}
                <span className="tx">
                  <span>JSON Lines</span>
                  <span className="meta">{t('One record per line, with its hashes')}</span>
                </span>
              </button>
              <button
                type="button"
                role="menuitem"
                className="mi"
                onClick={() => void pick('csv')}
                data-testid="settings.audit.export.csv"
              >
                {Icon.file()}
                <span className="tx">
                  <span>CSV</span>
                  <span className="meta">{t('For a spreadsheet')}</span>
                </span>
              </button>
            </div>
          </Popover>
        </span>
      </div>
      <p className="rmeta alead">
        {t(
          'Who did what, to which session, and when. Never prompts, assistant text, tool output, file contents or diffs.',
        )}
      </p>
      <Banner verify={audit.verify} onAgain={audit.runVerify} />
      <div className="afb">
        <div className="asrch">
          {Icon.search()}
          <TextField
            label={t('Search summaries, commands and paths')}
            type="search"
            value={search}
            placeholder={t('Search summaries, commands and paths')}
            onChange={(e) => setSearch(e.target.value)}
            data-testid="settings.audit.search"
          />
        </div>
      </div>
      {/* biome-ignore lint/a11y/useSemanticElements: a row of selects, as in the mockup */}
      <div className="afb" role="group" aria-label={t('Filters')}>
        <Select
          label={t('Session')}
          prefix={t('Session')}
          value={filters.session}
          options={sessionOptions}
          active={filters.session !== 'all'}
          onChange={set('session')}
          data-testid="settings.audit.filter.session"
        />
        <Select
          label={t('Machine')}
          prefix={t('Machine')}
          value={filters.machine}
          options={machineOptions}
          active={filters.machine !== 'all'}
          onChange={set('machine')}
          data-testid="settings.audit.filter.machine"
        />
        <Select
          label={t('Actor')}
          prefix={t('Actor')}
          value={filters.actor}
          options={actorOptions()}
          active={filters.actor !== 'all'}
          onChange={set('actor')}
          data-testid="settings.audit.filter.actor"
        />
        <Select
          label={t('Type')}
          prefix={t('Type')}
          value={filters.type}
          options={typeOptions()}
          active={filters.type !== 'all'}
          onChange={set('type')}
          data-testid="settings.audit.filter.type"
        />
        <Select
          label={t('Time')}
          prefix={t('Time')}
          value={filters.time}
          options={timeOptions()}
          active={filters.time !== DEFAULT_FILTERS.time}
          onChange={set('time')}
          data-testid="settings.audit.filter.time"
        />
      </div>
      {body}
      <div className="aret">
        {Icon.lock()}
        <span>
          {fill(
            t(
              'Old records are replaced by a checkpoint that keeps the chain verifiable. Retention is set on the host with {env}: 180 days by default, 0 keeps everything. Deleting a session keeps its records.',
            ),
            { env: mono('HOPLET_AUDIT_RETENTION_DAYS') },
          )}
        </span>
      </div>
      {toast ? <Toast {...(toast.failed ? { failed: true } : {})}>{toast.text}</Toast> : null}
    </div>
  );
}
