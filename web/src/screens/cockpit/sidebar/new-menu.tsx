import type { Template } from '@hoplet/shared';
import { type KeyboardEvent, useCallback, useEffect, useRef, useState } from 'react';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { t } from '../../../lib/i18n.ts';
import { type EntryChoice, menuDetail } from '../template-entry.tsx';

/**
 * The chevron beside New session: Blank session, then the templates, then Manage templates. A template starts a
 * session at once when it asks for nothing, else the fill dialog opens; what a pick does is up to the cockpit.
 */
export function NewMenu(props: {
  choices: readonly EntryChoice[];
  onBlank: () => void;
  onPick: (template: Template) => void;
  onManage: () => void;
  /** Starts open (development fixtures). */
  initialOpen?: boolean;
}) {
  const [open, setOpen] = useState(props.initialOpen ?? false);
  const hostRef = useRef<HTMLSpanElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const focusOnOpen = useRef(false);

  const close = useCallback((restoreFocus: boolean) => {
    setOpen(false);
    if (restoreFocus) hostRef.current?.querySelector<HTMLElement>('.ib')?.focus();
  }, []);

  useEffect(() => {
    if (!open || !focusOnOpen.current) return;
    focusOnOpen.current = false;
    menuRef.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
  }, [open]);

  function onKeyDown(e: KeyboardEvent) {
    const items = [...(menuRef.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])];
    const at = items.indexOf(document.activeElement as HTMLElement);
    const go = (i: number) => items[(i + items.length) % items.length]?.focus();
    if (e.key === 'ArrowDown') go(at + 1);
    else if (e.key === 'ArrowUp') go(at < 0 ? -1 : at - 1);
    else if (e.key === 'Home') go(0);
    else if (e.key === 'End') go(-1);
    else if (e.key === 'Escape') close(true);
    else if (e.key === 'Tab') setOpen(false);
    else return;
    if (e.key !== 'Tab') {
      e.preventDefault();
      e.stopPropagation();
    }
  }

  const choose = (run: () => void) => () => {
    close(false);
    run();
  };

  return (
    <span ref={hostRef} className="new-menu">
      <IconButton
        label={t('New session from a template')}
        icon={Icon.down()}
        on={open}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => {
          focusOnOpen.current = !open;
          setOpen((v) => !v);
        }}
        data-testid="cockpit.new-menu"
      />
      <Popover open={open} onClose={() => setOpen(false)} className="mn tp-pop">
        <div
          ref={menuRef}
          role="menu"
          aria-label={t('New session')}
          onKeyDown={onKeyDown}
          data-testid="cockpit.new-popover"
        >
          <button
            type="button"
            role="menuitem"
            className="menu-item one"
            tabIndex={-1}
            onClick={choose(props.onBlank)}
            data-testid="cockpit.new-blank"
          >
            <span className="menu-check">{Icon.compose()}</span>
            {t('Blank session')}
            <span className="sc">⌘N</span>
          </button>
          <hr className="menu-sep" />
          {/* biome-ignore lint/a11y/useSemanticElements: a menu's groups are role=group, a fieldset is not a menu child */}
          <div role="group" aria-label={t('Templates')}>
            <div className="menu-label" aria-hidden="true">
              {t('Templates')}
            </div>
            {props.choices.length === 0 ? (
              <p className="menu-none">{t('No templates yet')}</p>
            ) : (
              props.choices.map((c) => (
                <button
                  key={c.template.id}
                  type="button"
                  role="menuitem"
                  className="menu-item"
                  tabIndex={-1}
                  onClick={choose(() => props.onPick(c.template))}
                  data-testid="cockpit.new-template"
                >
                  {Icon.template()}
                  <span className="tx">
                    <b>{c.template.name}</b>
                    <span>{menuDetail(c)}</span>
                  </span>
                </button>
              ))
            )}
          </div>
          <hr className="menu-sep" />
          <button
            type="button"
            role="menuitem"
            className="menu-item one"
            tabIndex={-1}
            onClick={choose(props.onManage)}
            data-testid="cockpit.new-manage"
          >
            {Icon.gear()}
            {t('Manage templates…')}
          </button>
        </div>
      </Popover>
    </span>
  );
}
