import { type KeyboardEvent, type ReactNode, useId, useRef, useState } from 'react';
import { Icon } from './icons.tsx';
import { Popover } from './Popover.tsx';

export type SelectOption = {
  value: string;
  label: string;
  leading?: ReactNode;
  /** Meta text at the right of the option, such as the folder a directory is in. */
  detail?: string | undefined;
  disabled?: boolean;
};

/** An action after the options, below a separator: Add project under a machine's directories. */
export type SelectAction = {
  label: string;
  icon: ReactNode;
  onSelect: () => void;
  'data-testid'?: string;
};

/**
 * A single choice from a short list; opens a listbox under the trigger. As a chip it is a quiet inline button
 * whose list opens above it (the composer's location row), and it may carry a heading and a closing action.
 */
export function Select(props: {
  label: string;
  value: string;
  options: SelectOption[];
  onChange: (value: string) => void;
  /** Tooltip of the trigger, when it says more than the label. */
  detail?: string;
  disabled?: boolean;
  appearance?: 'field' | 'chip';
  /** A title over the options, such as the machine whose directories they are. */
  heading?: string;
  action?: SelectAction;
  /** Starts open (development fixtures). */
  initialOpen?: boolean;
  'data-testid'?: string;
}) {
  const { label, value, options, onChange, detail, disabled, heading, action } = props;
  const chip = props.appearance === 'chip';
  const [open, setOpen] = useState(props.initialOpen ?? false);
  const [active, setActive] = useState(0);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const id = useId();
  const current = options.find((o) => o.value === value);

  function show() {
    if (disabled) return;
    setActive(
      Math.max(
        0,
        options.findIndex((o) => o.value === value),
      ),
    );
    setOpen(true);
    requestAnimationFrame(() => listRef.current?.focus());
  }
  function close() {
    setOpen(false);
    triggerRef.current?.focus();
  }
  function choose(index: number) {
    const option = options[index];
    if (!option || option.disabled) return;
    onChange(option.value);
    close();
  }
  function step(delta: number) {
    let next = active;
    for (let i = 0; i < options.length; i++) {
      next = (next + delta + options.length) % options.length;
      if (!options[next]?.disabled) break;
    }
    setActive(next);
  }
  function onListKey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') step(1);
    else if (e.key === 'ArrowUp') step(-1);
    else if (e.key === 'Home') setActive(0);
    else if (e.key === 'End') setActive(options.length - 1);
    else if (e.key === 'Enter' || e.key === ' ') choose(active);
    else if (e.key === 'Escape') close();
    else if (e.key === 'Tab') setOpen(false);
    else return;
    if (e.key !== 'Tab') {
      e.preventDefault();
      e.stopPropagation();
    }
  }

  return (
    <div className={`select${chip ? ' select-chip' : ''}`}>
      <button
        ref={triggerRef}
        type="button"
        className={`sel${chip ? ' chip' : ''}${open ? ' open' : ''}`}
        aria-label={current?.label ? `${label}, ${current.label}` : label}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={detail ?? label}
        disabled={disabled}
        onClick={() => (open ? close() : show())}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            e.preventDefault();
            show();
          }
        }}
        data-testid={props['data-testid']}
      >
        {current?.leading ? <span className="sel-lead">{current.leading}</span> : null}
        <span className="sel-value">{current?.label ?? ''}</span>
        {Icon.down()}
      </button>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        {...(chip ? { className: 'popover-above' } : {})}
      >
        {heading ? <div className="popover-title">{heading}</div> : null}
        <div
          ref={listRef}
          role="listbox"
          tabIndex={-1}
          aria-label={label}
          aria-activedescendant={`${id}-${active}`}
          className="listbox"
          onKeyDown={onListKey}
        >
          {options.map((o, i) => (
            // biome-ignore lint/a11y/useKeyWithClickEvents: the listbox owns the keys (aria-activedescendant)
            <div
              key={o.value}
              id={`${id}-${i}`}
              role="option"
              tabIndex={-1}
              aria-selected={o.value === value}
              aria-disabled={o.disabled || undefined}
              className={`option${i === active ? ' active' : ''}`}
              onMouseMove={() => setActive(i)}
              onClick={() => choose(i)}
            >
              {o.leading ? <span className="sel-lead">{o.leading}</span> : null}
              <span className="sel-value">{o.label}</span>
              {o.detail ? <span className="option-detail">{o.detail}</span> : null}
            </div>
          ))}
        </div>
        {action ? (
          <>
            <div className="popover-sep" />
            <button
              type="button"
              className="option option-action"
              onClick={() => {
                setOpen(false);
                action.onSelect();
              }}
              data-testid={action['data-testid']}
            >
              <span className="sel-lead">{action.icon}</span>
              <span className="sel-value">{action.label}</span>
            </button>
          </>
        ) : null}
      </Popover>
    </div>
  );
}
