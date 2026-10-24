import type { ChildCollect, LiveSessionMeta } from '@hoplet/shared';
import { type ReactNode, useState } from 'react';
import { Badge } from '../../components/Badge.tsx';
import { Button } from '../../components/Button.tsx';
import { KidGlyph, KidState } from '../../components/child-parts.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { fill as fillNode } from '../../components/fill.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { postChildCancel, postChildCollect } from '../../lib/api.ts';
import {
  askLine,
  type ChildAsk,
  cancelAllRoots,
  cancelPlan,
  type Kid,
  liveKids,
  rollTags,
  rollup,
  usedTokens,
} from '../../lib/children.ts';
import { errorText } from '../../lib/errors.ts';
import { formatTimer } from '../../lib/format.ts';
import { t } from '../../lib/i18n.ts';
import { fill } from '../../lib/templates.ts';
import { formatTokens } from '../../lib/usage.ts';
import type { ChildrenView } from '../../lib/use-children.ts';
import './children-tab.css';
import { Counts, StatusTag } from './lists.tsx';
import { splitPath } from './model.ts';
import { Quiet } from './viewer.tsx';

/** The live sessions a tree may hold (ADR 0035): the core refuses a start past it. */
export const TREE_LIVE_LIMIT = 8;

/** Development fixture of the Children tab: the row that starts open, its report, and an open cancel. */
export type ChildrenTabFixture = {
  open?: string;
  /** What collecting each child answered, by child. */
  collected?: Record<string, ChildCollect>;
  /** The child whose cancel dialog starts open, or `all` for Cancel all. */
  cancel?: string;
};

type Collected =
  | { phase: 'loading' }
  | { phase: 'ok'; data: ChildCollect }
  | { phase: 'error'; error: string };

type Cancelling = {
  /** The sessions it stops, the one cancelled first. */
  targets: Kid[];
  /** The direct children to cancel with their subtrees; one for a single cancel. */
  roots: { id: string; subtree: boolean }[];
  all: boolean;
};

const sep = (
  <i className="kid-sep" aria-hidden="true">
    ·
  </i>
);

/**
 * The Children tab of the inspector (issue #81, ADR 0035): every session this one started and theirs below them,
 * with state, elapsed time, machine and branch; a row opens in place to the child's report and changed files; a
 * child or its subtree is cancelled after a dialog says what stops and what stays.
 */
export function ChildrenTab(props: {
  session: LiveSessionMeta;
  view: ChildrenView;
  onOpenSession?: ((sessionId: string) => void) | undefined;
  fixture?: ChildrenTabFixture | undefined;
}) {
  const { session, view, fixture } = props;
  const kids = view.data.kids;
  const [openId, setOpenId] = useState<string | null>(fixture?.open ?? null);
  const [collected, setCollected] = useState<Record<string, Collected>>(() =>
    Object.fromEntries(
      Object.entries(fixture?.collected ?? {}).map(([id, data]) => [id, { phase: 'ok', data }]),
    ),
  );
  const [cancelling, setCancelling] = useState<Cancelling | null>(() =>
    fixture?.cancel ? planOf(fixture.cancel) : null,
  );
  const [cancelBusy, setCancelBusy] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);

  function planOf(id: string): Cancelling | null {
    if (id === 'all') {
      const roots = cancelAllRoots(kids, session.sessionId);
      const targets = liveKids(kids);
      return targets.length > 0
        ? { targets, roots: roots.map((k) => ({ id: k.id, subtree: true })), all: true }
        : null;
    }
    const plan = cancelPlan(kids, id);
    const first = plan.targets[0];
    return first
      ? { targets: plan.targets, roots: [{ id, subtree: plan.subtree }], all: false }
      : null;
  }

  function collect(kid: Kid) {
    if (fixture) return;
    setCollected((c) => ({ ...c, [kid.id]: { phase: 'loading' } }));
    // A child is collected through the session that started it: the core answers only for direct children.
    postChildCollect(kid.parentId ?? session.sessionId, kid.id)
      .then((data) => setCollected((c) => ({ ...c, [kid.id]: { phase: 'ok', data } })))
      .catch((err: unknown) =>
        setCollected((c) => ({ ...c, [kid.id]: { phase: 'error', error: errorText(err) } })),
      );
  }

  function toggle(kid: Kid) {
    if (openId === kid.id) {
      setOpenId(null);
      return;
    }
    setOpenId(kid.id);
    if (!collected[kid.id] || collected[kid.id]?.phase === 'error') collect(kid);
  }

  function askCancel(id: string) {
    setCancelError(null);
    setCancelling(planOf(id));
  }

  async function confirmCancel(plan: Cancelling) {
    setCancelBusy(true);
    setCancelError(null);
    try {
      for (const root of plan.roots)
        await postChildCancel(session.sessionId, root.id, root.subtree);
      setCancelling(null);
      view.refresh();
    } catch (err) {
      setCancelError(errorText(err));
    } finally {
      setCancelBusy(false);
    }
  }

  const titleOf = (id: string | null) =>
    id === session.sessionId || id === null
      ? session.titleOverride?.trim() || session.title || t('Untitled session')
      : (kids.find((k) => k.id === id)?.title ?? t('Untitled session'));

  let body: ReactNode;
  if (view.phase === 'loading' && kids.length === 0) {
    body = (
      <Quiet icon={<Spinner />} text={t('Loading…')} status testid="workspace.children.loading" />
    );
  } else if (view.phase === 'error' && kids.length === 0) {
    body = (
      <Quiet
        icon={Icon.alert()}
        title={t('The child sessions could not be read')}
        text={view.error ?? ''}
        action={
          <Button onClick={view.refresh} data-testid="workspace.children.retry">
            {t('Try again')}
          </Button>
        }
        testid="workspace.children.error"
      />
    );
  } else if (kids.length === 0) {
    body = (
      <Quiet
        icon={Icon.fork()}
        title={t('No child sessions')}
        text={t(
          'When this session starts sessions of its own to work on parts of its task, they appear here.',
        )}
        testid="workspace.children.empty"
      />
    );
  } else {
    const tags = rollTags(rollup(kids));
    body = (
      <div className="kp-b" data-testid="workspace.children.list">
        <div className="kp-roll" data-testid="workspace.children.roll">
          {tags.map((tag) => (
            <Badge key={tag.state} tone={toneOf(tag.state)}>
              {tag.text}
            </Badge>
          ))}
        </div>
        {kids.map((kid) => (
          <KidRow
            key={kid.id}
            kid={kid}
            open={openId === kid.id}
            collected={collected[kid.id]}
            asks={view.asks[kid.id]}
            canCancel={cancelPlan(kids, kid.id).targets.some(
              (k) => k.state === 'running' || k.state === 'blocked',
            )}
            onToggle={() => toggle(kid)}
            onRetry={() => collect(kid)}
            onCancel={() => askCancel(kid.id)}
            onOpenSession={props.onOpenSession}
          />
        ))}
        {view.data.truncated ? (
          <p className="kp-more" data-testid="workspace.children.truncated">
            {t('This session has more descendants than the list shows.')}
          </p>
        ) : null}
      </div>
    );
  }

  const live = liveKids(kids).length;
  return (
    <div className="kp" data-testid="workspace.children">
      {body}
      {kids.length > 0 ? (
        <div className="kp-f">
          <span className="kp-meta" data-testid="workspace.children.live">
            {session.parentId === null
              ? fill(t('{count} live of {limit} in this tree'), {
                  count: String(live),
                  limit: String(TREE_LIVE_LIMIT),
                })
              : fill(t('{count} live'), { count: String(live) })}
          </span>
          <Button
            variant="quiet"
            disabled={live === 0}
            title={fill(t('Cancel the {count} live sessions'), { count: String(live) })}
            onClick={() => askCancel('all')}
            data-testid="workspace.children.cancel-all"
          >
            {t('Cancel all')}
          </Button>
        </div>
      ) : null}
      {cancelling ? (
        <CancelDialog
          plan={cancelling}
          parentTitle={titleOf(cancelling.targets[0]?.parentId ?? null)}
          busy={cancelBusy}
          error={cancelError}
          onKeep={() => setCancelling(null)}
          onConfirm={() => void confirmCancel(cancelling)}
        />
      ) : null}
    </div>
  );
}

function toneOf(state: Kid['state']): 'live' | 'attention' | 'success' | 'danger' | 'neutral' {
  switch (state) {
    case 'running':
      return 'live';
    case 'blocked':
      return 'attention';
    case 'done':
      return 'success';
    case 'failed':
    case 'timed-out':
      return 'danger';
    default:
      return 'neutral';
  }
}

function KidRow(props: {
  kid: Kid;
  open: boolean;
  collected: Collected | undefined;
  asks: ChildAsk[] | undefined;
  canCancel: boolean;
  onToggle: () => void;
  onRetry: () => void;
  onCancel: () => void;
  onOpenSession?: ((sessionId: string) => void) | undefined;
}) {
  const { kid, open } = props;
  const need = kid.state === 'blocked';
  const ask = props.asks?.[0];
  const line = ask ? askLine(ask) : null;
  const cancelLabel = fill(t('Cancel “{title}”'), { title: kid.title });
  return (
    <div
      className={`kid${open ? ' open' : ''}${need ? ' need' : ''}`}
      style={{ marginLeft: `calc(${kid.depth - 1} * var(--space-4))` }}
      data-depth={kid.depth}
      data-testid={`workspace.children.row.${kid.id}`}
    >
      {kid.depth > 1 ? <span className="kid-guide" aria-hidden="true" /> : null}
      <div className="kid-h">
        <button
          type="button"
          className="kid-t"
          aria-expanded={open}
          onClick={props.onToggle}
          data-testid="workspace.children.toggle"
        >
          <KidGlyph state={kid.state} />
          <span className="kx">
            <b className="kt">{kid.title}</b>
            <span className="km">
              <span>{kid.machine}</span>
              {kid.branch ? (
                <>
                  {sep}
                  {Icon.branch()}
                  <span className="mono">{kid.branch}</span>
                </>
              ) : null}
            </span>
            <span className="ks">
              <KidState state={kid.state} />
              {kid.elapsedSeconds !== null ? (
                <>
                  {sep}
                  <span>{formatTimer(kid.elapsedSeconds * 1000)}</span>
                </>
              ) : null}
            </span>
          </span>
        </button>
        {props.canCancel ? (
          <IconButton
            label={cancelLabel}
            icon={Icon.stop()}
            onClick={props.onCancel}
            data-testid="workspace.children.cancel"
          />
        ) : null}
      </div>
      {need ? (
        <div className="kid-ask" data-testid="workspace.children.ask">
          <div className="q">
            {line
              ? fillNode(
                  line.command
                    ? t('Asks to run {command}. Only you can answer, in the child.')
                    : t('Asks to use {command}. Only you can answer, in the child.'),
                  { command: <b className={line.command ? 'mono' : ''}>{line.text}</b> },
                )
              : t('Waiting for a permission only you can answer, in the child.')}
          </div>
          {props.onOpenSession ? (
            <Button
              variant="primary"
              icon={Icon.external()}
              onClick={() => props.onOpenSession?.(kid.id)}
              data-testid="workspace.children.answer"
            >
              {t('Open to answer')}
            </Button>
          ) : null}
        </div>
      ) : null}
      {open ? (
        <KidDetail
          kid={kid}
          collected={props.collected}
          canCancel={props.canCancel}
          onRetry={props.onRetry}
          onCancel={props.onCancel}
          onOpenSession={props.onOpenSession}
        />
      ) : null}
    </div>
  );
}

/** An opened row: the child's report, what it cost and the files it changed. */
function KidDetail(props: {
  kid: Kid;
  collected: Collected | undefined;
  canCancel: boolean;
  onRetry: () => void;
  onCancel: () => void;
  onOpenSession?: ((sessionId: string) => void) | undefined;
}) {
  const { kid, collected } = props;
  let report: ReactNode = null;
  if (!collected || collected.phase === 'loading') {
    report = (
      <p className="kid-note" role="status">
        <Spinner />
        {t('Loading…')}
      </p>
    );
  } else if (collected.phase === 'error') {
    report = (
      <p className="kid-note bad" role="alert">
        <span>{collected.error}</span>
        <Button
          variant="quiet"
          onClick={props.onRetry}
          data-testid="workspace.children.collect-retry"
        >
          {t('Try again')}
        </Button>
      </p>
    );
  } else {
    const { summary, summaryTruncated, changes, usage } = collected.data;
    const files = changes.files ?? [];
    report = (
      <>
        {summary.trim() ? (
          <p className="kid-sum" data-testid="workspace.children.summary">
            {summary.trim()}
            {summaryTruncated ? '…' : ''}
          </p>
        ) : (
          <p className="kid-note">{t('No report yet.')}</p>
        )}
        <span className="kid-cost" data-testid="workspace.children.tokens">
          {fill(t('{tokens} tokens'), { tokens: formatTokens(usedTokens(usage)) })}
        </span>
        {changes.status === 'ok' ? (
          files.length > 0 ? (
            <div className="kid-files">
              <div className="fl-h">
                <b>{t('Changed files')}</b>
                <span>{String(changes.total ?? files.length)}</span>
              </div>
              <ul className="fls" data-testid="workspace.children.files">
                {files.map((f) => {
                  const { dir, name } = splitPath(f.path);
                  return (
                    <li key={f.path} className="fr" title={f.path}>
                      <StatusTag status={f.status} />
                      <span className="fn">
                        <b>{name}</b>
                        {dir ? <i>{dir}</i> : null}
                      </span>
                      <span className="fd">
                        <Counts change={f} />
                      </span>
                    </li>
                  );
                })}
              </ul>
              {changes.shared ? (
                <span className="kid-note">
                  {t(
                    'No worktree of its own: these are the uncommitted changes of the shared directory.',
                  )}
                </span>
              ) : null}
              {changes.truncated ? (
                <span className="kid-note">
                  {fill(t('and {count} more'), {
                    count: String((changes.total ?? files.length) - files.length),
                  })}
                </span>
              ) : null}
            </div>
          ) : (
            <p className="kid-note">{t('No changed files.')}</p>
          )
        ) : (
          <p className="kid-note">
            {changes.status === 'owner-offline'
              ? fill(t('Its changes cannot be read while {machine} is offline.'), {
                  machine: kid.machine,
                })
              : t('Its changes are not known: its directory is not a git repository.')}
          </p>
        )}
      </>
    );
  }
  return (
    <div className="kid-x" data-testid="workspace.children.detail">
      {report}
      <div className="kid-f">
        {props.onOpenSession ? (
          <Button
            icon={Icon.external()}
            onClick={() => props.onOpenSession?.(kid.id)}
            data-testid="workspace.children.open"
          >
            {t('Open session')}
          </Button>
        ) : null}
        {props.canCancel ? (
          <Button variant="quiet" icon={Icon.stop()} onClick={props.onCancel}>
            {t('Cancel')}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

/** What a cancel stops and what it keeps, before anything is stopped. */
function CancelDialog(props: {
  plan: Cancelling;
  parentTitle: string;
  busy: boolean;
  error: string | null;
  onKeep: () => void;
  onConfirm: () => void;
}) {
  const { plan } = props;
  const [first, second] = plan.targets;
  if (!first) return null;
  const one = plan.targets.length === 1;
  const n = String(plan.targets.length);
  const title = one
    ? fill(t('Cancel “{title}”?'), { title: first.title })
    : fill(t('Cancel {count} sessions?'), { count: n });
  const lead = one
    ? null
    : plan.all
      ? t('Every live session below this one is cancelled.')
      : plan.targets.length === 2 && second
        ? fillNode(t('{first} started {second}. Cancelling the first cancels both.'), {
            first: <b>{first.title}</b>,
            second: <b>{second.title}</b>,
          })
        : fillNode(t('{first} started the others. Cancelling it cancels all {count}.'), {
            first: <b>{first.title}</b>,
            count: n,
          });
  const where = (k: Kid) =>
    k.branch
      ? fillNode(t('{branch} on {machine}'), {
          branch: <span className="mono">{k.branch}</span>,
          machine: k.machine,
        })
      : fill(t('its directory on {machine}'), { machine: k.machine });
  const stops: ReactNode[] = one
    ? [
        t('The session stops now and its current turn is interrupted.'),
        fill(t('“{title}” is told it was cancelled and carries on.'), { title: props.parentTitle }),
      ]
    : [
        ...plan.targets.map((k) => (
          <>
            <b>{k.title}</b> · {k.machine}
          </>
        )),
        t('Each is interrupted now. Their parent is told and carries on.'),
      ];
  const stays: ReactNode[] = one
    ? [
        first.branch
          ? fillNode(
              t('The worktree and the branch {branch} on {machine}, with every change so far.'),
              {
                branch: <span className="mono">{first.branch}</span>,
                machine: first.machine,
              },
            )
          : fill(t('Its directory on {machine}, with every change so far.'), {
              machine: first.machine,
            }),
        t('Its transcript, which you can still open.'),
      ]
    : [
        t('Their worktrees and branches, with every change so far:'),
        ...plan.targets.map(where),
        t('Their transcripts, which you can still open.'),
      ];
  return (
    <Dialog
      label={title}
      onClose={() => {
        if (!props.busy) props.onKeep();
      }}
      className="kcd"
      data-testid="workspace.children.cancel-dialog"
    >
      <div className="dlg-h">{title}</div>
      <div className="dlg-b">
        {lead ? <p className="kcd-p">{lead}</p> : null}
        <div className="cdg">
          <span className="lb st">{t('Stops')}</span>
          <ul>
            {stops.map((s, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: a fixed list built for this dialog
              <li key={i}>{s}</li>
            ))}
          </ul>
          <span className="lb">{t('Stays')}</span>
          <ul>
            {stays.map((s, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: a fixed list built for this dialog
              <li key={i}>{s}</li>
            ))}
          </ul>
        </div>
        {props.error ? (
          <p className="kcd-err" role="alert">
            {props.error}
          </p>
        ) : null}
      </div>
      <div className="dlg-f">
        <Button
          variant="quiet"
          onClick={props.onKeep}
          disabled={props.busy}
          data-autofocus=""
          data-testid="workspace.children.keep"
        >
          {t('Keep running')}
        </Button>
        <Button
          variant="danger"
          onClick={props.onConfirm}
          disabled={props.busy}
          data-testid="workspace.children.confirm"
        >
          {one ? t('Cancel session') : fill(t('Cancel {count} sessions'), { count: n })}
        </Button>
      </div>
    </Dialog>
  );
}
