import type { LiveSessionMeta } from '@hoplet/shared';
import { Badge } from '../../components/Badge.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { HopletMark, Icon } from '../../components/icons.tsx';
import { t } from '../../lib/i18n.ts';

/** The session's title and the inspector toggle, which moves into the inspector while it is open. */
export function TopBar(props: {
  session: LiveSessionMeta | null;
  sidebarFolded: boolean;
  inspectorOpen: boolean;
  changes: number;
  onShowSidebar: () => void;
  onToggleInspector: () => void;
}) {
  const { session } = props;
  return (
    <header className="top" data-testid="cockpit.topbar">
      {props.sidebarFolded ? (
        <IconButton
          label={t('Show sidebar')}
          hint="⌘\"
          icon={Icon.sidebar()}
          onClick={props.onShowSidebar}
          data-testid="cockpit.sidebar-show"
        />
      ) : null}
      {session ? <h1 className="title">{session.title || t('Untitled session')}</h1> : null}
      <span className="top-gap" />
      {session && !props.inspectorOpen ? (
        <IconButton
          label={t('Workspace')}
          hint="⌘I"
          detail={
            props.changes > 0
              ? t('Workspace · {n} changed').replace('{n}', String(props.changes))
              : t('Workspace')
          }
          icon={Icon.panel()}
          pressed={props.inspectorOpen}
          dot={props.changes > 0}
          onClick={props.onToggleInspector}
          data-testid="cockpit.workspace-toggle"
        />
      ) : null}
    </header>
  );
}

/** No session selected: the way in is search or a new session. */
export function EmptyState() {
  return (
    <div className="empty" data-testid="cockpit.no-session">
      <div className="empty-in">
        <span className="tile" aria-hidden="true">
          <HopletMark size={22} width={2.6} />
        </span>
        <h1 className="empty-title">{t('Pick a session, or start a new one')}</h1>
        <span className="empty-keys">
          <span>
            <Badge kind="key">⌘K</Badge>
            {t('search')}
          </span>
          <span>
            <Badge kind="key">⌘N</Badge>
            {t('new session')}
          </span>
        </span>
      </div>
    </div>
  );
}
