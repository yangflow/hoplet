import type { ReactNode } from 'react';
import { t } from '../lib/i18n.ts';

// Provider marks are monochrome bundled SVGs drawn in the colour they sit in (docs/design.md#icons).
// This file is the only place that holds the artwork. The Claude mark is a placeholder for the official
// artwork; it sits in a padded box so its optical size matches the 16 px stroke icons.

const sparkPath = (() => {
  const lengths = [11.2, 8.1, 10.3, 9.2, 11, 7.7, 10.6, 9.7, 10, 8.3, 11.1, 8.9];
  const jitter = [0, 0.09, -0.06, 0.11, -0.04, 0.08, -0.1, 0.05, 0.12, -0.07, 0.03, -0.09];
  const c = 12;
  let d = '';
  lengths.forEach((r, k) => {
    const a = (k / lengths.length) * Math.PI * 2 - Math.PI / 2 + (jitter[k] ?? 0);
    const pt = (rad: number, off: number) =>
      `${(c + rad * Math.cos(a) - off * Math.sin(a)).toFixed(2)} ${(c + rad * Math.sin(a) + off * Math.cos(a)).toFixed(2)}`;
    d += `M${pt(1, -0.4)}L${pt(r - 1.2, -1.2)}C${pt(r + 0.1, -1.25)} ${pt(r + 0.1, 1.25)} ${pt(r - 1.2, 1.2)}L${pt(1, 0.4)}Z`;
  });
  return d;
})();

export type ProviderId = 'claude';

const providers: Record<ProviderId, { art: (size: number) => ReactNode }> = {
  claude: {
    art: (size) => (
      <svg width={size} height={size} viewBox="-1.5 -1.5 27 27" aria-hidden="true">
        <path d={sparkPath} fill="currentColor" />
        <circle cx="12" cy="12" r="1.9" fill="currentColor" />
      </svg>
    ),
  },
};

export function providerName(provider: ProviderId): string {
  switch (provider) {
    case 'claude':
      return t('Claude Code');
  }
}

/** The vendor mark that leads a session row; named for assistive technology. */
export function ProviderMark(props: { provider?: ProviderId | undefined; size?: number }) {
  const { provider = 'claude', size = 16 } = props;
  const name = providerName(provider);
  return (
    <span className="provider-mark" role="img" title={name} aria-label={name}>
      {providers[provider].art(size)}
    </span>
  );
}
