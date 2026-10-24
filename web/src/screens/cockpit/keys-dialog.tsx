import { useState } from 'react';
import { Badge } from '../../components/Badge.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { TextField } from '../../components/TextField.tsx';
import { t } from '../../lib/i18n.ts';
import { filterShortcuts, type ShortcutGroup } from '../../lib/shortcuts.ts';

/** The dialog's two columns: what reaches things on the left, what acts inside a session on the right. */
const COLUMNS: readonly (readonly ShortcutGroup[])[] = [
  ['General', 'Sessions', 'Palette'],
  ['Workspace', 'Composer'],
];

/** Keyboard shortcuts (⌘/): every shortcut of the registry by group, filtered by label or key. */
export function KeysDialog(props: { onClose: () => void; initialFilter?: string }) {
  const [filter, setFilter] = useState(props.initialFilter ?? '');
  const groups = filterShortcuts(filter, (s) => t(s.label));
  return (
    <Dialog
      label={t('Keyboard shortcuts')}
      size="wide"
      className="keys"
      onClose={props.onClose}
      data-testid="cockpit.shortcuts"
    >
      <div className="dlg-h">
        <h2 className="kt">{t('Keyboard shortcuts')}</h2>
        <span className="sp" />
        <span className="kf">
          {Icon.search()}
          <TextField
            bare
            label={t('Filter shortcuts')}
            placeholder={t('Filter')}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            autoComplete="off"
            spellCheck={false}
            data-autofocus
            data-testid="cockpit.shortcuts-filter"
          />
        </span>
        <IconButton label={t('Close (esc)')} icon={Icon.close()} onClick={props.onClose} />
      </div>
      <div className="kgrid">
        {COLUMNS.map((column) => (
          <div key={column.join()}>
            {groups
              .filter((g) => column.includes(g.group))
              .map((g) => (
                <section key={g.group} className="kgrp" data-testid="cockpit.shortcuts-group">
                  <h3>{t(g.group)}</h3>
                  {g.items.map((s) => (
                    <div key={s.id} className="kr" data-testid="cockpit.shortcut">
                      <span className="d">
                        {t(s.label)}
                        {s.browser ? <span className="fn">*</span> : null}
                      </span>
                      <span className="ks">
                        {s.keys.map((k) => (
                          <Badge key={k} kind="key">
                            {k}
                          </Badge>
                        ))}
                      </span>
                    </div>
                  ))}
                </section>
              ))}
          </div>
        ))}
      </div>
      {groups.length === 0 ? (
        <p className="knone" role="status">
          {t('No shortcut matches “{text}”').replace('{text}', filter.trim())}
        </p>
      ) : null}
      <p className="knote">
        {`* ${t(
          'A browser tab may take this chord first: ⌘N opens a window in Chrome, ⇧⌘A is Chrome’s tab search, ⌘I is Page Info in Firefox, Ctrl+Alt+arrows can rotate the screen on Windows. Every chord also has a palette entry, so nothing is reachable only from the keyboard. On Windows and Linux ⌘ reads Ctrl.',
        )}`}
      </p>
    </Dialog>
  );
}
