import type { ButtonHTMLAttributes, ReactNode } from 'react';

/** A worded button. Only decisions are worded; actions whose icon carries the intent use IconButton. */
export function Button(
  props: ButtonHTMLAttributes<HTMLButtonElement> & {
    variant?: 'default' | 'primary' | 'quiet' | 'danger';
    icon?: ReactNode;
  },
) {
  const { variant = 'default', icon, className, children, type = 'button', ...rest } = props;
  return (
    <button
      type={type}
      className={`btn btn-${variant}${className ? ` ${className}` : ''}`}
      {...rest}
    >
      {icon}
      {children}
    </button>
  );
}
