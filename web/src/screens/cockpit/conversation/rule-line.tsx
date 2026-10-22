import { Icon } from '../../../components/icons.tsx';
import { t } from '../../../lib/i18n.ts';
import type { AppliedRule } from '../../../lib/permission-rules.ts';
import '../../../components/rules.css';
import { useView } from './view-context.tsx';

function scopeName(scope: AppliedRule['scope']): string {
  switch (scope) {
    case 'builtin':
      return t('Built in');
    case 'global':
      return t('Global');
    case 'project':
      return t('This project');
    case 'session':
      return t('This session');
    case 'agent':
      return t('This machine');
  }
}

/**
 * The line a rule leaves at the call it decided: allowed, denied or asked, the rule's name, its scope and a link to it.
 * Presentational: the rule is what `session.rule` told, named from the rules list (lib/rule-lines.ts).
 */
export function RuleLine(props: { rule: AppliedRule; onView?: (ruleId: string) => void }) {
  const { rule } = props;
  const label =
    rule.decision === 'allow'
      ? t('Allowed by rule')
      : rule.decision === 'deny'
        ? t('Denied by rule')
        : t('Asked by rule');
  const icon =
    rule.decision === 'allow' ? Icon.rules() : rule.decision === 'deny' ? Icon.ban() : Icon.hand();
  return (
    <div
      className={`qline ${rule.decision === 'ask' ? 'qask' : rule.decision}`}
      data-testid="cockpit.rule-line"
    >
      {icon}
      <span>
        {label}
        {rule.name === '' ? null : (
          <>
            {' '}
            <b>{rule.scope === 'builtin' ? t(rule.name) : rule.name}</b>
          </>
        )}
      </span>
      <span>·</span>
      <span>{scopeName(rule.scope)}</span>
      {props.onView && rule.name !== '' ? (
        <button
          type="button"
          className="lnk"
          onClick={() => props.onView?.(rule.ruleId)}
          data-testid="cockpit.rule-line.view"
        >
          {t('View rule')}
        </button>
      ) : null}
    </div>
  );
}

/**
 * The line under a tool call that a rule allowed or denied. A rule that asked shows on its permission card
 * instead, so it is not repeated here.
 */
export function ToolRule(props: { toolUseId: string | undefined }) {
  const view = useView();
  const rule = props.toolUseId !== undefined ? view.ruleOf?.(props.toolUseId) : undefined;
  const unenforced = props.toolUseId !== undefined && view.unenforcedAt?.(props.toolUseId) === true;
  return (
    <>
      {rule && rule.decision !== 'ask' ? (
        <RuleLine rule={rule} {...(view.openRule ? { onView: view.openRule } : {})} />
      ) : null}
      {unenforced ? (
        <RulesUnenforcedNote {...(view.openRule ? { onView: () => view.openRule?.('') } : {})} />
      ) : null}
    </>
  );
}

const ADR_URL =
  'https://github.com/yangflow/hoplet/blob/main/docs/adr/0032-codex-rule-hook-calls-the-agent-over-a-local-socket.md';

/**
 * At the start of a Codex session whose machine cannot take the rule hook (ADR 0032): rules cover only the approvals
 * Codex asks for, and in Full access Codex asks for nothing.
 */
export function CodexPromptsNote() {
  return (
    <div className="cxn" data-testid="cockpit.codex-prompts-note">
      {Icon.shield()}
      <span>
        {t(
          'Rules apply only to the approvals Codex asks for on this machine. In Full access Codex asks for nothing, so its commands are not checked.',
        )}{' '}
        <a
          className="lnk"
          href={ADR_URL}
          target="_blank"
          rel="noopener noreferrer"
          title={t('Opens the decision record')}
        >
          {t('Why')}
        </a>
      </span>
    </div>
  );
}

/** Under a Codex call the hook did not run for: no rule was checked, and the agent logged it. */
export function RulesUnenforcedNote(props: { onView?: () => void }) {
  return (
    <div className="cxn att" data-testid="cockpit.rules-unenforced-note">
      {Icon.hand()}
      <span>
        <b>{t('Rules were not enforced')}</b> ·{' '}
        {t(
          'The Codex hook did not run for this call, so no rule was checked. The agent logged it.',
        )}{' '}
        {props.onView ? (
          <button type="button" className="lnk" onClick={props.onView}>
            {t('Permission rules')}
          </button>
        ) : null}
      </span>
    </div>
  );
}
