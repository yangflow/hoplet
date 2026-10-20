import type { ProviderInfo } from '@hoplet/shared';
import { type KeyboardEvent, useEffect, useId, useRef, useState } from 'react';
import { Icon } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { ProviderMark, providerName } from '../../../components/provider-marks.tsx';
import { groupByVendor, notReady } from '../../../lib/configurations.ts';
import { t } from '../../../lib/i18n.ts';

/**
 * Whether the composer shows the configuration chip: the machine has more than one enabled configuration, or
 * its only one is not the vendor's own (ADR 0028).
 */
export function showsConfigurationChip(enabled: readonly ProviderInfo[]): boolean {
  if (enabled.length > 1) return true;
  const only = enabled[0];
  return only !== undefined && only.configuration.id !== only.provider;
}

/**
 * The composer's configuration chip and its menu: the enabled configurations of the draft's machine grouped by
 * the vendor they extend. One whose variable has no value is dim and says so; the last item opens Settings.
 */
export function ConfigurationMenu(props: {
  /** The enabled configurations of the machine. */
  configurations: ProviderInfo[];
  /** The ID of the chosen configuration. */
  value: string;
  machine: string;
  onChange: (id: string) => void;
  onManage: () => void;
  disabled?: boolean;
  /** Starts open (development fixtures). */
  initialOpen?: boolean;
}) {
  const { configurations, value, onChange } = props;
  const [open, setOpen] = useState(props.initialOpen ?? false);
  const trigger = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const focusOnOpen = useRef(false);
  const id = useId();
  const current = configurations.find((c) => c.configuration.id === value) ?? configurations[0];
  const label = current?.configuration.name ?? '';
  const heading = t('Configuration on {machine}').replace('{machine}', props.machine);

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

  return (
    <span className="model-menu cfg-menu">
      <button
        ref={trigger}
        type="button"
        className={`tool-btn${open ? ' open' : ''}`}
        aria-label={`${t('Configuration')}, ${label}`}
        aria-haspopup="menu"
        aria-expanded={open}
        title={t('Configuration')}
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
        data-testid="cockpit.configuration"
      >
        <ProviderMark provider={current?.provider ?? 'claude'} />
        {label}
        {Icon.down()}
      </button>
      <Popover open={open} onClose={() => setOpen(false)} className="popover-above popover-wide">
        <div
          ref={menuRef}
          role="menu"
          aria-label={heading}
          tabIndex={-1}
          onKeyDown={onKeyDown}
          data-testid="cockpit.configuration-menu"
        >
          <div className="popover-title">{props.machine}</div>
          {groupByVendor(configurations).map((g) => (
            // biome-ignore lint/a11y/useSemanticElements: a menu's groups are role=group, a fieldset is not a menu child
            <div key={g.provider} role="group" aria-labelledby={`${id}-${g.provider}`}>
              <div className="popover-label" id={`${id}-${g.provider}`}>
                {providerName(g.provider)}
              </div>
              {g.items.map((c) => {
                const checked = c.configuration.id === current?.configuration.id;
                const blocked = notReady(c);
                return (
                  <button
                    key={c.configuration.id}
                    type="button"
                    role="menuitemradio"
                    aria-checked={checked}
                    aria-disabled={blocked || undefined}
                    tabIndex={-1}
                    className="option cfg-row"
                    onClick={() => {
                      if (blocked) return;
                      onChange(c.configuration.id);
                      close();
                    }}
                    data-testid="cockpit.configuration-option"
                  >
                    <span className="mode-check">{checked ? Icon.check() : null}</span>
                    <ProviderMark provider={c.provider} />
                    <span className="cfg-name">{c.configuration.name}</span>
                    {blocked ? <span className="option-detail">{t('Key not set')}</span> : null}
                  </button>
                );
              })}
            </div>
          ))}
          <div className="popover-sep" aria-hidden="true" />
          <button
            type="button"
            role="menuitem"
            tabIndex={-1}
            className="option cfg-row"
            onClick={() => {
              setOpen(false);
              props.onManage();
            }}
            data-testid="cockpit.configuration-manage"
          >
            <span className="mode-check">{Icon.gear()}</span>
            {t('Manage in Settings…')}
          </button>
        </div>
      </Popover>
    </span>
  );
}
