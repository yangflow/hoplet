import type { ConfigChangeObject, SessionConfigChange } from '@hoplet/shared';
import { type CSSProperties, useMemo, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import {
  ObjectCheck,
  ObjectCounts,
  ObjectIcon,
  SettingValues,
  TakesTag,
} from '../../components/config-change-parts.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import {
  objectKind,
  objectName,
  objectPath,
  takesEffectLine,
  tookEffectLine,
} from '../../lib/config-changes.ts';
import { clock } from '../../lib/format.ts';
import { languageOfPath } from '../../lib/highlight.ts';
import { t } from '../../lib/i18n.ts';
import { gutterFor, largestLine, parseDiff } from './model.ts';
import { DiffRow, Quiet } from './viewer.tsx';

const keyOf = (o: ConfigChangeObject) => `${o.kind}:${o.name}`;

/** A file object's diff, its unchanged regions folded and named; the review cannot read the file to open them. */
function ObjectDiff({ object }: { object: ConfigChangeObject }) {
  const rows = useMemo(() => parseDiff(object.diff), [object.diff]);
  const language = languageOfPath(objectPath(object) ?? '');
  const gutter = { '--ws-gutter': gutterFor(largestLine(rows)) } as CSSProperties;
  return (
    <section
      className="rw-d"
      // biome-ignore lint/a11y/noNoninteractiveTabindex: the diff scrolls sideways and must be reachable by keyboard
      tabIndex={0}
      aria-label={t('Diff')}
      data-testid="config-review.diff"
    >
      <div className="ws-diff" style={gutter}>
        {rows.map((row, i) =>
          row.kind === 'fold' ? (
            // biome-ignore lint/suspicious/noArrayIndexKey: rows have no identity
            <div className="ws-dl hunk" key={i}>
              <span className="n" />
              <span className="n" />
              <span className="s" />
              <span className="c">
                {t('{n} unchanged lines').replace('{n}', String(row.count))}
              </span>
            </div>
          ) : (
            // biome-ignore lint/suspicious/noArrayIndexKey: rows have no identity
            <DiffRow key={i} row={row} language={language}>
              {null}
            </DiffRow>
          ),
        )}
      </div>
    </section>
  );
}

/** What became of a change that is no longer waiting, for the review's footer. */
function outcome(change: SessionConfigChange): string {
  const at = (ts: number | null) => clock(ts ?? undefined);
  switch (change.state) {
    case 'approved':
      return t('Applying changes');
    case 'applied':
      return `${t('Changes applied')} · ${tookEffectLine(change.objects)}`;
    case 'declined':
      return `${t('Changes declined')} · ${at(change.decidedAt)}`;
    case 'expired':
      return t('Change request expired');
    case 'failed':
      return `${t('Changes not applied')} · ${change.error ?? 'failed'}`;
    case 'undone':
      return `${t('Changes undone')} · ${at(change.undoneAt)}`;
    default:
      return '';
  }
}

/**
 * The review of a session's request to change its configuration, in the wide inspector
 * (design/screens/self-config.html b-review): the objects on the left, the selected one's diff on the right, and
 * the decision on all of them in the footer.
 */
export function ConfigReview(props: {
  change: SessionConfigChange | null;
  /** A decision or an undo is running for this change. */
  busy: boolean;
  /** Deciding is possible: this window is connected to the core. */
  decidable: boolean;
  /** The object shown first, by `kind:name`; the first object otherwise. */
  initial?: string | undefined;
  onDecide: (approve: boolean) => Promise<string | null>;
  onUndo: () => Promise<string | null>;
}) {
  const { change } = props;
  const [picked, setPicked] = useState<string | null>(props.initial ?? null);
  const [error, setError] = useState<string | null>(null);
  if (!change) {
    return (
      <Quiet
        icon={Icon.sliders()}
        title={t('Change request')}
        text={t('This request is no longer there.')}
        testid="config-review.gone"
      />
    );
  }
  const objects = change.objects;
  const current = objects.find((o) => keyOf(o) === picked) ?? objects[0];
  const n = objects.length;
  const pending = change.state === 'pending';
  const run = (act: Promise<string | null>) => {
    setError(null);
    void act.then(setError);
  };
  return (
    <div className="rw-w cfx" data-testid="config-review">
      <div className="rw">
        <div className="rw-l">
          <div className="rw-lh">
            {(n === 1 ? t('1 object') : t('{n} objects')).replace('{n}', String(n))}
          </div>
          {objects.map((o) => {
            const on = o === current;
            return (
              <button
                type="button"
                key={keyOf(o)}
                className={`rw-i${on ? ' on' : ''}`}
                aria-current={on || undefined}
                onClick={() => setPicked(keyOf(o))}
                data-testid="config-review.object"
              >
                <ObjectIcon object={o} />
                <span className="nm">
                  <b>{objectName(o)}</b>
                  <ObjectCounts object={o} />
                </span>
              </button>
            );
          })}
        </div>
        {current ? (
          <section
            className="rw-v"
            // biome-ignore lint/a11y/noNoninteractiveTabindex: the object scrolls and must be reachable by keyboard
            tabIndex={0}
            aria-label={objectName(current)}
            data-testid="config-review.view"
          >
            <div className="rw-vh">
              <b className="rw-name">{objectName(current)}</b>
              <span className="tag line">{objectKind(current)}</span>
              <span className="sp" />
              <TakesTag object={current} />
            </div>
            <span className="rw-path">{objectPath(current) ?? current.name}</span>
            {current.kind === 'config' ? (
              <SettingValues object={current} />
            ) : current.content === null && current.diff === '' ? null : (
              <ObjectDiff object={current} />
            )}
            <ObjectCheck object={current} />
          </section>
        ) : null}
      </div>
      <div className="rw-f">
        {pending ? (
          <>
            <Button
              variant="primary"
              disabled={props.busy || !props.decidable}
              onClick={() => run(props.onDecide(true))}
              data-testid="config-review.approve"
            >
              {(n === 1 ? t('Approve') : t('Approve all {n}')).replace('{n}', String(n))}
            </Button>
            <Button
              disabled={props.busy || !props.decidable}
              onClick={() => run(props.onDecide(false))}
              data-testid="config-review.decline"
            >
              {t('Decline')}
            </Button>
          </>
        ) : change.state === 'applied' && change.undoable ? (
          <Button
            icon={Icon.undo()}
            disabled={props.busy || !props.decidable}
            onClick={() => run(props.onUndo())}
            data-testid="config-review.undo"
          >
            {t('Undo')}
          </Button>
        ) : null}
        {error ? (
          <span className="rw-err" role="alert" data-testid="config-review.error">
            {error}
          </span>
        ) : null}
        <span className="meta" data-testid="config-review.state">
          {props.busy || change.state === 'approved' ? <Spinner /> : Icon.clock()}
          <span>{pending ? takesEffectLine(objects) : outcome(change)}</span>
        </span>
      </div>
    </div>
  );
}
