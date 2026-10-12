import { type KeyboardEvent, type ReactNode, useId, useRef, useState } from 'react';
import { Icon } from './icons.tsx';
import { Popover } from './Popover.tsx';

export type SelectOption = {
  value: string;
  label: string;
  leading?: ReactNode;
  disabled?: boolean;
};

/** A single choice from a short list; opens a listbox under the trigger. */
export function Select(props: {
  label: string;
  value: string;
  options: SelectOption[];
  onChange: (value: string) => void;
  /** Tooltip of the trigger, when it says more than the label. */
  detail?: string;
  disabled?: boolean;
  'data-testid'?: string;
}) {
  const { label, value, options, onChange, detail, disabled } = props;
  const [open, setOpen] = useState(false);
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
    <div className="select">
      <button
        ref={triggerRef}
        type="button"
        className="sel"
        aria-label={label}
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
      <Popover open={open} onClose={() => setOpen(false)}>
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
            </div>
          ))}
        </div>
      </Popover>
    </div>
  );
}
