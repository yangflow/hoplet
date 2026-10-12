import type {
  HttpErrorCode,
  SnapshotChanges,
  WorkspaceChange,
  WorkspaceDiff,
  WorkspaceFile,
  WorkspaceListing,
} from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import { fileKind } from '../../components/file-types.tsx';
import {
  ApiError,
  fetchSnapshotChanges,
  fetchSnapshotDiff,
  fetchWorkspace,
  fetchWorkspaceDiff,
  fetchWorkspaceFile,
} from '../../lib/api.ts';
import { errorText } from '../../lib/errors.ts';
import { usePageVisible } from '../../lib/visibility.ts';

/** What the inspector knows of the working tree: the owner's listing, or the last snapshot while it is offline. */
export type WorkspaceData =
  | { phase: 'loading' }
  | { phase: 'error'; code: HttpErrorCode | null; text: string }
  | { phase: 'live'; listing: WorkspaceListing }
  | { phase: 'snapshot'; snapshot: SnapshotChanges }
  | { phase: 'no-snapshot' };

/** Between listings while the inspector is open; closed, only the toggle's changes mark needs them. */
const LIVE_MS = 3000;
const LIVE_CLOSED_MS = 15_000;
/** An offline owner sends no new snapshot; this only notices one stored by a hand-off or a reconnect. */
const SNAPSHOT_MS = 30_000;

function failure(err: unknown): WorkspaceData {
  return {
    phase: 'error',
    code: err instanceof ApiError ? err.code : null,
    text: errorText(err),
  };
}

/** The changed paths of what the inspector knows, or none. */
export function changesOf(data: WorkspaceData): WorkspaceChange[] {
  if (data.phase === 'live') return data.listing.changes;
  if (data.phase === 'snapshot') return data.snapshot.changes;
  return [];
}

/**
 * Polls the selected session's working tree while the page is visible: the owner's listing while it is online,
 * the last snapshot's changes while it is not, and nothing while the session's owner is not known yet. An answer equal to the last one does not re-render; a failed
 * poll keeps what was shown, and only a first read that fails shows the error.
 */
export function useWorkspaceData(
  sessionId: string | null,
  online: boolean | null,
  inspectorOpen: boolean,
  fixture?: WorkspaceData,
): { data: WorkspaceData; retry: () => void } {
  const locked = fixture !== undefined;
  const visible = usePageVisible();
  const [data, setData] = useState<WorkspaceData>(fixture ?? { phase: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const last = useRef({ key: '', body: '' });

  // biome-ignore lint/correctness/useExhaustiveDependencies: `attempt` reads again after Try again
  useEffect(() => {
    if (locked || !sessionId || !visible || online === null) return;
    const key = `${sessionId}:${online}`;
    if (last.current.key !== key) {
      last.current = { key, body: '' };
      setData({ phase: 'loading' });
    }
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const commit = (next: WorkspaceData) => {
      const body = JSON.stringify(next);
      if (cancelled || body === last.current.body) return;
      last.current = { key, body };
      setData(next);
    };
    const load = async () => {
      try {
        if (online) commit({ phase: 'live', listing: await fetchWorkspace(sessionId) });
        else commit({ phase: 'snapshot', snapshot: await fetchSnapshotChanges(sessionId) });
      } catch (err) {
        if (err instanceof ApiError && err.code === 'no-snapshot') commit({ phase: 'no-snapshot' });
        else if (!last.current.body || last.current.body.includes('"phase":"error"'))
          commit(failure(err));
      }
      if (!cancelled)
        timer = setTimeout(load, online ? (inspectorOpen ? LIVE_MS : LIVE_CLOSED_MS) : SNAPSHOT_MS);
    };
    void load();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [locked, sessionId, online, visible, inspectorOpen, attempt]);

  const retry = useCallback(() => {
    last.current = { key: last.current.key, body: '' };
    setData({ phase: 'loading' });
    setAttempt((n) => n + 1);
  }, []);

  return { data, retry };
}

export type Loaded<T> =
  | { state: 'loading' }
  | { state: 'error'; text: string }
  | { state: 'ok'; value: T };

/** What the viewer shows: a diff, a file, or a changed image before and after. */
export type ViewerContent = {
  diff?: Loaded<WorkspaceDiff>;
  file?: Loaded<WorkspaceFile>;
  before?: Loaded<WorkspaceFile>;
  after?: Loaded<WorkspaceFile>;
  /** The file as it is now, line by line, once a fold asked for it. */
  full?: Loaded<string[]>;
};

/** Development fixtures: what the endpoints would have answered for an open path. */
export type ViewerFixture = (opened: Opened) => ViewerAnswers;

export type ViewerAnswers = {
  diff?: WorkspaceDiff;
  file?: WorkspaceFile;
  before?: WorkspaceFile;
  after?: WorkspaceFile;
  full?: string;
};

export type Opened = { path: string; mode: 'diff' | 'file' };

const ok = <T>(value: T): Loaded<T> => ({ state: 'ok', value });
const fail = (err: unknown): Loaded<never> => ({ state: 'error', text: errorText(err) });

function fromFixture(fixture: ViewerFixture, opened: Opened | null): ViewerContent {
  if (!opened) return {};
  const f = fixture(opened);
  return {
    ...(f.diff ? { diff: ok(f.diff) } : {}),
    ...(f.file ? { file: ok(f.file) } : {}),
    ...(f.before ? { before: ok(f.before) } : {}),
    ...(f.after ? { after: ok(f.after) } : {}),
  };
}

/**
 * Reads what the open path needs: in diff mode its diff (from the snapshot while the owner is offline) and, for
 * a changed image, the image at `HEAD` and now; in file mode the file. A changed line count reads it again.
 */
export function useViewerContent(
  sessionId: string | null,
  opened: Opened | null,
  change: WorkspaceChange | undefined,
  online: boolean,
  fixture?: ViewerFixture,
): { content: ViewerContent; loadFull: () => void } {
  const [content, setContent] = useState<ViewerContent>(() =>
    fixture ? fromFixture(fixture, opened) : {},
  );
  const signature = change ? `${change.status}:${change.added}:${change.removed}` : '';
  const path = opened?.path;
  const mode = opened?.mode;
  const status = change?.status;

  // biome-ignore lint/correctness/useExhaustiveDependencies: `signature` reads the path again when its line counts change
  useEffect(() => {
    if (fixture) {
      setContent(fromFixture(fixture, path && mode ? { path, mode } : null));
      return;
    }
    if (!sessionId || !path || !mode) return;
    const abort = new AbortController();
    const { signal } = abort;
    const set = (patch: ViewerContent) => {
      if (!signal.aborted) setContent((c) => ({ ...c, ...patch }));
    };
    if (mode === 'file') {
      setContent({ file: { state: 'loading' } });
      fetchWorkspaceFile(sessionId, path, undefined, signal).then(
        (f) => set({ file: ok(f) }),
        (err: unknown) => set({ file: fail(err) }),
      );
    } else {
      setContent({ diff: { state: 'loading' } });
      const read = online ? fetchWorkspaceDiff : fetchSnapshotDiff;
      read(sessionId, path, signal).then(
        (d) => {
          set({ diff: ok(d) });
          if (d.kind !== 'binary' || !online || fileKind(path) !== 'image') return;
          if (d.status === 'modified' || d.status === 'deleted') {
            set({ before: { state: 'loading' } });
            fetchWorkspaceFile(sessionId, path, 'head', signal).then(
              (f) => set({ before: ok(f) }),
              (err: unknown) => set({ before: fail(err) }),
            );
          }
          if (d.status !== 'deleted') {
            set({ after: { state: 'loading' } });
            fetchWorkspaceFile(sessionId, path, undefined, signal).then(
              (f) => set({ after: ok(f) }),
              (err: unknown) => set({ after: fail(err) }),
            );
          }
        },
        (err: unknown) => set({ diff: fail(err) }),
      );
    }
    return () => abort.abort();
  }, [fixture, sessionId, path, mode, online, signature]);

  const loadFull = useCallback(() => {
    if (fixture) {
      const full = path && mode ? fixture({ path, mode }).full : undefined;
      setContent((c) => ({ ...c, full: ok(full?.split('\n') ?? []) }));
      return;
    }
    if (!sessionId || !path || !online || status === 'deleted') return;
    setContent((c) => ({ ...c, full: { state: 'loading' } }));
    fetchWorkspaceFile(sessionId, path).then(
      (f) =>
        setContent((c) => ({ ...c, full: f.kind === 'text' ? ok(f.text.split('\n')) : ok([]) })),
      (err: unknown) => setContent((c) => ({ ...c, full: fail(err) })),
    );
  }, [fixture, sessionId, path, mode, online, status]);

  return { content, loadFull };
}
