import { type KeyboardEvent, type ReactNode, useEffect, useRef } from 'react';

const FOCUSABLE =
  'button:not([disabled]), [href], input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * A centred modal over a scrim: the command palette, New session and Add project. Focus moves in and is trapped, Esc and
 * a press on the scrim close it, and focus returns to what had it before.
 */
export function Dialog(props: {
  label: string;
  onClose: () => void;
  size?: 'palette' | 'dialog' | 'wide';
  children: ReactNode;
  className?: string;
  'data-testid'?: string;
}) {
  const { label, onClose, size = 'dialog', children } = props;
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const before = document.activeElement as HTMLElement | null;
    const el = ref.current;
    const first =
      el?.querySelector<HTMLElement>('[data-autofocus]') ??
      el?.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? el)?.focus();
    return () => before?.focus?.();
  }, []);

  function onKeyDown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.stopPropagation();
      onClose();
      return;
    }
    if (e.key !== 'Tab') return;
    const items = [...(ref.current?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [])];
    const first = items[0];
    const last = items[items.length - 1];
    if (!first || !last) {
      e.preventDefault();
      return;
    }
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  }

  return (
    <>
      <div className="scrim" onPointerDown={onClose} />
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={label}
        tabIndex={-1}
        className={`modal modal-${size}${props.className ? ` ${props.className}` : ''}`}
        onKeyDown={onKeyDown}
        data-testid={props['data-testid']}
      >
        {children}
      </div>
    </>
  );
}
