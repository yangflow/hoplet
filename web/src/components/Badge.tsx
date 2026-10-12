import type { ReactNode } from 'react';

/** A keycap (`key`) or a state label with an optional dot (`state`). */
export function Badge(props: {
  kind?: 'key' | 'state';
  tone?: 'neutral' | 'live' | 'attention' | 'danger' | 'success';
  dot?: boolean;
  children: ReactNode;
}) {
  const { kind = 'state', tone = 'neutral', dot, children } = props;
  if (kind === 'key') return <kbd className="badge-key">{children}</kbd>;
  return (
    <span className={`badge badge-${tone}`}>
      {dot ? <i className="badge-dot" aria-hidden="true" /> : null}
      {children}
    </span>
  );
}
