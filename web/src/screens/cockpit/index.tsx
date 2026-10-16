import type {
  DeviceInfo,
  GlobalSearchHit,
  LiveSessionMeta,
  RuntimeCapabilities,
  SessionPatchRequest,
  SessionRuntimeInfo,
} from '@hoplet/shared';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Toast, type ToastState, useToast } from '../../components/Toast.tsx';
import { postAddProject } from '../../lib/api.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { pageKeysSuspended } from '../../lib/page-keys.ts';
import {
  changesOf,
  Inspector,
  useHandoff,
  useInspectorView,
  useWorkspaceData,
  type WorkspaceFixture,
} from '../workspace/index.tsx';
import { ArchivedPage, archivedCount, withSessions } from './archived.tsx';
import { Conversation, type ConversationFixture } from './conversation/conversation.tsx';
import { Draft, type DraftFixture, type DraftWhere } from './draft.tsx';
import { buildTree, isArchived, sessionTargets } from './group.ts';
import type { NewSessionRequest, RuntimeState } from './model.ts';
import { Palette } from './palette.tsx';
import { RenameDialog } from './rename-dialog.tsx';
import { type SessionActions, sessionMenu } from './session-actions.tsx';
import { AddProject, type AddProjectFixture } from './sidebar/add-project.tsx';
import { Sidebar } from './sidebar/sidebar.tsx';
import type { MachineViews } from './sidebar/sidebar-view.ts';
import { useWatchedProjects } from './sidebar/use-watched-projects.ts';
import { EmptyState, TopBar } from './topbar.tsx';
import { useDevices } from './use-devices.ts';
import type { SearchState } from './use-search.ts';
import '../../components/components.css';
import './shell.css';
import './conversation/conversation.css';
import './composer/composer.css';

/** Development fixtures for /__states: they replace what the API would serve. */
export type CockpitFixture = {
  query?: string;
  search?: SearchState;
  conversation?: ConversationFixture;
  voiceAvailable?: boolean;
  aroundLine?: number;
  devices?: DeviceInfo[];
  /** The inspector's data and view; without it a fixture's inspector reads nothing. */
  workspace?: WorkspaceFixture;
  sidebarFolded?: boolean;
  paletteOpen?: boolean;
  /** Opens New session as a draft, here or in the first watched directory. */
  draft?: DraftFixture & Partial<DraftWhere>;
  inspectorOpen?: boolean;
  hoverDir?: string;
  hoverDevice?: string;
  /** Opens Add project for this machine, browsing what the fixture lists. */
  addProject?: { deviceName: string; browse: AddProjectFixture };
  /** Directories to start folded, as `dirKey` writes them. */
  foldedDirs?: readonly string[];
  /** The machines' view options to start with, by device ID. */
  machineViews?: MachineViews;
  /** The machine whose view menu starts open. */
  menuDevice?: string;
  /** Opens the session actions menu in the top bar, with this submenu open. */
  actionsMenu?: { sub?: string };
  /** Opens the rename dialog for the open session, with this text in the field. */
  rename?: string;
  /** Shows the Archived page. */
  archived?: boolean;
  toast?: ToastState;
  now?: number;
};

export function Cockpit(props: {
  sessions: LiveSessionMeta[];
  selected: string | null;
  onSelect: (id: string | null) => void;
  /** Changes a session's title override, pin or archive state; resolves when the core has it. */
  onPatchSession?: (sessionId: string, patch: SessionPatchRequest) => Promise<void>;
  /** This window's link to the core. */
  live: boolean;
  error: string | null;
  host?: string;
  /** The latest runtime state per session from the socket; it wins over what the session list carried. */
  runtime?: Record<string, SessionRuntimeInfo>;
  /** Starts a session; resolves when it exists, rejects with the reason it could not start. */
  onCreateSession?: (request: NewSessionRequest) => Promise<void>;
  /** Opens the Settings page, which replaces this screen. */
  onSettings?: () => void;
  fixture?: CockpitFixture;
}) {
  const { sessions, selected, onSelect, live, error, fixture } = props;
  const openSettings = () => props.onSettings?.();
  const host = props.host ?? (typeof location === 'undefined' ? '' : location.host);
  const [sidebarFolded, setSidebarFolded] = useState(fixture?.sidebarFolded ?? false);
  const [paletteOpen, setPaletteOpen] = useState(fixture?.paletteOpen ?? false);
  // New session is a draft in place of the conversation; it holds where the session will start.
  const [draft, setDraft] = useState<Partial<DraftWhere> | null>(
    fixture?.draft
      ? {
          ...(fixture.draft.deviceName ? { deviceName: fixture.draft.deviceName } : {}),
          ...(fixture.draft.path ? { path: fixture.draft.path } : {}),
        }
      : null,
  );
  const [dirsVersion, setDirsVersion] = useState(0);
  const [addFor, setAddFor] = useState<string | null>(fixture?.addProject?.deviceName ?? null);
  const [inspectorOpen, setInspectorOpen] = useState(fixture?.inspectorOpen ?? false);
  const [aroundLine, setAroundLine] = useState<number | null>(fixture?.aroundLine ?? null);
  const [archivedOpen, setArchivedOpen] = useState(fixture?.archived ?? false);
  const [renaming, setRenaming] = useState<LiveSessionMeta | null>(null);
  const [toast, say] = useToast(fixture?.toast ?? null);
  const devices = useDevices(fixture?.devices);
  const now = useNow(fixture?.now);

  const session = sessions.find((s) => s.sessionId === selected) ?? null;
  // Unknown until the session list names the session: nothing is read before then.
  const online = session ? session.ownerConnected : null;
  const workspace = fixture
    ? (fixture.workspace ?? { data: { phase: 'loading' as const } })
    : undefined;
  const { data, retry } = useWorkspaceData(selected, online, inspectorOpen, workspace?.data);
  const changes = changesOf(data);
  const [view, inspector] = useInspectorView(workspace ? (workspace.view ?? {}) : undefined);
  const handoff = useHandoff({
    sessionId: selected,
    ownerDeviceId: session?.ownerDeviceId,
    ownerName: session?.deviceName || t('the owner'),
    devices,
    origin:
      data.phase === 'live'
        ? data.listing.origin
        : data.phase === 'snapshot'
          ? data.snapshot.origin
          : undefined,
    open: inspectorOpen && view.handoffOpen,
    fixture: workspace?.handoff,
  });
  // A wide inspector folds the sidebar on narrower windows, so the conversation keeps its room.
  const narrow = useNarrowWindow();
  const [sidebarPinned, setSidebarPinned] = useState(false);
  const wide = inspectorOpen && session !== null && view.opened !== null && view.tabs.length > 0;
  const sidebarHidden = sidebarFolded || (wide && narrow && !sidebarPinned);
  useEffect(() => {
    if (!wide) setSidebarPinned(false);
  }, [wide]);

  /** An Edit row's open-diff: the path's diff when it is changed, else the file. */
  const openDiff = useCallback(
    (path: string) => {
      setInspectorOpen(true);
      const changed = changes.some((c) => c.path === path) || !online;
      inspector.openPath(path, changed ? 'diff' : 'file', changed ? 'changes' : 'files');
    },
    [changes, online, inspector],
  );

  const runtimeOf = useCallback(
    (s: LiveSessionMeta): SessionRuntimeInfo =>
      props.runtime?.[s.sessionId] ?? s.runtime ?? { state: s.ownerConnected ? 'idle' : 'offline' },
    [props.runtime],
  );
  const states = useMemo(() => {
    const out: Record<string, RuntimeState> = {};
    for (const s of sessions) out[s.sessionId] = runtimeOf(s).state;
    return out;
  }, [sessions, runtimeOf]);

  /** What a session's driver can do: as its process reported, else as its machine announced. */
  const capabilitiesOf = (s: LiveSessionMeta): RuntimeCapabilities | undefined =>
    runtimeOf(s).capabilities ??
    devices
      .find((d) => d.deviceId === s.ownerDeviceId)
      ?.providers.find((p) => p.provider === s.provider)?.capabilities;

  const { watched, remember } = useWatchedProjects(devices, !fixture);
  // Archived sessions stay in the store and keep syncing; the sidebar and the palette list only the others.
  const visible = useMemo(() => sessions.filter((s) => !isArchived(s)), [sessions]);
  const groups = useMemo(() => buildTree(visible, devices, watched), [visible, devices, watched]);
  const targets = useMemo(() => sessionTargets(groups), [groups]);
  const archivedGroups = useMemo(
    () =>
      withSessions(
        buildTree(
          sessions.filter((s) => isArchived(s)),
          devices,
          {},
        ),
      ),
    [sessions, devices],
  );
  const archived = archivedCount(archivedGroups);

  /** Applies a change; `null` when it went through, else why it did not. */
  const apply = useCallback(
    async (id: string, change: SessionPatchRequest): Promise<unknown> => {
      try {
        await props.onPatchSession?.(id, change);
      } catch (e) {
        return e;
      }
      // The archived session was the open one: the next visible session opens, or none.
      if (change.archived === true && id === selected) {
        const rest = visible.filter((s) => s.sessionId !== id);
        const at = visible.findIndex((s) => s.sessionId === id);
        onSelect((rest[at] ?? rest[at - 1] ?? null)?.sessionId ?? null);
      }
      return null;
    },
    [props.onPatchSession, selected, visible, onSelect],
  );
  const patch = useCallback(
    async (id: string, change: SessionPatchRequest) => {
      const failure = await apply(id, change);
      if (failure !== null) say(errorText(failure), true);
    },
    [apply, say],
  );
  const restore = useCallback(
    async (ids: string[]) => {
      const results = await Promise.all(ids.map((id) => apply(id, { archived: false })));
      const failed = results.filter((r) => r !== null);
      if (failed.length === 0)
        say(
          t(ids.length === 1 ? '{n} session restored' : '{n} sessions restored').replace(
            '{n}',
            String(ids.length),
          ),
        );
      else
        say(
          t(
            failed.length === 1
              ? '{n} session could not be restored'
              : '{n} sessions could not be restored',
          ).replace('{n}', String(failed.length)),
          true,
        );
    },
    [apply, say],
  );
  const actions: SessionActions = useMemo(
    () => ({ patch, rename: setRenaming, say }),
    [patch, say],
  );

  function pickSession(id: string) {
    setArchivedOpen(false);
    setDraft(null);
    setAroundLine(null);
    inspector.reset();
    onSelect(id);
  }

  function pickHit(hit: GlobalSearchHit) {
    setArchivedOpen(false);
    setDraft(null);
    inspector.reset();
    setAroundLine(hit.lineNo);
    onSelect(hit.sessionId);
  }

  // A directory row's button presets the draft; ⌘N starts where the open session runs.
  const openNewSession = useCallback(
    (target?: DraftWhere) => {
      setArchivedOpen(false);
      setAddFor(null);
      setPaletteOpen(false);
      setDraft(
        target ?? (session ? { deviceName: session.deviceName, path: session.projectPath } : {}),
      );
    },
    [session],
  );

  const openAddProject = useCallback((deviceName: string) => {
    setPaletteOpen(false);
    setAddFor(deviceName);
  }, []);
  const addTarget = groups.find((g) => g.name === addFor) ?? null;

  // The session actions' shortcuts act on the selected session: ⌥⌘R rename, ⌥⌘P pin, ⇧⌘A archive.
  // ⇧⌘A is also the browser's tab search in Chrome on macOS; the page takes it while it has the focus.
  const shortcut = useRef<(e: KeyboardEvent) => void>(() => undefined);
  // They are ⌘-chords, so they also fire from the composer; a dialog owns the keys while it is open. A held key
  // acts once, and AltGr or an input method composing is not a chord.
  shortcut.current = (e) => {
    if (pageKeysSuspended()) return;
    if (!(e.metaKey || e.ctrlKey) || e.repeat || e.isComposing || e.getModifierState('AltGraph'))
      return;
    if (!session || draft || archivedOpen || renaming || paletteOpen || addFor) return;
    if (e.altKey && !e.shiftKey && e.code === 'KeyR') {
      e.preventDefault();
      setRenaming(session);
    } else if (e.altKey && !e.shiftKey && e.code === 'KeyP') {
      e.preventDefault();
      void patch(session.sessionId, { pinned: session.pinnedAt === undefined });
    } else if (e.shiftKey && !e.altKey && e.code === 'KeyA') {
      e.preventDefault();
      void patch(session.sessionId, { archived: !isArchived(session) });
    }
  };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => shortcut.current(e);
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (pageKeysSuspended() || !(e.metaKey || e.ctrlKey) || e.altKey) return;
      if (e.shiftKey) {
        // ⇧⌘G and ⇧⌘E open Changes and Files in the inspector.
        const tool = { g: 'changes', e: 'files' }[e.key.toLowerCase()] as
          | 'changes'
          | 'files'
          | undefined;
        if (tool && selected) {
          e.preventDefault();
          setInspectorOpen(true);
          inspector.openTool(tool);
        }
        return;
      }
      if (e.key === 'k') {
        e.preventDefault();
        setAddFor(null);
        setPaletteOpen((v) => !v);
      } else if (e.key === 'n') {
        e.preventDefault();
        openNewSession();
      } else if (e.key === '\\') {
        e.preventDefault();
        setSidebarFolded((v) => !v);
      } else if (e.key === 'i' && selected) {
        e.preventDefault();
        setInspectorOpen((v) => !v);
      } else if (e.key === ',') {
        e.preventDefault();
        props.onSettings?.();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [selected, openNewSession, inspector, props.onSettings]);

  return (
    <div className={`cockpit${wide ? ' inspector-wide' : ''}`} data-testid="cockpit">
      {sidebarHidden ? null : (
        <Sidebar
          groups={groups}
          selected={draft || archivedOpen ? null : selected}
          runtime={states}
          now={now}
          host={host}
          live={live}
          draft={draft}
          {...(fixture?.hoverDir ? { hoverDir: fixture.hoverDir } : {})}
          {...(fixture?.hoverDevice ? { hoverDevice: fixture.hoverDevice } : {})}
          {...(fixture?.foldedDirs ? { foldedDirs: fixture.foldedDirs } : {})}
          {...(fixture?.machineViews ? { views: fixture.machineViews } : {})}
          {...(fixture?.menuDevice ? { menuDevice: fixture.menuDevice } : {})}
          archivedCount={archived}
          archivedOpen={archivedOpen}
          menuFor={(s) => sessionMenu(s, actions)}
          onArchived={() => {
            setDraft(null);
            setArchivedOpen(true);
          }}
          onSelect={pickSession}
          onNewSession={openNewSession}
          onAddProject={openAddProject}
          onSearch={() => setPaletteOpen(true)}
          onFold={() => setSidebarFolded(true)}
          onSettings={openSettings}
        />
      )}
      <main className="main">
        {archivedOpen ? (
          <ArchivedPage
            groups={archivedGroups}
            now={now}
            onRestore={(ids) => void restore(ids)}
            onBack={() => setArchivedOpen(false)}
          />
        ) : (
          <>
            <TopBar
              session={draft ? null : session}
              {...(session && !draft ? { actions: sessionMenu(session, actions) } : {})}
              {...(fixture?.actionsMenu ? { menuOpen: true } : {})}
              {...(fixture?.actionsMenu?.sub ? { menuSub: fixture.actionsMenu.sub } : {})}
              {...(draft ? { title: t('New session') } : {})}
              sidebarFolded={sidebarHidden}
              inspectorOpen={inspectorOpen}
              changes={changes.length}
              onShowSidebar={() => {
                setSidebarFolded(false);
                setSidebarPinned(true);
              }}
              onToggleInspector={() => setInspectorOpen((v) => !v)}
            />
            {error ? (
              <div className="banner-error" role="alert">
                {error}
              </div>
            ) : null}
            {draft ? (
              <Draft
                targets={targets}
                where={draft}
                onWhere={setDraft}
                loadDirectories={!fixture}
                dirsVersion={dirsVersion}
                voiceAvailable={fixture?.voiceAvailable ?? false}
                {...(fixture?.draft ? { fixture: fixture.draft } : {})}
                onAddProject={openAddProject}
                onSubmit={async (request) => {
                  await props.onCreateSession?.(request);
                  setDraft(null);
                }}
              />
            ) : session ? (
              <Conversation
                key={session.sessionId}
                session={session}
                runtime={runtimeOf(session)}
                live={live}
                aroundLine={aroundLine}
                shortcuts={!paletteOpen && !addFor}
                voiceAvailable={fixture?.voiceAvailable ?? false}
                capabilities={capabilitiesOf(session)}
                onHandoff={() => {
                  setInspectorOpen(true);
                  inspector.setHandoffOpen(true);
                }}
                openDiff={openDiff}
                {...(fixture?.conversation ? { fixture: fixture.conversation } : {})}
              />
            ) : selected ? (
              <p className="conv-empty" role="status" data-testid="cockpit.loading-session">
                {t('Loading…')}
              </p>
            ) : (
              <EmptyState />
            )}
          </>
        )}
        {toast ? <Toast {...(toast.failed ? { failed: true } : {})}>{toast.text}</Toast> : null}
      </main>
      {inspectorOpen && session && !draft ? (
        <Inspector
          key={session.sessionId}
          session={session}
          data={data}
          onRetry={retry}
          view={view}
          actions={inspector}
          handoff={handoff}
          busy={['running', 'waiting-permission'].includes(runtimeOf(session).state)}
          onHide={() => setInspectorOpen(false)}
          {...(workspace ? { fixture: workspace } : {})}
        />
      ) : null}
      {renaming || fixture?.rename !== undefined ? (
        <RenameDialog
          session={(renaming ?? session) as LiveSessionMeta}
          {...(fixture?.rename !== undefined ? { initial: fixture.rename } : {})}
          onSave={(title) => {
            const target = renaming ?? session;
            setRenaming(null);
            if (target) void patch(target.sessionId, { title });
          }}
          onClose={() => setRenaming(null)}
        />
      ) : null}
      {paletteOpen ? (
        <Palette
          sessions={sessions}
          {...(fixture?.query ? { initialQuery: fixture.query } : {})}
          {...(fixture?.search ? { searchFixture: fixture.search } : {})}
          onClose={() => setPaletteOpen(false)}
          onSelectSession={pickSession}
          onSelectHit={pickHit}
          onNewSession={() => openNewSession()}
          onSettings={openSettings}
        />
      ) : null}
      {addFor && addTarget ? (
        <AddProject
          machines={groups.map((g) => ({ deviceId: g.deviceId, name: g.name, online: g.online }))}
          initialMachine={addTarget.name}
          {...(fixture?.addProject ? { fixture: fixture.addProject.browse } : {})}
          onClose={() => setAddFor(null)}
          onSubmit={async (deviceId, path) => {
            const listing = await postAddProject(deviceId, path);
            remember(
              listing.deviceId,
              listing.projects.map((p) => p.path),
            );
            setAddFor(null);
            // Adding a project from a draft starts the draft there.
            if (draft) {
              setDraft({ deviceName: addTarget?.name ?? '', path });
              setDirsVersion((v) => v + 1);
            }
          }}
        />
      ) : null}
    </div>
  );
}

/** Whether the window is narrower than a wide inspector, the conversation's 440 px and the sidebar need. */
const NARROW = '(max-width: 1407px)';

function useNarrowWindow(): boolean {
  const [query] = useState(() =>
    typeof window !== 'undefined' && window.matchMedia ? window.matchMedia(NARROW) : null,
  );
  const [narrow, setNarrow] = useState(query?.matches ?? false);
  useEffect(() => {
    if (!query) return;
    const onChange = () => setNarrow(query.matches);
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }, [query]);
  return narrow;
}

/** The current time, refreshed every minute so the sidebar ages read true; fixtures pin it. */
function useNow(pinned?: number): number {
  const [now, setNow] = useState(() => pinned ?? Date.now());
  useEffect(() => {
    if (pinned !== undefined) return;
    const tick = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(tick);
  }, [pinned]);
  return pinned ?? now;
}
