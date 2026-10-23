import type { SessionHandoffRequest } from '@hoplet/shared';
import { Fragment, type ReactNode } from 'react';
import { Button } from '../../../components/Button.tsx';
import { Icon, Spinner } from '../../../components/icons.tsx';
import { clock } from '../../../lib/format.ts';
import { t } from '../../../lib/i18n.ts';

/** A `{name}` template with elements in place of its placeholders. */
function fill(template: string, parts: Record<string, ReactNode>): ReactNode[] {
  return template.split(/(\{\w+\})/).map((piece, i) => {
    const hole = /^\{(\w+)\}$/.exec(piece);
    // biome-ignore lint/suspicious/noArrayIndexKey: pieces have no identity
    return <Fragment key={i}>{hole ? (parts[hole[1] ?? ''] ?? piece) : piece}</Fragment>;
  });
}

/** How long a pending request has left: `23 h 41 min`, `41 min`, or `under a minute`. */
export function formatExpiry(ms: number): string {
  const minutes = Math.max(0, Math.floor(ms / 60_000));
  if (minutes < 1) return t('under a minute');
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  if (h === 0) return t('{m} min').replace('{m}', String(m));
  return t('{h} h {m} min').replace('{h}', String(h)).replace('{m}', String(m));
}

/** A session's request to be handed off: the user approves or declines it (ADR 0034). */
export function HandoffCard(props: {
  request: SessionHandoffRequest;
  providerName: string;
  /** The machine that holds the session now. */
  fromName: string;
  toName: string;
  now: number;
  busy: boolean;
  /** Deciding is possible: this window is connected to the core. */
  decidable: boolean;
  onDecide: (approve: boolean) => void;
}) {
  const { request, now, busy } = props;
  const title = fill(t('{provider} asks to hand this session off to {target}'), {
    provider: props.providerName,
    target: props.toName,
  });
  const left = formatExpiry(request.expiresAt - now);
  return (
    <fieldset className="ask" data-testid="cockpit.handoff-request">
      <legend className="sr-only">
        {t('{provider} asks to hand this session off to {target}')
          .replace('{provider}', props.providerName)
          .replace('{target}', props.toName)}
      </legend>
      <div className="ask-h">
        {Icon.handoff()}
        <span className="q">{title}</span>
      </div>
      <div className="hf">
        {Icon.laptop()}
        <span>{props.fromName}</span>
        {Icon.chevron()}
        {Icon.laptop()}
        <b>{props.toName}</b>
        <span className="meta" data-testid="cockpit.handoff-expiry">
          {t('Expires in {time}').replace('{time}', left)}
        </span>
      </div>
      <div className="hq" data-testid="cockpit.handoff-reason">
        {request.reason}
      </div>
      <div className="ask-f">
        <Button
          variant="primary"
          disabled={busy || !props.decidable}
          onClick={() => props.onDecide(true)}
          data-testid="cockpit.handoff-approve"
        >
          {t('Approve and hand off')}
        </Button>
        <Button
          disabled={busy || !props.decidable}
          onClick={() => props.onDecide(false)}
          data-testid="cockpit.handoff-decline"
        >
          {t('Decline')}
        </Button>
        <span className="ask-meta">
          {Icon.clock()}
          <span>{t('Starts when this turn ends')}</span>
        </span>
      </div>
    </fieldset>
  );
}

function Row(props: {
  tone?: 'ok' | 'err';
  icon: ReactNode;
  head: string;
  detail: string;
  testid: string;
}) {
  return (
    <div
      className={`hres${props.tone ? ` ${props.tone}` : ''}`}
      data-testid={props.testid}
      role={props.tone === 'err' ? 'alert' : 'status'}
    >
      {props.icon}
      <span>
        <b>{props.head}</b> · {props.detail}
      </span>
    </div>
  );
}

/** The code of a failed request with what the user can do about it. */
function failure(request: SessionHandoffRequest, toName: string, projectName: string): string {
  const code = request.error ?? 'failed';
  const hint =
    code === 'no-clone'
      ? t('{target} has no clone of {project}. Clone it there and ask again.')
          .replace('{target}', toName)
          .replace('{project}', projectName)
      : request.errorReason;
  return hint ? `${code}: ${hint}` : code;
}

/** What is left in the conversation once a request is answered, expired or has run. */
export function HandoffResult(props: {
  request: SessionHandoffRequest;
  toName: string;
  projectName: string;
}) {
  const { request, toName } = props;
  const id = 'cockpit.handoff-result';
  switch (request.state) {
    case 'pending':
      return null;
    case 'approved':
      return request.decision === 'auto' ? (
        <Row
          tone="ok"
          icon={Icon.check()}
          head={t('Hand-off approved automatically')}
          detail={t("{target} is on this session's auto-approve list").replace('{target}', toName)}
          testid={id}
        />
      ) : (
        <Row
          icon={<Spinner />}
          head={t('Hand-off approved')}
          detail={t('moves to {target} when this turn ends').replace('{target}', toName)}
          testid={id}
        />
      );
    case 'done':
      return (
        <Row
          tone="ok"
          icon={Icon.check()}
          head={t('Hand-off done')}
          detail={t('moved to {target}').replace('{target}', toName)}
          testid={id}
        />
      );
    case 'declined':
      return (
        <Row
          icon={Icon.ban()}
          head={t('Hand-off declined')}
          detail={t('by you, {time}').replace('{time}', clock(request.decidedAt ?? undefined))}
          testid={id}
        />
      );
    case 'expired':
      return (
        <Row
          icon={Icon.clock()}
          head={t('Hand-off request expired')}
          detail={
            request.decision === null ? t('no answer in 24 hours') : t('the session ended or moved')
          }
          testid={id}
        />
      );
    case 'failed':
      return (
        <Row
          tone="err"
          icon={Icon.alert()}
          head={t('Hand-off failed')}
          detail={failure(request, toName, props.projectName)}
          testid={id}
        />
      );
  }
}
