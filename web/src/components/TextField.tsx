import { type InputHTMLAttributes, type TextareaHTMLAttributes, useId } from 'react';

type Common = {
  /** The accessible name; shown above the field when `showLabel` is set. */
  label: string;
  showLabel?: boolean;
  mono?: boolean;
  bare?: boolean;
};

/** A single-line input, or a textarea with `multiline`. */
export function TextField(
  props: Common &
    (
      | ({ multiline?: false } & Omit<InputHTMLAttributes<HTMLInputElement>, 'aria-label'>)
      | ({ multiline: true } & Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'aria-label'>)
    ),
) {
  const id = useId();
  const { label, showLabel, mono, bare, className, ...rest } = props;
  const cls = `field${mono ? ' mono' : ''}${bare ? ' bare' : ''}${className ? ` ${className}` : ''}`;
  const control =
    rest.multiline === true
      ? (() => {
          const { multiline: _m, ...area } = rest;
          return <textarea id={id} className={cls} aria-label={label} {...area} />;
        })()
      : (() => {
          const { multiline: _m, ...input } = rest;
          return <input id={id} className={cls} aria-label={label} {...input} />;
        })();
  if (!showLabel) return control;
  return (
    <div className="field-group">
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      {control}
    </div>
  );
}
