import type { ConfigOption, ConfigSetting, ProviderInfo, SessionConfig } from '@hoplet/shared';
import {
  Fragment,
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useId,
  useRef,
  useState,
} from 'react';
import { Icon } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { ProviderMark, providerName } from '../../../components/provider-marks.tsx';
import { currentValueName, optionName, valueName } from '../../../lib/config-options.ts';
import { t } from '../../../lib/i18n.ts';

/**
 * The options the menu shows: the driver's, in its order, without the `mode` category (the permission-mode
 * control shows those) and without a type this client does not know.
 */
export function menuConfig(config: readonly ConfigOption[] | undefined): ConfigOption[] {
  return (config ?? []).filter(
    (o) => o.category !== 'mode' && (o.type === 'select' || o.type === 'boolean'),
  );
}

/** The driver's options with `values` (option ID to setting) laid over their current values. */
export function withValues(
  config: readonly ConfigOption[] | undefined,
  values: SessionConfig,
): ConfigOption[] {
  return (config ?? []).map((o) => {
    const v = values[o.id];
    if (o.type === 'select' && typeof v === 'string') return { ...o, currentValue: v };
    if (o.type === 'boolean' && typeof v === 'boolean') return { ...o, currentValue: v };
    return o;
  });
}

/** Whether a machine offers anything for the menu to choose: a second provider or an option. */
export function hasChoices(providers: ProviderInfo[], chosen: ProviderInfo | undefined): boolean {
  return providers.length > 1 || menuConfig(chosen?.capabilities.config).length > 0;
}

/** The glyph of a switch that is on, by ACP's reserved categories. */
function glyph(o: ConfigOption): ReactNode {
  if (o.category === 'thought_level') return Icon.thinking();
  if (o.category === 'model_config') return Icon.fast();
  return null;
}

/**
 * The composer's model button and its menu, drawn from the driver's config options (ADR 0018): the provider
 * (when the machine runs more than one), then every option in the driver's order, a choice as a radio list and
 * a yes or no as a switch. The button reads the model category's current value and the glyph of each switch
 * that is on.
 */
export function ModelMenu(props: {
  providers: ProviderInfo[];
  provider: ProviderInfo['provider'];
  /** The options with the values to show. */
  config: ConfigOption[];
  onProvider: (provider: ProviderInfo['provider']) => void;
  onChange: (id: string, value: ConfigSetting) => void;
  disabled?: boolean;
  /** Starts open (development fixtures). */
  initialOpen?: boolean;
}) {
  const { providers, provider, onProvider, onChange } = props;
  const [open, setOpen] = useState(props.initialOpen ?? false);
  const trigger = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const focusOnOpen = useRef(false);
  const id = useId();
  const options = menuConfig(props.config);
  const modelOption = options.find((o) => o.type === 'select' && o.category === 'model');
  const label = (modelOption && currentValueName(modelOption)) || providerName(provider);
  const on = options.filter((o) => o.type === 'boolean' && o.currentValue === true);
  const glyphs = on.filter((o) => glyph(o) !== null);
  const detail = [label, ...on.map(optionName)].filter(Boolean).join(' · ');

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

  const radio = (
    key: string,
    checked: boolean,
    text: string,
    pick: () => void,
    lead?: ReactNode,
  ) => (
    <button
      key={key}
      type="button"
      role="menuitemradio"
      aria-checked={checked}
      tabIndex={-1}
      className="option"
      onClick={pick}
      data-testid="cockpit.model-option"
    >
      <span className="sel-lead">{checked ? Icon.check() : (lead ?? null)}</span>
      <span className="sel-value">{text}</span>
    </button>
  );
  const toggle = (o: ConfigOption & { type: 'boolean' }) => (
    <button
      key={o.id}
      type="button"
      role="menuitemcheckbox"
      aria-checked={o.currentValue}
      tabIndex={-1}
      className={`option switch-row${o.currentValue ? ' on' : ''}`}
      onClick={() => onChange(o.id, !o.currentValue)}
      data-testid={`cockpit.config.${o.id}`}
    >
      <span className="sel-lead">{glyph(o)}</span>
      <span className="sel-value">{optionName(o)}</span>
      <span className="switch" aria-hidden="true" />
    </button>
  );

  return (
    <span className="model-menu">
      <button
        ref={trigger}
        type="button"
        className={`tool-btn${open ? ' open' : ''}`}
        aria-label={`${t('Model and options')}, ${detail}`}
        aria-haspopup="menu"
        aria-expanded={open}
        title={detail}
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
        data-testid="cockpit.model"
      >
        {label}
        {glyphs.length > 0 ? <i aria-hidden="true">·</i> : null}
        {glyphs.map((o) => (
          <Fragment key={o.id}>{glyph(o)}</Fragment>
        ))}
        {Icon.down()}
      </button>
      <Popover open={open} onClose={() => setOpen(false)} className="popover-above popover-end">
        <div
          ref={menuRef}
          role="menu"
          aria-label={t('Model and options')}
          tabIndex={-1}
          onKeyDown={onKeyDown}
          data-testid="cockpit.model-menu"
        >
          {providers.length > 1 ? (
            // biome-ignore lint/a11y/useSemanticElements: a menu's groups are role=group, a fieldset is not a menu child
            <div role="group" aria-labelledby={`${id}-provider`}>
              <div className="popover-label" id={`${id}-provider`}>
                {t('Provider')}
              </div>
              {providers.map((p) =>
                radio(
                  p.provider,
                  p.provider === provider,
                  providerName(p.provider),
                  () => onProvider(p.provider),
                  <ProviderMark provider={p.provider} />,
                ),
              )}
            </div>
          ) : null}
          {options.map((o, i) => {
            const before = options[i - 1];
            if (o.type === 'boolean') {
              return (
                <Fragment key={o.id}>
                  {before?.type !== 'boolean' && (i > 0 || providers.length > 1) ? (
                    <div className="popover-sep" aria-hidden="true" />
                  ) : null}
                  {toggle(o)}
                </Fragment>
              );
            }
            return (
              // biome-ignore lint/a11y/useSemanticElements: a menu's groups are role=group, a fieldset is not a menu child
              <div key={o.id} role="group" aria-labelledby={`${id}-${o.id}`}>
                <div className="popover-label" id={`${id}-${o.id}`}>
                  {optionName(o)}
                </div>
                {o.options.map((c) =>
                  radio(c.value, c.value === o.currentValue, valueName(o, c.value, c.name), () =>
                    onChange(o.id, c.value),
                  ),
                )}
              </div>
            );
          })}
        </div>
      </Popover>
    </span>
  );
}
