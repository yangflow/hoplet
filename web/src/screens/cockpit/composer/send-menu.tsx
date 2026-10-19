import { type KeyboardEvent, useEffect, useRef, useState } from 'react';
import { Badge } from '../../../components/Badge.tsx';
import { Button } from '../../../components/Button.tsx';
import { Icon } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { t } from '../../../lib/i18n.ts';
import type { SendMode } from '../../../lib/prefs.ts';

/** The name of a way to send while a turn runs. */
export function sendModeName(mode: SendMode): string {
  return mode === 'steer' ? t('Steer') : t('Queue');
}

/**
 * The send button of a running turn (ADR 0020): it queues or steers by the preference, and, when the driver can
 * steer, its chevron lists both ways with their keys. The button submits the form; a menu row sends in its way.
 */
export function SendWhileRunning(props: {
  /** The way Enter sends. */
  mode: SendMode;
  /** The driver takes a steer and nothing blocks it. */
  steer: boolean;
  /** Something can be sent now. */
  ready: boolean;
  providerName: string;
  onSend: (mode: SendMode) => void;
  initialOpen?: boolean;
}) {
  const { mode, steer } = props;
  const [open, setOpen] = useState(props.initialOpen ?? false);
  const menu = useRef<HTMLDivElement>(null);
  const anchor = useRef<HTMLSpanElement>(null);
  const focusMenu = useRef(false);
  const name = sendModeName(mode);
  const icon = mode === 'steer' ? Icon.steer() : Icon.queue();

  useEffect(() => {
    if (!open || !focusMenu.current) return;
    focusMenu.current = false;
    menu.current
      ?.querySelector<HTMLElement>('[role="menuitemradio"][aria-checked="true"]')
      ?.focus();
  }, [open]);

  const main = (
    <Button
      variant="primary"
      type="submit"
      disabled={!props.ready}
      icon={icon}
      title={`${name} · ↩`}
      data-testid="cockpit.send"
    >
      {name}
    </Button>
  );
  if (!steer) return main;

  function close() {
    setOpen(false);
    anchor.current?.querySelector('button')?.focus();
  }
  function onKey(e: KeyboardEvent) {
    const items = [
      ...(menu.current?.querySelectorAll<HTMLElement>('[role="menuitemradio"]') ?? []),
    ];
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

  const rows: { mode: SendMode; text: string; keys: string[] }[] = [
    {
      mode: 'queue',
      text: t('Sent as the next turn, after this one ends'),
      keys: mode === 'queue' ? ['↩'] : ['⌥', '↩'],
    },
    {
      mode: 'steer',
      text: t('Added to this turn at its next tool call'),
      keys: mode === 'steer' ? ['↩'] : ['⌥', '↩'],
    },
  ];
  return (
    <span className="split sendw">
      {main}
      <span className="model-menu anc" ref={anchor}>
        <Button
          variant="primary"
          aria-label={t('Queue or steer')}
          title={t('Queue or steer')}
          aria-haspopup="menu"
          aria-expanded={open}
          onClick={() => {
            focusMenu.current = true;
            setOpen((o) => !o);
          }}
          data-testid="cockpit.send-menu"
        >
          {Icon.down()}
        </Button>
        <Popover
          open={open}
          onClose={() => setOpen(false)}
          className="popover-above popover-end popover-wide"
        >
          <div
            ref={menu}
            role="menu"
            aria-label={t('Send while a turn runs')}
            tabIndex={-1}
            onKeyDown={onKey}
            data-testid="cockpit.send-options"
          >
            <div className="popover-label">
              {t('Send while {provider} works').replace('{provider}', props.providerName)}
            </div>
            {rows.map((row) => (
              <button
                key={row.mode}
                type="button"
                role="menuitemradio"
                aria-checked={row.mode === mode}
                tabIndex={-1}
                className="option mode-row"
                disabled={!props.ready}
                onClick={() => {
                  setOpen(false);
                  props.onSend(row.mode);
                }}
                data-testid={`cockpit.send-${row.mode}`}
              >
                <span className="mode-check">
                  {row.mode === 'steer' ? Icon.steer() : Icon.queue()}
                </span>
                <span className="mode-text">
                  <span className="mode-name">{sendModeName(row.mode)}</span>
                  <span className="mode-desc">{row.text}</span>
                </span>
                <span className="kbs">
                  {row.keys.map((k) => (
                    <Badge key={k} kind="key">
                      {k}
                    </Badge>
                  ))}
                </span>
              </button>
            ))}
            <div className="popover-sep" />
            <div className="qfoot">
              {t('↩ sends as {mode} · Change in Settings').replace('{mode}', name)}
            </div>
          </div>
        </Popover>
      </span>
    </span>
  );
}
