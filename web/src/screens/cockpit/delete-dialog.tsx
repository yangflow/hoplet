import type {
  DeleteSessionRequest,
  DeletionPreview,
  DeletionStep,
  DeletionStepName,
  LiveSessionMeta,
} from '@hoplet/shared';
import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { deleteSession, fetchDeletionPreview, postStop } from '../../lib/api.ts';
import { ApiError, errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { displayTitle } from './group.ts';

/** One line of the progress list. `worktree-branch` is the worktree and its branch removed together. */
export type DeleteRowStep = DeletionStepName | 'worktree-branch';
export type DeleteRowState = 'ok' | 'run' | 'wait' | 'bad' | 'skip';
export type DeleteRow = { step: DeleteRowStep; state: DeleteRowState; reason?: string };

type Phase = 'confirm' | 'progress' | 'partial';

/** Development fixtures for /__states: the preview the core would answer, and the phase to show. */
export type DeleteFixture = {
  preview: DeletionPreview;
  phase?: Phase;
  rows?: DeleteRow[];
  /** The optional items start ticked. */
  ticked?: { vendorFile?: boolean; worktree?: boolean };
};

type Loaded =
  | { kind: 'loading' }
  | { kind: 'failed'; error: unknown }
  | { kind: 'ready'; preview: DeletionPreview };

/** The request a set of ticks makes: an item the owner cannot reach, or that is not there, is not asked for. */
function requestOf(
  preview: DeletionPreview,
  ticks: { vendorFile: boolean; worktree: boolean },
): DeleteSessionRequest {
  const reachable = preview.owner.online;
  const worktree = reachable && preview.worktree !== null && ticks.worktree;
  const dirty = preview.worktree
    ? preview.worktree.dirty.modified + preview.worktree.dirty.untracked > 0
    : false;
  return {
    vendorFile: reachable && ticks.vendorFile,
    worktree,
    worktreeDirty: worktree && dirty,
  };
}

/** The rows of a finished or failed deletion, in the order the steps ran. */
function rowsOf(steps: readonly DeletionStep[]): DeleteRow[] {
  const state = { done: 'ok', failed: 'bad', skipped: 'skip' } as const;
  const rows: DeleteRow[] = [];
  for (const s of steps) {
    const prev = rows[rows.length - 1];
    if (
      s.step === 'branch' &&
      prev?.step === 'worktree' &&
      prev.state === 'ok' &&
      s.status === 'done'
    ) {
      prev.step = 'worktree-branch';
      continue;
    }
    rows.push({ step: s.step, state: state[s.status], ...(s.reason ? { reason: s.reason } : {}) });
  }
  return rows;
}

/** What is about to run, the first line running and the others waiting. */
function plannedRows(request: DeleteSessionRequest, online: boolean): DeleteRow[] {
  const steps: DeleteRowStep[] = [];
  if (online) steps.push('stop-syncing');
  if (request.worktree) steps.push('worktree-branch');
  if (request.vendorFile) steps.push('vendor-file');
  steps.push('core-copy');
  return steps.map((step, i) => ({ step, state: i === 0 ? 'run' : 'wait' }));
}

const mono = (text: string) => <span className="mono">{text}</span>;

/** The title of a row and, under it, what is known of it. */
function rowCopy(
  row: DeleteRow,
  machine: string,
  worktree: string | undefined,
): { label: string; detail: ReactNode } {
  const m = (text: string) => t(text).replace('{m}', machine);
  const going = row.state === 'run' || row.state === 'wait';
  const path = worktree ? mono(worktree) : null;
  switch (row.step) {
    case 'stop-syncing':
      if (row.state === 'ok') return { label: m('Stopped syncing from {m}'), detail: null };
      if (going) return { label: m('Stopping syncing from {m}'), detail: null };
      if (row.reason === 'owner-offline')
        return {
          label: m('{m} was offline'),
          detail: m('It is told to stop syncing when it reconnects.'),
        };
      return {
        label:
          row.state === 'bad'
            ? m('Could not stop syncing from {m}')
            : m('Syncing from {m} not stopped'),
        detail: null,
      };
    case 'worktree-branch':
      return {
        label: going ? t('Removing the worktree and branch') : t('Removed the worktree and branch'),
        detail: going ? path : null,
      };
    case 'worktree':
      if (row.state === 'ok') return { label: t('Removed the worktree'), detail: null };
      if (going) return { label: t('Removing the worktree'), detail: path };
      if (row.state === 'skip') return { label: t('The worktree was not removed'), detail: null };
      return {
        label: t('Could not remove the worktree'),
        detail:
          row.reason === 'dirty' ? (
            <>
              {path}{' '}
              {t(
                'changed since this dialog opened and now holds uncommitted files. Nothing was deleted there.',
              )}
            </>
          ) : row.reason === 'not-ours' ? (
            <>
              {path} {t('was not made by Hoplet, so it was left alone.')}
            </>
          ) : (
            <>
              {path} {t('could not be removed.')}
            </>
          ),
      };
    case 'branch':
      if (row.state === 'ok') return { label: t('Deleted the branch'), detail: null };
      if (going) return { label: t('Deleting the branch'), detail: null };
      return {
        label:
          row.state === 'bad' ? t('Could not delete the branch') : t('The branch was not deleted'),
        detail: null,
      };
    case 'vendor-file':
      if (row.state === 'ok') return { label: m('Deleted the session file on {m}'), detail: null };
      if (going) return { label: m('Deleting the session file on {m}'), detail: null };
      return {
        label:
          row.state === 'bad'
            ? m('Could not delete the session file on {m}')
            : m('The session file on {m} was not deleted'),
        detail: null,
      };
    case 'core-copy':
      if (row.state === 'ok') return { label: t('Deleted Hoplet’s copy'), detail: null };
      if (going) return { label: t('Deleting Hoplet’s copy'), detail: null };
      return {
        label:
          row.state === 'bad'
            ? t('Could not delete Hoplet’s copy')
            : t('Hoplet’s copy not deleted yet'),
        detail: t('It is hidden from the list and will not sync. Retry finishes it.'),
      };
  }
}

const STEP_ICON: Record<DeleteRowState, () => ReactNode> = {
  ok: () => <span className="ok">{Icon.check()}</span>,
  run: () => (
    <span className="run">
      <Spinner />
    </span>
  ),
  bad: () => <span className="bad">{Icon.close()}</span>,
  wait: () => <span className="skip">{Icon.waiting()}</span>,
  skip: () => <span className="skip">{Icon.skipped()}</span>,
};

function Steps(props: { rows: DeleteRow[]; machine: string; worktree: string | undefined }) {
  return (
    <div className="dlg-b" role="status" data-testid="cockpit.delete-steps">
      {props.rows.map((row) => {
        const { label, detail } = rowCopy(row, props.machine, props.worktree);
        return (
          <div
            key={row.step}
            className={`xs${row.state === 'wait' ? ' wait' : ''}${row.state === 'bad' ? ' badrow' : ''}`}
            data-state={row.state}
            data-testid={`cockpit.delete-step-${row.step}`}
          >
            {STEP_ICON[row.state]()}
            <div>
              <div className="xl">{label}</div>
              {detail ? <div className="od">{detail}</div> : null}
            </div>
          </div>
        );
      })}
    </div>
  );
}

/** One line of the checklist. A ticked and locked line is what always goes; a dimmed one cannot be reached. */
function Option(props: {
  id: string;
  label: string;
  detail: ReactNode;
  reason?: ReactNode;
  checked: boolean;
  locked?: boolean;
  disabled?: boolean;
  warn?: boolean;
  onChange?: (on: boolean) => void;
}) {
  const { checked, locked, disabled, warn } = props;
  const cls = `opt${checked ? ' on' : ''}${locked ? ' lock' : ''}${disabled ? ' dis' : ''}${warn ? ' warn' : ''}`;
  return (
    <label className={cls} data-testid={`cockpit.delete-opt-${props.id}`}>
      <input
        type="checkbox"
        className="opt-in"
        checked={checked}
        disabled={locked || disabled}
        onChange={(e) => props.onChange?.(e.target.checked)}
      />
      <span className="cbx" aria-hidden="true">
        {checked ? Icon.check() : null}
      </span>
      <span className="opt-t">
        <span className="ol">{props.label}</span>
        <span className="od">{props.detail}</span>
        {props.reason ? <span className="rsn">{props.reason}</span> : null}
      </span>
    </label>
  );
}

/** The warning of a worktree that holds work no other place has. */
function lossText(w: NonNullable<DeletionPreview['worktree']>): string | null {
  const files = w.dirty.modified + w.dirty.untracked > 0;
  const fill = (s: string) =>
    s
      .replace('{modified}', String(w.dirty.modified))
      .replace('{untracked}', String(w.dirty.untracked))
      .replace('{n}', String(w.unpushed));
  if (files && w.unpushed > 1)
    return fill(
      t(
        '{modified} modified and {untracked} untracked files would be lost, and {n} commits are not pushed. Tick it to delete them too.',
      ),
    );
  if (files && w.unpushed === 1)
    return fill(
      t(
        '{modified} modified and {untracked} untracked files would be lost, and 1 commit is not pushed. Tick it to delete them too.',
      ),
    );
  if (files)
    return fill(
      t(
        '{modified} modified and {untracked} untracked files would be lost. Tick it to delete them too.',
      ),
    );
  if (w.unpushed > 1)
    return fill(t('{n} commits are not pushed and would be lost with the branch.'));
  if (w.unpushed === 1) return t('1 commit is not pushed and would be lost with the branch.');
  return null;
}

/**
 * Deleting a session (ADR 0023): one checklist. Hoplet's copy always goes and cannot be unticked; the session file
 * on the owner and the worktree and branch a handoff made are optional and start unticked. The owner is asked what
 * exists first. A running session has to be stopped, an unreachable owner leaves its items out, and a deletion that
 * stops half way keeps Hoplet's copy for a retry.
 */
export function DeleteDialog(props: {
  session: LiveSessionMeta;
  fixture?: DeleteFixture;
  /** The session is gone from the core. */
  onDeleted: (sessionId: string) => void;
  onClose: () => void;
}) {
  const { session, fixture } = props;
  const title = displayTitle(session);
  const [loaded, setLoaded] = useState<Loaded>(
    fixture ? { kind: 'ready', preview: fixture.preview } : { kind: 'loading' },
  );
  const [ticks, setTicks] = useState({
    vendorFile: fixture?.ticked?.vendorFile ?? false,
    worktree: fixture?.ticked?.worktree ?? false,
  });
  const [phase, setPhase] = useState<Phase>(fixture?.phase ?? 'confirm');
  const [rows, setRows] = useState<DeleteRow[]>(fixture?.rows ?? []);
  const [last, setLast] = useState<DeleteSessionRequest | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [stopping, setStopping] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const closed = useRef(false);

  const load = useCallback(() => {
    fetchDeletionPreview(session.sessionId).then(
      (preview) => {
        if (!closed.current) setLoaded({ kind: 'ready', preview });
      },
      (error: unknown) => {
        if (!closed.current) setLoaded({ kind: 'failed', error });
      },
    );
  }, [session.sessionId]);
  useEffect(() => {
    closed.current = false;
    if (!fixture) load();
    return () => {
      closed.current = true;
    };
  }, [fixture, load]);

  // The focus follows the phase: a button that went away or was disabled must not strand it on the page.
  const first = useRef(true);
  // biome-ignore lint/correctness/useExhaustiveDependencies: moves the focus when the phase changes
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    const dialog = root.current?.closest<HTMLElement>('[role="dialog"]');
    (dialog?.querySelector<HTMLElement>('[data-autofocus]') ?? dialog)?.focus();
  }, [phase]);

  const preview = loaded.kind === 'ready' ? loaded.preview : null;
  const machine = preview?.owner.name ?? session.deviceName;
  const online = preview?.owner.online ?? true;
  const busy = phase === 'progress';

  async function run(request: DeleteSessionRequest) {
    if (!preview) return;
    setLast(request);
    setProblem(null);
    setRows(plannedRows(request, preview.owner.online));
    setPhase('progress');
    try {
      await deleteSession(session.sessionId, request);
    } catch (e) {
      if (closed.current) return;
      if (e instanceof ApiError && e.code === 'delete-partial' && e.steps) {
        setRows(rowsOf(e.steps));
        setPhase('partial');
        return;
      }
      setProblem(errorText(e));
      setPhase('confirm');
      // A deletion that was refused may have changed what is there: a stopped process, an owner that left.
      if (e instanceof ApiError && (e.code === 'session-running' || e.code === 'owner-offline'))
        load();
      return;
    }
    if (!closed.current) props.onDeleted(session.sessionId);
  }

  async function stop() {
    setStopping(true);
    setProblem(null);
    try {
      await postStop(session.sessionId);
      if (!fixture) load();
    } catch (e) {
      if (!closed.current) setProblem(errorText(e));
    }
    if (!closed.current) setStopping(false);
  }

  const request = preview ? requestOf(preview, ticks) : null;
  const w = preview?.worktree ?? null;
  const loss = w ? lossText(w) : null;
  const running = preview?.running ?? false;
  const file = preview?.vendorFile ?? null;
  const showFile = file !== null || !online;
  const ownerName = machine;

  const head = (heading: string, sub: string) => (
    <>
      <div className="dlg-h">{heading}</div>
      <div className="dlg-sub">
        <span className="tag">{machine}</span>
        <span>{sub}</span>
      </div>
    </>
  );

  let body: ReactNode;
  if (phase === 'progress') {
    body = (
      <>
        {head(t('Deleting “{title}”').replace('{title}', title), t('Keep this window open.'))}
        <Steps rows={rows} machine={machine} worktree={w?.path} />
        <div className="dlg-f">
          <Button variant="quiet" disabled>
            {t('Cancel')}
          </Button>
          <Button variant="danger" disabled>
            {t('Deleting…')}
          </Button>
        </div>
      </>
    );
  } else if (phase === 'partial') {
    const worktreeFailed = rows.some((r) => r.step === 'worktree' && r.state === 'bad');
    const again = last ?? (request as DeleteSessionRequest | null);
    body = (
      <>
        {head(
          t('“{title}” was only partly deleted').replace('{title}', title),
          t('Hoplet’s copy is kept so you can retry.'),
        )}
        <Steps rows={rows} machine={machine} worktree={w?.path} />
        <div className="dlg-f">
          <Button
            variant="quiet"
            onClick={props.onClose}
            data-autofocus=""
            data-testid="cockpit.delete-keep"
          >
            {t('Keep as is')}
          </Button>
          {worktreeFailed ? (
            <Button
              onClick={() => again && void run({ ...again, worktree: false, worktreeDirty: false })}
              data-testid="cockpit.delete-retry-without"
            >
              {t('Retry without the worktree')}
            </Button>
          ) : null}
          <Button
            variant="danger"
            onClick={() => again && void run(again)}
            data-testid="cockpit.delete-retry"
          >
            {t('Retry')}
          </Button>
        </div>
      </>
    );
  } else {
    body = (
      <>
        {head(t('Delete “{title}”?').replace('{title}', title), t('This cannot be undone.'))}
        {loaded.kind === 'loading' ? (
          <div className="dlg-b">
            <div className="xs" role="status" data-testid="cockpit.delete-loading">
              <span className="run">
                <Spinner />
              </span>
              <div>
                <div className="xl">{t('Checking what can be deleted…')}</div>
              </div>
            </div>
          </div>
        ) : loaded.kind === 'failed' ? (
          <div className="xnote bad" role="alert" data-testid="cockpit.delete-error">
            {Icon.alert()}
            <span>{errorText(loaded.error)}</span>
          </div>
        ) : preview ? (
          <>
            <div className="dlg-b">
              <Option
                id="core"
                label={t('Hoplet’s copy')}
                detail={t(
                  'The session in the list, its stored lines and snapshots. It stops syncing from {m}.',
                ).replace('{m}', ownerName)}
                checked
                locked
              />
              {showFile ? (
                <Option
                  id="file"
                  label={t('The session file on {m}').replace('{m}', ownerName)}
                  detail={file ? mono(file.path) : null}
                  checked={online && ticks.vendorFile}
                  disabled={!online}
                  reason={
                    online ? null : (
                      <>
                        {Icon.lock()}
                        <span>
                          {t('{m} is offline. The file stays there.').replace('{m}', ownerName)}
                        </span>
                      </>
                    )
                  }
                  onChange={(on) => setTicks((cur) => ({ ...cur, vendorFile: on }))}
                />
              ) : null}
              {w ? (
                <Option
                  id="worktree"
                  label={t('The worktree and branch Hoplet created')}
                  detail={
                    <>
                      {mono(w.path)} {t('and')} {mono(w.branch)}.{' '}
                      {t('Your own clone stays as it is.')}
                      {loss ? '' : ` ${t('The branch has no unpushed commits.')}`}
                    </>
                  }
                  checked={online && ticks.worktree}
                  disabled={!online}
                  warn={online && loss !== null}
                  reason={
                    !online ? (
                      <>
                        {Icon.lock()}
                        <span>{t('{m} is offline.').replace('{m}', ownerName)}</span>
                      </>
                    ) : loss ? (
                      <>
                        {Icon.alert()}
                        <span>{loss}</span>
                      </>
                    ) : null
                  }
                  onChange={(on) => setTicks((cur) => ({ ...cur, worktree: on }))}
                />
              ) : null}
            </div>
            {running ? (
              <div className="xnote bad" role="alert" data-testid="cockpit.delete-running">
                {Icon.alert()}
                <span>
                  {t(
                    'Claude is running in this session, started by Hoplet on {m}. Stop it first, then delete.',
                  ).replace('{m}', ownerName)}
                </span>
              </div>
            ) : null}
            {!online ? (
              <div className="xnote att" data-testid="cockpit.delete-offline">
                {Icon.info()}
                <span>
                  {t(
                    '{m} is offline. Only Hoplet’s copy can be deleted now. Hoplet will tell {m} to stop syncing it when it reconnects; the file and worktree stay for you to clean up.',
                  ).replaceAll('{m}', ownerName)}
                </span>
              </div>
            ) : null}
            {preview.previousOwner ? (
              <div className="xnote" data-testid="cockpit.delete-previous">
                {Icon.info()}
                <span>
                  {t(
                    'This session was handed off from {m}. Copies made there stay where they are.',
                  ).replace('{m}', preview.previousOwner.name)}
                </span>
              </div>
            ) : null}
          </>
        ) : null}
        {problem ? (
          <div className="xnote bad" role="alert" data-testid="cockpit.delete-problem">
            {Icon.alert()}
            <span>{problem}</span>
          </div>
        ) : null}
        <div className="dlg-f">
          {running ? (
            <span className="l">
              <Button
                className="sm"
                icon={stopping ? <Spinner /> : Icon.stop()}
                disabled={stopping}
                onClick={() => void stop()}
                data-testid="cockpit.delete-stop"
              >
                {t('Stop the session')}
              </Button>
            </span>
          ) : null}
          <Button
            variant="quiet"
            onClick={props.onClose}
            data-autofocus=""
            data-testid="cockpit.delete-cancel"
          >
            {t('Cancel')}
          </Button>
          <Button
            variant="danger"
            disabled={!request || running || stopping}
            onClick={() => request && void run(request)}
            data-testid="cockpit.delete-confirm"
          >
            {t('Delete')}
          </Button>
        </div>
      </>
    );
  }

  return (
    <Dialog
      label={t('Delete session')}
      onClose={() => {
        if (!busy) props.onClose();
      }}
      className="dx"
      data-testid="cockpit.delete-dialog"
    >
      <div ref={root}>{body}</div>
    </Dialog>
  );
}
