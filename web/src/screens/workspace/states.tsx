import type { DeviceInfo, LiveSessionMeta, ProviderInfo, SessionPort } from '@hoplet/shared';
import type { ReactNode } from 'react';
import { t } from '../../lib/i18n.ts';
import {
  capabilities,
  claudeConfiguration,
  fixtureDevices,
  running,
  Scene,
  type Variant,
} from '../cockpit/states.tsx';
import type { CloneIntoFixture } from './clone-into.tsx';
import {
  CHUNKS_NOW,
  chunksDiff,
  chunksFile,
  hostingDiff,
  iconDiff,
  iconHead,
  iconNow,
  live,
  notesDiff,
  notesFile,
  sketchFile,
  snapshot,
  truncatedListing,
} from './fixtures.ts';
import type { CloneCheck, HandoffFixture, HandoffRun } from './handoff.ts';
import type { WorkspaceFixture } from './index.tsx';
import type { PortsData } from './use-ports.ts';
import type { TerminalPhase } from './use-terminal.ts';
import type { Opened, ViewerAnswers, WorkspaceData } from './use-workspace.ts';

// Fixtures for /__states/workspace/<state>: every state of design/screens/workspace.html, the inspector open
// beside the catalog session. Nothing here is read by the real screen.

/** What the endpoints answer for each path the fixtures can open. */
function answers(opened: Opened): ViewerAnswers {
  switch (opened.path) {
    case 'core/src/store/chunks.ts':
      return { diff: chunksDiff, file: chunksFile, full: CHUNKS_NOW };
    case 'docs/hosting.md':
      return { diff: hostingDiff };
    case 'design/icon.png':
      return { diff: iconDiff, file: iconNow, before: iconHead, after: iconNow };
    case 'notes.txt':
      return { diff: notesDiff, file: notesFile };
    case 'design/hoplet.sketch':
      return { file: sketchFile };
    default:
      return {};
  }
}

const offlineData: WorkspaceData = { phase: 'snapshot', snapshot };

function Inspect(props: {
  offline?: boolean;
  /** The owner has not switched the terminal on. */
  terminalOff?: boolean;
  variant?: Variant;
  /** The machines to show instead of the design's. */
  devices?: DeviceInfo[];
  /** Changes the session list. */
  sessions?: (base: LiveSessionMeta[]) => LiveSessionMeta[];
  workspace: Omit<WorkspaceFixture, 'viewer'>;
}) {
  return (
    <Scene
      macbookOnline={!props.offline}
      {...(props.variant ? { variant: props.variant } : {})}
      {...(props.sessions ? { sessions: props.sessions } : {})}
      fixture={{
        inspectorOpen: true,
        ...(props.devices
          ? { devices: props.devices }
          : props.terminalOff
            ? { devices: fixtureDevices(!props.offline, false) }
            : {}),
        workspace: { viewer: answers, ...props.workspace },
      }}
    />
  );
}

const files = { active: 'files' as const };
const changes = { active: 'changes' as const };
const open = (path: string, mode: Opened['mode']) => ({ opened: { path, mode } });

/** The clone studio would use, and the handoff runs the panel shows; times are the snapshot's day. */
const found: CloneCheck = { state: 'found', path: '/Users/mark/github/hoplet' };
const at = (h: number, m: number, s: number) => new Date(2026, 9, 8, h, m, s).getTime();
const T0 = at(14, 7, 0);
const ran = (over: Partial<HandoffRun>): HandoffFixture => ({
  run: {
    handoffId: 'h1',
    targetDeviceId: 'dev-studio',
    ownerName: 'MacBook Pro',
    outcome: 'running',
    steps: {},
    current: null,
    total: 10_905_190,
    ...over,
  },
});
const prepared = { prepare: { at: T0, end: T0 + 800 } };
const transferred = { ...prepared, transfer: { at: T0 + 800, end: T0 + 3700 } };
const installed = { ...transferred, install: { at: T0 + 3700, end: T0 + 5300 } };
/** Studio has no clone: where the core would put one, and why it would not. */
const CLONE_DEST = '/Users/mark/github/client-projects/platform-services/hoplet';
const offer = (
  over: Partial<Extract<CloneCheck, { state: 'offer' }>['offer']> = {},
): CloneCheck => ({
  state: 'offer',
  offer: {
    origin: 'https://github.com/yangflow/hoplet.git',
    repoName: 'hoplet',
    cloneInto: CLONE_DEST,
    reason: 'ok',
    ...over,
  },
});
const cloneRun = { into: CLONE_DEST, origin: 'github.com/yangflow/hoplet' };
const cloneEntry = (name: string, git = false) => ({ name, git });
const studioHome = '/Users/mark';
const studioListings: CloneIntoFixture['listings'] = {
  [studioHome]: {
    path: studioHome,
    parent: null,
    root: studioHome,
    entries: ['Desktop', 'Documents', 'Downloads', 'github', 'notes', 'scratch'].map((n) =>
      cloneEntry(n),
    ),
    truncated: false,
  },
  [`${studioHome}/github`]: {
    path: `${studioHome}/github`,
    parent: studioHome,
    root: studioHome,
    entries: [
      cloneEntry('dotfiles', true),
      cloneEntry('experiments'),
      cloneEntry('repopro'),
      cloneEntry('website', true),
    ],
    truncated: false,
  },
  [`${studioHome}/Documents`]: {
    path: `${studioHome}/Documents`,
    parent: studioHome,
    root: studioHome,
    entries: [cloneEntry('Shared'), cloneEntry('Thesis')],
    truncated: false,
  },
};
const withHoplet = (): CloneIntoFixture['listings'] => ({
  ...studioListings,
  [`${studioHome}/github`]: {
    ...(studioListings[`${studioHome}/github`] as NonNullable<
      CloneIntoFixture['listings'][string]
    >),
    entries: [
      cloneEntry('dotfiles', true),
      cloneEntry('experiments'),
      cloneEntry('hoplet'),
      cloneEntry('repopro'),
      cloneEntry('website', true),
    ],
  },
});
const dialog = (over: Partial<CloneIntoFixture> = {}): CloneIntoFixture => ({
  listings: studioListings,
  start: `${studioHome}/github`,
  name: 'hoplet',
  ...over,
});
const handoffOpen = (offline = false) => ({ ...(offline ? changes : files), handoffOpen: true });

/** Every state the design shows, rendered at /__states/workspace/<state> in development builds. */
/** The Terminal and Browser tabs (design/screens/inspector-tools.html). */
const tools = ['changes', 'files', 'terminal', 'browser'] as const;
const terminalView = { tabs: [...tools], active: 'terminal' as const };
const browserView = { tabs: [...tools], active: 'browser' as const };
const none: TerminalPhase = { kind: 'none' };
const closed = (reason: string): TerminalPhase => ({ kind: 'closed', reason });
const port = (n: number, process: string, address: SessionPort['address']): SessionPort => ({
  port: n,
  process,
  address,
});
const ready = (ports: SessionPort[], previewHost?: string): PortsData => ({
  phase: 'ready',
  ports,
  ...(previewHost ? { previewHost } : {}),
});
const PREVIEW_HOST = 'mbp.tail1f2a.ts.net';
const external: Variant = { runtime: { state: 'external', capabilities } };
const terminalState = (
  phase: TerminalPhase,
  extra: {
    variant?: Variant;
    offline?: boolean;
    terminalOff?: boolean;
    handoff?: HandoffFixture;
  } = {},
) => (
  <Inspect
    {...(extra.variant ? { variant: extra.variant } : {})}
    {...(extra.offline ? { offline: true } : {})}
    {...(extra.terminalOff ? { terminalOff: true } : {})}
    workspace={{
      data: live(),
      view: terminalView,
      terminal: phase,
      ...(extra.handoff ? { handoff: extra.handoff } : {}),
    }}
  />
);
const browserState = (ports: PortsData, offline = false) => (
  <Inspect
    {...(offline ? { offline: true } : {})}
    workspace={{ data: live(), view: browserView, browser: ports }}
  />
);

/** A machine that offers Codex alone. */
function codexConfiguration(): ProviderInfo {
  const base = claudeConfiguration('codex', 'Codex');
  return {
    ...base,
    provider: 'codex',
    configuration: { ...base.configuration, extends: 'codex', env: [] },
  };
}

/** Hand off of a GLM session to studio, which announces `studio`. */
const glmHandoff = (studio: ProviderInfo[], open = false, enabling?: string) => (
  <Inspect
    devices={fixtureDevices(true, true, false, false, { 'dev-studio': studio })}
    sessions={(base) =>
      base.map((s) =>
        s.sessionId === '11111111-2222-4333-8444-555555555555'
          ? { ...s, configuration: { id: 'glm', name: 'GLM' } }
          : s,
      )
    }
    workspace={{
      data: live(),
      view: handoffOpen(),
      handoff: {
        clone: found,
        ...(open ? { configurationOpen: true } : {}),
        ...(enabling ? { enabling } : {}),
      },
    }}
  />
);

export const states: Record<string, () => ReactNode> = {
  workspace: () => <Inspect workspace={{ data: live(), view: files }} />,
  'file-open': () => (
    <Inspect
      workspace={{
        data: live(),
        view: { ...files, ...open('core/src/store/chunks.ts', 'file') },
      }}
    />
  ),
  'file-image': () => (
    <Inspect workspace={{ data: live(), view: { ...files, ...open('design/icon.png', 'file') } }} />
  ),
  changes: () => <Inspect workspace={{ data: live(), view: changes }} />,
  'tools-menu': () => (
    <Inspect
      workspace={{ data: live(), view: { ...changes, tabs: ['changes'] }, toolsOpen: true }}
    />
  ),
  'diff-open': () => (
    <Inspect
      workspace={{
        data: live(),
        view: { ...changes, ...open('core/src/store/chunks.ts', 'diff') },
      }}
    />
  ),
  'diff-large': () => (
    <Inspect
      workspace={{ data: live(), view: { ...changes, ...open('docs/hosting.md', 'diff') } }}
    />
  ),
  'diff-wrap': () => (
    <Inspect
      workspace={{
        data: live(),
        view: { ...changes, ...open('docs/hosting.md', 'diff') },
        wrap: true,
      }}
    />
  ),
  'diff-binary': () => (
    <Inspect
      workspace={{ data: live(), view: { ...changes, ...open('design/icon.png', 'diff') } }}
    />
  ),
  'file-binary': () => (
    <Inspect
      workspace={{ data: live(), view: { ...files, ...open('design/hoplet.sketch', 'file') } }}
    />
  ),
  'changes-empty': () => <Inspect workspace={{ data: live({ changes: [] }), view: changes }} />,
  'files-truncated': () => <Inspect workspace={{ data: truncatedListing(), view: files }} />,
  'workspace-loading': () => <Inspect workspace={{ data: { phase: 'loading' }, view: changes }} />,
  'workspace-error': () => (
    <Inspect
      workspace={{
        data: { phase: 'error', code: 'timeout', text: t('The machine did not answer in time.') },
        view: changes,
      }}
    />
  ),
  'changes-offline': () => <Inspect offline workspace={{ data: offlineData, view: changes }} />,
  'diff-offline': () => (
    <Inspect
      offline
      workspace={{
        data: offlineData,
        view: { ...changes, ...open('core/src/store/chunks.ts', 'diff') },
      }}
    />
  ),
  'offline-no-snapshot': () => (
    <Inspect offline workspace={{ data: { phase: 'no-snapshot' }, view: changes }} />
  ),
  'no-git': () => (
    <Inspect workspace={{ data: live({ git: false, paths: [], changes: [] }), view: changes }} />
  ),
  handoff: () => (
    <Inspect workspace={{ data: live(), view: handoffOpen(), handoff: { clone: found } }} />
  ),
  'handoff-checking': () => (
    <Inspect
      workspace={{ data: live(), view: handoffOpen(), handoff: { clone: { state: 'checking' } } }}
    />
  ),
  'handoff-path': () => (
    <Inspect
      workspace={{
        data: live(),
        view: handoffOpen(),
        handoff: { clone: { state: 'none' }, targetPath: '/Users/mark/src/hoplet' },
      }}
    />
  ),
  'handoff-busy': () => (
    <Inspect
      variant={running(68_000)}
      workspace={{ data: live(), view: handoffOpen(), handoff: { clone: found } }}
    />
  ),
  'handoff-progress': () => (
    <Inspect
      workspace={{
        data: live(),
        view: files,
        handoff: ran({
          steps: { ...prepared, transfer: { at: T0 + 800 } },
          current: 'transfer',
          sent: 6_501_171,
        }),
      }}
    />
  ),
  'handoff-failed': () => (
    <Inspect
      workspace={{
        data: live(),
        view: handoffOpen(),
        handoff: ran({
          outcome: 'failed',
          steps: { ...installed, verify: { at: T0 + 5300 } },
          failedStep: 'verify',
          reason: 'verify-failed',
          error: { code: 'verify-failed' },
        }),
      }}
    />
  ),
  'handoff-done': () => (
    <Inspect
      workspace={{
        data: live(),
        view: handoffOpen(),
        handoff: ran({
          outcome: 'done',
          steps: {
            ...installed,
            verify: { at: T0 + 5300, end: T0 + 5600 },
            commit: { at: T0 + 5600, end: T0 + 5700 },
          },
          worktree: '/Users/mark/github/hoplet-11111111',
          branch: 'hoplet/11111111',
        }),
      }}
    />
  ),
  'handoff-offline': () => (
    <Inspect
      offline
      workspace={{ data: offlineData, view: handoffOpen(true), handoff: { clone: found } }}
    />
  ),
  'handoff-configuration': () =>
    glmHandoff([claudeConfiguration('claude', 'Claude Code'), claudeConfiguration('glm', 'GLM')]),
  'handoff-configuration-pick': () =>
    glmHandoff(
      [claudeConfiguration('claude', 'Claude Code'), claudeConfiguration('deepseek', 'DeepSeek')],
      true,
    ),
  'handoff-configuration-enable': () =>
    glmHandoff([
      claudeConfiguration('claude', 'Claude Code', { enabled: false }),
      claudeConfiguration('deepseek', 'DeepSeek', { enabled: false }),
    ]),
  'handoff-configuration-enabling': () =>
    glmHandoff(
      [
        claudeConfiguration('claude', 'Claude Code', { enabled: false }),
        claudeConfiguration('deepseek', 'DeepSeek', { enabled: false }),
      ],
      false,
      'claude',
    ),
  'handoff-configuration-none': () => glmHandoff([codexConfiguration()]),
  'handoff-clone': () => (
    <Inspect workspace={{ data: live(), view: handoffOpen(), handoff: { clone: offer() } }} />
  ),
  'handoff-no-access': () => (
    <Inspect
      workspace={{
        data: live(),
        view: handoffOpen(),
        handoff: { clone: offer({ reason: 'no-access' }) },
      }}
    />
  ),
  'handoff-browse': () => (
    <Inspect
      workspace={{
        data: live(),
        view: handoffOpen(),
        handoff: { clone: offer(), dialog: dialog() },
      }}
    />
  ),
  'handoff-browse-exists': () => (
    <Inspect
      workspace={{
        data: live(),
        view: handoffOpen(),
        handoff: {
          clone: offer(),
          dialog: dialog({ listings: withHoplet(), taken: 'exists' }),
        },
      }}
    />
  ),
  'handoff-browse-readonly': () => (
    <Inspect
      workspace={{
        data: live(),
        view: handoffOpen(),
        handoff: {
          clone: offer(),
          dialog: dialog({ start: `${studioHome}/Documents`, taken: 'not-writable' }),
        },
      }}
    />
  ),
  'handoff-browse-outside': () => (
    <Inspect
      workspace={{
        data: live(),
        view: handoffOpen(),
        handoff: {
          clone: offer(),
          dialog: dialog({
            start: studioHome,
            typed: { text: '/etc', reason: 'outside the browse root' },
          }),
        },
      }}
    />
  ),
  'handoff-cloning': () => (
    <Inspect
      workspace={{
        data: live(),
        view: files,
        handoff: ran({
          clone: cloneRun,
          steps: { clone: { at: T0 } },
          current: 'clone',
          sent: 20_552_090,
          total: 50_121_933,
        }),
      }}
    />
  ),
  'handoff-clone-failed': () => (
    <Inspect
      workspace={{
        data: live(),
        view: handoffOpen(),
        handoff: ran({
          clone: cloneRun,
          outcome: 'failed',
          steps: { clone: { at: T0 } },
          failedStep: 'clone',
          reason: 'clone-failed',
          error: { code: 'clone-failed', reason: 'no-space' },
        }),
      }}
    />
  ),
  'handoff-clone-continue': () => (
    <Inspect
      workspace={{
        data: live(),
        view: files,
        handoff: ran({
          clone: cloneRun,
          steps: {
            clone: { at: T0, end: T0 + 38_000 },
            prepare: { at: T0 + 38_000, end: T0 + 38_800 },
            transfer: { at: T0 + 38_800 },
          },
          current: 'transfer',
          cloned: 50_121_933,
          sent: 6_501_171,
        }),
      }}
    />
  ),
  'terminal-off': () => terminalState({ kind: 'off' }, { terminalOff: true }),
  'terminal-blocked-turn': () => terminalState(none, { variant: running(0) }),
  'terminal-blocked-external': () => terminalState(none, { variant: external }),
  'terminal-blocked-handoff': () => terminalState(none, { handoff: ran({}) }),
  'terminal-blocked-offline': () => terminalState(none, { offline: true }),
  'terminal-ready': () => terminalState(none),
  'terminal-open': () => terminalState({ kind: 'open', terminalId: 't1', cols: 90, rows: 32 }),
  'terminal-closed-turn': () => terminalState(closed('turn-started')),
  'terminal-closed-idle': () => terminalState(closed('idle')),
  'terminal-closed-offline': () => terminalState(closed('owner-offline'), { offline: true }),
  'terminal-closed-exit': () => terminalState({ kind: 'closed', reason: 'exit', exitCode: 0 }),
  'terminal-busy': () => terminalState({ kind: 'busy' }),
  'browser-ports': () =>
    browserState(ready([port(5173, 'node', 'loopback'), port(4000, 'bun', 'any')], PREVIEW_HOST)),
  'browser-loopback': () => browserState(ready([port(5173, 'node', 'loopback')], PREVIEW_HOST)),
  'browser-empty': () => browserState(ready([], PREVIEW_HOST)),
  'browser-offline': () => browserState(ready([]), true),
};
