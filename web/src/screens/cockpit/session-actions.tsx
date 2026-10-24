import type { LiveSessionMeta, SessionPatchRequest } from '@hoplet/shared';
import { Icon } from '../../components/icons.tsx';
import type { MenuEntry } from '../../components/Menu.tsx';
import { copyLater, copyText } from '../../lib/clipboard.ts';
import { t } from '../../lib/i18n.ts';
import { isArchived, isPinned } from './group.ts';
import { lastAnswerMarkdown } from './last-answer.ts';

/** The shortcuts of the actions, shown in the menu and handled by the cockpit. */
export const SHORTCUT = { rename: '⌥⌘R', pin: '⌥⌘P', archive: '⇧⌘A' } as const;

/** The address that opens a session in this core. */
export function sessionLink(sessionId: string): string {
  return `${location.origin}/?session=${encodeURIComponent(sessionId)}`;
}

export type SessionActions = {
  /** Applies a change to the session's state; resolves when the core has it. */
  patch: (sessionId: string, patch: SessionPatchRequest) => Promise<void>;
  rename: (session: LiveSessionMeta) => void;
  /** Opens the delete dialog (ADR 0023). */
  remove: (session: LiveSessionMeta) => void;
  /** Opens the save dialog, prefilled from the session's first prompt and options. */
  saveTemplate: (session: LiveSessionMeta) => void;
  /** Opens the export dialog (ADR 0040). */
  exportMarkdown: (session: LiveSessionMeta) => void;
  /** Says what happened: a copy was made, or it could not be. */
  say: (text: string, failed?: boolean) => void;
};

/** The session actions menu: Rename and Pin | Copy, Open in new window, Save as template and Export | Archive | Delete. */
export function sessionMenu(session: LiveSessionMeta, actions: SessionActions): MenuEntry[] {
  const id = session.sessionId;
  const pinned = isPinned(session);
  const archived = isArchived(session);
  const copy = async (text: string | null, done: string) => {
    if (text !== null && (await copyText(text))) actions.say(done);
    else actions.say(text === null ? t('No answer to copy') : t('Could not copy'), true);
  };
  return [
    {
      kind: 'item',
      id: 'rename',
      label: t('Rename'),
      icon: Icon.pen(),
      shortcut: SHORTCUT.rename,
      onSelect: () => actions.rename(session),
    },
    {
      kind: 'item',
      id: 'pin',
      label: pinned ? t('Unpin') : t('Pin'),
      icon: pinned ? Icon.unpin() : Icon.pin(),
      shortcut: SHORTCUT.pin,
      onSelect: () => void actions.patch(id, { pinned: !pinned }),
    },
    { kind: 'separator', id: 'sep-1' },
    {
      kind: 'submenu',
      id: 'copy',
      label: t('Copy'),
      icon: Icon.copy(),
      items: [
        {
          kind: 'item',
          id: 'copy-id',
          label: t('Session ID'),
          onSelect: () => void copy(id, t('Session ID copied')),
        },
        {
          kind: 'item',
          id: 'copy-link',
          label: t('Link'),
          onSelect: () => void copy(sessionLink(id), t('Link copied')),
        },
        {
          kind: 'item',
          id: 'copy-answer',
          label: t('Last answer as Markdown'),
          // The write starts inside the click, with the answer still to come (Safari and Firefox need that).
          onSelect: () =>
            void copyLater(() => lastAnswerMarkdown(id)).then((result) =>
              result === 'copied'
                ? actions.say(t('Answer copied'))
                : actions.say(
                    result === 'empty' ? t('No answer to copy') : t('Could not copy'),
                    true,
                  ),
            ),
        },
      ],
    },
    {
      kind: 'item',
      id: 'open',
      label: t('Open in new window'),
      icon: Icon.external(),
      onSelect: () => {
        window.open(sessionLink(id), '_blank', 'noopener,width=1280,height=860');
      },
    },
    {
      kind: 'item',
      id: 'save-template',
      label: t('Save as template…'),
      icon: Icon.template(),
      onSelect: () => actions.saveTemplate(session),
    },
    {
      kind: 'item',
      id: 'export',
      label: t('Export as Markdown…'),
      icon: Icon.fileDown(),
      onSelect: () => actions.exportMarkdown(session),
    },
    { kind: 'separator', id: 'sep-2' },
    {
      kind: 'item',
      id: 'archive',
      label: archived ? t('Unarchive') : t('Archive'),
      icon: archived ? Icon.restore() : Icon.archive(),
      shortcut: SHORTCUT.archive,
      onSelect: () => void actions.patch(id, { archived: !archived }),
    },
    { kind: 'separator', id: 'sep-3' },
    {
      kind: 'item',
      id: 'delete',
      label: t('Delete…'),
      icon: Icon.trash(),
      danger: true,
      onSelect: () => actions.remove(session),
    },
  ];
}
