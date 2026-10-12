import type { ButtonHTMLAttributes, ReactNode } from 'react';

/**
 * An icon-only button. `label` is its accessible name and tooltip; `hint` (a shortcut) is appended to the
 * tooltip only. `pressed` makes it a toggle.
 */
export function IconButton(
  props: Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'aria-label' | 'title'> & {
    label: string;
    icon: ReactNode;
    hint?: string;
    detail?: string;
    pressed?: boolean;
    on?: boolean;
    bordered?: boolean;
    /** The round accent button that sends or starts. */
    primary?: boolean;
    dot?: boolean;
  },
) {
  const {
    label,
    icon,
    hint,
    detail,
    pressed,
    on = pressed === true,
    bordered,
    primary,
    dot,
    className,
    type = 'button',
    ...rest
  } = props;
  const tip = [detail ?? label, hint].filter(Boolean).join(' · ');
  return (
    <button
      type={type}
      className={`ib${on ? ' on' : ''}${bordered ? ' bordered' : ''}${primary ? ' primary' : ''}${className ? ` ${className}` : ''}`}
      aria-label={label}
      title={tip}
      {...(pressed === undefined ? {} : { 'aria-pressed': pressed })}
      {...(hint ? { 'aria-keyshortcuts': hint } : {})}
      {...rest}
    >
      {icon}
      {dot ? <span className="ib-dot" aria-hidden="true" /> : null}
    </button>
  );
}
