import { type ReactNode, useEffect, useRef } from 'react';

/**
 * A floating panel under its trigger. The parent is the positioning context (`position: relative`);
 * pressing outside it or Esc closes it.
 */
export function Popover(props: {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  className?: string;
}) {
  const { open, onClose, children, className } = props;
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      const host = ref.current?.parentElement;
      if (host && !host.contains(e.target as Node)) onClose();
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div ref={ref} className={`popover${className ? ` ${className}` : ''}`}>
      {children}
    </div>
  );
}
