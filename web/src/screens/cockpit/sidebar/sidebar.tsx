import type { LiveSessionMeta, Template } from '@hoplet/shared';
import {
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { Badge } from '../../../components/Badge.tsx';
import { Button } from '../../../components/Button.tsx';
import { IconButton } from '../../../components/IconButton.tsx';
import { HopletMark, Icon } from '../../../components/icons.tsx';
import { MachineIcon } from '../../../components/MachineIcon.tsx';
import { Menu, type MenuEntry } from '../../../components/Menu.tsx';
import { ProviderMark } from '../../../components/provider-marks.tsx';
import { Row } from '../../../components/Row.tsx';
import { type GlyphState, glyphState, StateGlyph } from '../../../components/StateGlyph.tsx';
import { t } from '../../../lib/i18n.ts';
import { machineLabel, systemOf } from '../../../lib/system.ts';
import { type DeviceGroup, displayTitle } from '../group.ts';
import type { RuntimeState } from '../model.ts';
import type { EntryChoice } from '../template-entry.tsx';
import { DEVICE_ROW, dirKey, useFoldedDirs } from './folded-dirs.ts';
import { GroupMenu } from './group-menu.tsx';
import { MachineMenu } from './machine-menu.tsx';
import { NewMenu } from './new-menu.tsx';
import {
  type Bucket,
  filterByTag,
  type Grouping,
  type Located,
  pinnedAcross,
  projectGroups,
  tagCounts,
  timeGroups,
  useGrouping,
} from './organize.ts';
import { compactRel } from './rel.ts';
import {
  flatList,
  type MachineViews,
  pinnedList,
  type Sort,
  sortedDirs,
  useMachineViews,
  viewKey,
} from './sidebar-view.ts';

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
  /** Shows the pin glyph before the time (Flat, where pinned rows lead). */
  pinGlyph?: boolean;
  onSelect: (id: string) => void;
  /** Opens the session's actions at a point of the window; `opener` gets the focus back. */
  onMenu: (
    session: LiveSessionMeta,
    at: { x: number; y: number },
    opener: HTMLElement,
    keyboard: boolean,
  ) => void;
}) {
  const { session, state, selected, dim, now, where, onSelect } = props;
  const title = displayTitle(session);
  const glyph = glyphState(state);
  // The menu key also fires a native `contextmenu` (at 0,0, on key up): it anchors to the row like the key did.
  const keyAt = useRef(0);
  const anchor = (row: HTMLElement) => {
    const box = row.getBoundingClientRect();
    props.onMenu(session, { x: box.left + 32, y: box.bottom }, row, true);
  };
  const onContextMenu = (e: MouseEvent<HTMLElement>) => {
    e.preventDefault();
    const fromKey = Date.now() - keyAt.current < 1000 || (e.clientX === 0 && e.clientY === 0);
    if (fromKey) anchor(e.currentTarget);
    else props.onMenu(session, { x: e.clientX, y: e.clientY }, e.currentTarget, false);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    if (e.key !== 'ContextMenu' && !(e.key === 'F10' && e.shiftKey)) return;
    e.preventDefault();
    keyAt.current = Date.now();
    anchor(e.currentTarget);
  };
  return (
    <Row
      leading={<ProviderMark provider={session.provider} />}
      trailing={
        glyph && !dim ? (
          <StateGlyph state={glyph} />
        ) : (
          <>
            {props.pinGlyph ? (
              <span className="glyph" role="img" title={t('Pinned')} aria-label={t('Pinned')}>
                {Icon.pin()}
              </span>
            ) : null}
            <span>{compactRel(session.updatedAt, now)}</span>
          </>
        )
      }
      selected={selected}
      dim={dim}
      title={where ? `${title}\n${where}` : title}
      aria-current={selected ? 'true' : undefined}
      onPress={() => onSelect(session.sessionId)}
      onContextMenu={onContextMenu}
      onKeyDown={onKeyDown}
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

/** A draft (New session): the selected row at the top of its directory until its first prompt starts it. */
function DraftRow() {
  return (
    <Row
      leading={Icon.compose()}
      selected
      aria-current="true"
      className="sess draft"
      data-testid="cockpit.draft-row"
    >
      {t('New session')}
    </Row>
  );
}

/** The key the project rows fold under, beside the machines' names. */
const PROJECT_ROWS = '\u0001project';

const bucketLabel = (b: Bucket): string =>
  ({
    today: t('Today'),
    yesterday: t('Yesterday'),
    week: t('This week'),
    earlier: t('Earlier'),
  })[b];

/** A folder row that folds what it holds: the whole row is the button, the folder glyph says which. */
function FolderRow(props: {
  name: string;
  /** A quiet note after the name: how many machines a project joins, or that it has no remote. */
  note?: string;
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
        <span className="dn">
          {props.name}
          {props.note ? <span className="prj-o">{props.note}</span> : null}
        </span>
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
  /** An open draft and where it will start; its row shows in that directory. */
  draft: { deviceName?: string; path?: string } | null;
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
  /** The grouping to start with, replacing what the browser remembers, and whether its menu starts open (development fixtures). */
  grouping?: Grouping;
  groupMenu?: boolean;
  /** The tag the list starts filtered by (development fixtures). */
  tagFilter?: string;
  /** Archived sessions: the footer button opens the Archived page. */
  archivedCount?: number;
  archivedOpen?: boolean;
  /** The actions of a session, for its row's context menu. */
  menuFor?: (session: LiveSessionMeta) => MenuEntry[];
  onArchived?: () => void;
  /** The chevron beside New session: the templates to start from. */
  templates?: {
    choices: readonly EntryChoice[];
    onPick: (template: Template) => void;
    onManage: () => void;
    /** Starts open (development fixtures). */
    open?: boolean;
  };
  onSelect: (id: string) => void;
  onNewSession: (target?: { deviceName: string; path: string }) => void;
  onAddProject: (deviceName: string) => void;
  onSearch: () => void;
  onFold: () => void;
  onSettings: () => void;
  /** Opens Settings > Machines on a machine's tab. */
  onResources?: (deviceId: string) => void;
}) {
  const { selected, runtime, now, host, live, draft } = props;
  const folding = useFoldedDirs(props.foldedDirs);
  const { viewOf, setView } = useMachineViews(props.views);
  const [grouping, setGrouping] = useGrouping(props.grouping);
  const [tagPicked, setTag] = useState<string | null>(props.tagFilter ?? null);
  const tags = useMemo(
    () => tagCounts(props.groups.flatMap((g) => g.dirs.flatMap((d) => d.sessions))),
    [props.groups],
  );
  // A tag no session carries any more filters nothing: the list shows whole again.
  const tag = tagPicked !== null && tags.some((x) => x.tag === tagPicked) ? tagPicked : null;
  const total = useMemo(
    () => props.groups.reduce((n, g) => n + g.dirs.reduce((m, d) => m + d.sessions.length, 0), 0),
    [props.groups],
  );
  const groups = useMemo(() => filterByTag(props.groups, tag), [props.groups, tag]);
  const shown = groups.reduce((n, g) => n + g.dirs.reduce((m, d) => m + d.sessions.length, 0), 0);
  const projects = useMemo(
    () => (grouping === 'project' ? projectGroups(groups) : []),
    [grouping, groups],
  );
  const treeRef = useRef<HTMLElement>(null);
  const [ctx, setCtx] = useState<{
    session: LiveSessionMeta;
    at: { x: number; y: number };
    opener: HTMLElement;
    keyboard: boolean;
  } | null>(null);

  // The sidebar follows the selected session, whoever selected it (a click, the palette, the URL): its
  // machine and directory open and it scrolls into view. Other folds stay as the reader left them.
  const at = useMemo(() => locate(groups, selected), [groups, selected]);
  const scrollPending = useRef(false);
  const { reveal } = folding;
  const atFlat = at ? viewOf(at.view).organize === 'flat' : false;
  const atProject = projects.find((p) =>
    p.sessions.some((l) => l.session.sessionId === selected),
  )?.key;
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new selection reveals again; `at` is read through its parts
  useEffect(() => {
    scrollPending.current = selected !== null;
    if (atProject) reveal(dirKey(PROJECT_ROWS, atProject));
    else if (at)
      reveal(dirKey(at.device, DEVICE_ROW), ...(atFlat ? [] : [dirKey(at.device, at.path)]));
  }, [selected, at?.device, at?.path, atFlat, atProject, reveal]);
  // A draft's directory opens as well, so its row shows.
  useEffect(() => {
    if (!draft?.deviceName) return;
    reveal(
      dirKey(draft.deviceName, DEVICE_ROW),
      ...(draft.path ? [dirKey(draft.deviceName, draft.path)] : []),
    );
  }, [draft?.deviceName, draft?.path, reveal]);
  useEffect(() => {
    if (!scrollPending.current) return;
    const row = treeRef.current?.querySelector('[aria-current="true"]');
    if (!row) return;
    scrollPending.current = false;
    row.scrollIntoView?.({ block: 'nearest' });
  });

  const row = (session: LiveSessionMeta, online: boolean, where?: string, pinGlyph?: boolean) => (
    <SessionRow
      key={session.sessionId}
      session={session}
      state={runtime[session.sessionId] ?? 'idle'}
      selected={session.sessionId === selected}
      dim={!online}
      now={now}
      {...(where ? { where } : {})}
      {...(pinGlyph ? { pinGlyph } : {})}
      onSelect={props.onSelect}
      onMenu={(s, at, opener, keyboard) =>
        props.menuFor && setCtx({ session: s, at, opener, keyboard })
      }
    />
  );

  /** A machine's directories with their sessions. */
  function folders(g: DeviceGroup, sort: Sort) {
    const pinned = pinnedList(g, sort);
    const dirs = sortedDirs(g, sort).map((d) => {
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
          {folded ? null : (
            <>
              {draft?.deviceName === g.name && draft.path === d.path ? <DraftRow /> : null}
              {d.sessions.map((s) => row(s, g.online))}
            </>
          )}
        </div>
      );
    });
    return [
      ...(pinned.length > 0
        ? [
            <div className="grp" key="pinned" data-testid="cockpit.pinned">
              <div className="grp-h">
                <span className="fi">{Icon.pin()}</span>
                {t('Pinned')}
                <span className="cnt">{pinned.length}</span>
              </div>
              {pinned.map((p) => row(p.session, g.online, p.dir))}
            </div>,
          ]
        : []),
      ...dirs,
    ];
  }

  /** A machine's sessions as one list, each with the name of its directory. */
  function flat(g: DeviceGroup, sort: Sort) {
    const pinned = pinnedList(g, sort);
    return (
      <div className="grp flat">
        {draft?.deviceName === g.name ? <DraftRow /> : null}
        {pinned.map((p) => row(p.session, g.online, p.dir, true))}
        {pinned.length > 0 ? <div className="rule" /> : null}
        {flatList(g, sort).map((p) => row(p.session, g.online, p.dir))}
      </div>
    );
  }

  /** The pinned sessions of every machine, each with its machine and folder: they lead Project and Time. */
  function pinnedAll() {
    const pinned = pinnedAcross(groups);
    if (pinned.length === 0) return null;
    return (
      <div className="grp" key="pinned" data-testid="cockpit.pinned">
        <div className="grp-h">
          <span className="fi">{Icon.pin()}</span>
          {t('Pinned')}
          <span className="cnt">{pinned.length}</span>
        </div>
        {pinned.map((l) => row(l.session, l.online, `${l.device} · ${l.dir}`))}
      </div>
    );
  }

  const draftRow = () => (draft && grouping !== 'machine' ? <DraftRow /> : null);

  /** One group per project across machines (ADR 0041); a folder without a remote stays its machine's. */
  function byProject() {
    return (
      <div className="devg" data-testid="cockpit.projects">
        {draftRow()}
        {pinnedAll()}
        {projects.map((p) => {
          const folded = folding.isFolded(PROJECT_ROWS, p.key);
          const online = p.sessions.some((l) => l.online);
          const note = !p.repository
            ? t('no remote')
            : p.machines > 1
              ? t('{n} machines').replace('{n}', String(p.machines))
              : undefined;
          return (
            <div className="grp" key={p.key} data-testid="cockpit.project">
              <FolderRow
                name={p.name}
                {...(note ? { note } : {})}
                title={p.repository ?? `${p.sessions[0]?.device ?? ''} · ${p.name}`}
                folded={folded}
                dim={!online}
                count={p.sessions.length}
                urgent={
                  folded
                    ? urgentState({ sessions: p.sessions.map((l) => l.session) }, runtime)
                    : null
                }
                hover={false}
                onToggle={() => folding.toggle(PROJECT_ROWS, p.key)}
                testid="cockpit.project-toggle"
              />
              {folded ? null : p.sessions.map((l: Located) => row(l.session, l.online, l.device))}
            </div>
          );
        })}
      </div>
    );
  }

  /** The sessions by last activity: Today, Yesterday, This week, Earlier. */
  function byTime() {
    return (
      <div className="devg" data-testid="cockpit.by-time">
        {draftRow()}
        {pinnedAll()}
        {timeGroups(groups, now).map((b) => (
          <div className="grp" key={b.bucket} data-testid="cockpit.time-bucket">
            <div className="grp-h">
              <span className="fi">{Icon.clock()}</span>
              {bucketLabel(b.bucket)}
              <span className="cnt">{b.sessions.length}</span>
            </div>
            {b.sessions.map((l) => row(l.session, l.online, `${l.device} · ${l.dir}`))}
          </div>
        ))}
      </div>
    );
  }

  function machines() {
    return groups.map((g) => {
      const key = viewKey(g);
      const view = viewOf(key);
      const deviceFolded = folding.isFolded(g.name, DEVICE_ROW);
      const label = machineLabel(g.name, g.system, g.online);
      const warnings = [
        g.warn?.memory && g.resources
          ? t('memory {n}% used').replace(
              '{n}',
              `${Math.round((g.resources.memUsed / g.resources.memTotal) * 100)}`,
            )
          : null,
        g.warn?.disk && g.resources
          ? t('disk {n}% free').replace(
              '{n}',
              `${Math.round((g.resources.diskFree / g.resources.diskTotal) * 100)}`,
            )
          : null,
      ].filter((x): x is string => x !== null);
      const warnText = t('{name}: {warnings} · details in Settings › Machines')
        .replace('{name}', g.name)
        .replace('{warnings}', warnings.join(', '));
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
              aria-label={label}
              title={label}
              onClick={() => folding.toggle(g.name, DEVICE_ROW)}
              data-testid="cockpit.device-toggle"
            >
              <span className="lead">
                <MachineIcon kind={systemOf(g.system?.os)} online={g.online} />
              </span>
              <span className="dn">{g.name}</span>
              <span className="dev-chv">{Icon.chevron()}</span>
            </button>
            {warnings.length > 0 ? (
              <span
                className="rm"
                role="img"
                title={warnText}
                aria-label={warnText}
                data-testid="cockpit.resource-warning"
              >
                {Icon.alert()}
              </span>
            ) : null}
            <MachineMenu
              name={g.name}
              {...(g.resources && g.deviceId
                ? { onResources: () => props.onResources?.(g.deviceId as string) }
                : {})}
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
          <GroupMenu
            grouping={grouping}
            onChange={setGrouping}
            initialOpen={props.groupMenu ?? false}
          />
          <IconButton
            label={t('New session')}
            hint="⌘N"
            icon={Icon.compose()}
            on={draft !== null}
            onClick={() => props.onNewSession()}
            data-testid="cockpit.new-session"
          />
          {props.templates ? (
            <NewMenu
              choices={props.templates.choices}
              onBlank={() => props.onNewSession()}
              onPick={props.templates.onPick}
              onManage={props.templates.onManage}
              initialOpen={props.templates.open ?? false}
            />
          ) : null}
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
      {tags.length > 0 ? (
        <div
          className="tag-strip"
          role="toolbar"
          aria-label={t('Filter by tag')}
          data-testid="cockpit.tag-strip"
        >
          {tags.map((x) => (
            <button
              key={x.tag}
              type="button"
              className={`fch${x.tag === tag ? ' on' : ''}`}
              aria-pressed={x.tag === tag}
              onClick={() => setTag(x.tag === tag ? null : x.tag)}
              data-testid="cockpit.tag-chip"
            >
              {x.tag}
              <span className="c">{x.count}</span>
            </button>
          ))}
        </div>
      ) : null}
      {tag ? (
        <div className="fbar" role="status" data-testid="cockpit.tag-filter">
          <span>
            {t('{n} of {m} sessions · tagged {tag}')
              .replace('{n}', String(shown))
              .replace('{m}', String(total))
              .replace('{tag}', tag)}
          </span>
          <Button variant="quiet" onClick={() => setTag(null)} data-testid="cockpit.tag-clear">
            {t('Clear')}
          </Button>
        </div>
      ) : null}
      <nav ref={treeRef} className="tree" aria-label={t('Sessions')} data-testid="cockpit.tree">
        {props.groups.length === 0 ? (
          <p className="tree-empty" data-testid="cockpit.empty">
            {t('No sessions synced yet')}
          </p>
        ) : grouping === 'project' ? (
          byProject()
        ) : grouping === 'time' ? (
          byTime()
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
        {props.onArchived ? (
          <IconButton
            label={t('Archived')}
            detail={`${t('Archived')} · ${props.archivedCount ?? 0}`}
            icon={Icon.archive()}
            on={props.archivedOpen ?? false}
            onClick={props.onArchived}
            data-testid="cockpit.archived"
          />
        ) : null}
        <IconButton
          label={t('Settings')}
          hint="⌘,"
          icon={Icon.gear()}
          on={props.settingsOpen ?? false}
          onClick={props.onSettings}
          data-testid="cockpit.settings"
        />
      </div>
      {ctx && props.menuFor ? (
        <Menu
          label={t('Session actions')}
          entries={props.menuFor(ctx.session)}
          at={ctx.at}
          keyboard={ctx.keyboard}
          onClose={(restore) => {
            setCtx(null);
            if (restore) ctx.opener.focus();
          }}
          data-testid="cockpit.row-menu"
        />
      ) : null}
    </aside>
  );
}
