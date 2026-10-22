import { Icon } from '../../../components/icons.tsx';
import { t } from '../../../lib/i18n.ts';
import type { AppliedRule } from '../../../lib/permission-rules.ts';
import '../../../components/rules.css';

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
  }
}

/**
 * The line a rule leaves at the call it decided: allowed, denied or asked, the rule's name, its scope and a link to it.
 * Presentational: it is driven by the rule that decided, which no browser message carries yet.
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
        {label} <b>{rule.scope === 'builtin' ? t(rule.name) : rule.name}</b>
      </span>
      <span>·</span>
      <span>{scopeName(rule.scope)}</span>
      {props.onView ? (
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
