import type { LiveSessionMeta } from '@hoplet/shared';
import { type ReactNode, useEffect, useMemo, useRef } from 'react';
import { Badge } from '../../components/Badge.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { HopletMark, Icon, Spinner } from '../../components/icons.tsx';
import { ProviderMark } from '../../components/provider-marks.tsx';
import { Row } from '../../components/Row.tsx';
import { t } from '../../lib/i18n.ts';
import { DEVICE_ROW, dirKey, useFoldedDirs } from './folded-dirs.ts';
import type { DeviceGroup } from './group.ts';
import { MachineMenu } from './machine-menu.tsx';
import { type GlyphState, glyphState, type RuntimeState } from './model.ts';
import { compactRel } from './rel.ts';
import { stateName } from './runtime.ts';
import {
  flatList,
  type MachineViews,
  type Sort,
  sortedDirs,
  useMachineViews,
  viewKey,
} from './sidebar-view.ts';

/** The runtime state in place of the time: a glyph whose shape differs per state and that names it. */
export function StateGlyph(props: { state: GlyphState }) {
  const name = stateName(props.state);
  const art = {
    starting: <Spinner />,
    running: <Spinner />,
    'waiting-permission': <span className="dot-need" />,
    ready: <span className="dot-ready" />,
    failed: <span className="dot-bad" />,
    external: Icon.eye(),
  }[props.state];
  return (
    <span className={`glyph glyph-${props.state}`} role="img" title={name} aria-label={name}>
      {art}
    </span>
  );
}

/** The state that needs the user, strongest first: a folded directory shows the strongest it holds. */
const URGENT: GlyphState[] = ['waiting-permission', 'failed'];

export function urgentState(
  dir: { sessions: LiveSessionMeta[] },
  runtime: Record<string, RuntimeState>,
): GlyphState | null {
  for (const state of URGENT) {
    if (dir.sessions.some((s) => runtime[s.sessionId] === state)) return state;
  }
  return null;
}

function SessionRow(props: {
  session: LiveSessionMeta;
  state: RuntimeState;
  selected: boolean;
  dim: boolean;
  now: number;
  /** Flat: the second line, the name of the session's directory. */
  where?: string;
  onSelect: (id: string) => void;
}) {
  const { session, state, selected, dim, now, where, onSelect } = props;
  const title = session.title || t('Untitled session');
  const glyph = glyphState(state);
  return (
    <Row
      leading={<ProviderMark provider={session.provider} />}
      trailing={
        glyph && !dim ? (
          <StateGlyph state={glyph} />
        ) : (
          <span>{compactRel(session.updatedAt, now)}</span>
        )
      }
      selected={selected}
      dim={dim}
      title={where ? `${title}\n${where}` : title}
      aria-current={selected ? 'true' : undefined}
      onPress={() => onSelect(session.sessionId)}
      className={`sess${where ? ' two' : ''}`}
      data-testid="cockpit.session"
    >
      {where ? (
        <span className="two-lines">
          <span className="two-title">{title}</span>
          <span className="two-where" data-testid="cockpit.session-where">
            {where}
          </span>
        </span>
      ) : (
        title
      )}
    </Row>
  );
}

/** A folder row that folds what it holds: the whole row is the button, the folder glyph says which. */
function FolderRow(props: {
  name: string;
  title: string;
  folded: boolean;
  dim: boolean;
  count: number;
  urgent: GlyphState | null;
  hover: boolean;
  onToggle: () => void;
  testid: string;
  children?: ReactNode;
}) {
  const { folded, count } = props;
  return (
    <div
      className={`sdir${folded ? '' : ' open'}${props.dim ? ' dim' : ''}`}
      title={props.title}
      data-hover={props.hover ? 'true' : undefined}
    >
      <button
        type="button"
        className="sdir-toggle"
        aria-expanded={!folded}
        onClick={props.onToggle}
        data-testid={props.testid}
      >
        <span className="fi">{folded ? Icon.folder() : Icon.folderOpen()}</span>
        <span className="dn">{props.name}</span>
        {folded ? (
          <span className="rt">
            <span
              className="cnt"
              title={t(count === 1 ? '{n} session' : '{n} sessions').replace('{n}', String(count))}
              data-testid="cockpit.directory-count"
            >
              {count}
            </span>
            {props.urgent ? <StateGlyph state={props.urgent} /> : null}
          </span>
        ) : null}
      </button>
      {props.children}
    </div>
  );
}

/** Where the selected session lives, for the sidebar to open and show it. */
function locate(groups: DeviceGroup[], id: string | null) {
  if (!id) return null;
  for (const g of groups)
    for (const d of g.dirs)
      if (d.sessions.some((s) => s.sessionId === id))
        return { device: g.name, path: d.path, view: viewKey(g) };
  return null;
}

export function Sidebar(props: {
  groups: DeviceGroup[];
  selected: string | null;
  runtime: Record<string, RuntimeState>;
  now: number;
  host: string;
  live: boolean;
  newSessionOpen: boolean;
  settingsOpen?: boolean;
  /** Shows the hover affordance of one directory (the design's empty state). */
  hoverDir?: string;
  /** Shows the hover affordance of one machine row. */
  hoverDevice?: string;
  /** The folded directories to start with, replacing what the browser remembers (development fixtures). */
  foldedDirs?: readonly string[];
  /** The views of the machines to start with, by device ID, replacing what the browser remembers (development fixtures). */
  views?: MachineViews;
  /** Starts with the view menu of this machine open (development fixtures). */
  menuDevice?: string;
  onSelect: (id: string) => void;
  onNewSession: (target?: { deviceName: string; path: string }) => void;
  onAddProject: (deviceName: string) => void;
  onSearch: () => void;
  onFold: () => void;
  onSettings: () => void;
}) {
  const { selected, runtime, now, host, live } = props;
  const folding = useFoldedDirs(props.foldedDirs);
  const { viewOf, setView } = useMachineViews(props.views);
  const groups = props.groups;
  const treeRef = useRef<HTMLElement>(null);

  // The sidebar follows the selected session, whoever selected it (a click, the palette, the URL): its
  // machine and directory open and it scrolls into view. Other folds stay as the reader left them.
  const at = useMemo(() => locate(groups, selected), [groups, selected]);
  const scrollPending = useRef(false);
  const { reveal } = folding;
  const atFlat = at ? viewOf(at.view).organize === 'flat' : false;
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new selection reveals again; `at` is read through its parts
  useEffect(() => {
    scrollPending.current = selected !== null;
    if (at) reveal(dirKey(at.device, DEVICE_ROW), ...(atFlat ? [] : [dirKey(at.device, at.path)]));
  }, [selected, at?.device, at?.path, atFlat, reveal]);
  useEffect(() => {
    if (!scrollPending.current) return;
    const row = treeRef.current?.querySelector('[aria-current="true"]');
    if (!row) return;
    scrollPending.current = false;
    row.scrollIntoView?.({ block: 'nearest' });
  });

  const row = (session: LiveSessionMeta, online: boolean, where?: string) => (
    <SessionRow
      key={session.sessionId}
      session={session}
      state={runtime[session.sessionId] ?? 'idle'}
      selected={session.sessionId === selected}
      dim={!online}
      now={now}
      {...(where ? { where } : {})}
      onSelect={props.onSelect}
    />
  );

  /** A machine's directories with their sessions. */
  function folders(g: DeviceGroup, sort: Sort) {
    return sortedDirs(g, sort).map((d) => {
      const folded = folding.isFolded(g.name, d.path);
      return (
        <div className="grp" key={d.path}>
          <FolderRow
            name={d.name}
            title={d.path}
            folded={folded}
            dim={!g.online}
            count={d.sessions.length}
            urgent={folded && g.online ? urgentState(d, runtime) : null}
            hover={props.hoverDir === d.name}
            onToggle={() => folding.toggle(g.name, d.path)}
            testid="cockpit.directory-toggle"
          >
            {g.online ? (
              <IconButton
                label={t('New session in {name}').replace('{name}', d.name)}
                icon={Icon.plus()}
                onClick={() => props.onNewSession({ deviceName: g.name, path: d.path })}
                data-testid="cockpit.dir-new"
              />
            ) : null}
          </FolderRow>
          {folded ? null : d.sessions.map((s) => row(s, g.online))}
        </div>
      );
    });
  }

  /** A machine's sessions as one list, each with the name of its directory. */
  function flat(g: DeviceGroup, sort: Sort) {
    return (
      <div className="grp flat">
        {flatList(g, sort).map((p) => row(p.session, g.online, p.dir))}
      </div>
    );
  }

  function machines() {
    return groups.map((g) => {
      const key = viewKey(g);
      const view = viewOf(key);
      const deviceFolded = folding.isFolded(g.name, DEVICE_ROW);
      const presence = g.online ? t('Online') : t('Offline');
      return (
        <div key={g.name} className={`devg${g.online ? '' : ' off'}`} data-testid="cockpit.device">
          <div
            className={`dev${deviceFolded ? '' : ' open'}`}
            data-hover={props.hoverDevice === g.name ? 'true' : undefined}
          >
            <button
              type="button"
              className="dev-toggle"
              aria-expanded={!deviceFolded}
              aria-label={`${g.name} · ${presence}`}
              onClick={() => folding.toggle(g.name, DEVICE_ROW)}
              data-testid="cockpit.device-toggle"
            >
              <span className="lead">
                <span className={`dot${g.online ? '' : ' off'}`} title={presence} />
              </span>
              <span className="dn">{g.name}</span>
              <span className="dev-chv">{Icon.chevron()}</span>
            </button>
            <MachineMenu
              name={g.name}
              view={view}
              onChange={(patch) => setView(key, patch)}
              initialOpen={props.menuDevice === g.name}
            />
            <IconButton
              label={t('Add project')}
              detail={
                g.online ? t('Add project') : t('{name} is offline').replace('{name}', g.name)
              }
              icon={Icon.plus()}
              className="dev-add"
              disabled={!g.online}
              onClick={() => props.onAddProject(g.name)}
              data-testid="cockpit.add-project"
            />
          </div>
          {deviceFolded
            ? null
            : view.organize === 'flat'
              ? flat(g, view.sort)
              : folders(g, view.sort)}
        </div>
      );
    });
  }

  return (
    <aside
      className={`side${live ? '' : ' frozen'}`}
      data-testid="cockpit.sidebar"
      aria-label={t('Sidebar')}
    >
      <div className="brand">
        <span className="mark" aria-hidden="true">
          <HopletMark />
        </span>
        <b>Hoplet</b>
        <span className="brand-gap" />
        <span className="brand-buttons">
          <IconButton
            label={t('New session')}
            hint="⌘N"
            icon={Icon.compose()}
            on={props.newSessionOpen}
            onClick={() => props.onNewSession()}
            data-testid="cockpit.new-session"
          />
          <IconButton
            label={t('Hide sidebar')}
            hint="⌘\"
            icon={Icon.sidebar()}
            onClick={props.onFold}
            data-testid="cockpit.sidebar-fold"
          />
        </span>
      </div>
      <button
        type="button"
        className="search"
        aria-keyshortcuts="Meta+K"
        title={`${t('Search')} · ⌘K`}
        onClick={props.onSearch}
        data-testid="cockpit.search"
      >
        {Icon.search()}
        <span className="search-label">{t('Search')}</span>
        <span aria-hidden="true">
          <Badge kind="key">⌘K</Badge>
        </span>
      </button>
      <nav ref={treeRef} className="tree" aria-label={t('Sessions')} data-testid="cockpit.tree">
        {groups.length === 0 ? (
          <p className="tree-empty" data-testid="cockpit.empty">
            {t('No sessions synced yet')}
          </p>
        ) : (
          machines()
        )}
      </nav>
      <div className="side-foot">
        <span
          className={`conn${live ? '' : ' lost'}`}
          role="status"
          title={
            live
              ? t('Connected to {host}').replace('{host}', host)
              : t('Disconnected from {host}').replace('{host}', host)
          }
          data-testid="cockpit.live"
        >
          <span className="lead">
            <span className="dot" aria-hidden="true" />
          </span>
          {live ? host : t('Reconnecting…')}
        </span>
        <IconButton
          label={t('Settings')}
          hint="⌘,"
          icon={Icon.gear()}
          on={props.settingsOpen ?? false}
          onClick={props.onSettings}
          data-testid="cockpit.settings"
        />
      </div>
    </aside>
  );
}
