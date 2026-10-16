import { useState } from 'react';
import { t } from '../lib/i18n.ts';
import { Button } from './Button.tsx';
import { IconButton } from './IconButton.tsx';
import { Icon } from './icons.tsx';

const DISMISSED = 'hoplet.notify-hint-dismissed';

/** Whether this browser blocks notifications for the site and the person has not said "not now". */
export function notifyHintWanted(): boolean {
  if (typeof Notification === 'undefined' || Notification.permission !== 'denied') return false;
  try {
    return localStorage.getItem(DISMISSED) !== '1';
  } catch {
    return true;
  }
}

/** The in-page hint of design/screens/action.html#hint-blocked: shown once per browser until dismissed. */
export function NotifyHint(props: { onHowTo: () => void; onDismiss: () => void }) {
  return (
    <div className="nbanner" role="note" data-testid="notify.hint">
      {Icon.bellOff()}
      <div className="bt">
        <b>{t('Notifications are blocked in this browser.')}</b>
        <p>{t('You will not hear when Claude needs you while this tab is in the background.')}</p>
        <div className="ba">
          <Button onClick={props.onHowTo} data-testid="notify.hint.how">
            {t('How to allow them')}
          </Button>
          <Button variant="quiet" onClick={props.onDismiss} data-testid="notify.hint.later">
            {t('Not now')}
          </Button>
        </div>
      </div>
      <IconButton label={t('Dismiss')} icon={Icon.close()} onClick={props.onDismiss} />
    </div>
  );
}

/** The hint with its own memory: it hides itself for good when dismissed. */
export function NotifyHintGate(props: { onHowTo: () => void }) {
  const [show, setShow] = useState(notifyHintWanted);
  if (!show) return null;
  return (
    <NotifyHint
      onHowTo={props.onHowTo}
      onDismiss={() => {
        try {
          localStorage.setItem(DISMISSED, '1');
        } catch {
          // The hint stays hidden for this page view only.
        }
        setShow(false);
      }}
    />
  );
}
