import type { KeyboardEvent, ReactNode } from 'react';

/** A small set of exclusive choices side by side: one radio group, arrow keys move between them. */
export function Segmented<T extends string>(props: {
  label: string;
  value: T;
  options: readonly {
    value: T;
    label: string;
    lang?: string;
    icon?: ReactNode;
    disabled?: boolean;
  }[];
  onChange: (value: T) => void;
  'data-testid'?: string;
}) {
  const { label, value, options, onChange } = props;
  const move = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (step === undefined) return;
    e.preventDefault();
    const at = options.findIndex((o) => o.value === value);
    const next = options[(at + step + options.length) % options.length];
    if (!next) return;
    onChange(next.value);
    const radios = e.currentTarget.querySelectorAll<HTMLElement>('[role="radio"]');
    radios[options.indexOf(next)]?.focus();
  };
  return (
    <div
      className="seg"
      role="radiogroup"
      aria-label={label}
      onKeyDown={move}
      data-testid={props['data-testid']}
    >
      {options.map((o) => (
        // biome-ignore lint/a11y/useSemanticElements: see above
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          tabIndex={o.value === value ? 0 : -1}
          className={o.value === value ? 'on' : undefined}
          lang={o.lang}
          disabled={o.disabled}
          onClick={() => onChange(o.value)}
          data-testid={props['data-testid'] ? `${props['data-testid']}.${o.value}` : undefined}
        >
          {o.icon}
          {o.label}
        </button>
      ))}
    </div>
  );
}
