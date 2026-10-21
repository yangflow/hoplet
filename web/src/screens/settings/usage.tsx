import type { LiveSessionMeta, UsagePrices, UsageResponse } from '@hoplet/shared';
import { type KeyboardEvent, type ReactNode, useRef, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { Segmented } from '../../components/Segmented.tsx';
import { t } from '../../lib/i18n.ts';
import {
  buildChart,
  chartQuery,
  customRange,
  dayKey,
  dayKeys,
  formatCost,
  formatShare,
  formatTokens,
  GROUPS,
  type Group,
  groupQuery,
  groupRows,
  RANGE_KINDS,
  type RangeKind,
  rangeLabel,
  rangeOf,
  shiftRange,
  totalTokens,
  type UsageRange,
  unpricedModels,
  unpricedTokens,
  utcOffsetOf,
} from '../../lib/usage.ts';
import { useBudgets } from '../../lib/use-budgets.ts';
import { useUsage } from '../../lib/use-usage.ts';
import { type BudgetsFixture, BudgetsTab } from './budgets.tsx';
import { PricesTab } from './prices.tsx';
import { UsageChart, UsageLegend } from './usage-chart.tsx';

export type UsageTab = 'usage' | 'budgets' | 'prices';

/** What the development fixtures hand to Settings > Usage instead of the core's answers. */
export type UsageFixture = {
  tab?: UsageTab;
  /** The instant the page treats as now, so the next-range button is the same on every run. */
  now: number;
  range: UsageRange;
  group?: Group;
  /** The rows by day and model, for the chart and the totals. */
  chart: UsageResponse;
  /** The ranked rows of each grouping. */
  groups: Record<Group, UsageResponse>;
  prices: UsagePrices;
  /** The column whose tooltip starts open. */
  hover?: number;
  /** The model whose price starts in the editor. */
  editing?: string;
  /** The Budgets tab: its list, and the form or toast that starts open. */
  budgets?: BudgetsFixture;
};

const RANGE_LABEL: Record<RangeKind, () => string> = {
  day: () => t('Day'),
  week: () => t('Week'),
  month: () => t('Month'),
  custom: () => t('Custom'),
};

const GROUP_LABEL: Record<Group, () => string> = {
  session: () => t('Session'),
  project: () => t('Project'),
  machine: () => t('Machine'),
  model: () => t('Model'),
};

const GROUP_TITLE: Record<Group, () => string> = {
  session: () => t('By session'),
  project: () => t('By project'),
  machine: () => t('By machine'),
  model: () => t('By model'),
};

const PREVIOUS: Record<RangeKind, () => string> = {
  day: () => t('Previous day'),
  week: () => t('Previous week'),
  month: () => t('Previous month'),
  custom: () => t('Previous'),
};

const NEXT: Record<RangeKind, () => string> = {
  day: () => t('Next day'),
  week: () => t('Next week'),
  month: () => t('Next month'),
  custom: () => t('Next'),
};

const TABS: readonly { id: UsageTab; label: () => string }[] = [
  { id: 'usage', label: () => t('Usage') },
  { id: 'budgets', label: () => t('Budgets') },
  { id: 'prices', label: () => t('Prices') },
];

/** One day of a custom range, as a date field. */
function DateField(props: {
  label: string;
  value: string;
  onChange: (day: string) => void;
  testid: string;
}) {
  return (
    <span className="th-in">
      <input
        type="date"
        className="field bare"
        aria-label={props.label}
        value={props.value}
        onChange={(e) => props.onChange(e.target.value)}
        data-testid={props.testid}
      />
    </span>
  );
}

function RangeBar(props: {
  range: UsageRange;
  offset: number;
  now: number;
  onRange: (range: UsageRange) => void;
}) {
  const { range, offset, now, onRange } = props;
  const anchor = range.from;
  const pick = (kind: RangeKind) => {
    if (kind === 'custom') onRange({ ...range, kind: 'custom' });
    else onRange(rangeOf(kind, anchor, offset));
  };
  const first = dayKey(range.from, offset);
  const last = dayKey(range.to - 1, offset);
  return (
    <div className="ubar">
      <Segmented
        label={t('Time range')}
        value={range.kind}
        options={RANGE_KINDS.map((k) => ({ value: k, label: RANGE_LABEL[k]() }))}
        onChange={pick}
        data-testid="settings.usage.range"
      />
      {range.kind === 'custom' ? (
        <div className="rnav">
          <DateField
            label={t('First day')}
            value={first}
            onChange={(day) => {
              const next = customRange(day, last, offset);
              if (next) onRange(next);
            }}
            testid="settings.usage.first"
          />
          <span className="to" aria-hidden="true">
            –
          </span>
          <DateField
            label={t('Last day')}
            value={last}
            onChange={(day) => {
              const next = customRange(first, day, offset);
              if (next) onRange(next);
            }}
            testid="settings.usage.last"
          />
        </div>
      ) : (
        <div className="rnav">
          <IconButton
            label={PREVIOUS[range.kind]()}
            icon={Icon.back()}
            onClick={() => onRange(shiftRange(range, -1, offset))}
            data-testid="settings.usage.prev"
          />
          <span className="lab" aria-live="polite" data-testid="settings.usage.label">
            {rangeLabel(range, offset)}
          </span>
          <IconButton
            label={NEXT[range.kind]()}
            icon={Icon.chevron()}
            disabled={range.to > now}
            onClick={() => onRange(shiftRange(range, 1, offset))}
            data-testid="settings.usage.next"
          />
        </div>
      )}
    </div>
  );
}

function Footnote(props: { tokensOnly: boolean }) {
  return (
    <p className="rmeta ufoot" data-testid="settings.usage.footnote">
      {props.tokensOnly
        ? t('Showing tokens only. Turn it off in Prices to show estimated cost.')
        : t(
            'Costs are estimates from the price table. — marks a model without a price: its tokens are counted, its cost is not.',
          )}
    </p>
  );
}

function Table(props: {
  group: Group;
  response: UsageResponse;
  tokensOnly: boolean;
  loading: boolean;
}) {
  const { group, response, tokensOnly } = props;
  const rows = groupRows(response);
  const total = totalTokens(response.total);
  const cols = tokensOnly ? 'minmax(0,1fr) 84px 56px' : 'minmax(0,1fr) 84px 96px';
  const more = response.rowCount - rows.length;
  const shown = rows.reduce((n, r) => n + r.tokens, 0);
  const shownCost = rows.reduce((n, r) => n + (r.costUsd ?? 0), 0);
  const costTip = t('Estimated from the price table');
  return (
    <div
      className="mp-list"
      aria-busy={props.loading || undefined}
      data-testid={`settings.usage.table.${group}`}
    >
      <div className="ur hd" style={{ '--cols': cols } as React.CSSProperties}>
        <span>{GROUP_LABEL[group]()}</span>
        <span className="n">{t('Tokens')}</span>
        <span className="n" title={tokensOnly ? undefined : costTip}>
          {tokensOnly ? t('Share') : t('Est. cost')}
        </span>
      </div>
      {rows.map((r) => (
        <div
          key={r.id}
          className="ur"
          style={{ '--cols': cols } as React.CSSProperties}
          data-testid="settings.usage.row"
        >
          <span className="un">
            <span className={`t1${group === 'model' ? ' mono' : ''}`}>{r.name}</span>
            {r.detail ? <span className="t2">{r.detail}</span> : null}
          </span>
          <span className="n">{formatTokens(r.tokens)}</span>
          {tokensOnly ? (
            <span className="n">{formatShare(r.tokens, total)}</span>
          ) : group === 'model' && r.costUsd === null ? (
            <span className="np" title={t('No price for this model')}>
              {t('No price')}
            </span>
          ) : (
            <span className="n c">{formatCost(r.costUsd)}</span>
          )}
        </div>
      ))}
      {more > 0 ? (
        <div
          className="ur"
          style={{ '--cols': cols } as React.CSSProperties}
          data-testid="settings.usage.more"
        >
          <span className="un">
            <span className="t1">{t('And {n} more').replace('{n}', String(more))}</span>
          </span>
          <span className="n">{formatTokens(Math.max(0, total - shown))}</span>
          {tokensOnly ? (
            <span className="n">{formatShare(Math.max(0, total - shown), total)}</span>
          ) : (
            <span className="n c">
              {formatCost(
                response.total.estimatedCostUsd === null
                  ? null
                  : Math.max(0, response.total.estimatedCostUsd - shownCost),
              )}
            </span>
          )}
        </div>
      ) : null}
    </div>
  );
}

function Note(props: { icon: ReactNode; children: ReactNode; action?: ReactNode; role?: string }) {
  return (
    <div className="unote" role={props.role ?? 'note'}>
      {props.icon}
      {props.children}
      {props.action ? <span className="acts">{props.action}</span> : null}
    </div>
  );
}

function UsageTabPanel(props: {
  fixture: UsageFixture | undefined;
  tokensOnly: boolean;
  onPrices: () => void;
}) {
  const { fixture, tokensOnly } = props;
  const offset = fixture ? 0 : utcOffsetOf();
  const now = fixture?.now ?? Date.now();
  const [range, setRange] = useState<UsageRange>(fixture?.range ?? rangeOf('week', now, offset));
  const [group, setGroup] = useState<Group>(fixture?.group ?? 'project');
  const chartRes = useUsage(chartQuery(range, offset), fixture?.chart);
  const groupRes = useUsage(groupQuery(range, group, offset), fixture?.groups[group]);
  const chartData = chartRes.data;

  let body: ReactNode;
  if (chartData === null) {
    body = chartRes.error ? (
      <p className="rmeta" role="alert" data-testid="settings.usage.error">
        {chartRes.error}
      </p>
    ) : (
      <p className="rmeta" role="status">
        {t('Loading usage…')}
      </p>
    );
  } else if (chartData.total.messages === 0 && !chartData.backfilling) {
    body = (
      <div className="mp-list" data-testid="settings.usage.empty">
        <div className="uempty">
          <span className="tile" aria-hidden="true">
            {Icon.activity()}
          </span>
          <b>{t('No usage yet')}</b>
          <p>
            {t(
              'Hoplet counts tokens as sessions run. Start a session and its usage shows here, by session, project, machine and model.',
            )}
          </p>
        </div>
      </div>
    );
  } else {
    const chart = buildChart(chartData.rows, dayKeys(range, offset));
    const unpriced = unpricedTokens(chartData.rows);
    const total = chartData.total;
    const missing = group === 'model' && groupRes.data ? unpricedModels(groupRes.data.rows) : [];
    const missingTokens = missing.reduce((n, m) => n + m.tokens, 0);
    body = (
      <>
        <div className="ucard">
          <div className="utot" data-testid="settings.usage.total">
            <b>{formatTokens(totalTokens(total))}</b>
            <span className="u">{t('tokens')}</span>
            {tokensOnly ? null : (
              <span className="cost" title={t('Estimated from the price table')}>
                <b>{formatCost(total.estimatedCostUsd)}</b> {t('estimated')}
                {unpriced > 0 && !chartData.backfilling ? (
                  <span className="meta">
                    {' · '}
                    {t('excludes {n} tokens without a price').replace(
                      '{n}',
                      formatTokens(unpriced),
                    )}
                  </span>
                ) : null}
              </span>
            )}
          </div>
          <div className="ubrk">
            <span>
              {t('Input')} <b>{formatTokens(total.input)}</b>
            </span>
            <span>
              {t('Output')} <b>{formatTokens(total.output)}</b>
            </span>
            <span>
              {t('Cache write')} <b>{formatTokens(total.cacheWrite)}</b>
            </span>
            <span>
              {t('Cache read')} <b>{formatTokens(total.cacheRead)}</b>
            </span>
          </div>
          <UsageChart
            chart={chart}
            tokensOnly={tokensOnly}
            pending={chartData.backfilling}
            {...(fixture?.hover !== undefined ? { hover: fixture.hover } : {})}
          />
          <UsageLegend chart={chart} />
        </div>
        {missing.length > 0 && !tokensOnly ? (
          <Note
            icon={Icon.info()}
            action={
              <Button
                variant="quiet"
                onClick={props.onPrices}
                data-testid="settings.usage.add-price"
              >
                {t('Add a price')}
              </Button>
            }
          >
            <span data-testid="settings.usage.unknown">
              {missing.length === 1 && missing[0] ? (
                <>
                  <span className="mono">{missing[0].label}</span>{' '}
                  {t(
                    'has no price, so its {n} tokens are counted but not in the estimated cost.',
                  ).replace('{n}', formatTokens(missingTokens))}
                </>
              ) : (
                t(
                  '{count} models have no price, so their {n} tokens are counted but not in the estimated cost.',
                )
                  .replace('{count}', String(missing.length))
                  .replace('{n}', formatTokens(missingTokens))
              )}
            </span>
          </Note>
        ) : null}
        <div className="ugh">
          <b>{GROUP_TITLE[group]()}</b>
          <Segmented
            label={t('Group by')}
            value={group}
            options={GROUPS.map((g) => ({ value: g, label: GROUP_LABEL[g]() }))}
            onChange={setGroup}
            data-testid="settings.usage.group"
          />
        </div>
        {groupRes.data ? (
          <Table
            group={group}
            response={groupRes.data}
            tokensOnly={tokensOnly}
            loading={groupRes.loading}
          />
        ) : groupRes.error ? (
          <p className="rmeta" role="alert">
            {groupRes.error}
          </p>
        ) : null}
        <Footnote tokensOnly={tokensOnly} />
      </>
    );
  }

  return (
    <>
      <RangeBar range={range} offset={offset} now={now} onRange={setRange} />
      {chartData?.backfilling ? (
        <Note icon={<Spinner />} role="status">
          <div className="bf" data-testid="settings.usage.backfill">
            <b>{t('Counting existing sessions')}</b>
            <span className="rmeta dim">
              {t('The totals below are partial until counting finishes. You can leave this page.')}
            </span>
          </div>
        </Note>
      ) : null}
      {chartData?.backfilling ? <div style={{ height: 'var(--space-4)' }} /> : null}
      {body}
    </>
  );
}

/** Settings > Usage: tokens and estimated cost by range, grouping and model, and the price table behind the estimate. */
export function UsageSection(props: {
  fixture?: UsageFixture;
  tokensOnly: boolean;
  onTokensOnly: (on: boolean) => void;
  sessions?: readonly LiveSessionMeta[];
}) {
  const budgets = useBudgets(props.fixture?.budgets?.list);
  const projectPaths = [...new Set((props.sessions ?? []).map((x) => x.projectPath))];
  const [tab, setTab] = useState<UsageTab>(props.fixture?.tab ?? 'usage');
  const list = useRef<HTMLDivElement>(null);
  const onKeyDown = (e: KeyboardEvent) => {
    const at = TABS.findIndex((x) => x.id === tab);
    const to =
      e.key === 'ArrowRight'
        ? at + 1
        : e.key === 'ArrowLeft'
          ? at - 1
          : e.key === 'Home'
            ? 0
            : e.key === 'End'
              ? -1
              : null;
    if (to === null) return;
    e.preventDefault();
    const next = TABS[(to + TABS.length) % TABS.length];
    if (!next) return;
    setTab(next.id);
    list.current?.querySelectorAll<HTMLElement>('[role="tab"]')[TABS.indexOf(next)]?.focus();
  };
  return (
    <>
      <h1>{t('Usage')}</h1>
      <div
        ref={list}
        className="mp-tabs"
        role="tablist"
        aria-label={t('Usage')}
        onKeyDown={onKeyDown}
        data-testid="settings.usage.tabs"
      >
        {TABS.map((x) => (
          <button
            key={x.id}
            type="button"
            role="tab"
            id={`usage-tab-${x.id}`}
            className="mp-tab"
            aria-selected={tab === x.id}
            aria-controls={tab === x.id ? 'usage-panel' : undefined}
            tabIndex={tab === x.id ? 0 : -1}
            onClick={() => setTab(x.id)}
            data-testid={`settings.usage.tab.${x.id}`}
          >
            {x.label()}
            {x.id === 'budgets' && budgets.data && budgets.data.budgets.length > 0 ? (
              <span className="meta">{budgets.data.budgets.length}</span>
            ) : null}
          </button>
        ))}
      </div>
      <div role="tabpanel" id="usage-panel" aria-labelledby={`usage-tab-${tab}`}>
        {tab === 'usage' ? (
          <UsageTabPanel
            fixture={props.fixture}
            tokensOnly={props.tokensOnly}
            onPrices={() => setTab('prices')}
          />
        ) : tab === 'budgets' ? (
          <BudgetsTab
            budgets={budgets}
            fixture={props.fixture?.budgets}
            projectPaths={projectPaths}
          />
        ) : (
          <PricesTab
            fixture={props.fixture}
            tokensOnly={props.tokensOnly}
            onTokensOnly={props.onTokensOnly}
          />
        )}
      </div>
    </>
  );
}
