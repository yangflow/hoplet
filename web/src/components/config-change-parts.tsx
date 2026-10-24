import type { ConfigChangeObject } from '@hoplet/shared';
import { diffCounts, settingValues, takesEffectTag } from '../lib/config-changes.ts';
import { t } from '../lib/i18n.ts';
import { Icon } from './icons.tsx';
import './config-change.css';

/**
 * The parts a session's request to change its configuration (ADR 0036, design/screens/self-config.html B) shows
 * in the turn and in the review: an object's glyph, its counts or values, and when it takes effect.
 */
export function ObjectIcon({ object }: { object: ConfigChangeObject }) {
  switch (object.kind) {
    case 'memory':
      return Icon.file();
    case 'skill':
      return Icon.fast();
    case 'command':
      return Icon.terminal();
    default:
      return Icon.sliders();
  }
}

/** The lines a file object adds and removes, or a setting's values as `medium → high`. */
export function ObjectCounts({ object }: { object: ConfigChangeObject }) {
  if (object.kind === 'config') {
    const { from, to } = settingValues(object);
    return (
      <span className="cnt">{from === null ? (to ?? '') : `${from} → ${to ?? t('default')}`}</span>
    );
  }
  const { added, removed } = diffCounts(object.diff);
  return (
    <span className="cnt">
      {added ? <span className="a">+{added}</span> : null}
      {removed ? <span className="d">−{removed}</span> : null}
    </span>
  );
}

/** When one object takes effect, as a tag. */
export function TakesTag({ object }: { object: ConfigChangeObject }) {
  return (
    <span className="tag line">
      {Icon.clock()}
      {takesEffectTag(object.takesEffect)}
    </span>
  );
}

/** A setting's value before and after. */
export function SettingValues({ object }: { object: ConfigChangeObject }) {
  const { from, to } = settingValues(object);
  return (
    <div className="ba" data-testid="config-change.values">
      <span className="from">{from ?? t('default')}</span>
      {Icon.chevron()}
      <span className="to">{to ?? t('default')}</span>
    </div>
  );
}

/** What the core checked before the request reached the user, under an object's diff. */
export function ObjectCheck({ object }: { object: ConfigChangeObject }) {
  const text =
    object.kind === 'config'
      ? t("Applies from the session's next turn, without a restart.")
      : object.kind === 'memory'
        ? t('Checked for anything that looks like a credential before it reached you.')
        : t(
            'Content check passed: the front matter has only name and description, and no line runs a shell command.',
          );
  return (
    <div className="chkn">
      {Icon.check()}
      <span>{text}</span>
    </div>
  );
}
