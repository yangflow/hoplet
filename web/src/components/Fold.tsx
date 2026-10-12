import type { ReactNode } from 'react';

/** A native disclosure: `summary` stays visible, the children fold away. */
export function Fold(props: {
  summary: ReactNode;
  children: ReactNode;
  defaultOpen?: boolean;
  className?: string;
  'data-testid'?: string;
}) {
  const { summary, children, defaultOpen = true, className, ...rest } = props;
  return (
    <details className={`fold${className ? ` ${className}` : ''}`} open={defaultOpen} {...rest}>
      <summary className="fold-summary">{summary}</summary>
      {children}
    </details>
  );
}
