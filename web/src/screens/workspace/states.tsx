import type { ReactNode } from 'react';
import { t } from '../../lib/i18n.ts';
import { running, Scene, type Variant } from '../cockpit/states.tsx';
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
  variant?: Variant;
  workspace: Omit<WorkspaceFixture, 'viewer'>;
}) {
  return (
    <Scene
      macbookOnline={!props.offline}
      {...(props.variant ? { variant: props.variant } : {})}
      fixture={{ inspectorOpen: true, workspace: { viewer: answers, ...props.workspace } }}
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
const handoffOpen = (offline = false) => ({ ...(offline ? changes : files), handoffOpen: true });

/** Every state the design shows, rendered at /__states/workspace/<state> in development builds. */
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
};
