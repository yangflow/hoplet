import type { Budget, LiveSessionMeta, SessionUsage } from '@hoplet/shared';
import { useRef, useState } from 'react';
import { Badge } from '../../components/Badge.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { HopletMark, Icon } from '../../components/icons.tsx';
import { Menu, type MenuEntry } from '../../components/Menu.tsx';
import { ProviderMark } from '../../components/provider-marks.tsx';
import { t } from '../../lib/i18n.ts';
import { displayTitle } from './group.ts';
import { SessionUsageChip } from './session-usage.tsx';

/** The session's title and the inspector toggle, which moves into the inspector while it is open. */
export function TopBar(props: {
  session: LiveSessionMeta | null;
  /** A title when no session is shown: New session for a draft. */
  title?: string;
  sidebarFolded: boolean;
  inspectorOpen: boolean;
  changes: number;
  /** The session's actions, for the ··· menu; without them the button is absent. */
  actions?: MenuEntry[];
  /** Starts with the actions menu open (development fixtures). */
  menuOpen?: boolean;
  menuSub?: string;
  menuItem?: string;
  /** Hides estimated cost in the usage chip. */
  tokensOnly?: boolean;
  /** The usage chip's data, for development fixtures, and whether its popover starts open. */
  usage?: { data: SessionUsage; open?: boolean; budgets?: readonly Budget[] };
  onShowSidebar: () => void;
  onToggleInspector: () => void;
}) {
  const { session } = props;
  const [menu, setMenu] = useState(props.menuOpen ?? false);
  const [byKey, setByKey] = useState(false);
  const moreRef = useRef<HTMLSpanElement>(null);
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
      {session ? (
        <>
          <h1 className="title">{displayTitle(session)}</h1>
          {session.configuration ? (
            <span
              className="ctag"
              title={t('Configuration {name}').replace('{name}', session.configuration.name)}
              data-testid="cockpit.configuration-tag"
            >
              <ProviderMark provider={session.provider} />
              {session.configuration.name}
            </span>
          ) : null}
        </>
      ) : props.title ? (
        <h1 className="title">{props.title}</h1>
      ) : null}
      <span className="top-gap" />
      {session && !props.title ? (
        <SessionUsageChip
          sessionId={session.sessionId}
          refresh={session.lineCount}
          tokensOnly={props.tokensOnly ?? false}
          projectPath={session.projectPath}
          ownerDeviceId={session.ownerDeviceId}
          {...(props.usage
            ? {
                fixture: props.usage.data,
                open: props.usage.open ?? false,
                budgetsFixture: props.usage.budgets ?? [],
              }
            : {})}
        />
      ) : null}
      {session && props.actions ? (
        <span className="more" ref={moreRef}>
          <IconButton
            label={t('Session actions')}
            icon={Icon.ellipsis()}
            on={menu}
            aria-haspopup="menu"
            aria-expanded={menu}
            onClick={(e) => {
              // A click with no pointer (Enter or Space on the button) opens the menu for the keyboard.
              setByKey(e.detail === 0);
              setMenu((v) => !v);
            }}
            data-testid="cockpit.session-actions"
          />
          {menu ? (
            <Menu
              label={t('Session actions')}
              entries={props.actions}
              className="mn-top"
              keyboard={byKey || !!props.menuItem}
              {...(props.menuSub ? { initialSub: props.menuSub } : {})}
              {...(props.menuItem ? { initialItem: props.menuItem } : {})}
              onClose={(restore) => {
                setMenu(false);
                if (restore) moreRef.current?.querySelector('button')?.focus();
              }}
              data-testid="cockpit.actions-menu"
            />
          ) : null}
        </span>
      ) : null}
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
