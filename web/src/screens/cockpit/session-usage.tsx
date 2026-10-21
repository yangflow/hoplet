import type { SessionUsage } from '@hoplet/shared';
import { useState } from 'react';
import { Popover } from '../../components/Popover.tsx';
import { t } from '../../lib/i18n.ts';
import { formatCost, formatTokens, totalTokens } from '../../lib/usage.ts';
import { useSessionUsage } from '../../lib/use-usage.ts';

/** What the chip says: tokens, and the estimated cost when the session has a price and cost is not hidden. */
export function chipText(usage: SessionUsage, tokensOnly: boolean) {
  const tokens = `${formatTokens(totalTokens(usage.total))} ${t('tokens')}`;
  const cost = usage.total.estimatedCostUsd;
  return cost === null || tokensOnly
    ? { tokens, cost: null }
    : { tokens, cost: `≈ ${formatCost(cost)}` };
}

function Detail(props: { usage: SessionUsage; tokensOnly: boolean }) {
  const { usage, tokensOnly } = props;
  const total = usage.total;
  const kinds: [string, number][] = [
    [t('Input'), total.input],
    [t('Output'), total.output],
    [t('Cache write'), total.cacheWrite],
    [t('Cache read'), total.cacheRead],
  ];
  const missing = usage.models.filter((m) => m.estimatedCostUsd === null);
  const showCost = !tokensOnly;
  return (
    <>
      <div className="ph1">
        <b>{t('Usage of this session')}</b>
      </div>
      <div className="big">
        <b>{formatTokens(totalTokens(total))}</b>
        <span>{t('tokens')}</span>
        {showCost && total.estimatedCostUsd !== null ? (
          <span title={t('Estimated from the price table')}>
            ≈ {formatCost(total.estimatedCostUsd)} {t('estimated')}
          </span>
        ) : null}
      </div>
      <div className="ukv">
        {kinds.map(([name, n]) => (
          <span key={name} style={{ display: 'contents' }}>
            <span className="k">{name}</span>
            <span />
            <span className="v">{formatTokens(n)}</span>
          </span>
        ))}
      </div>
      <div className="rule" />
      <div className="ukv hd">
        <span>{t('Model')}</span>
        <span className="v">{t('Tokens')}</span>
        {showCost ? <span className="v m">{t('Est. cost')}</span> : null}
      </div>
      <div className={`ukv${showCost ? '' : ' two'}`}>
        {usage.models.map((m) => (
          <span key={m.keys.map((k) => k.value).join('/')} style={{ display: 'contents' }}>
            <span className="k mono">{m.keys[0]?.label}</span>
            <span className="v">{formatTokens(totalTokens(m))}</span>
            {showCost ? (
              <span className="v m">
                {m.estimatedCostUsd === null ? (
                  <span className="np">{t('No price')}</span>
                ) : (
                  formatCost(m.estimatedCostUsd)
                )}
              </span>
            ) : null}
          </span>
        ))}
      </div>
      <p className="rmeta" style={{ margin: 0 }}>
        {showCost && missing.length > 0
          ? t(
              '{model} has no price, so only its tokens are shown. Add one in Settings › Usage › Prices.',
            ).replace('{model}', missing.map((m) => m.keys[0]?.label ?? '').join(', '))
          : showCost
            ? t('Estimated from the price table.')
            : t('Showing tokens only. Turn it off in Prices to show estimated cost.')}
      </p>
    </>
  );
}

/** The session's cumulative usage in the top bar, and the per-model popover behind it. */
export function SessionUsageChip(props: {
  sessionId: string;
  /** Changes when the conversation grows, so the chip follows it. */
  refresh: number;
  tokensOnly: boolean;
  /** The core's answer, for development fixtures. */
  fixture?: SessionUsage;
  open?: boolean;
}) {
  const { data } = useSessionUsage(props.sessionId, props.refresh, props.fixture);
  const [open, setOpen] = useState(props.open ?? false);
  if (!data || data.total.messages === 0) return null;
  const text = chipText(data, props.tokensOnly);
  return (
    <span className="usage" data-testid="cockpit.usage">
      <button
        type="button"
        className="uchip"
        aria-haspopup="dialog"
        aria-expanded={open}
        title={t('Usage of this session')}
        onClick={() => setOpen((v) => !v)}
        data-testid="cockpit.usage-chip"
      >
        <span>{text.tokens}</span>
        {text.cost ? (
          <>
            <span className="sep">·</span>
            <span>{text.cost}</span>
          </>
        ) : null}
      </button>
      <Popover open={open} onClose={() => setOpen(false)} className="upop">
        <div
          className="ubody"
          role="dialog"
          aria-label={t('Usage of this session')}
          data-testid="cockpit.usage-popover"
        >
          <Detail usage={data} tokensOnly={props.tokensOnly} />
        </div>
      </Popover>
    </span>
  );
}
