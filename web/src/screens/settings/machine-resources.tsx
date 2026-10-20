// biome-ignore-all lint/a11y/useSemanticElements: a grid laid out with CSS, not a <table>: the roles name its parts
// biome-ignore-all lint/a11y/useFocusableInteractive: a header row and a session unknown to the store are not actionable
import {
  type DeviceInfo,
  type DeviceResourceSample,
  type DeviceResourcesResult,
  type DeviceSessionResources,
  type LiveSessionMeta,
  RESOURCES_DISK_WARN_DEFAULT,
  RESOURCES_MEMORY_WARN_DEFAULT,
  type ResourceThresholdPair,
  type SessionRuntimeInfo,
} from '@hoplet/shared';
import type { KeyboardEvent, ReactNode } from 'react';
import { Icon } from '../../components/icons.tsx';
import { ProviderMark } from '../../components/provider-marks.tsx';
import { glyphState, StateGlyph } from '../../components/StateGlyph.tsx';
import { t } from '../../lib/i18n.ts';
import { lastSeenText } from './last-seen.ts';
import {
  cpuSeries,
  diskText,
  memoryGb,
  memorySeries,
  percent,
  sortSessions,
  sparkPoints,
  stack,
} from './resources-model.ts';
import { readingWarn } from './thresholds-model.ts';

/** A trend line of the last hour; with fewer than two points it keeps its place and draws nothing. */
function Spark(props: { values: readonly number[]; label: string; small?: boolean }) {
  const w = props.small ? 64 : 120;
  const h = props.small ? 16 : 24;
  const cls = `rsp${props.small ? ' sm' : ''}`;
  const points = sparkPoints(props.values, w, h);
  if (points === '') return <span className={cls} aria-hidden="true" />;
  return (
    <svg
      className={cls}
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      role="img"
      aria-label={t('{label}, last hour').replace('{label}', props.label)}
    >
      <polyline
        points={points}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}

function Tile(props: { label: string; warn?: boolean; value: ReactNode; children: ReactNode }) {
  return (
    <div className={`rtile${props.warn ? ' warn' : ''}`}>
      <span className="rk">{props.label}</span>
      <span className="rv">{props.value}</span>
      {props.children}
    </div>
  );
}

const memText = (bytes: number) => `${memoryGb(bytes)} GB`;
const cpuText = (v: number) => `${Math.round(v)}%`;

/** One stacked bar: each session a segment, then the other processes, the rest free. */
function StackBar(props: {
  label: string;
  sessions: readonly DeviceSessionResources[];
  values: readonly number[];
  total: number;
  used: number;
  format: (v: number) => string;
  tick?: { at: number; warn: boolean };
}) {
  const bar = stack(props.sessions, props.values, props.total, props.used);
  const sum = t('{used} of {total}')
    .replace('{used}', props.format(props.used))
    .replace('{total}', props.format(props.total));
  return (
    <div className="rbar">
      <span className="rl2">{props.label}</span>
      <span
        className="rstack"
        role="img"
        aria-label={t('{label}: {sum}').replace('{label}', props.label).replace('{sum}', sum)}
      >
        {bar.segments.map((s) => (
          <i
            key={s.sessionId}
            className={`s${s.shade}`}
            style={{ width: `${s.width}%` }}
            title={`${s.title ?? t('Untitled session')}: ${props.format(s.value)}`}
          />
        ))}
        <i
          className="ot"
          style={{ width: `${bar.otherWidth}%` }}
          title={`${t('Other processes')}: ${props.format(bar.other)}`}
        />
        {props.tick ? (
          <b
            className={`rtick${props.tick.warn ? ' warn' : ''}`}
            style={{ left: `${props.tick.at}%` }}
          />
        ) : null}
      </span>
      <span className="rn">{props.format(props.used)}</span>
    </div>
  );
}

function projectName(path: string | null): string | null {
  if (path === null) return null;
  const trimmed = path.replace(/[/\\]+$/, '');
  return trimmed.split(/[/\\]/).pop() || trimmed || null;
}

function SessionRow(props: {
  session: DeviceSessionResources;
  index: number;
  provider: LiveSessionMeta['provider'] | undefined;
  state: SessionRuntimeInfo['state'] | undefined;
  history: readonly number[];
  hovered: boolean;
  onOpen: (sessionId: string) => void;
}) {
  const { session: s } = props;
  const title = s.title ?? t('Untitled session');
  const known = s.title !== null;
  const dir = projectName(s.projectPath);
  const children =
    s.children > 0
      ? t(s.children === 1 ? '+{n} child process' : '+{n} child processes').replace(
          '{n}',
          `${s.children}`,
        )
      : null;
  const sub = [dir, children].filter((x): x is string => x !== null).join(' · ');
  const glyph = props.state ? glyphState(props.state) : null;
  const open = () => props.onOpen(s.sessionId);
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== 'Enter' || e.target !== e.currentTarget) return;
    e.preventDefault();
    open();
  };
  const interactive = known
    ? {
        tabIndex: 0,
        'aria-label': t('Open {title}').replace('{title}', title),
        title: t('Open {title}').replace('{title}', title),
        onClick: open,
        onKeyDown,
      }
    : { 'aria-label': title };
  return (
    <div
      className={`rp${known ? '' : ' static'}${props.hovered ? ' hov' : ''}`}
      role="row"
      data-testid="settings.resources.session"
      {...interactive}
    >
      <i className={`rw s${Math.min(props.index, 3)}`} aria-hidden="true" />
      <span className="c" role="cell">
        <ProviderMark provider={props.provider} />
      </span>
      <span className="ti" role="cell">
        <span className="t1">{title}</span>
        {sub ? <span className="t2">{sub}</span> : null}
      </span>
      <span className="c" role="cell">
        {glyph ? <StateGlyph state={glyph} /> : <span aria-hidden="true" />}
      </span>
      <span className="c" role="cell">
        <Spark small values={props.history} label={t('{title} memory').replace('{title}', title)} />
      </span>
      <span className="n" role="cell">
        {s.cpu === null ? '—' : cpuText(s.cpu)}
      </span>
      <span className="n" role="cell">
        {memText(s.rss)}
      </span>
    </div>
  );
}

/** What the panel of a machine shows beyond its facts: the summary, where the load goes and its session processes. */
export function ResourcesBody(props: {
  device: DeviceInfo;
  sample: DeviceResourceSample;
  result: DeviceResourcesResult | null;
  sessions: readonly LiveSessionMeta[];
  runtime: Record<string, SessionRuntimeInfo> | undefined;
  now: number;
  hovered: string | undefined;
  onOpenSession: (sessionId: string) => void;
  /** What the machine warns at; null until the core has answered, then the warning is the core's own. */
  thresholds: ResourceThresholdPair | null;
  /** The Warnings group, shown above the footnote. */
  warnings?: ReactNode;
}) {
  const { device, sample, result } = props;
  const online = device.connected;
  const warn = !online
    ? { memory: false, disk: false }
    : props.thresholds
      ? readingWarn(sample, props.thresholds)
      : (device.warn ?? { memory: false, disk: false });
  const limit = props.thresholds ?? {
    memory: RESOURCES_MEMORY_WARN_DEFAULT,
    disk: RESOURCES_DISK_WARN_DEFAULT,
  };
  const memPct = percent(sample.memUsed, sample.memTotal);
  const freePct = percent(sample.diskFree, sample.diskTotal);
  const samples = result?.samples ?? [];
  const sessions = online ? sortSessions(result?.sessions ?? []) : [];
  const warnings = [
    warn.memory
      ? t('Memory {n}% used, above {limit}%')
          .replace('{n}', `${memPct}`)
          .replace('{limit}', `${limit.memory}`)
      : null,
    warn.disk
      ? t('Disk {n}% free, under {limit}% free')
          .replace('{n}', `${freePct}`)
          .replace('{limit}', `${limit.disk}`)
      : null,
  ].filter((x): x is string => x !== null);
  const sessionCpu = sessions.reduce((a, s) => a + (s.cpu ?? 0), 0);

  const runtimeOf = (s: LiveSessionMeta): SessionRuntimeInfo =>
    props.runtime?.[s.sessionId] ?? s.runtime ?? { state: s.ownerConnected ? 'idle' : 'offline' };

  return (
    <div className={`rbody${online ? '' : ' stale'}`} data-testid="settings.resources">
      {online ? null : (
        <p className="rmeta rst" data-testid="settings.resources.stale">
          {Icon.clock()}
          {t('Last reported {when}').replace(
            '{when}',
            lastSeenText({ connected: false, lastSeen: sample.at }, props.now),
          )}
        </p>
      )}
      <div className="rtiles">
        <Tile label={t('CPU')} value={sample.cpu === null ? '—' : cpuText(sample.cpu)}>
          <Spark values={cpuSeries(samples)} label={t('CPU')} />
        </Tile>
        <Tile
          label={t('Memory')}
          warn={warn.memory}
          value={
            <>
              {memoryGb(sample.memUsed)}{' '}
              <span className="rmeta">
                {t('of {total} GB').replace('{total}', `${memoryGb(sample.memTotal)}`)}
              </span>
            </>
          }
        >
          <Spark values={memorySeries(samples)} label={t('Memory')} />
        </Tile>
        <Tile
          label={t('Disk free')}
          warn={warn.disk}
          value={
            <>
              {diskText(sample.diskFree)}{' '}
              <span className="rmeta">
                {t('of {total}').replace('{total}', diskText(sample.diskTotal))}
              </span>
            </>
          }
        >
          <span
            className="rdisk"
            role="img"
            aria-label={t('{n}% used').replace('{n}', `${100 - freePct}`)}
          >
            <i style={{ width: `${100 - freePct}%` }} />
          </span>
        </Tile>
      </div>
      {warnings.map((w) => (
        <p key={w} className="rwn" data-testid="settings.resources.warning">
          {Icon.alert()}
          {w}
        </p>
      ))}
      {!online ? (
        <p className="rmeta dim">
          {t('Sessions are not known while {name} is offline').replace('{name}', device.name)}
        </p>
      ) : sessions.length === 0 ? (
        <p className="rmeta dim">{t('No session is running here')}</p>
      ) : (
        <>
          <div className="rsec">
            <StackBar
              label={t('Memory')}
              sessions={sessions}
              values={sessions.map((s) => s.rss)}
              total={sample.memTotal}
              used={sample.memUsed}
              format={memText}
              tick={{ at: limit.memory, warn: warn.memory }}
            />
            <StackBar
              label={t('CPU')}
              sessions={sessions}
              values={sessions.map((s) => s.cpu ?? 0)}
              total={100}
              used={sample.cpu ?? sessionCpu}
              format={cpuText}
            />
            <div className="rleg">
              <span>
                <i className="rw ot" />
                {t('Other processes')}
              </span>
              <span>
                <i className="rw fr" />
                {t('Free')}
              </span>
            </div>
          </div>
          <div
            className="rps"
            role="table"
            aria-label={t('Sessions running on {name}').replace('{name}', device.name)}
            data-testid="settings.resources.sessions"
          >
            <div className="rp hd" role="row">
              <span className="h1" role="columnheader">
                {t('Sessions · {n}').replace('{n}', `${sessions.length}`)}
              </span>
              <span role="columnheader">{t('Last hour')}</span>
              <span className="n" role="columnheader">
                {t('CPU')}
              </span>
              <span className="n" role="columnheader">
                {t('Memory')}
              </span>
            </div>
            {sessions.map((s, i) => {
              const meta = props.sessions.find((m) => m.sessionId === s.sessionId);
              return (
                <SessionRow
                  key={s.sessionId}
                  session={s}
                  index={i}
                  provider={meta?.provider}
                  state={meta ? runtimeOf(meta).state : undefined}
                  history={s.history.map((pt) => (pt.rss / sample.memTotal) * 100)}
                  hovered={props.hovered === s.sessionId}
                  onOpen={props.onOpenSession}
                />
              );
            })}
          </div>
        </>
      )}
      {props.warnings}
      <p className="set-meta rfoot">
        {t('Reported every 15 s; the trend lines show the last hour.')}
      </p>
    </div>
  );
}
