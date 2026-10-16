import type { ActionDecideResponse, ActionInspectResponse } from '@hoplet/shared';
import { useEffect, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Icon } from '../../components/icons.tsx';
import { postActionDecide, postActionInspect } from '../../lib/api.ts';
import { ApiError, errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { Markdown } from '../cockpit/conversation/markdown.tsx';
import './action.css';
import { expiryText } from './model.ts';

/** What the page shows: the stage of the one request it makes. */
export type ActionView =
  | { stage: 'loading' }
  | { stage: 'invalid' }
  | { stage: 'error'; message: string }
  | { stage: 'open'; info: ActionInspectResponse; busy?: boolean; failure?: string }
  | { stage: 'closed'; state: 'answered' | 'expired' | 'used' | 'webui-only'; sessionId?: string }
  | { stage: 'decided'; decision: 'allow' | 'deny'; kind: 'permission' | 'plan' };

function Mark() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="var(--color-on-accent)"
      strokeWidth="3.2"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <path d="M6 18L18 6" />
      <circle cx="18" cy="6" r=".6" fill="var(--color-on-accent)" />
    </svg>
  );
}

function OpenLink(props: { sessionId: string | undefined; primary?: boolean }) {
  const href = props.sessionId ? `/?session=${encodeURIComponent(props.sessionId)}` : '/';
  return (
    <a
      className={`btn ${props.primary ? 'btn-primary' : 'btn-quiet'}`}
      href={href}
      data-testid="action.open"
    >
      {t('Open in Hoplet')}
      {Icon.external()}
    </a>
  );
}

function Context(props: { info: ActionInspectResponse }) {
  const { info } = props;
  return (
    <dl className="ctx">
      {info.title ? (
        <>
          <dt>{t('Session')}</dt>
          <dd>{info.title}</dd>
        </>
      ) : null}
      {info.project ? (
        <>
          <dt>{t('Project')}</dt>
          <dd>{info.project}</dd>
        </>
      ) : null}
    </dl>
  );
}

/** One card of the page; it has no sidebar, no navigation and nothing that needs the host token. */
export function ActionCard(props: {
  view: ActionView;
  now: number;
  onDecide: (decision: 'allow' | 'deny') => void;
}) {
  const { view } = props;
  let card: React.ReactNode;
  if (view.stage === 'loading') {
    card = (
      <p className="msg" role="status" data-testid="action.loading">
        {t('Checking the link…')}
      </p>
    );
  } else if (view.stage === 'invalid') {
    card = (
      <div data-testid="action.invalid" className="body">
        <h1>
          <span className="bad-ic">{Icon.alert()}</span>
          {t('This link is not valid')}
        </h1>
        <p className="msg">
          {t('Open the notification again, or open Hoplet to see the session.')}
        </p>
        <div className="acts">
          <OpenLink sessionId={undefined} primary />
        </div>
      </div>
    );
  } else if (view.stage === 'error') {
    card = (
      <div data-testid="action.error" className="body">
        <h1>
          <span className="bad-ic">{Icon.alert()}</span>
          {t('The link could not be checked')}
        </h1>
        <p className="msg" role="alert">
          {view.message}
        </p>
      </div>
    );
  } else if (view.stage === 'decided') {
    const allow = view.decision === 'allow';
    card = (
      <div data-testid="action.decided" className="body">
        <h1>
          <span className="ok-ic">{Icon.check()}</span>
          {view.kind === 'plan'
            ? allow
              ? t('Plan approved')
              : t('Plan rejected')
            : allow
              ? t('Allowed once')
              : t('Denied')}
        </h1>
        <p className="msg" role="status">
          {t('Your answer was sent to Claude. You can close this page.')}
        </p>
        <div className="acts">
          <OpenLink sessionId={undefined} primary />
        </div>
      </div>
    );
  } else if (view.stage === 'closed') {
    const closed = {
      answered: {
        id: 'action.answered',
        icon: <span className="ok-ic">{Icon.check()}</span>,
        title: t('Already answered'),
        text: t('This request was answered elsewhere. Nothing more to do here.'),
      },
      expired: {
        id: 'action.expired',
        icon: <span className="bad-ic">{Icon.clock()}</span>,
        title: t('This link expired'),
        text: t(
          'A link works for the time the request stays open, at most 10 minutes. Open Hoplet to see the session and continue.',
        ),
      },
      used: {
        id: 'action.used',
        icon: <span className="ok-ic">{Icon.check()}</span>,
        title: t('This link was already used'),
        text: t('Each link answers once. Your answer is recorded; open Hoplet to see the session.'),
      },
      'webui-only': {
        id: 'action.webui',
        icon: <span className="att-ic">{Icon.lock()}</span>,
        title: t('Answer this one in Hoplet'),
        text: t(
          'Deleting files is never decided from a notification link. Open Hoplet to read the full request and answer it.',
        ),
      },
    }[view.state];
    card = (
      <div data-testid={closed.id} className="body">
        <h1>
          {closed.icon}
          {closed.title}
        </h1>
        <p className="msg">{closed.text}</p>
        <div className="acts">
          <OpenLink sessionId={view.sessionId} primary />
        </div>
      </div>
    );
  } else {
    const { info } = view;
    const plan = info.kind === 'plan';
    const left = expiryText(info.expiresAt, props.now);
    card = (
      <div data-testid="action.card" className="body">
        <h1>
          <span className="att-ic">{plan ? Icon.listChecks() : Icon.lock()}</span>
          {plan ? t('Claude has a plan to review') : t('Claude wants to run a command')}
        </h1>
        <Context info={info} />
        {plan ? (
          info.plan ? (
            <div className="plan" data-testid="action.plan">
              <Markdown text={info.plan} base={0} />
            </div>
          ) : null
        ) : (
          <>
            {info.tool && info.tool !== 'Bash' ? <div className="tool">{info.tool}</div> : null}
            {info.summary ? <div className="cmd mono">{info.summary}</div> : null}
          </>
        )}
        {view.failure ? (
          <p className="mrow-err" role="alert" data-testid="action.failure">
            {view.failure}
          </p>
        ) : null}
        <div className="acts two">
          <Button
            disabled={view.busy}
            onClick={() => props.onDecide('deny')}
            data-testid="action.deny"
          >
            {plan ? t('Reject') : t('Deny')}
          </Button>
          <Button
            variant="primary"
            disabled={view.busy}
            onClick={() => props.onDecide('allow')}
            data-testid="action.allow"
          >
            {plan ? t('Approve plan') : t('Allow once')}
          </Button>
        </div>
        <div className="foot">
          {left ? (
            <span className="when">
              {Icon.clock()}
              {left} · {t('works once')}
            </span>
          ) : null}
          <OpenLink sessionId={info.sessionId} />
        </div>
      </div>
    );
  }
  return (
    <div className="cfw">
      <main className="cf">
        <div className="brand">
          <span className="app">
            <Mark />
          </span>
          Hoplet
        </div>
        <div className="card">{card}</div>
      </main>
    </div>
  );
}

/** The `/a` page: inspects the link on load and decides on a click. The token lives only in memory. */
export function ActionPage(props: { token: string | null }) {
  const { token } = props;
  const [view, setView] = useState<ActionView>(token ? { stage: 'loading' } : { stage: 'invalid' });
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    document.title = 'Hoplet';
    if (!token) return;
    let cancelled = false;
    postActionInspect(token)
      .then((r) => {
        if (cancelled) return;
        setNow(Date.now());
        if (r.state === 'open') {
          setView({ stage: 'open', info: r });
        } else {
          setView({
            stage: 'closed',
            state: r.state,
            ...(r.sessionId ? { sessionId: r.sessionId } : {}),
          });
        }
      })
      .catch((e) => {
        if (cancelled) return;
        if (e instanceof ApiError && e.code === 'invalid-token') setView({ stage: 'invalid' });
        else setView({ stage: 'error', message: errorText(e) });
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  const decide = (decision: 'allow' | 'deny') => {
    if (!token || view.stage !== 'open' || view.busy) return;
    const open = view.info;
    setView({ stage: 'open', info: open, busy: true });
    postActionDecide(token, decision)
      .then((r: ActionDecideResponse) => {
        if (r.state === 'decided')
          setView({ stage: 'decided', decision, kind: open.kind ?? 'permission' });
        else setView({ stage: 'closed', state: r.state });
      })
      .catch((e) => {
        if (e instanceof ApiError && e.code === 'invalid-token') setView({ stage: 'invalid' });
        else setView({ stage: 'open', info: open, failure: errorText(e) });
      });
  };

  return <ActionCard view={view} now={now} onDecide={decide} />;
}
