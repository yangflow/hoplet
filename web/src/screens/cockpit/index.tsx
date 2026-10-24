import type {
  Budget,
  DeviceInfo,
  GlobalSearchHit,
  LiveSessionMeta,
  RuntimeCapabilities,
  SessionConfigChange,
  SessionPatchRequest,
  SessionRuntimeInfo,
  SessionUsage,
  Template,
} from '@hoplet/shared';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { NotifyHintGate } from '../../components/NotifyHint.tsx';
import { Toast, type ToastState, useToast } from '../../components/Toast.tsx';
import { postAddProject, postStop } from '../../lib/api.ts';
import { liveKids } from '../../lib/children.ts';
import { useConfigChanges } from '../../lib/config-changes.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { pageKeysSuspended } from '../../lib/page-keys.ts';
import { shortcutOf } from '../../lib/shortcuts.ts';
import { type ChildrenFixture, useChildren } from '../../lib/use-children.ts';
import { useDevices } from '../../lib/use-devices.ts';
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
import { DeleteDialog, type DeleteFixture } from './delete-dialog.tsx';
import { Draft, type DraftEntry, type DraftFixture, type DraftWhere } from './draft.tsx';
import { buildTree, isArchived, sessionTargets } from './group.ts';
import { KeysDialog } from './keys-dialog.tsx';
import type { NewSessionRequest, RuntimeState, TemplateStart } from './model.ts';
import { CockpitPalette, type PaletteCommands } from './palette-sources.tsx';
import { RenameDialog } from './rename-dialog.tsx';
import { type SessionActions, sessionMenu } from './session-actions.tsx';
import { AddProject, type AddProjectFixture } from './sidebar/add-project.tsx';
import { dirKey, loadFolded } from './sidebar/folded-dirs.ts';
import type { Grouping } from './sidebar/organize.ts';
import { tagCounts } from './sidebar/organize.ts';
import { Sidebar } from './sidebar/sidebar.tsx';
import {
  adjacent,
  DEFAULT_VIEW,
  loadViews,
  type MachineViews,
  sidebarOrder,
} from './sidebar/sidebar-view.ts';
import { useWatchedProjects } from './sidebar/use-watched-projects.ts';
import { type TemplateEntryFixture, useTemplateEntry } from './template-entry.tsx';
import { EmptyState, TopBar } from './topbar.tsx';
import type { SearchState } from './use-search.ts';
import '../../components/components.css';
import './shell.css';
import './conversation/conversation.css';
import './composer/composer.css';
import './queue/queue.css';

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
  /** Opens the palette asking to confirm this row (`c-stop`). */
  paletteConfirm?: string;
  /** Opens Keyboard shortcuts, with this text in its filter. */
  shortcuts?: { filter?: string };
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
  actionsMenu?: { sub?: string; item?: string };
  /** Opens the rename dialog for the open session, with this text in the field. */
  rename?: string;
  /** Opens the delete dialog for the open session, with the owner's answer to what exists. */
  delete?: DeleteFixture;
  /** Shows the Archived page, filtered by this tag when one is given. */
  archived?: boolean;
  archivedTag?: string;
  /** The sidebar's grouping, whether its menu starts open, and the tag it is filtered by. */
  grouping?: Grouping;
  groupMenu?: boolean;
  tagFilter?: string;
  /** Opens the open session's tag editor with this text in its field. */
  tagEditor?: string;
  toast?: ToastState;
  now?: number;
  /** The session's usage chip: its data and whether the popover starts open. */
  usage?: { data: SessionUsage; open?: boolean; budgets?: readonly Budget[] };
  /** The templates of the sidebar menu, the palette and the save dialog, and which of those start open. */
  templates?: TemplateEntryFixture;
  /** The open session's descendants and their permission requests, in place of asking the core. */
  children?: ChildrenFixture;
  /** The open session's requests to change its configuration, in place of asking the core. */
  configChanges?: SessionConfigChange[];
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
  /** A plan was approved into this new session (ADR 0025): the app opens it and waits for it to reach the list. */
  onSessionStarted?: (sessionId: string) => void;
  /** Opens the rules in Settings: the rule lines under tool calls link to it. */
  onManageRules?: () => void;
  /** Opens the Settings page, which replaces this screen. */
  onSettings?: () => void;
  /** A session is gone from the core: the app drops it from its lists. */
  onSessionDeleted?: (sessionId: string) => void;
  /** Opens Settings > Notifications, where a blocked browser is explained. */
  onNotifySettings?: () => void;
  /** Opens Settings > Machines on this machine's tab. */
  onMachineResources?: (deviceId: string) => void;
  /** Opens Settings > Model providers on this machine's tab. */
  onManageProviders?: (deviceId: string) => void;
  /** Starts a session from a template; resolves when it exists, rejects with the reason it could not start. */
  onStartTemplate?: (request: TemplateStart) => Promise<void>;
  /** Opens Settings > Templates. */
  onManageTemplates?: () => void;
  /** Hides estimated cost (the Appearance preference). */
  tokensOnly?: boolean;
  fixture?: CockpitFixture;
}) {
  const { sessions, selected, onSelect, live, error, fixture } = props;
  const openSettings = () => props.onSettings?.();
  const host = props.host ?? (typeof location === 'undefined' ? '' : location.host);
  const [sidebarFolded, setSidebarFolded] = useState(fixture?.sidebarFolded ?? false);
  const [paletteOpen, setPaletteOpen] = useState(fixture?.paletteOpen ?? false);
  const [shortcutsOpen, setShortcutsOpen] = useState(fixture?.shortcuts !== undefined);
  // The prompt a draft opened from the palette starts with; `n` opens a fresh draft each time.
  const [draftText, setDraftText] = useState<{ text: string; n: number } | null>(null);
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
  // A template for the draft to take up: its text, its place, and the values its fields already have.
  const [draftEntry, setDraftEntry] = useState<DraftEntry | null>(null);
  const [renaming, setRenaming] = useState<LiveSessionMeta | null>(null);
  // The session whose tag editor is open in the top bar.
  const [tagging, setTagging] = useState<string | null>(
    fixture?.tagEditor !== undefined ? selected : null,
  );
  // The session being deleted, and the one to open when it was the open one.
  const [deleting, setDeleting] = useState<{
    session: LiveSessionMeta;
    next: string | null;
  } | null>(null);
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
    provider: session?.provider,
    configuration: session?.configuration?.id,
    configurationName: session?.configuration?.name,
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
  const wide =
    inspectorOpen &&
    session !== null &&
    (view.review !== null ||
      (view.tabs.length > 0 && (view.opened !== null || view.active === 'terminal')));
  const sidebarHidden = sidebarFolded || (wide && narrow && !sidebarPinned);
  const sidebarRows = useRef<readonly string[]>([]);
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
  const busy = session
    ? ['running', 'waiting-permission'].includes(runtimeOf(session).state)
    : false;
  // Read once for the card in the turn and the review in the inspector.
  const configChanges = useConfigChanges(selected, fixture?.configChanges);
  // Read once for the inspector's Children tab and the permission strips of the turn.
  const children = useChildren(selected, {
    active: busy,
    enabled: (session?.childCount ?? 0) > 0 && (!fixture || fixture.children !== undefined),
    fixture: fixture?.children,
  });
  const states = useMemo(() => {
    const out: Record<string, RuntimeState> = {};
    for (const s of sessions) out[s.sessionId] = runtimeOf(s).state;
    return out;
  }, [sessions, runtimeOf]);

  /** What a session's driver can do: as its process reported, else as its machine announced. */
  const capabilitiesOf = useCallback(
    (s: LiveSessionMeta): RuntimeCapabilities | undefined =>
      runtimeOf(s).capabilities ??
      devices
        .find((d) => d.deviceId === s.ownerDeviceId)
        ?.providers.find(
          (p) =>
            p.provider === s.provider && p.configuration.id === (s.configuration?.id ?? s.provider),
        )?.capabilities,
    [runtimeOf, devices],
  );

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
  const existingTags = useMemo(() => tagCounts(sessions), [sessions]);

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
  const askDelete = useCallback(
    (s: LiveSessionMeta) => {
      const at = visible.findIndex((v) => v.sessionId === s.sessionId);
      const rest = visible.filter((v) => v.sessionId !== s.sessionId);
      setDeleting({ session: s, next: (rest[at] ?? rest[at - 1] ?? null)?.sessionId ?? null });
    },
    [visible],
  );
  const openDraftWith = useCallback(
    (template: Template, values?: Record<string, string>) => {
      setArchivedOpen(false);
      setAddFor(null);
      setPaletteOpen(false);
      setDraft(session ? { deviceName: session.deviceName, path: session.projectPath } : {});
      setDraftEntry(values ? { template, values } : { template });
    },
    [session],
  );
  const entry = useTemplateEntry({
    targets,
    devices,
    fixture: fixture?.templates,
    session,
    capabilities: session ? capabilitiesOf(session) : undefined,
    startTemplate: props.onStartTemplate,
    openDraft: openDraftWith,
    onStarted: () => {
      setDraft(null);
      setArchivedOpen(false);
      setPaletteOpen(false);
    },
    say,
  });
  const actions: SessionActions = useMemo(
    () => ({
      patch,
      rename: setRenaming,
      editTags: (s) => {
        setArchivedOpen(false);
        setDraft(null);
        onSelect(s.sessionId);
        setTagging(s.sessionId);
      },
      remove: askDelete,
      saveTemplate: (s) => entry.saveAs(s, capabilitiesOf(s)),
      say,
    }),
    [patch, askDelete, say, entry.saveAs, capabilitiesOf, onSelect],
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
      setDraftText(null);
      setDraft(
        target ?? (session ? { deviceName: session.deviceName, path: session.projectPath } : {}),
      );
    },
    [session],
  );

  /** A draft whose composer starts with the palette's text. */
  const openNewSessionWith = useCallback(
    (text: string) => {
      openNewSession();
      setDraftText((cur) => ({ text, n: (cur?.n ?? 0) + 1 }));
    },
    [openNewSession],
  );

  /**
   * Opens the session above or below the selected one in the sidebar's order (⌥⌘↑/↓): the rows it shows, or with
   * the sidebar hidden, its machines' views.
   */
  function step(dir: -1 | 1) {
    const views = fixture?.machineViews ?? loadViews();
    const folded = fixture?.foldedDirs ? new Set(fixture.foldedDirs) : loadFolded();
    const order = sidebarHidden
      ? sidebarOrder(
          groups,
          (key) => views[key] ?? DEFAULT_VIEW,
          (device, path) => folded.has(dirKey(device, path)),
        )
      : sidebarRows.current;
    const next = adjacent(order, draft || archivedOpen ? null : selected, dir);
    if (next !== null && (next !== selected || draft || archivedOpen)) pickSession(next);
  }

  const openAddProject = useCallback((deviceName: string) => {
    setPaletteOpen(false);
    setAddFor(deviceName);
  }, []);
  const addTarget = groups.find((g) => g.name === addFor) ?? null;

  // The page's chords (lib/shortcuts.ts). ⌘-chords fire from the composer too; a dialog owns the keys while it is
  // open. A held key acts once except ⌥⌘↑/↓, and AltGr or an input method composing is not a chord. ⇧⌘A is also
  // the browser's tab search in Chrome on macOS; the page takes it while it has the focus.
  const keys = useRef<(e: KeyboardEvent) => void>(() => undefined);
  keys.current = (e) => {
    if (pageKeysSuspended() || e.isComposing || e.getModifierState('AltGraph')) return;
    const hit = shortcutOf(e);
    if (!hit || (e.repeat && hit.id !== 'adjacent-session')) return;
    if (renaming || deleting || fixture?.delete || addFor) return;
    const overlay = paletteOpen || shortcutsOpen;
    const onSession = session !== null && !draft && !archivedOpen && !overlay;
    const act = (run: () => void) => {
      e.preventDefault();
      run();
    };
    switch (hit.id) {
      case 'palette':
        return act(() => {
          setShortcutsOpen(false);
          setPaletteOpen((v) => !v);
        });
      case 'shortcuts':
        return act(() => {
          setPaletteOpen(false);
          setShortcutsOpen((v) => !v);
        });
      case 'new-session':
        return act(() => {
          setShortcutsOpen(false);
          openNewSession();
        });
      case 'sidebar':
        return act(() => setSidebarFolded((v) => !v));
      case 'settings':
        return act(() => {
          setPaletteOpen(false);
          setShortcutsOpen(false);
          props.onSettings?.();
        });
      case 'adjacent-session':
        if (overlay) return;
        return act(() => step(hit.at === 0 ? -1 : 1));
      case 'workspace':
        if (!selected || overlay) return;
        return act(() => setInspectorOpen((v) => !v));
      case 'changes':
      case 'files':
        if (!selected || overlay) return;
        return act(() => {
          setInspectorOpen(true);
          inspector.openTool(hit.id === 'changes' ? 'changes' : 'files');
        });
      case 'rename':
        if (!onSession || !session) return;
        return act(() => setRenaming(session));
      case 'pin':
        if (!onSession || !session) return;
        return act(() => void patch(session.sessionId, { pinned: session.pinnedAt === undefined }));
      case 'tags':
        if (!onSession || !session) return;
        return act(() => setTagging((id) => (id === session.sessionId ? null : session.sessionId)));
      case 'archive':
        if (!onSession || !session) return;
        return act(() => void patch(session.sessionId, { archived: !isArchived(session) }));
    }
  };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => keys.current(e);
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const kidsData = children.data.kids;
  const commands: PaletteCommands = {
    session: session && !draft && !archivedOpen ? session : null,
    running: busy,
    stopsWith: liveKids(kidsData),
    workspaceOpen: inspectorOpen,
    sidebarShown: !sidebarHidden,
    stop: (s, cascade) => {
      postStop(s.sessionId, cascade).catch((err: unknown) => say(errorText(err), true));
    },
    handoff: () => {
      setInspectorOpen(true);
      inspector.setHandoffOpen(true);
    },
    rename: setRenaming,
    tags: (s) => setTagging(s.sessionId),
    patch: (s, change) => void patch(s.sessionId, change),
    remove: askDelete,
    saveTemplate: (s) => entry.saveAs(s, capabilitiesOf(s)),
    workspace: () => setInspectorOpen((v) => !v),
    tool: (tool) => {
      setInspectorOpen(true);
      inspector.openTool(tool);
    },
    adjacent: step,
    newSession: () => openNewSession(),
    sidebar: () => setSidebarFolded((v) => !v),
    shortcuts: () => setShortcutsOpen(true),
    settings: openSettings,
  };

  return (
    <div className={`cockpit${wide ? ' inspector-wide' : ''}`} data-testid="cockpit">
      {sidebarHidden ? null : (
        <Sidebar
          groups={groups}
          order={sidebarRows}
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
          {...(fixture?.grouping ? { grouping: fixture.grouping } : {})}
          {...(fixture?.groupMenu ? { groupMenu: true } : {})}
          {...(fixture?.tagFilter ? { tagFilter: fixture.tagFilter } : {})}
          archivedCount={archived}
          archivedOpen={archivedOpen}
          menuFor={(s) => sessionMenu(s, actions)}
          templates={{
            choices: entry.choices,
            onPick: entry.choose,
            onManage: () => props.onManageTemplates?.(),
            ...(fixture?.templates?.menu ? { open: true } : {}),
          }}
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
          onResources={(deviceId) => props.onMachineResources?.(deviceId)}
        />
      )}
      <main className="main">
        {archivedOpen ? (
          <ArchivedPage
            groups={archivedGroups}
            {...(fixture?.archivedTag ? { tag: fixture.archivedTag } : {})}
            now={now}
            onRestore={(ids) => void restore(ids)}
            onDelete={askDelete}
            onBack={() => setArchivedOpen(false)}
          />
        ) : (
          <>
            <TopBar
              session={draft ? null : session}
              {...(session && !draft ? { actions: sessionMenu(session, actions) } : {})}
              {...(fixture?.actionsMenu ? { menuOpen: true } : {})}
              {...(fixture?.actionsMenu?.sub ? { menuSub: fixture.actionsMenu.sub } : {})}
              {...(fixture?.actionsMenu?.item ? { menuItem: fixture.actionsMenu.item } : {})}
              {...(draft ? { title: t('New session') } : {})}
              {...(session && !draft
                ? {
                    tags: {
                      existing: existingTags,
                      editing: tagging === session.sessionId,
                      ...(fixture?.tagEditor !== undefined
                        ? { initialQuery: fixture.tagEditor }
                        : {}),
                      onEdit: () => setTagging(session.sessionId),
                      onAdd: (tag: string) => void patch(session.sessionId, { addTags: [tag] }),
                      onRemove: (tag: string) =>
                        void patch(session.sessionId, { removeTags: [tag] }),
                      onClose: () => setTagging(null),
                    },
                  }
                : {})}
              tokensOnly={props.tokensOnly ?? false}
              {...(fixture?.usage ? { usage: fixture.usage } : {})}
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
            {!fixture && props.onNotifySettings ? (
              <NotifyHintGate onHowTo={props.onNotifySettings} />
            ) : null}
            {draft ? (
              <Draft
                key={draftText?.n ?? 0}
                {...(draftText ? { initialText: draftText.text } : {})}
                targets={targets}
                where={draft}
                onWhere={setDraft}
                entry={draftEntry}
                onEntryUsed={() => setDraftEntry(null)}
                loadDirectories={!fixture}
                dirsVersion={dirsVersion}
                voiceAvailable={fixture?.voiceAvailable ?? false}
                {...(fixture?.draft ? { fixture: fixture.draft } : {})}
                onAddProject={openAddProject}
                onManageProviders={(deviceId) => props.onManageProviders?.(deviceId)}
                onManageTemplates={() => props.onManageTemplates?.()}
                onStartTemplate={async (request) => {
                  await props.onStartTemplate?.(request);
                  setDraft(null);
                }}
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
                shortcuts={!paletteOpen && !shortcutsOpen && !addFor}
                voiceAvailable={fixture?.voiceAvailable ?? false}
                capabilities={capabilitiesOf(session)}
                system={groups.find((g) => g.name === session.deviceName)?.system ?? null}
                onHandoff={() => {
                  setInspectorOpen(true);
                  inspector.setHandoffOpen(true);
                }}
                openDiff={openDiff}
                {...(props.onManageRules ? { onViewRules: props.onManageRules } : {})}
                onOpenSession={pickSession}
                kids={children}
                configChanges={configChanges}
                onReviewChange={(changeId) => {
                  setInspectorOpen(true);
                  inspector.openReview(changeId);
                }}
                deviceName={(id) => devices.find((d) => d.deviceId === id)?.name ?? id}
                {...(props.onSessionStarted ? { onSessionStarted: props.onSessionStarted } : {})}
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
          busy={busy}
          runtimeState={runtimeOf(session).state}
          canSteer={
            runtimeOf(session).state === 'running' &&
            runtimeOf(session).capabilities?.steer === true
          }
          terminalCapable={
            devices.find((d) => d.deviceId === session.ownerDeviceId)?.terminal === true
          }
          onHide={() => setInspectorOpen(false)}
          kids={children}
          onOpenSession={pickSession}
          configChanges={configChanges}
          live={live}
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
      {deleting || fixture?.delete ? (
        <DeleteDialog
          session={(deleting?.session ?? session) as LiveSessionMeta}
          {...(fixture?.delete ? { fixture: fixture.delete } : {})}
          onDeleted={(id) => {
            const next = deleting?.next ?? null;
            setDeleting(null);
            props.onSessionDeleted?.(id);
            if (id === selected) onSelect(next);
            say(t('Session deleted'));
          }}
          onClose={() => setDeleting(null)}
        />
      ) : null}
      {paletteOpen ? (
        <CockpitPalette
          sessions={sessions}
          groups={groups}
          {...(fixture?.query ? { initialQuery: fixture.query } : {})}
          {...(fixture?.search ? { searchFixture: fixture.search } : {})}
          {...(fixture?.paletteConfirm ? { confirming: fixture.paletteConfirm } : {})}
          templates={entry.choices}
          onTemplate={entry.choose}
          commands={commands}
          onClose={() => setPaletteOpen(false)}
          onSelectSession={pickSession}
          onSelectHit={pickHit}
          onMachine={(deviceId) => props.onMachineResources?.(deviceId)}
          onStartIn={openNewSession}
          onNewWithText={openNewSessionWith}
        />
      ) : null}
      {shortcutsOpen ? (
        <KeysDialog
          {...(fixture?.shortcuts?.filter ? { initialFilter: fixture.shortcuts.filter } : {})}
          onClose={() => setShortcutsOpen(false)}
        />
      ) : null}
      {entry.dialogs}
      {addFor && addTarget ? (
        <AddProject
          machines={groups.map((g) => ({
            deviceId: g.deviceId,
            name: g.name,
            online: g.online,
            system: g.system,
          }))}
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
