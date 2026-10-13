import { type CSSProperties, type ReactNode, useState } from 'react';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon } from '../../../components/icons.tsx';
import { t } from '../../../lib/i18n.ts';

/** One line of a well; `kind` colours the command line and the lines a caller knows to be fine, failed or quiet. */
export type WellLine = {
  text: string;
  kind?: 'cmd' | 'ok' | 'bad' | 'dim';
  /** The number in the gutter, when the well is numbered; the position by default. */
  no?: number;
  /** The line as markup, when it carries more than text (a search hit). */
  node?: ReactNode;
};

/** The line height of the well's code type (--leading-code); lines of a large block are placed by it. */
const LINE_HEIGHT = 20;
/** The well's padding above and below the lines (--space-2). */
const PAD = 8;
/** Lines below this count are laid out in flow; more are mounted only where they are scrolled to. */
const FLOW_MAX = 300;
const OVERSCAN = 12;
const VIEW_HEIGHT = 240;
/** Lines that fit under the well's maximum height; more fade at the bottom. */
export const WELL_LINES = Math.floor((VIEW_HEIGHT - 2 * PAD) / LINE_HEIGHT);

/** The lines of a text, one per line of the text. */
export function linesOf(text: string, kind?: WellLine['kind']): WellLine[] {
  const parts = text.split('\n');
  if (parts.length > 1 && parts[parts.length - 1] === '') parts.pop();
  return parts.map((line) => ({ text: line, ...(kind ? { kind } : {}) }));
}

function plainText(lines: WellLine[]): string {
  return lines.map((l) => l.text).join('\n');
}

function LineView(props: { line: WellLine; no: number | null }) {
  const { line, no } = props;
  return (
    <div className={`wl${line.kind ? ` ${line.kind}` : ''}`}>
      {no !== null ? <span className="n">{no}</span> : null}
      <span className="c">{line.node ?? line.text}</span>
    </div>
  );
}

/**
 * The output well: one frame for every expanded result (docs/design.md#layout-and-breakpoints). Mono 12.5/20,
 * optional line numbers, a fade past `--layout-well-max`, a copy button; a block of any size stays cheap because
 * past a few hundred lines only the lines in view are mounted.
 */
export function OutputWell(props: {
  lines: WellLine[];
  numbered?: boolean;
  /** A failure: the well is tinted. */
  error?: boolean;
  /** A short preview that fades to the right (an elided block before it is opened). */
  peek?: boolean;
  /** Sits flush left, not under a tool row. */
  solo?: boolean;
  /** Extra icon buttons after Copy. */
  actions?: ReactNode;
  /** Below the lines, over the frame: the Open button of a preview. */
  children?: ReactNode;
  label?: string;
  'data-testid'?: string;
  /** The test id of the scrolling region of a large block. */
  scrollTestId?: string;
  /** The text Copy copies, when it is more than the lines (a block whose preview is shown). */
  copyText?: string;
}) {
  const { lines, numbered = false, error, peek, solo, actions, children } = props;
  const [scrolled, setScrolled] = useState({ top: 0, end: false });
  const flow = lines.length <= FLOW_MAX;
  const fade = (peek || lines.length > WELL_LINES) && !scrolled.end;
  const digits = Math.max(1, ...lines.map((l, i) => String(l.no ?? i + 1).length));
  const first = flow ? 0 : Math.max(0, Math.floor(scrolled.top / LINE_HEIGHT) - OVERSCAN);
  const last = flow
    ? lines.length
    : Math.min(lines.length, first + Math.ceil(VIEW_HEIGHT / LINE_HEIGHT) + 2 * OVERSCAN);
  const body = lines.slice(first, last).map((line, i) => {
    const at = first + i;
    return (
      <div key={at} className="wl-slot" style={flow ? undefined : { top: PAD + at * LINE_HEIGHT }}>
        <LineView line={line} no={numbered ? (line.no ?? at + 1) : null} />
      </div>
    );
  });
  const scrollable = lines.length > WELL_LINES;
  return (
    <div
      className={`well${numbered ? ' ln' : ''}${fade ? ' fade' : ''}${peek ? ' peek' : ''}${error ? ' err' : ''}${solo ? ' solo' : ''}${flow ? '' : ' big'}`}
      style={numbered ? ({ '--well-no': `${digits + 1}ch` } as CSSProperties) : undefined}
      data-testid={props['data-testid']}
    >
      <div className="wact">
        <IconButton
          label={t('Copy')}
          icon={Icon.copy()}
          onClick={() => void navigator.clipboard?.writeText(props.copyText ?? plainText(lines))}
          data-testid="cockpit.well-copy"
        />
        {actions}
      </div>
      <div
        className="wb"
        {...(scrollable
          ? {
              tabIndex: 0,
              role: 'region',
              'aria-label': props.label ?? t('Output'),
            }
          : {})}
        onScroll={(e) => {
          const el = e.currentTarget;
          setScrolled({
            top: el.scrollTop,
            end: el.scrollTop + el.clientHeight >= el.scrollHeight - 1,
          });
        }}
        data-testid={props.scrollTestId}
      >
        {flow ? (
          body
        ) : (
          <div className="wl-box" style={{ height: lines.length * LINE_HEIGHT + 2 * PAD }}>
            {body}
          </div>
        )}
      </div>
      {children}
    </div>
  );
}
