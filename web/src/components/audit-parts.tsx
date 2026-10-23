import type { AuditEvent } from '@hoplet/shared';
import {
  type AuditNames,
  actorLabel,
  detailOf,
  formatCount,
  fullTime,
  shortHash,
  summaryParts,
  summaryRest,
} from '../lib/audit.ts';
import { t } from '../lib/i18n.ts';
import './audit-parts.css';

const mono = (s: string) => <span className="mono">{s}</span>;

/** One record's summary in a line: lead, the monospace part, and the path in the quiet colour. */
export function SummaryView(props: { event: AuditEvent; names: AuditNames }) {
  const parts = summaryParts(props.event, props.names);
  return (
    <>
      {parts.lead}
      {parts.mono ? <> · {mono(parts.mono)}</> : null}
      {parts.tail ? (
        <>
          {' '}
          <span className="pth">{parts.tail}</span>
        </>
      ) : null}
    </>
  );
}

export function Detail(props: { event: AuditEvent; names: AuditNames; testid: string }) {
  const { event, names } = props;
  const { command, paths } = detailOf(event);
  const rest = summaryRest(event);
  const facts: [string, string, boolean][] = [
    [t('Seq'), `#${formatCount(event.seq)}`, true],
    [t('Recorded'), fullTime(event.recordedAt), true],
    [t('Channel'), event.channel, true],
    [t('Actor'), actorLabel(event, names), false],
    [t('Session'), event.sessionId ? names.session(event.sessionId) : '—', false],
    [t('Machine'), event.deviceId ? names.device(event.deviceId) : '—', false],
    ...(event.ref ? ([[t('Ref'), event.ref, true]] as [string, string, boolean][]) : []),
    [t('Hash'), `${shortHash(event.hash)} ← ${shortHash(event.prevHash)}`, true],
  ];
  return (
    <div className="axd" data-testid={`${props.testid}.${event.seq}`}>
      {command ? (
        <div>
          <div className="meta">{t('Command · secrets scrubbed by the agent')}</div>
          <div className="cmd">{command}</div>
        </div>
      ) : null}
      {paths.length > 0 ? (
        <div>
          <div className="meta">{t('Paths this call changes')}</div>
          <div className="cmd">
            {paths.map((p) => (
              <div key={p}>{p}</div>
            ))}
          </div>
        </div>
      ) : null}
      <dl className="hkv">
        {facts.map(([k, v, m]) => (
          <div key={k} className="hkvr">
            <dt className="k">{k}</dt>
            <dd className={`v${m ? ' mono' : ''}`} title={v}>
              {v}
            </dd>
          </div>
        ))}
      </dl>
      {rest.length > 0 ? (
        <dl className="hkv">
          {rest.map(([k, v]) => (
            <div key={k} className="hkvr">
              <dt className="k">{k}</dt>
              <dd className="v mono" title={v}>
                {v}
              </dd>
            </div>
          ))}
        </dl>
      ) : null}
      <div className="meta">
        {t('Never records prompts, assistant text, tool output, file contents or diffs.')}
      </div>
    </div>
  );
}
