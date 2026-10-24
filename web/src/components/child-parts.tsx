import type { ChildState } from '@hoplet/shared';
import { stateLabel } from '../lib/children.ts';
import { t } from '../lib/i18n.ts';
import { Icon, Spinner } from './icons.tsx';
import './child-parts.css';

/** The colour class of a child's state label: running, needs you, done, gone wrong, or stopped. */
export function stateTone(state: ChildState): 'run' | 'need' | 'done' | 'out' | 'can' {
  switch (state) {
    case 'running':
      return 'run';
    case 'blocked':
      return 'need';
    case 'done':
      return 'done';
    case 'failed':
    case 'timed-out':
      return 'out';
    default:
      return 'can';
  }
}

/** A child's state as a glyph whose shape differs per state and that names it. */
export function KidGlyph(props: { state: ChildState }) {
  const { state } = props;
  const name =
    state === 'blocked' ? t('Waiting for a permission only you can answer') : stateLabel(state);
  const art = {
    running: <Spinner />,
    blocked: <span className="dot-need" />,
    done: Icon.check(),
    failed: <span className="dot-bad" />,
    'timed-out': <span className="dot-bad" />,
    cancelled: Icon.ban(),
    ended: <span className="kid-dot-off" />,
  }[state];
  return (
    <span className={`glyph kid-gl ${stateTone(state)}`} role="img" title={name} aria-label={name}>
      {art}
    </span>
  );
}

/** The state of a child as a coloured word. */
export function KidState(props: { state: ChildState }) {
  return <span className={`kid-cs ${stateTone(props.state)}`}>{stateLabel(props.state)}</span>;
}
