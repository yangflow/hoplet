import type { RuntimeState } from '@hoplet/shared';
import { stateName } from '../lib/runtime-state.ts';
import { Icon, Spinner } from './icons.tsx';

/** The states shown as a glyph; `idle` and `offline` have none. */
export type GlyphState = Exclude<RuntimeState, 'idle' | 'offline'>;

export function glyphState(state: RuntimeState): GlyphState | null {
  return state === 'idle' || state === 'offline' ? null : state;
}

/** The runtime state in place of the time: a glyph whose shape differs per state and that names it. */
export function StateGlyph(props: { state: GlyphState }) {
  const name = stateName(props.state);
  const art = {
    starting: <Spinner />,
    running: <Spinner />,
    'waiting-permission': <span className="dot-need" />,
    ready: <span className="dot-ready" />,
    failed: <span className="dot-bad" />,
    external: Icon.eye(),
  }[props.state];
  return (
    <span className={`glyph glyph-${props.state}`} role="img" title={name} aria-label={name}>
      {art}
    </span>
  );
}
