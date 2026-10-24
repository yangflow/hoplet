import type { LiveSessionMeta } from '@hoplet/shared';
import {
  type KeyboardEvent,
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from 'react';
import { Badge } from '../../components/Badge.tsx';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { Popover } from '../../components/Popover.tsx';
import { NO_KIDS } from '../../lib/children.ts';
import type { ConfigChanges } from '../../lib/config-changes.ts';
import { clock } from '../../lib/format.ts';
import { t } from '../../lib/i18n.ts';
import { pageKeysSuspended } from '../../lib/page-keys.ts';
import { gateOf } from '../../lib/terminal.ts';
import type { ChildrenView } from '../../lib/use-children.ts';
import { AuditTab, type AuditTabFixture } from './audit-tab.tsx';
import { BrowserTab } from './browser-tab.tsx';
import { ChildrenTab, type ChildrenTabFixture } from './children-tab.tsx';
import { ConfigReview } from './config-review.tsx';
import type { Handoff, HandoffFixture } from './handoff.ts';
import { HandoffPanel } from './handoff-panel.tsx';
import { ChangesList, FilesTree } from './lists.tsx';
import { buildTree, formatCount } from './model.ts';
import { loadWrap, saveWrap, TOOLS, type Tool } from './prefs.ts';
import { type SessionsFixture, SessionsTab } from './sessions-tab.tsx';
import { TerminalTab } from './terminal-tab.tsx';
import type { InspectorActions, InspectorView } from './use-inspector.ts';
import type { PortsData } from './use-ports.ts';
import type { TerminalPhase } from './use-terminal.ts';
import {
  changesOf,
  useViewerContent,
  type ViewerFixture,
  type WorkspaceData,
} from './use-workspace.ts';
import { Quiet, Viewer } from './viewer.tsx';
import './workspace.css';

export { useHandoff } from './handoff.ts';
export { type InspectorView, useInspectorView } from './use-inspector.ts';
export { changesOf, useWorkspaceData, type WorkspaceData } from './use-workspace.ts';

/** Development fixtures for /__states/workspace: what the inspector starts with. */
export type WorkspaceFixture = {
  data: WorkspaceData;
  view?: Partial<InspectorView>;
  viewer?: ViewerFixture;
  toolsOpen?: boolean;
  wrap?: boolean;
  handoff?: HandoffFixture;
  /** The Terminal tab's state, in place of asking the core. */
  terminal?: TerminalPhase;
  /** The Browser tab's ports, in place of asking the core. */
  browser?: PortsData;
  /** The Other sessions tab, in place of asking the core. */
  sessions?: SessionsFixture;
  /** The Audit tab, in place of asking the core. */
  audit?: AuditTabFixture;
  /** The Children tab's open row, reports and cancel dialog, in place of asking the core. */
  childrenTab?: ChildrenTabFixture;
  /** The object the review of a change request shows first, by `kind:name`. */
  reviewObject?: string;
};

/** The Children tab of a caller that reads no tree: nothing to show. */
const NO_CHILDREN: ChildrenView = {
  phase: 'live',
  data: NO_KIDS,
  error: null,
  asks: {},
  refresh: () => {},
  answered: () => {},
};

const toolLabel = (tool: Tool) =>
  ({
    changes: t('Changes'),
    files: t('Files'),
    terminal: t('Terminal'),
    browser: t('Browser'),
    children: t('Children'),
    sessions: t('Other sessions'),
    audit: t('Audit'),
  })[tool];
const toolHint: Partial<Record<Tool, string>> = { changes: '⇧⌘G', files: '⇧⌘E' };
const toolIcon: Record<Tool, () => ReturnType<typeof Icon.pen>> = {
  changes: Icon.pen,
  files: Icon.folder,
  terminal: Icon.terminal,
  browser: Icon.globe,
  children: Icon.fork,
  sessions: Icon.link,
  audit: Icon.listChecks,
};

/**
 * The workspace inspector (docs/spec.md#workspace-inspector): tools as tabs (Changes, Files, Terminal and
 * Browser), a viewer that widens the inspector with the list on its right, and Hand off at the foot.
 */
export function Inspector(props: {
  session: LiveSessionMeta;
  data: WorkspaceData;
  onRetry: () => void;
  view: InspectorView;
  actions: InspectorActions;
  handoff: Handoff;
  /** The session is in a turn: Hand off waits until it ends. */
  busy: boolean;
  /** The session's runtime state, and whether its machine announced a terminal: the Terminal tab's gate. */
  runtimeState: string;
  /** The owner is offline and was in a turn when last heard (ADR 0039): the handoff warns. */
  ownerMayRun?: boolean;
  terminalCapable: boolean;
  onHide: () => void;
  /** The session's descendants, read once by the cockpit for this tab and the turn's permission strips. */
  kids?: ChildrenView;
  onOpenSession?: (sessionId: string) => void;
  /** The session's requests to change its configuration, which the review shows (ADR 0036). */
  configChanges?: ConfigChanges;
  /** This window is connected to the core: a request can be decided. */
  live?: boolean;
  fixture?: WorkspaceFixture;
}) {
  const { session, data, view, actions, fixture } = props;
  const online = session.ownerConnected;
  const owner = session.deviceName || t('the owner');
  const changes = changesOf(data);
  const git = data.phase !== 'live' || data.listing.git;
  const filesOff = !online || !git;
  const [toolsOpen, setToolsOpen] = useState(fixture?.toolsOpen ?? false);
  const [wrap, setWrap] = useState(() => fixture?.wrap ?? loadWrap());
  const id = useId();
  const focusMenu = useRef(false);
  const [terminalLive, setTerminalLive] = useState(false);
  const kids = props.kids ?? NO_CHILDREN;
  // A session that started children always shows their tab: it cannot be closed while they exist.
  const pinned: Tool | null = session.childCount > 0 ? 'children' : null;
  const tabs: Tool[] =
    pinned && !view.tabs.includes(pinned)
      ? TOOLS.filter((x) => x === pinned || view.tabs.includes(x))
      : view.tabs;
  const kidNeedsYou = kids.data.kids.some((k) => k.state === 'blocked');

  const active: Tool | null =
    view.active && tabs.includes(view.active)
      ? view.active
      : (view.tabs.find((tab) =>
          tab === 'changes' ? changes.length > 0 || filesOff || data.phase !== 'live' : true,
        ) ??
        tabs[0] ??
        null);

  // The review of a change request takes the tools' place until a tab is picked or it is closed.
  const configChanges = props.configChanges;
  const reviewing = view.review !== null && configChanges !== undefined;
  const reviewed = reviewing
    ? (configChanges.changes.find((c) => c.changeId === view.review) ?? null)
    : null;
  useEffect(() => {
    if (!reviewing) return;
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (pageKeysSuspended() || e.key !== 'Escape' || e.altKey || e.shiftKey || toolsOpen) return;
      const target = e.target as HTMLElement | null;
      if (target?.closest('input, textarea, [contenteditable]')) return;
      if (e.target === document.body || target?.closest('.ws')) actions.closeReview();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [reviewing, actions, toolsOpen]);

  const opened = view.opened;
  const change = opened ? changes.find((c) => c.path === opened.path) : undefined;
  const { content, loadFull } = useViewerContent(
    session.sessionId,
    opened,
    change,
    online,
    fixture?.viewer,
  );
  const order = useMemo(() => changes.map((c) => c.path), [changes]);

  const toggleWrap = useCallback(() => {
    setWrap((w) => {
      if (!fixture) saveWrap(!w);
      return !w;
    });
  }, [fixture]);

  const openChange = useCallback((path: string) => actions.openPath(path, 'diff'), [actions]);
  const openFile = useCallback(
    (path: string) => {
      const c = changes.find((x) => x.path === path);
      actions.openPath(
        path,
        c && c.status !== 'modified' && c.status !== 'untracked' ? 'diff' : 'file',
      );
    },
    [actions, changes],
  );

  // ⌥Z wraps, ⌥↑ and ⌥↓ step through the changed files, Esc closes the viewer.
  useEffect(() => {
    if (!opened) return;
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (pageKeysSuspended()) return;
      const typing = (e.target as HTMLElement | null)?.closest(
        'input, textarea, [contenteditable]',
      );
      if (typing || e.metaKey || e.ctrlKey) return;
      if (e.altKey && e.code === 'KeyZ') {
        e.preventDefault();
        toggleWrap();
      } else if (
        e.altKey &&
        (e.key === 'ArrowUp' || e.key === 'ArrowDown') &&
        opened.mode === 'diff'
      ) {
        const at = order.indexOf(opened.path);
        const to = order[e.key === 'ArrowUp' ? at - 1 : at + 1];
        if (at >= 0 && to) {
          e.preventDefault();
          actions.openPath(to, 'diff');
        }
      } else if (
        e.key === 'Escape' &&
        !e.altKey &&
        !e.shiftKey &&
        !toolsOpen &&
        (e.target === document.body || (e.target as HTMLElement | null)?.closest('.ws'))
      ) {
        actions.closeViewer();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [opened, order, actions, toggleWrap, toolsOpen]);

  // The terminal needs the room of a viewer, whatever it shows, so the pane does not jump when one opens.
  const wide = reviewing || active === 'terminal' || (opened !== null && active !== null);
  const roomy =
    active === 'terminal' ||
    active === 'browser' ||
    active === 'sessions' ||
    active === 'audit' ||
    active === 'children';
  // the terminal widens the inspector for itself: no viewer, no list beside it
  const split = wide && active !== 'terminal';
  const gate = gateOf({
    online,
    terminalCapable: props.terminalCapable,
    runtimeState: props.runtimeState,
    handoffRunning: props.handoff.run?.outcome === 'running',
  });
  // The panel stays open while a handoff runs, also one another window started.
  const handoffOpen = view.handoffOpen || props.handoff.run?.outcome === 'running';

  return (
    <aside
      className={`ws${wide ? ' wide' : ''}`}
      aria-label={t('Workspace')}
      data-testid="workspace.inspector"
    >
      <header className="ws-h">
        <div className="ws-tabs">
          <TabList
            id={id}
            tabs={tabs}
            pinned={pinned}
            needsYou={kidNeedsYou}
            active={reviewing ? null : active}
            {...(reviewing
              ? { review: { label: t('Change request'), onClose: actions.closeReview } }
              : {})}
            count={data.phase === 'live' || data.phase === 'snapshot' ? changes.length : 0}
            filesOff={filesOff}
            fold={reviewing || (tabs.length > 2 && active !== 'terminal')}
            terminalLive={terminalLive}
            filesWhy={
              git
                ? t('Files need {name} online').replace('{name}', owner)
                : t('Not a git repository')
            }
            onPick={actions.openTool}
            onClose={actions.closeTool}
          />
          <span className="ws-tools">
            <IconButton
              label={t('Open a tool')}
              icon={Icon.plus()}
              on={toolsOpen}
              aria-haspopup="menu"
              aria-expanded={toolsOpen}
              onClick={() => {
                focusMenu.current = !toolsOpen;
                setToolsOpen((v) => !v);
              }}
              data-testid="workspace.tools"
            />
            <ToolsMenu
              open={toolsOpen}
              focusFirst={focusMenu}
              onClose={() => setToolsOpen(false)}
              onPick={(tool) => {
                setToolsOpen(false);
                actions.openTool(tool);
              }}
            />
          </span>
        </div>
        <IconButton
          label={t('Hide workspace')}
          hint="⌘I"
          icon={Icon.panel()}
          on
          onClick={props.onHide}
          data-testid="workspace.hide"
        />
      </header>
      {reviewing ? (
        <div
          className="ws-body"
          role="tabpanel"
          id={`${id}-panel`}
          aria-labelledby={`${id}-review`}
        >
          <ConfigReview
            key={view.review}
            change={reviewed}
            busy={configChanges.busy === view.review}
            decidable={props.live ?? false}
            initial={fixture?.reviewObject}
            onDecide={(approve) => configChanges.decide(view.review as string, approve)}
            onUndo={() => configChanges.undo(view.review as string)}
          />
        </div>
      ) : (
        <div
          className={split ? 'ws-md' : 'ws-body'}
          {...(active
            ? { role: 'tabpanel', id: `${id}-panel`, 'aria-labelledby': `${id}-${active}` }
            : {})}
        >
          {split && opened ? (
            <Viewer
              opened={opened}
              change={change}
              content={content}
              order={order}
              wrap={wrap}
              online={online}
              owner={owner}
              onWrap={toggleWrap}
              onMode={(mode) => actions.openPath(opened.path, mode)}
              onStep={(path) => actions.openPath(path, 'diff')}
              onClose={actions.closeViewer}
              onLoadFull={loadFull}
            />
          ) : null}
          <div className={split ? 'ws-mlist' : 'ws-pane'}>
            {active === 'terminal' ? (
              <TerminalTab
                sessionId={session.sessionId}
                gate={gate}
                owner={owner}
                dir={session.projectPath}
                onLive={setTerminalLive}
                {...(fixture?.terminal ? { fixture: fixture.terminal } : {})}
              />
            ) : active === 'browser' ? (
              <BrowserTab
                sessionId={session.sessionId}
                online={online}
                owner={owner}
                {...(fixture?.browser ? { fixture: fixture.browser } : {})}
              />
            ) : active === 'sessions' ? (
              <SessionsTab
                session={session}
                {...(fixture?.sessions ? { fixture: fixture.sessions } : {})}
              />
            ) : active === 'children' ? (
              <ChildrenTab
                session={session}
                view={kids}
                onOpenSession={props.onOpenSession}
                {...(fixture?.childrenTab ? { fixture: fixture.childrenTab } : {})}
              />
            ) : active === 'audit' ? (
              <AuditTab session={session} {...(fixture?.audit ? { fixture: fixture.audit } : {})} />
            ) : (
              <ToolBody
                tool={active}
                data={data}
                online={online}
                owner={owner}
                compact={wide}
                selected={opened?.path ?? null}
                onRetry={props.onRetry}
                onOpenChange={openChange}
                onOpenFile={openFile}
              />
            )}
            {roomy ? null : handoffOpen ? (
              <HandoffPanel
                handoff={props.handoff}
                online={online}
                owner={owner}
                snapshot={data.phase === 'snapshot' ? data.snapshot : null}
                busy={props.busy}
                mayRun={props.ownerMayRun === true}
                onClose={() => {
                  actions.setHandoffOpen(false);
                  props.handoff.dismiss();
                }}
              />
            ) : (
              <HandoffRow
                data={data}
                online={online}
                owner={owner}
                hasTarget={props.handoff.otherDevices.length > 0}
                onOpen={() => actions.setHandoffOpen(true)}
              />
            )}
          </div>
        </div>
      )}
    </aside>
  );
}

/** The open tools as tabs; a tab closes with its ×, or with Delete while it has focus. */
function TabList(props: {
  id: string;
  tabs: readonly Tool[];
  active: Tool | null;
  count: number;
  filesOff: boolean;
  filesWhy: string;
  /** Inactive tabs show only their glyph, so four tabs fit the narrow inspector. */
  fold: boolean;
  /** The terminal is open: a green dot on its tab. */
  terminalLive: boolean;
  /** The tab that has no ×: Children while the session has any. */
  pinned?: Tool | null;
  /** A child waits for a permission: the attention dot on the Children tab, also folded. */
  needsYou?: boolean;
  /** The review of a change request, open as a tab of its own after the tools. */
  review?: { label: string; onClose: () => void };
  onPick: (tool: Tool) => void;
  onClose: (tool: Tool) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  function onKeyDown(e: KeyboardEvent) {
    const tabs = [...(ref.current?.querySelectorAll<HTMLElement>('[role="tab"]') ?? [])];
    const at = tabs.indexOf(document.activeElement as HTMLElement);
    if (at < 0) return;
    const go = (i: number) => tabs[(i + tabs.length) % tabs.length]?.focus();
    if (e.key === 'ArrowRight') go(at + 1);
    else if (e.key === 'ArrowLeft') go(at - 1);
    else if ((e.key === 'Delete' || e.key === 'Backspace') && at === props.tabs.length) {
      props.review?.onClose();
    } else if (e.key === 'Delete' || e.key === 'Backspace') {
      const tool = props.tabs[at];
      if (tool && tool !== props.pinned) props.onClose(tool);
    } else return;
    e.preventDefault();
  }
  return (
    <div
      ref={ref}
      className="ws-tablist"
      role="tablist"
      aria-label={t('Tools')}
      onKeyDown={onKeyDown}
    >
      {props.tabs.map((tool) => {
        const on = tool === props.active;
        const off = tool === 'files' && props.filesOff;
        const ico = props.fold && !on;
        // five tools leave no room for the active label beside the inspector's own toggle
        const bare = ico || (on && props.tabs.length >= 5);
        const hint = toolHint[tool];
        const dot = tool === 'terminal' && props.terminalLive && !ico;
        const need = tool === 'children' && props.needsYou === true;
        const needText = t('A child waits for a permission');
        return (
          <div
            key={tool}
            id={`${props.id}-${tool}`}
            role="tab"
            tabIndex={on ? 0 : -1}
            aria-selected={on}
            aria-controls={on ? `${props.id}-panel` : undefined}
            aria-disabled={off || undefined}
            className={`ws-tab${on ? ' on' : ''}${off ? ' off' : ''}${ico ? ' ico' : ''}`}
            title={off ? props.filesWhy : hint ? `${toolLabel(tool)} · ${hint}` : toolLabel(tool)}
            {...(bare
              ? { 'aria-label': need ? `${toolLabel(tool)}, ${needText}` : toolLabel(tool) }
              : {})}
            onClick={() => (off ? undefined : props.onPick(tool))}
            onKeyDown={(e) => {
              if (!off && (e.key === 'Enter' || e.key === ' ')) {
                e.preventDefault();
                props.onPick(tool);
              }
            }}
            data-testid={`workspace.tab-${tool}`}
          >
            {toolIcon[tool]()}
            {bare ? null : <span>{toolLabel(tool)}</span>}
            {tool === 'changes' && props.count > 0 && !ico ? (
              <span className="ws-tab-c">{formatCount(props.count)}</span>
            ) : null}
            {dot ? <span className="ws-live" aria-hidden="true" /> : null}
            {need ? (
              <span
                className="ws-live need"
                aria-hidden="true"
                data-testid="workspace.children-need"
              />
            ) : null}
            {need && !bare ? <span className="sr-only">, {needText}</span> : null}
            {tool === props.pinned ? null : (
              <span
                className="ws-tab-x"
                title={t('Close {tool}').replace('{tool}', toolLabel(tool))}
                aria-hidden="true"
                onClick={(e) => {
                  e.stopPropagation();
                  props.onClose(tool);
                }}
                data-testid={`workspace.close-${tool}`}
              >
                {Icon.close()}
              </span>
            )}
          </div>
        );
      })}
      {props.review ? (
        <div
          id={`${props.id}-review`}
          role="tab"
          tabIndex={0}
          aria-selected
          aria-controls={`${props.id}-panel`}
          className="ws-tab on"
          title={props.review.label}
          data-testid="workspace.tab-review"
        >
          {Icon.sliders()}
          <span>{props.review.label}</span>
          <span
            className="ws-tab-x"
            title={`${t('Close')} · Esc`}
            aria-hidden="true"
            onClick={(e) => {
              e.stopPropagation();
              props.review?.onClose();
            }}
            data-testid="workspace.close-review"
          >
            {Icon.close()}
          </span>
        </div>
      ) : null}
    </div>
  );
}

/** The + menu: opens a tool, or brings a closed one back. */
function ToolsMenu(props: {
  open: boolean;
  /** Set when the + button opened the menu: its first item takes the focus. */
  focusFirst: { current: boolean };
  onClose: () => void;
  onPick: (tool: Tool) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!props.open || !props.focusFirst.current) return;
    props.focusFirst.current = false;
    ref.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
  }, [props.open, props.focusFirst]);
  function onKeyDown(e: KeyboardEvent) {
    const items = [...(ref.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])];
    const at = items.indexOf(document.activeElement as HTMLElement);
    const go = (i: number) => items[(i + items.length) % items.length]?.focus();
    if (e.key === 'ArrowDown') go(at + 1);
    else if (e.key === 'ArrowUp') go(at < 0 ? -1 : at - 1);
    else if (e.key === 'Escape') {
      props.onClose();
      (
        ref.current?.parentElement?.parentElement?.querySelector('.ib') as HTMLElement | null
      )?.focus();
    } else if (e.key === 'Tab') props.onClose();
    else return;
    if (e.key !== 'Tab') {
      e.preventDefault();
      e.stopPropagation();
    }
  }
  return (
    <Popover open={props.open} onClose={props.onClose} className="ws-menu">
      <div
        ref={ref}
        role="menu"
        aria-label={t('Open a tool')}
        onKeyDown={onKeyDown}
        data-testid="workspace.tools-menu"
      >
        {TOOLS.map((tool) => (
          <button
            key={tool}
            type="button"
            role="menuitem"
            tabIndex={-1}
            className="menu-item"
            {...(toolHint[tool] ? { 'aria-keyshortcuts': toolHint[tool] } : {})}
            onClick={() => props.onPick(tool)}
            data-testid={`workspace.open-${tool}`}
          >
            {toolIcon[tool]()}
            <span className="ws-menu-l">{toolLabel(tool)}</span>
            {toolHint[tool] ? <Badge kind="key">{toolHint[tool]}</Badge> : null}
          </button>
        ))}
      </div>
    </Popover>
  );
}

/** The chosen tool's list, or why it has none. */
function ToolBody(props: {
  tool: Tool | null;
  data: WorkspaceData;
  online: boolean;
  owner: string;
  compact: boolean;
  selected: string | null;
  onRetry: () => void;
  onOpenChange: (path: string) => void;
  onOpenFile: (path: string) => void;
}) {
  const { tool, data, owner } = props;
  const named = (text: string) => t(text).replace('{name}', owner);
  if (!tool)
    return (
      <Quiet icon={Icon.tools()} title={t('No tools open')} text={t('Open one from the + menu.')} />
    );
  if (data.phase === 'loading')
    return (
      <Quiet
        icon={<Spinner />}
        text={
          props.online
            ? named('Reading the working tree on {name}')
            : t('Reading the last snapshot')
        }
        status
        testid="workspace.loading"
      />
    );
  if (data.phase === 'error')
    return (
      <Quiet
        icon={Icon.alert()}
        title={t('Could not read the working tree')}
        text={data.code === 'timeout' ? named('{name} did not answer in time.') : data.text}
        action={
          <Button icon={Icon.retry()} onClick={props.onRetry} data-testid="workspace.retry">
            {t('Try again')}
          </Button>
        }
        testid="workspace.error"
      />
    );
  if (data.phase === 'no-snapshot')
    return (
      <Quiet
        icon={Icon.clock()}
        title={t('No snapshot yet')}
        text={named(
          '{name} is offline and has not sent a snapshot of this working tree. Changes appear when it is back.',
        )}
        testid="workspace.no-snapshot"
      />
    );
  if (data.phase === 'live' && !data.listing.git)
    return (
      <Quiet
        icon={Icon.info()}
        title={t('Not a git repository')}
        text={t(
          "The session's directory has no commit. Changes and Hand off need a git repository with one.",
        )}
        testid="workspace.no-git"
      />
    );
  if (tool === 'files') {
    if (data.phase !== 'live')
      return <Quiet icon={Icon.lock()} text={named('Files need {name} online')} />;
    return (
      <>
        <FilesTree
          nodes={buildTree(data.listing.paths, data.listing.changes)}
          selected={props.selected}
          onOpen={props.onOpenFile}
        />
        {data.listing.truncated ? (
          <p className="ws-trunc" data-testid="workspace.truncated">
            {Icon.info()}
            <span>
              {t('The first {n} paths; open a file from a turn to read others').replace(
                '{n}',
                formatCount(data.listing.paths.length),
              )}
            </span>
          </p>
        ) : null}
      </>
    );
  }
  const changes = changesOf(data);
  const snapshot = data.phase === 'snapshot' ? data.snapshot : null;
  const note = snapshot ? (
    <p className="ws-snap" data-testid="workspace.snapshot">
      {Icon.clock()}
      <span>
        <WithCommit
          text={t('Snapshot {time} · base {commit}').replace('{time}', clock(snapshot.snapshotAt))}
          commit={snapshot.baseCommit}
        />
      </span>
    </p>
  ) : null;
  if (changes.length === 0) {
    const commit = data.phase === 'live' ? data.listing.commit : (snapshot?.baseCommit ?? '');
    return (
      <>
        {note}
        <Quiet
          icon={Icon.check()}
          title={t('No changes')}
          text={<WithCommit text={t('The working tree matches {commit}.')} commit={commit} />}
          testid="workspace.no-changes"
        />
      </>
    );
  }
  return (
    <>
      {note}
      <ChangesList
        changes={changes}
        selected={props.selected}
        compact={props.compact}
        onOpen={props.onOpenChange}
      />
    </>
  );
}

/** A sentence with a short commit hash in mono type where it says `{commit}`. */
function WithCommit({ text, commit }: { text: string; commit: string }) {
  const [before = '', after = ''] = text.split('{commit}');
  return (
    <>
      {before}
      <span className="ws-mono">{commit.slice(0, 7)}</span>
      {after}
    </>
  );
}

/** Hand off at the foot: one row that opens into the panel, disabled with the reason when it cannot run. */
function HandoffRow(props: {
  data: WorkspaceData;
  online: boolean;
  owner: string;
  hasTarget: boolean;
  onOpen: () => void;
}) {
  const { data, online, owner } = props;
  if (data.phase === 'loading' || data.phase === 'error') return null;
  const why =
    data.phase === 'live' && !data.listing.git
      ? t('Needs a git repository with a commit')
      : data.phase === 'no-snapshot'
        ? t('Needs a snapshot or {name} online').replace('{name}', owner)
        : !props.hasTarget
          ? t('Connect another agent to hand off this session.')
          : null;
  const tip =
    why ?? (online ? t('Hand off with the working tree') : t('Hand off from the last snapshot'));
  return (
    <div className="ws-hrow">
      <Button
        variant="quiet"
        icon={Icon.handoff()}
        title={tip}
        disabled={why !== null}
        aria-expanded={false}
        onClick={props.onOpen}
        data-testid="workspace.handoff"
      >
        {t('Hand off')}
      </Button>
    </div>
  );
}
