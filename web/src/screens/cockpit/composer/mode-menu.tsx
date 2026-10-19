import type { ConfigOption, ConfigSetting } from '@hoplet/shared';
import { type KeyboardEvent, useEffect, useId, useRef, useState } from 'react';
import { Badge } from '../../../components/Badge.tsx';
import { configIcon, Icon } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { optionName, valueDescription, valueName } from '../../../lib/config-options.ts';
import { t } from '../../../lib/i18n.ts';

export type Select = ConfigOption & { type: 'select' };

/** The option the permission-mode control drives: the driver's first choice of the `mode` category. */
export function modeOption(config: readonly ConfigOption[] | undefined): Select | undefined {
  return (config ?? []).find((o): o is Select => o.type === 'select' && o.category === 'mode');
}

/** The value after the current one in the driver's order, wrapping around; undefined when there is no other. */
export function nextMode(option: Select): string | undefined {
  const values = option.options;
  if (values.length < 2) return undefined;
  const at = values.findIndex((v) => v.value === option.currentValue);
  return values[(at + 1) % values.length]?.value;
}

/** Whether the current value carries a high risk. */
export function isRisky(o: Select): boolean {
  return o.options.find((v) => v.value === o.currentValue)?.risk === 'high';
}

/**
 * The composer's permission-mode pill and its menu, drawn from the driver's `mode` option (ADR 0018): the
 * current mode's name, every mode with its description, the high-risk ones apart and marked. A pick applies at
 * once; Shift+Tab in the prompt cycles the modes (the composer wires it). A high-risk current mode turns the
 * pill amber.
 */
export function ModeMenu(props: {
  option: Select;
  onChange: (id: string, value: ConfigSetting) => void;
  disabled?: boolean;
  /** Starts open (development fixtures). */
  initialOpen?: boolean;
}) {
  const { option, onChange } = props;
  const [open, setOpen] = useState(props.initialOpen ?? false);
  const trigger = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const focusOnOpen = useRef(false);
  const id = useId();
  const current = option.options.find((v) => v.value === option.currentValue);
  const label = current ? valueName(option, current.value, current.name) : optionName(option);
  const risky = isRisky(option);
  const normal = option.options.filter((v) => v.risk !== 'high');
  const high = option.options.filter((v) => v.risk === 'high');

  function close() {
    setOpen(false);
    trigger.current?.focus();
  }

  useEffect(() => {
    if (!open || !focusOnOpen.current) return;
    focusOnOpen.current = false;
    menuRef.current
      ?.querySelector<HTMLElement>('[role="menuitemradio"][aria-checked="true"]')
      ?.focus();
  }, [open]);

  function onKeyDown(e: KeyboardEvent) {
    const items = [...(menuRef.current?.querySelectorAll<HTMLElement>('[role^="menuitem"]') ?? [])];
    const at = items.indexOf(document.activeElement as HTMLElement);
    const go = (i: number) => items[(i + items.length) % items.length]?.focus();
    if (e.key === 'ArrowDown') go(at + 1);
    else if (e.key === 'ArrowUp') go(at < 0 ? -1 : at - 1);
    else if (e.key === 'Home') go(0);
    else if (e.key === 'End') go(-1);
    else if (e.key === 'Escape') close();
    else if (e.key === 'Tab') setOpen(false);
    else return;
    if (e.key !== 'Tab') {
      e.preventDefault();
      e.stopPropagation();
    }
  }

  const row = (v: Select['options'][number]) => {
    const checked = v.value === option.currentValue;
    const description = valueDescription(option, v.value, v.description);
    return (
      <button
        key={v.value}
        type="button"
        role="menuitemradio"
        aria-checked={checked}
        tabIndex={-1}
        className={`option mode-row${v.risk === 'high' ? ' risk' : ''}`}
        onClick={() => {
          onChange(option.id, v.value);
          close();
        }}
        data-testid="cockpit.mode-option"
      >
        <span className="sel-lead">
          {configIcon(v.icon) ?? (v.risk === 'high' ? Icon.shieldAlert() : null)}
        </span>
        <span className="mode-text">
          <span className="mode-name">{valueName(option, v.value, v.name)}</span>
          {description ? <span className="mode-desc">{description}</span> : null}
        </span>
        {v.risk === 'high' ? <Badge tone="attention">{t('High risk')}</Badge> : null}
        <span className="mode-check">{checked ? Icon.check() : null}</span>
      </button>
    );
  };

  return (
    <span className="model-menu mode-menu">
      <button
        ref={trigger}
        type="button"
        className={`tool-btn mode-pill${risky ? ' warn' : ''}${open ? ' open' : ''}`}
        aria-label={`${optionName(option)}, ${label}`}
        aria-haspopup="menu"
        aria-expanded={open}
        title={`${optionName(option)} · ${label}`}
        disabled={props.disabled}
        onClick={() => {
          if (open) close();
          else {
            focusOnOpen.current = true;
            setOpen(true);
          }
        }}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            e.preventDefault();
            focusOnOpen.current = true;
            setOpen(true);
          }
        }}
        data-testid="cockpit.mode"
        data-risk={risky ? 'high' : 'normal'}
      >
        {configIcon(current?.icon) ?? (risky ? Icon.shieldAlert() : Icon.shield())}
        {label}
        {Icon.down()}
      </button>
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        className="popover-above popover-end popover-wide"
      >
        <div
          ref={menuRef}
          role="menu"
          aria-label={optionName(option)}
          tabIndex={-1}
          onKeyDown={onKeyDown}
          data-testid="cockpit.mode-menu"
        >
          <div className="popover-label" id={`${id}-label`}>
            {optionName(option)}
          </div>
          {normal.map(row)}
          {high.length > 0 ? <div className="popover-sep" aria-hidden="true" /> : null}
          {high.map(row)}
          <div className="popover-sep" aria-hidden="true" />
          <p className="mode-hint">
            <Badge kind="key">⇧ ⇥</Badge>
            {t('Cycle modes')}
          </p>
        </div>
      </Popover>
      <span className="sr-only" role="status" data-testid="cockpit.mode-live">
        {`${optionName(option)}: ${label}`}
      </span>
    </span>
  );
}
