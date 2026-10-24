import type { ConfigChangeObject, SessionConfigChange } from '@hoplet/shared';
import type { ReactNode } from 'react';
import { Button } from '../../../components/Button.tsx';
import { ObjectCounts, ObjectIcon, TakesTag } from '../../../components/config-change-parts.tsx';
import { fill } from '../../../components/fill.tsx';
import { Icon, Spinner } from '../../../components/icons.tsx';
import {
  objectKind,
  objectName,
  objectPath,
  takesEffectLine,
  tookEffectLine,
} from '../../../lib/config-changes.ts';
import { ERROR_COPY } from '../../../lib/errors.ts';
import { clock } from '../../../lib/format.ts';
import { t } from '../../../lib/i18n.ts';
import { formatExpiry } from './handoff-card.tsx';

/** A session's requests to change its configuration and how to answer them (ADR 0036). */
export type ConfigChangeView = {
  changes: readonly SessionConfigChange[];
  /** The changes decided or undone in this window: their outcome stays in view. */
  touched: ReadonlySet<string>;
  /** The changes whose refused undo was put away in this window. */
  dismissed: ReadonlySet<string>;
  /** The change a decision or an undo is running for. */
  busy: string | null;
  /** Deciding is possible: this window is connected to the core. */
  decidable: boolean;
  providerName: string;
  projectName: string;
  deviceName: string;
  onDecide: (changeId: string, approve: boolean) => void;
  onUndo: (changeId: string) => void;
  /** Opens the change in the review in the inspector. */
  onReview: (changeId: string) => void;
  onDismiss: (changeId: string) => void;
};

/** Where an object lives: the file's path, or the setting's name. */
function where(object: ConfigChangeObject): string {
  return objectPath(object) ?? object.name;
}

function title(change: SessionConfigChange, provider: string): string {
  const n = change.objects.length;
  return (
    n === 1
      ? t('{provider} proposes a change to this project')
      : t('{provider} proposes {n} changes to this project')
  )
    .replace('{provider}', provider)
    .replace('{n}', String(n));
}

/** The pending request: its objects in rows, Review changes and Decline (design/screens/self-config.html b-card). */
export function ConfigChangeCard(props: {
  change: SessionConfigChange;
  view: ConfigChangeView;
  now: number;
}) {
  const { change, view } = props;
  const heading = title(change, view.providerName);
  const busy = view.busy === change.changeId;
  return (
    <fieldset className="ask cfx" data-testid="cockpit.config-change">
      <legend className="sr-only">{heading}</legend>
      <div className="ask-h">
        {Icon.sliders()}
        <span className="q">{heading}</span>
      </div>
      <div className="tgs">
        <span className="tag line">
          {Icon.folder()}
          {view.projectName} · {view.deviceName}
        </span>
        <span className="tag line">
          {Icon.check()}
          {t('Decided as a whole')}
        </span>
        <span className="tag line" data-testid="cockpit.config-change-expiry">
          {Icon.clock()}
          {t('Expires in {time}').replace('{time}', formatExpiry(change.expiresAt - props.now))}
        </span>
      </div>
      {change.reason ? (
        <div className="hq" data-testid="cockpit.config-change-reason">
          {change.reason}
        </div>
      ) : null}
      {change.objects.map((o) => (
        <div className="oc" key={`${o.kind}:${o.name}`} data-testid="cockpit.config-change-object">
          <div className="oc-h">
            <ObjectIcon object={o} />
            <span className="nm">
              <b>{objectName(o)}</b>
              <span className="mono">
                {objectKind(o)} · {where(o)}
              </span>
            </span>
            <ObjectCounts object={o} />
            <TakesTag object={o} />
          </div>
        </div>
      ))}
      <div className="ask-f">
        <Button
          variant="primary"
          icon={Icon.panel()}
          onClick={() => view.onReview(change.changeId)}
          data-testid="cockpit.config-change-review"
        >
          {t('Review changes')}
        </Button>
        <Button
          disabled={busy || !view.decidable}
          onClick={() => view.onDecide(change.changeId, false)}
          data-testid="cockpit.config-change-decline"
        >
          {t('Decline')}
        </Button>
        <span className="ask-meta">
          {Icon.clock()}
          <span>{takesEffectLine(change.objects)}</span>
        </span>
      </div>
      <div className="rq-note">
        {Icon.info()}
        <span>
          {t(
            'Nothing is written until you approve. Earlier versions are kept for 30 days, so you can undo. The request also ends if the session stops or is handed off.',
          )}
        </span>
      </div>
    </fieldset>
  );
}

/** The objects as rows of an outcome; `struck` when they were undone. */
function Rows(props: {
  objects: readonly ConfigChangeObject[];
  struck?: boolean;
  /** The key (`kind:name`) of the object that changed since, of an undo that did not run. */
  stale?: string | undefined;
}) {
  return (
    <div className="cres-rows">
      {props.objects.map((o) => {
        const key = `${o.kind}:${o.name}`;
        return (
          <div className={`cres-r${props.struck ? ' struck' : ''}`} key={key}>
            <ObjectIcon object={o} />
            <span className="nmt">{objectName(o)}</span>
            <span className="mono">{where(o)}</span>
            {props.stale === undefined ? (
              <ObjectCounts object={o} />
            ) : props.stale === key ? (
              <span className="tag att">{t('Changed since')}</span>
            ) : (
              <span className="tag line">{t('Unchanged')}</span>
            )}
          </div>
        );
      })}
    </div>
  );
}

function Head(props: { icon: ReactNode; title: string; meta?: string }) {
  return (
    <div className="cres-h">
      {props.icon}
      <b>{props.title}</b>
      {props.meta ? <span className="meta">{props.meta}</span> : null}
    </div>
  );
}

/** Why a request was not applied: the copy of its code, or its reason. */
function failure(change: SessionConfigChange): string {
  const copy = (ERROR_COPY as Record<string, string | undefined>)[change.error ?? ''];
  if (copy) return t(copy);
  return [t('Nothing was written.'), change.errorReason].filter(Boolean).join(' ');
}

/** The name of the object that changed since, from the key an undo reported (`memory:project`). */
function staleName(change: SessionConfigChange): string {
  const o = change.objects.find((x) => `${x.kind}:${x.name}` === change.errorReason);
  return o ? (objectPath(o) ?? objectName(o)) : (change.errorReason ?? '');
}

/**
 * What is left in the turn once a request is decided, expired, has run or was undone
 * (design/screens/self-config.html b-applied, b-undone, b-expired, conv-undo-stale).
 */
export function ConfigChangeResult(props: { change: SessionConfigChange; view: ConfigChangeView }) {
  const { change, view } = props;
  const busy = view.busy === change.changeId;
  const at = (ts: number | null) => clock(ts ?? undefined);
  const id = 'cockpit.config-change-result';
  const review = (
    <Button
      variant="quiet"
      onClick={() => view.onReview(change.changeId)}
      data-testid="cockpit.config-change-open"
    >
      {t('Open review')}
    </Button>
  );
  switch (change.state) {
    case 'pending':
      return null;
    case 'approved':
      return (
        <div className="cres cfx" role="status" data-testid={id} data-state="approved">
          <Head
            icon={<Spinner />}
            title={t('Applying changes')}
            meta={t('You approved · {time}').replace('{time}', at(change.decidedAt))}
          />
          <Rows objects={change.objects} />
        </div>
      );
    case 'applied':
      if (change.error && !view.dismissed.has(change.changeId)) {
        const file = staleName(change);
        return (
          <div className="cres cfx bad" role="alert" data-testid={id} data-state="undo-refused">
            <Head icon={Icon.alert()} title={t('Undo did not run')} meta={change.error} />
            <Rows
              objects={change.objects}
              {...(change.error === 'stale' ? { stale: change.errorReason ?? '' } : {})}
            />
            <p>
              {change.error === 'stale'
                ? fill(
                    t(
                      '{file} changed after the change was applied, so Hoplet restored nothing: an undo is all or none.',
                    ),
                    { file: <b>{file}</b> },
                  )
                : t('Hoplet restored nothing: an undo is all or none. {reason}').replace(
                    '{reason}',
                    change.errorReason ?? '',
                  )}
            </p>
            <div className="cres-f">
              {change.error !== 'stale' && change.undoable ? (
                <Button
                  icon={Icon.undo()}
                  disabled={busy || !view.decidable}
                  onClick={() => view.onUndo(change.changeId)}
                  data-testid="cockpit.config-change-undo"
                >
                  {t('Undo')}
                </Button>
              ) : null}
              <Button
                variant="quiet"
                onClick={() => view.onDismiss(change.changeId)}
                data-testid="cockpit.config-change-dismiss"
              >
                {t('Dismiss')}
              </Button>
            </div>
          </div>
        );
      }
      return (
        <div className="cres cfx ok" role="status" data-testid={id} data-state="applied">
          <Head
            icon={Icon.check()}
            title={t('Changes applied')}
            meta={t('You approved · {time}').replace('{time}', at(change.decidedAt))}
          />
          <Rows objects={change.objects} />
          <div className="cres-f">
            {change.undoable ? (
              <Button
                icon={Icon.undo()}
                disabled={busy || !view.decidable}
                onClick={() => view.onUndo(change.changeId)}
                data-testid="cockpit.config-change-undo"
              >
                {t('Undo')}
              </Button>
            ) : null}
            {review}
            <span className="meta">{tookEffectLine(change.objects)}</span>
          </div>
        </div>
      );
    case 'undone':
      return (
        <div className="cres cfx" role="status" data-testid={id} data-state="undone">
          <Head
            icon={Icon.undo()}
            title={t('Changes undone')}
            meta={t('You · {time}').replace('{time}', at(change.undoneAt))}
          />
          <Rows objects={change.objects} struck />
          <p>{t('The earlier versions are back. Hoplet keeps the undone versions for 30 days.')}</p>
        </div>
      );
    case 'declined':
      return (
        <div className="cres cfx" role="status" data-testid={id} data-state="declined">
          <Head
            icon={Icon.ban()}
            title={t('Changes declined')}
            meta={t('You · {time}').replace('{time}', at(change.decidedAt))}
          />
          <Rows objects={change.objects} struck />
          <p>{t('Nothing was written.')}</p>
        </div>
      );
    case 'expired':
      return (
        <div className="cres cfx" role="status" data-testid={id} data-state="expired">
          <Head
            icon={Icon.clock()}
            title={t('Change request expired')}
            meta={at(change.decidedAt ?? change.expiresAt)}
          />
          <Rows objects={change.objects} struck />
          <p>
            {t(
              'Nobody decided in 24 hours, or the session stopped or was handed off first. Nothing was written. The session can propose the changes again.',
            )}
          </p>
        </div>
      );
    case 'failed':
      return (
        <div className="cres cfx bad" role="alert" data-testid={id} data-state="failed">
          <Head
            icon={Icon.alert()}
            title={t('Changes not applied')}
            meta={change.error ?? 'failed'}
          />
          <Rows
            objects={change.objects}
            struck
            {...(change.error === 'stale' ? { stale: change.errorReason ?? '' } : {})}
          />
          <p>{failure(change)}</p>
        </div>
      );
  }
}

/** The pending request, if any. */
export function PendingConfigChange(props: { view: ConfigChangeView; now: number }) {
  const pending = props.view.changes.find((c) => c.state === 'pending');
  return pending ? <ConfigChangeCard change={pending} view={props.view} now={props.now} /> : null;
}

/** The outcomes a turn that began at `since` shows: its own requests, and the ones decided in this window. */
export function ConfigChangeResults(props: { view: ConfigChangeView; since: number }) {
  const { view } = props;
  return view.changes
    .filter(
      (c) => c.state !== 'pending' && (c.createdAt >= props.since || view.touched.has(c.changeId)),
    )
    .reverse()
    .map((c) => <ConfigChangeResult key={c.changeId} change={c} view={view} />);
}

/** With no live turn: the newest outcome decided or undone in this window. */
export function LatestConfigChangeResult(props: { view: ConfigChangeView }) {
  const { view } = props;
  const latest = view.changes.find((c) => c.state !== 'pending' && view.touched.has(c.changeId));
  return latest ? <ConfigChangeResult change={latest} view={view} /> : null;
}
