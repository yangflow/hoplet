import { useCallback, useEffect, useRef, useState } from 'react';
import { Icon } from './icons.tsx';

/** How long a toast stays. */
export const TOAST_MS = 3000;

export type ToastState = { text: string; failed?: boolean };

/** A short confirmation at the foot of the main pane, in the style of the jump-to-end pill. */
export function Toast(props: { children: string; failed?: boolean }) {
  return (
    <div className={`toast${props.failed ? ' failed' : ''}`} role="status" data-testid="toast">
      {props.failed ? Icon.alert() : Icon.check()}
      {props.children}
    </div>
  );
}

/** The toast on screen, if any, and `show` to put one up; it clears itself. */
export function useToast(
  initial: ToastState | null = null,
): [ToastState | null, (text: string, failed?: boolean) => void] {
  const [toast, setToast] = useState<ToastState | null>(initial);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const show = useCallback((text: string, failed?: boolean) => {
    clearTimeout(timer.current);
    setToast(failed ? { text, failed } : { text });
    timer.current = setTimeout(() => setToast(null), TOAST_MS);
  }, []);
  useEffect(() => () => clearTimeout(timer.current), []);
  return [toast, show];
}
