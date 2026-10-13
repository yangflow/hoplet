import { type KeyboardEvent, useCallback, useEffect, useId, useRef, useState } from 'react';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { t } from '../../../lib/i18n.ts';
import { type MachineView, ORGANIZE, type Organize, SORTS, type Sort } from './sidebar-view.ts';

const organizeLabel = (o: Organize): string => ({ folder: t('By folder'), flat: t('Flat') })[o];
const sortLabel = (s: Sort): string =>
  ({ activity: t('Recent activity'), created: t('Created'), title: t('Title') })[s];

/**
 * A machine row's view options: how its sessions are organised and in which order. One menu of two radio
 * groups under the machine's name; a choice applies at once and closes the menu.
 */
export function MachineMenu(props: {
  /** The machine's name: the title of the menu. */
  name: string;
  view: MachineView;
  onChange: (patch: Partial<MachineView>) => void;
  /** Starts open (development fixtures). */
  initialOpen?: boolean;
}) {
  const { view, onChange } = props;
  const [open, setOpen] = useState(props.initialOpen ?? false);
  const hostRef = useRef<HTMLSpanElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const focusOnOpen = useRef(false);
  const id = useId();

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

  const item = <V extends string>(
    group: 'organize' | 'sort',
    value: V,
    label: string,
    current: V,
    pick: (v: V) => void,
  ) => (
    <button
      key={value}
      type="button"
      role="menuitemradio"
      aria-checked={value === current}
      className="menu-item"
      tabIndex={-1}
      onClick={() => {
        pick(value);
        close(true);
      }}
      data-testid={`cockpit.${group}-${value}`}
    >
      <span className="menu-check">{value === current ? Icon.check() : null}</span>
      {label}
    </button>
  );

  return (
    <span ref={hostRef} className="dev-menu">
      <IconButton
        label={t('View options')}
        icon={Icon.ellipsis()}
        className="dev-more"
        on={open}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => {
          focusOnOpen.current = !open;
          setOpen((v) => !v);
        }}
        data-testid="cockpit.machine-menu"
      />
      <Popover open={open} onClose={() => setOpen(false)} className="dev-pop">
        <div
          ref={menuRef}
          role="menu"
          aria-label={props.name}
          onKeyDown={onKeyDown}
          data-testid="cockpit.machine-popover"
        >
          <div className="menu-title" aria-hidden="true">
            {props.name}
          </div>
          {/* biome-ignore lint/a11y/useSemanticElements: a menu's groups are role=group, a fieldset is not a menu child */}
          <div role="group" aria-labelledby={`${id}-organize`}>
            <div id={`${id}-organize`} className="menu-label">
              {t('Organize')}
            </div>
            {ORGANIZE.map((o) =>
              item('organize', o, organizeLabel(o), view.organize, (v) =>
                onChange({ organize: v }),
              ),
            )}
          </div>
          <hr className="menu-sep" />
          {/* biome-ignore lint/a11y/useSemanticElements: a menu's groups are role=group, a fieldset is not a menu child */}
          <div role="group" aria-labelledby={`${id}-sort`}>
            <div id={`${id}-sort`} className="menu-label">
              {t('Sort')}
            </div>
            {SORTS.map((s) =>
              item('sort', s, sortLabel(s), view.sort, (v) => onChange({ sort: v })),
            )}
          </div>
        </div>
      </Popover>
    </span>
  );
}
