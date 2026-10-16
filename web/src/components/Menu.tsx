import {
  type CSSProperties,
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { Icon } from './icons.tsx';

/** One command of a menu: an icon column, a label and a keycap. */
export type MenuCommand = {
  kind: 'item';
  id: string;
  label: string;
  icon?: ReactNode;
  shortcut?: string;
  onSelect: () => void;
};
/** A command that opens a list of commands to its side. */
export type MenuSubmenu = {
  kind: 'submenu';
  id: string;
  label: string;
  icon?: ReactNode;
  items: MenuCommand[];
};
export type MenuEntry = MenuCommand | MenuSubmenu | { kind: 'separator'; id: string };

const ITEM = '[role="menuitem"]';

/** The items of one menu level, not those of a submenu inside it. */
function itemsOf(level: HTMLElement): HTMLElement[] {
  return [...level.querySelectorAll<HTMLElement>(ITEM)].filter(
    (el) => el.closest('[role="menu"]') === level,
  );
}

/**
 * A menu of commands in groups closed by hairlines, one level of submenu. It is positioned by its parent
 * (`position: relative` around the trigger) or, with `at`, at a point of the window (a context menu), moved
 * back inside the window when it would leave it. Arrows, Home and End move, → opens a submenu and ← closes it,
 * Esc closes with the focus given back to the caller, a press outside closes. A chosen command closes the menu.
 */
export function Menu(props: {
  label: string;
  entries: MenuEntry[];
  /** `restoreFocus` is set when the menu closed by Esc or by a chosen command: the trigger gets the focus back. */
  onClose: (restoreFocus: boolean) => void;
  /** A context menu opens here, in window coordinates. */
  at?: { x: number; y: number };
  className?: string;
  /** The keyboard opened it: the focused item shows its ring at once; a pointer-opened menu shows it after a key. */
  keyboard?: boolean;
  /** Starts with this submenu open (development fixtures). */
  initialSub?: string;
  'data-testid'?: string;
}) {
  const { entries, onClose, at } = props;
  const rootRef = useRef<HTMLDivElement>(null);
  const [sub, setSub] = useState<{ id: string; top: number } | null>(null);
  const [flip, setFlip] = useState(false);
  const [place, setPlace] = useState<{ left: number; top: number } | null>(null);
  const focusSub = useRef(false);
  const [kb, setKb] = useState(props.keyboard ?? false);

  useEffect(() => {
    const root = rootRef.current;
    root?.querySelector<HTMLElement>(ITEM)?.focus();
    const open = props.initialSub
      ? root?.querySelector<HTMLElement>(`[data-submenu="${props.initialSub}"]`)
      : null;
    if (open && props.initialSub) setSub({ id: props.initialSub, top: open.offsetTop });
  }, [props.initialSub]);

  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      // A press on the trigger (inside the menu's parent) is the trigger's to answer: it toggles.
      const inside = at ? rootRef.current : rootRef.current?.parentElement;
      if (!inside?.contains(e.target as Node)) onClose(false);
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [onClose, at]);

  // A context menu that would leave the window moves back in; a submenu that would leave it opens to the left.
  useLayoutEffect(() => {
    const el = rootRef.current;
    if (!el || !at) return;
    const r = el.getBoundingClientRect();
    const left = Math.max(8, Math.min(at.x, window.innerWidth - r.width - 8));
    const top = Math.max(8, Math.min(at.y, window.innerHeight - r.height - 8));
    setPlace({ left, top });
  }, [at]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: measured when a submenu opens or the menu moves
  useLayoutEffect(() => {
    const el = rootRef.current;
    if (!el || !sub) return;
    const r = el.getBoundingClientRect();
    setFlip(r.right + r.width > window.innerWidth);
  }, [sub?.id, place]);

  useEffect(() => {
    if (!sub || !focusSub.current) return;
    focusSub.current = false;
    rootRef.current?.querySelector<HTMLElement>('[data-sub="true"] [role="menuitem"]')?.focus();
  }, [sub]);

  function openSub(id: string, from: HTMLElement, focus: boolean) {
    focusSub.current = focus;
    setSub({ id, top: from.offsetTop });
  }

  function choose(command: MenuCommand) {
    // The focus goes back first: a command that opens a dialog finds the trigger focused and returns to it.
    onClose(true);
    command.onSelect();
  }

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    setKb(true);
    const level = (e.target as HTMLElement).closest<HTMLElement>('[role="menu"]');
    if (!level) return;
    const items = itemsOf(level);
    const cur = items.indexOf(document.activeElement as HTMLElement);
    const go = (i: number) => items[(i + items.length) % items.length]?.focus();
    const inSub = level.dataset.sub === 'true';
    if (e.key === 'ArrowDown') go(cur + 1);
    else if (e.key === 'ArrowUp') go(cur < 0 ? -1 : cur - 1);
    else if (e.key === 'Home') go(0);
    else if (e.key === 'End') go(-1);
    else if (e.key === 'ArrowRight' && !inSub) {
      const current = document.activeElement as HTMLElement | null;
      const id = current?.dataset.submenu;
      if (!current || !id) return;
      openSub(id, current, true);
    } else if (e.key === 'ArrowLeft' && inSub) {
      const parent = rootRef.current?.querySelector<HTMLElement>(`[data-submenu="${sub?.id}"]`);
      setSub(null);
      parent?.focus();
    } else if (e.key === 'Escape') onClose(true);
    else if (e.key === 'Tab') onClose(false);
    else return;
    if (e.key !== 'Tab') {
      e.preventDefault();
      e.stopPropagation();
    }
  }

  const style: CSSProperties | undefined = at
    ? { position: 'fixed', left: place?.left ?? at.x, top: place?.top ?? at.y }
    : undefined;
  const open = entries.find((e): e is MenuSubmenu => e.kind === 'submenu' && e.id === sub?.id);

  return (
    <div
      ref={rootRef}
      role="menu"
      aria-label={props.label}
      className={`mn${props.className ? ` ${props.className}` : ''}`}
      style={style}
      data-kb={kb ? 'true' : undefined}
      onKeyDown={onKeyDown}
      data-testid={props['data-testid']}
    >
      {entries.map((entry) => {
        if (entry.kind === 'separator') return <hr key={entry.id} className="menu-sep" />;
        if (entry.kind === 'item')
          return (
            <button
              key={entry.id}
              type="button"
              role="menuitem"
              className="menu-item"
              tabIndex={-1}
              onMouseEnter={() => setSub(null)}
              onClick={() => choose(entry)}
              data-testid={`menu.${entry.id}`}
            >
              <span className="menu-check">{entry.icon}</span>
              {entry.label}
              {entry.shortcut ? <span className="sc">{entry.shortcut}</span> : null}
            </button>
          );
        const isOpen = sub?.id === entry.id;
        return (
          <button
            key={entry.id}
            type="button"
            role="menuitem"
            className={`menu-item${isOpen ? ' on' : ''}`}
            tabIndex={-1}
            aria-haspopup="menu"
            aria-expanded={isOpen}
            data-submenu={entry.id}
            onMouseEnter={(e) => openSub(entry.id, e.currentTarget, false)}
            onClick={(e) => (isOpen ? setSub(null) : openSub(entry.id, e.currentTarget, true))}
            data-testid={`menu.${entry.id}`}
          >
            <span className="menu-check">{entry.icon}</span>
            {entry.label}
            <span className="chv">{Icon.chevron()}</span>
          </button>
        );
      })}
      {open && sub ? (
        <div
          role="menu"
          aria-label={open.label}
          data-sub="true"
          className={`mn mn-sub${flip ? ' flip' : ''}`}
          style={{ top: sub.top - 4 }}
        >
          {open.items.map((item) => (
            <button
              key={item.id}
              type="button"
              role="menuitem"
              className="menu-item"
              tabIndex={-1}
              onClick={() => choose(item)}
              data-testid={`menu.${item.id}`}
            >
              {item.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
