import type { ProviderInfo } from '@hoplet/shared';
import { type KeyboardEvent, type ReactNode, useEffect, useId, useRef, useState } from 'react';
import { Icon } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { ProviderMark, providerName } from '../../../components/provider-marks.tsx';
import { t } from '../../../lib/i18n.ts';

/** The choices a session starts with: only what the provider's runtime driver declares is offered. */
export type ModelChoice = {
  provider: ProviderInfo['provider'];
  model: string;
  thinking: boolean;
  fast: boolean;
};

/** Whether a machine offers anything for the menu to choose: a second provider, models, thinking or fast. */
export function hasChoices(providers: ProviderInfo[], chosen: ProviderInfo | undefined): boolean {
  const caps = chosen?.capabilities;
  return providers.length > 1 || !!caps?.thinking || !!caps?.fast || (caps?.models.length ?? 0) > 0;
}

/**
 * The composer's model button and its menu: the model's name and the Thinking glyph, opening the provider
 * (when the machine runs more than one), the model, Thinking and Fast.
 */
export function ModelMenu(props: {
  providers: ProviderInfo[];
  value: ModelChoice;
  onChange: (next: ModelChoice) => void;
  disabled?: boolean;
  /** Starts open (development fixtures). */
  initialOpen?: boolean;
}) {
  const { providers, value, onChange } = props;
  const [open, setOpen] = useState(props.initialOpen ?? false);
  const trigger = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const focusOnOpen = useRef(false);
  const id = useId();
  const chosen = providers.find((p) => p.provider === value.provider);
  const caps = chosen?.capabilities;
  const label = value.model || providerName(value.provider);
  const detail = [
    label,
    caps?.thinking && value.thinking ? t('Thinking') : '',
    caps?.fast && value.fast ? t('Fast') : '',
  ]
    .filter(Boolean)
    .join(' · ');

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

  const radio = (key: string, on: boolean, text: string, pick: () => void, lead?: ReactNode) => (
    <button
      key={key}
      type="button"
      role="menuitemradio"
      aria-checked={on}
      tabIndex={-1}
      className="option"
      onClick={pick}
      data-testid="cockpit.model-option"
    >
      <span className="sel-lead">{on ? Icon.check() : (lead ?? null)}</span>
      <span className="sel-value">{text}</span>
    </button>
  );
  const toggle = (on: boolean, text: string, icon: ReactNode, flip: () => void, testid: string) => (
    <button
      type="button"
      role="menuitemcheckbox"
      aria-checked={on}
      tabIndex={-1}
      className={`option switch-row${on ? ' on' : ''}`}
      onClick={flip}
      data-testid={testid}
    >
      <span className="sel-lead">{icon}</span>
      <span className="sel-value">{text}</span>
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
        {caps?.thinking && value.thinking ? (
          <>
            <i aria-hidden="true">·</i>
            {Icon.thinking()}
          </>
        ) : null}
        {caps?.fast && value.fast ? Icon.fast() : null}
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
                  p.provider === value.provider,
                  providerName(p.provider),
                  () =>
                    onChange({
                      ...value,
                      provider: p.provider,
                      model: p.capabilities.models[0] ?? '',
                    }),
                  <ProviderMark provider={p.provider} />,
                ),
              )}
            </div>
          ) : null}
          {caps && caps.models.length > 0 ? (
            // biome-ignore lint/a11y/useSemanticElements: a menu's groups are role=group, a fieldset is not a menu child
            <div role="group" aria-labelledby={`${id}-model`}>
              <div className="popover-label" id={`${id}-model`}>
                {t('Model')}
              </div>
              {caps.models.map((m) =>
                radio(m, m === value.model, m, () => onChange({ ...value, model: m })),
              )}
            </div>
          ) : null}
          {caps?.thinking || caps?.fast ? (
            <>
              <div className="popover-sep" aria-hidden="true" />
              {caps.thinking
                ? toggle(
                    value.thinking,
                    t('Thinking'),
                    Icon.thinking(),
                    () => onChange({ ...value, thinking: !value.thinking }),
                    'cockpit.thinking',
                  )
                : null}
              {caps.fast
                ? toggle(
                    value.fast,
                    t('Fast'),
                    Icon.fast(),
                    () => onChange({ ...value, fast: !value.fast }),
                    'cockpit.fast',
                  )
                : null}
            </>
          ) : null}
        </div>
      </Popover>
    </span>
  );
}
