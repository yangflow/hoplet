import {
  type KeyboardEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { t } from '../../../lib/i18n.ts';
import { GROUPINGS, type Grouping } from './organize.ts';

const buttonLabel = (g: Grouping): string =>
  ({ machine: t('Group: Machine'), project: t('Group: Project'), time: t('Group: Time') })[g];
const itemLabel = (g: Grouping): string =>
  ({ machine: t('Machine'), project: t('Project'), time: t('Last activity') })[g];
const itemIcon = (g: Grouping): ReactNode =>
  ({ machine: Icon.laptop(), project: Icon.folder(), time: Icon.clock() })[g];

/**
 * The Group button in the sidebar's header and its menu: Machine, Project or Last activity. A choice applies at
 * once, closes the menu and is remembered in this browser.
 */
export function GroupMenu(props: {
  grouping: Grouping;
  onChange: (g: Grouping) => void;
  /** Starts open (development fixtures). */
  initialOpen?: boolean;
}) {
  const { grouping, onChange } = props;
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
    menuRef.current?.querySelector<HTMLElement>('[aria-checked="true"]')?.focus();
  }, [open]);

  function onKeyDown(e: KeyboardEvent) {
    const items = [
      ...(menuRef.current?.querySelectorAll<HTMLElement>('[role="menuitemradio"]') ?? []),
    ];
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

  return (
    <span ref={hostRef} className="group-menu">
      <IconButton
        label={buttonLabel(grouping)}
        icon={grouping === 'time' ? Icon.clock() : Icon.layers()}
        on={open}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => {
          focusOnOpen.current = !open;
          setOpen((v) => !v);
        }}
        data-testid="cockpit.group"
      />
      <Popover open={open} onClose={() => setOpen(false)} className="group-pop">
        <div
          ref={menuRef}
          role="menu"
          aria-label={t('Group sessions by')}
          onKeyDown={onKeyDown}
          data-testid="cockpit.group-menu"
        >
          <div className="menu-label" aria-hidden="true">
            {t('Group sessions by')}
          </div>
          {GROUPINGS.map((g) => (
            <button
              key={g}
              type="button"
              role="menuitemradio"
              aria-checked={g === grouping}
              className="menu-item"
              tabIndex={-1}
              onClick={() => {
                onChange(g);
                close(true);
              }}
              data-testid={`cockpit.group-${g}`}
            >
              <span className="menu-check">{itemIcon(g)}</span>
              {itemLabel(g)}
              <span className="menu-check group-check">{g === grouping ? Icon.check() : null}</span>
            </button>
          ))}
        </div>
        <hr className="menu-sep" />
        <p className="menu-note">
          {t(
            'Project joins the machines that share a git remote. Pinned stays on top in every grouping.',
          )}
        </p>
      </Popover>
    </span>
  );
}
