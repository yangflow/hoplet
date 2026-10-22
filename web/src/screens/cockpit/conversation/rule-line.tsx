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
  if (!rule || rule.decision === 'ask') return null;
  return <RuleLine rule={rule} {...(view.openRule ? { onView: view.openRule } : {})} />;
}
