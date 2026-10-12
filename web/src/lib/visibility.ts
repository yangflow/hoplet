import { useEffect, useState } from 'react';

/** Whether this tab is visible; polling pauses while it is not (Page Visibility). */
export function pageVisible(): boolean {
  return typeof document === 'undefined' || document.visibilityState !== 'hidden';
}

export function usePageVisible(): boolean {
  const [visible, setVisible] = useState(pageVisible);
  useEffect(() => {
    const onChange = () => setVisible(pageVisible());
    document.addEventListener('visibilitychange', onChange);
    return () => document.removeEventListener('visibilitychange', onChange);
  }, []);
  return visible;
}
