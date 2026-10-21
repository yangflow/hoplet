import { useState } from 'react';
import { currentLanguage, t } from '../../lib/i18n.ts';
import { type Chart, columnLabel, formatCost, formatTokens, tooltipDate } from '../../lib/usage.ts';

/** More columns than this draw the day of the month only, closer together. */
const DENSE_FROM = 8;

/** The name a model in the legend and the stack carries; the grey step gathers every other model. */
export function modelName(label: string): string {
  return label === '' ? t('Other models') : label;
}

/** Which model each step of the stack is, for the legend. */
export function UsageLegend(props: { chart: Chart }) {
  return (
    <div className="uleg" data-testid="settings.usage.legend">
      {props.chart.models.map((m) => (
        <span key={m.id || 'other'}>
          <i className={`rw ${m.step}`} aria-hidden="true" />
          {modelName(m.label)}
        </span>
      ))}
    </div>
  );
}

/**
 * A column per day, stacked by model with the accent in opacity steps. A column is an image with its numbers as
 * its name; the tooltip repeats them for the pointer and is hidden from assistive technology.
 */
export function UsageChart(props: {
  chart: Chart;
  tokensOnly: boolean;
  /** Days with no usage are still to be counted (existing sessions are being counted). */
  pending: boolean;
  /** Starts with this column's tooltip open (development fixtures). */
  hover?: number;
}) {
  const { chart, tokensOnly, pending } = props;
  const [hover, setHover] = useState<number | null>(props.hover ?? null);
  const lang = currentLanguage();
  const n = chart.days.length;
  const dense = n >= DENSE_FROM;
  const every = Math.max(1, Math.ceil(n / 8));
  return (
    <div className="uchart" data-testid="settings.usage.chart">
      <div className="uy" aria-hidden="true">
        <span>{formatTokens(chart.max)}</span>
        <span>{formatTokens(chart.max / 2)}</span>
        <span>0</span>
      </div>
      {/* biome-ignore lint/a11y/noStaticElementInteractions: hover only repeats what each column's name says */}
      <div
        className={`uplot${dense ? ' dense' : ''}`}
        style={{ '--cols': Math.max(n, 1) } as React.CSSProperties}
        onMouseLeave={() => setHover(null)}
      >
        <div className="ugl" aria-hidden="true">
          <i style={{ top: 0 }} />
          <i style={{ top: '50%' }} />
          <i style={{ top: '100%' }} />
        </div>
        {chart.days.map((d, i) => {
          const day = tooltipDate(d.key, lang);
          const name = `${day}: ${formatTokens(d.tokens)} ${t('tokens')}${
            tokensOnly || d.costUsd === null ? '' : `, ${formatCost(d.costUsd)}`
          }`;
          const open = hover === i;
          return (
            // biome-ignore lint/a11y/noStaticElementInteractions: hover only repeats what the image's name says
            <div
              key={d.key}
              className={`uc${open ? ' hov' : ''}`}
              onMouseEnter={() => setHover(i)}
              data-testid={`settings.usage.day.${d.key}`}
            >
              {open ? (
                <div
                  className={`utip${i < n / 4 ? ' at-start' : i >= (n * 3) / 4 ? ' at-end' : ''}`}
                  aria-hidden="true"
                >
                  <b>{day}</b>
                  <span>
                    {formatTokens(d.tokens)} {t('tokens')}
                  </span>
                  {tokensOnly || d.costUsd === null ? null : (
                    <span>
                      {formatCost(d.costUsd)} {t('estimated')}
                    </span>
                  )}
                </div>
              ) : null}
              <div
                className={`ucs${pending && d.tokens === 0 ? ' pend' : ''}`}
                role="img"
                aria-label={name}
              >
                {d.segments.map((s) => (
                  <i
                    key={s.step}
                    className={s.step}
                    style={{ height: `${(s.tokens / chart.max) * 100}%` }}
                  />
                ))}
              </div>
              <span className="ucl" aria-hidden="true">
                {i % every !== 0
                  ? ''
                  : dense
                    ? String(Number(d.key.slice(8)))
                    : columnLabel(d.key, lang)}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
