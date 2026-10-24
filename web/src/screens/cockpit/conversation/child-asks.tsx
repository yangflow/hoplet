import { useState } from 'react';
import { Button } from '../../../components/Button.tsx';
import { fill } from '../../../components/fill.tsx';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon } from '../../../components/icons.tsx';
import { postPermission } from '../../../lib/api.ts';
import { askLine, type ChildAsk, type Kid } from '../../../lib/children.ts';
import { ApiError, errorText } from '../../../lib/errors.ts';
import { t } from '../../../lib/i18n.ts';
import type { ChildrenView } from '../../../lib/use-children.ts';
import './child-asks.css';

/**
 * The permission strips of a parent's turn (ADR 0035, design/screens/child-sessions.html B): one per open request
 * of a blocked child, answered here with the same call the child's own conversation makes.
 */
export function ChildAsks(props: {
  view: ChildrenView;
  /** Answering is possible: this window is connected to the core. */
  decidable: boolean;
  onOpenSession?: ((sessionId: string) => void) | undefined;
}) {
  const { view } = props;
  const blocked = view.data.kids.filter((k) => k.state === 'blocked');
  if (blocked.length === 0) return null;
  return (
    <div className="kw-asks" data-testid="cockpit.child-asks">
      {blocked.flatMap((kid) => {
        const asks = view.asks[kid.id] ?? [];
        if (asks.length === 0) {
          return [
            <ChildAskStrip
              key={kid.id}
              kid={kid}
              ask={null}
              view={view}
              decidable={false}
              onOpenSession={props.onOpenSession}
            />,
          ];
        }
        return asks.map((ask) => (
          <ChildAskStrip
            key={`${kid.id}:${ask.requestId}`}
            kid={kid}
            ask={ask}
            view={view}
            decidable={props.decidable && !ask.plan}
            onOpenSession={props.onOpenSession}
          />
        ));
      })}
    </div>
  );
}

function ChildAskStrip(props: {
  kid: Kid;
  ask: ChildAsk | null;
  view: ChildrenView;
  decidable: boolean;
  onOpenSession?: ((sessionId: string) => void) | undefined;
}) {
  const { kid, ask, view } = props;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const line = ask && !ask.plan ? askLine(ask) : null;

  async function answer(decision: 'allow' | 'deny') {
    if (!ask) return;
    setBusy(true);
    setError(null);
    try {
      await postPermission(kid.id, ask.requestId, decision);
      view.answered(kid.id, ask.requestId);
      view.refresh();
    } catch (err) {
      // The first answer wins: a request closed elsewhere is gone for everyone.
      if (err instanceof ApiError && err.code === 'request-closed') {
        view.answered(kid.id, ask.requestId);
        view.refresh();
      } else setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  const what = ask?.plan
    ? t('Asks you to approve its plan')
    : line
      ? fill(line.command ? t('Asks to run {command}') : t('Asks to use {tool}'), {
          command: <span className="mono">{line.text}</span>,
          tool: line.text,
        })
      : t('Waits for a permission');
  const meta = props.decidable
    ? t('{title} on {machine}, a session this one started. Only you can answer; the parent cannot.')
    : t('{title} on {machine}, a session this one started. Only you can answer, in the child.');
  return (
    <div className="kw-ask" data-testid="cockpit.child-ask">
      {Icon.hand()}
      <div className="tx">
        <b>{what}</b>
        <span className="meta">
          {fill(meta, { title: <b>{kid.title}</b>, machine: kid.machine })}
        </span>
        {error ? (
          <span className="kw-err" role="alert">
            {error}
          </span>
        ) : null}
      </div>
      {props.decidable && ask ? (
        <span className="kw-act">
          <Button
            variant="primary"
            disabled={busy}
            onClick={() => void answer('allow')}
            data-testid="cockpit.child-ask.allow"
          >
            {t('Allow')}
          </Button>
          <Button
            disabled={busy}
            onClick={() => void answer('deny')}
            data-testid="cockpit.child-ask.deny"
          >
            {t('Deny')}
          </Button>
          {props.onOpenSession ? (
            <IconButton
              label={t('Open session')}
              icon={Icon.external()}
              onClick={() => props.onOpenSession?.(kid.id)}
              data-testid="cockpit.child-ask.open"
            />
          ) : null}
        </span>
      ) : props.onOpenSession ? (
        <Button
          variant="primary"
          icon={Icon.external()}
          onClick={() => props.onOpenSession?.(kid.id)}
          data-testid="cockpit.child-ask.open"
        >
          {t('Open to answer')}
        </Button>
      ) : null}
    </div>
  );
}
