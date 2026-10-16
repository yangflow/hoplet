import type { LiveSessionMeta } from '@hoplet/shared';
import { type FormEvent, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { Icon } from '../../components/icons.tsx';
import { TextField } from '../../components/TextField.tsx';
import { t } from '../../lib/i18n.ts';
import { displayTitle } from './group.ts';

/** The longest title the core keeps (shared `MAX_TITLE_OVERRIDE_CHARS`). */
const MAX = 200;

/**
 * Renames a session in Hoplet only: the vendor's own title is shown beside the field and comes back when the
 * field is cleared or "Use Claude's title" is pressed. Resolves `onSave` with the title, or `null` for none.
 */
export function RenameDialog(props: {
  session: LiveSessionMeta;
  /** Starts with this text in the field (development fixtures). */
  initial?: string;
  onSave: (title: string | null) => void;
  onClose: () => void;
}) {
  const { session } = props;
  const [value, setValue] = useState(props.initial ?? displayTitle(session));
  const vendor = session.title || t('Untitled session');

  function submit(e: FormEvent) {
    e.preventDefault();
    const title = value.trim();
    props.onSave(title === '' || title === session.title ? null : title);
  }

  return (
    <Dialog label={t('Rename session')} onClose={props.onClose} data-testid="cockpit.rename">
      <form onSubmit={submit}>
        <div className="dlg-h">{t('Rename session')}</div>
        <div className="dlg-b">
          <TextField
            label={t('Session title')}
            value={value}
            maxLength={MAX}
            data-autofocus=""
            onFocus={(e) => e.currentTarget.select()}
            onChange={(e) => setValue(e.target.value)}
            data-testid="cockpit.rename-field"
          />
          <div className="dlg-u">
            <span>{t('Claude’s title')}</span>
            <span className="vt" data-testid="cockpit.rename-vendor">
              {vendor}
            </span>
            <span className="gap" />
            <Button
              variant="quiet"
              icon={Icon.retry()}
              onClick={() => setValue(session.title)}
              data-testid="cockpit.rename-reset"
            >
              {t('Use Claude’s title')}
            </Button>
          </div>
        </div>
        <div className="dlg-f">
          <Button variant="quiet" onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button variant="primary" type="submit" data-testid="cockpit.rename-save">
            {t('Save')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
