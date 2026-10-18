import {
  type ReactNode,
  type Ref,
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { Spinner } from '../../../components/icons.tsx';
import { t } from '../../../lib/i18n.ts';
import { RowView, TurnHeader } from './rows.tsx';
import type { NoteRow, Row, Turn } from './turns.ts';
import { ViewContext, type ViewState } from './view-context.tsx';
import { type Anchor, anchorAt, offsetsOf, restoreScroll, rowAt, visibleRange } from './virtual.ts';

/** One mounted unit of the virtual list: a turn header or one row of a turn's body. */
export type VRow =
  | { key: string; type: 'head'; turn: Turn; first: boolean; joined: boolean }
  | { key: string; type: 'row'; turn: Turn; row: Row; last: boolean; gap: number };

/**
 * The space above a row, as the design's margins give it (the turn body is a flex column, so margins add):
 * prose has 4 px above and 12 px below, a group of activity rows 4 px above and 8 px below.
 */
function gapBefore(previous: Row | undefined, row: Row): number {
  const prose = row.kind === 'text';
  if (!previous) return 4;
  if (previous.kind === 'text') return 16;
  return prose ? 12 : 0;
}

/** The rows of all turns in order. An elided prompt gets its panel as the first row of its turn. */
export function flatten(turns: Turn[]): VRow[] {
  const out: VRow[] = [];
  turns.forEach((turn, index) => {
    // A turn that follows a compaction boundary continues under the boundary's own rule.
    const joined = turns[index - 1]?.rows.at(-1)?.kind === 'boundary';
    out.push({ key: `head-${turn.key}`, type: 'head', turn, first: index === 0, joined });
    const rows: Row[] = turn.prompt?.elided
      ? [
          {
            kind: 'note',
            key: `prompt-${turn.key}`,
            lineNo: turn.startLine,
            family: 'other',
            label: t('Prompt'),
            text: turn.prompt.elided.preview,
            elided: turn.prompt.elided,
          } satisfies NoteRow,
          ...turn.rows,
        ]
      : turn.rows;
    rows.forEach((row, i) => {
      out.push({
        key: row.key,
        type: 'row',
        turn,
        row,
        last: i === rows.length - 1,
        gap: gapBefore(rows[i - 1], row),
      });
    });
    if (rows.length === 0) {
      const head = out[out.length - 1];
      if (head?.type === 'head') out[out.length - 1] = { ...head };
    }
  });
  return out;
}

/** The height of the origin line with its margin, for the estimate of the first header. */
const ORIGIN_HEIGHT = 44;

function estimate(v: VRow, origin: boolean): number {
  if (v.type === 'head')
    return (v.first || v.joined ? 76 : 84) + (v.first && origin ? ORIGIN_HEIGHT : 0);
  const pad = (v.last ? 32 : 0) + v.gap;
  const { row } = v;
  switch (row.kind) {
    case 'text':
      return 16 + Math.ceil(row.text.length / 88) * 24 + pad;
    case 'edit':
      return 28 + row.rows.length * 20 + 24 + pad;
    case 'tool':
      return (row.result?.elided ? 120 : 28) + pad;
    case 'command':
      return 60 + row.text.split('\n').length * 24 + pad;
    default:
      return 28 + pad;
  }
}

export type TranscriptHandle = {
  scrollToBottom: () => void;
};

const OVERSCAN = 500;
const NEAR_EDGE = 700;
const BOTTOM_SLACK = 80;

/** The line a row stands for, for search hits and for reporting where the reader is. */
function lineOf(v: VRow): number {
  return v.type === 'head' ? v.turn.startLine : v.row.lineNo;
}

function holdsLine(v: VRow, line: number): boolean {
  if (v.type === 'head') return v.turn.startLine === line;
  if (v.row.kind === 'fold') return v.row.tools.some((tool) => tool.lineNo === line);
  return v.row.lineNo === line;
}

export function Transcript(props: {
  sessionId: string;
  turns: Turn[];
  hasOlder: boolean;
  hasNewer: boolean;
  loadingOlder: boolean;
  loadingNewer: boolean;
  onNeedOlder: () => void;
  onNeedNewer: () => void;
  /** The line at the top of the view and whether the view is at the bottom. */
  onViewport: (view: { line: number; atBottom: boolean }) => void;
  /** Scrolls a line into view once, when it is in the window (search hits). */
  focusLine: number | null;
  /** Follows the bottom from the start. */
  startPinned: boolean;
  /** Where the session began, above its first turn: shown when the window reaches the start of the session. */
  origin?: ReactNode;
  /** What follows the stored turns: the live turn. */
  live?: ReactNode;
  /** Space the composer takes at the foot. */
  dock: number;
  openDiff?: (path: string) => void;
  /** Keys of the folds and blocks to start open (development fixtures). */
  initiallyOpen?: string[];
  /** Starts scrolled this far from the top (development fixtures). */
  scrollTo?: number;
  ref?: Ref<TranscriptHandle>;
}) {
  const { turns, focusLine } = props;
  const origin = props.origin && !props.hasOlder ? props.origin : null;
  const scroller = useRef<HTMLDivElement>(null);
  const column = useRef<HTMLDivElement>(null);
  const rows = useMemo(() => flatten(turns), [turns]);
  const heights = useRef(new Map<string, number>());
  const [, setVersion] = useState(0);
  const [view, setView] = useState<{ top: number | null; height: number }>({
    top: null,
    height: 800,
  });
  const pinned = useRef(props.startPinned);
  const anchor = useRef<Anchor | null>(null);
  const opened = useRef(
    new Map<string, boolean>((props.initiallyOpen ?? []).map((k) => [k, true])),
  );
  const [, setOpenVersion] = useState(0);
  const [hit, setHit] = useState<number | null>(null);
  const focused = useRef<number | null>(null);
  const reported = useRef({ line: -1, atBottom: true });

  const keys = useMemo(() => rows.map((r) => r.key), [rows]);
  const sizes = rows.map((r) => heights.current.get(r.key) ?? estimate(r, origin !== null));
  const offsets = offsetsOf(sizes);
  const total = offsets[rows.length] ?? 0;
  const offsetsRef = useRef(offsets);
  offsetsRef.current = offsets;
  const keysRef = useRef(keys);
  keysRef.current = keys;

  const top = view.top ?? Math.max(0, total - view.height);
  const [start, end] = visibleRange(offsets, top, view.height, OVERSCAN);
  // This render was laid out with measured heights for everything mounted.
  const settled = rows.slice(start, end).every((r) => heights.current.has(r.key));

  useImperativeHandle(
    props.ref,
    () => ({
      scrollToBottom() {
        pinned.current = true;
        const el = scroller.current;
        if (el) el.scrollTop = el.scrollHeight;
      },
    }),
    [],
  );

  const viewState = useMemo<ViewState>(
    () => ({
      sessionId: props.sessionId,
      isOpen: (key, byDefault = false) => opened.current.get(key) ?? byDefault,
      setOpen: (key, open) => {
        if (opened.current.get(key) === open) return;
        opened.current.set(key, open);
        setOpenVersion((v) => v + 1);
      },
      ...(props.openDiff ? { openDiff: props.openDiff } : {}),
    }),
    [props.sessionId, props.openDiff],
  );

  const measure = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    let changed = false;
    for (const node of el.querySelectorAll<HTMLElement>('[data-vkey]')) {
      const key = node.dataset.vkey ?? '';
      const h = node.offsetHeight;
      if (h > 0 && Math.abs((heights.current.get(key) ?? -1) - h) >= 1) {
        heights.current.set(key, h);
        changed = true;
      }
    }
    if (changed) setVersion((v) => v + 1);
  }, []);

  // After every render: measure what is mounted, then keep the reader where they were.
  useLayoutEffect(() => {
    measure();
    const el = scroller.current;
    if (!el) return;
    if (pinned.current) {
      if (el.scrollTop + el.clientHeight < el.scrollHeight - 0.5) el.scrollTop = el.scrollHeight;
    } else if (anchor.current) {
      const to = restoreScroll(keysRef.current, offsetsRef.current, anchor.current);
      if (to !== null && Math.abs(el.scrollTop - to) > 0.5) el.scrollTop = to;
    }
  });

  const initialScroll = useRef(props.scrollTo);
  useLayoutEffect(() => {
    const el = scroller.current;
    const from = initialScroll.current;
    if (!el || from === undefined || el.scrollHeight <= el.clientHeight) return;
    // Wait until what is mounted is measured, so the distance from the bottom is the real one.
    if (!settled) return;
    initialScroll.current = undefined;
    pinned.current = false;
    el.scrollTop = from;
    anchor.current = anchorAt(keysRef.current, offsetsRef.current, el.scrollTop);
  });

  // Prune the heights of rows that left the window.
  useEffect(() => {
    if (heights.current.size <= rows.length * 2 + 64) return;
    const live = new Set(keys);
    for (const key of heights.current.keys()) if (!live.has(key)) heights.current.delete(key);
  }, [keys, rows.length]);

  // Rows and the live turn change size on their own: measure again and follow the bottom.
  // biome-ignore lint/correctness/useExhaustiveDependencies: new rows mount when the range moves
  useEffect(() => {
    const el = scroller.current;
    const col = column.current;
    if (!el || !col || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => {
      measure();
      if (pinned.current) el.scrollTop = el.scrollHeight;
      setView((v) => (v.height === el.clientHeight ? v : { ...v, height: el.clientHeight }));
    });
    observer.observe(col);
    observer.observe(el);
    for (const node of col.querySelectorAll('[data-vkey]')) observer.observe(node);
    return () => observer.disconnect();
  }, [measure, start, end]);

  const report = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_SLACK;
    const index = rowAt(offsetsRef.current, el.scrollTop);
    const row = rows[index];
    const line = row ? lineOf(row) : 0;
    if (reported.current.line === line && reported.current.atBottom === atBottom) return;
    reported.current = { line, atBottom };
    props.onViewport({ line, atBottom });
  }, [rows, props.onViewport]);

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_SLACK;
    anchor.current = anchorAt(keysRef.current, offsetsRef.current, el.scrollTop);
    setView({ top: el.scrollTop, height: el.clientHeight });
    report();
  };

  // Reports where the reader is whenever the window changes under them.
  useEffect(() => {
    report();
  }, [report]);

  // Load more when the reader nears an edge of the window.
  const { hasOlder, hasNewer, loadingOlder, loadingNewer, onNeedOlder, onNeedNewer } = props;
  useEffect(() => {
    if (hasOlder && !loadingOlder && top < NEAR_EDGE) onNeedOlder();
    if (hasNewer && !loadingNewer && top + view.height > total - NEAR_EDGE) onNeedNewer();
  }, [
    hasOlder,
    hasNewer,
    loadingOlder,
    loadingNewer,
    top,
    view.height,
    total,
    onNeedOlder,
    onNeedNewer,
  ]);

  // Scroll a search hit into view once its line is in the window.
  useLayoutEffect(() => {
    if (focusLine === null || focused.current === focusLine) return;
    const index = rows.findIndex((r) => holdsLine(r, focusLine));
    const el = scroller.current;
    if (index < 0 || !el) return;
    focused.current = focusLine;
    pinned.current = false;
    setHit(focusLine);
    el.scrollTop = Math.max(0, (offsets[index] ?? 0) - el.clientHeight / 3);
    anchor.current = anchorAt(keys, offsets, el.scrollTop);
  });

  const pin = useMemo(() => {
    if (rows.length === 0) return null;
    const index = rowAt(offsets, top);
    const row = rows[index];
    if (!row) return null;
    const headIndex = rows.findIndex((r) => r.type === 'head' && r.turn.key === row.turn.key);
    const headTop = offsets[headIndex] ?? 0;
    let last = headIndex;
    while (rows[last + 1] && rows[last + 1]?.turn.key === row.turn.key) last++;
    if (top <= headTop + 1) return null;
    return { turn: row.turn, end: offsets[last + 1] ?? 0 };
  }, [rows, offsets, top]);

  const pushed = pin ? Math.min(0, pin.end - top - 64) : 0;

  return (
    <ViewContext value={viewState}>
      {loadingOlder ? (
        <div
          className="loadbar"
          role="status"
          aria-label={t('Loading earlier lines')}
          data-testid="cockpit.loading-older"
        >
          <Spinner />
        </div>
      ) : null}
      <div
        className="conv"
        ref={scroller}
        onScroll={onScroll}
        style={{ paddingBottom: props.dock }}
        data-testid="cockpit.conversation"
      >
        <div className="col" ref={column}>
          {pin ? (
            <div className="pin" style={{ top: loadingOlder ? 32 : 0 }} aria-hidden="true">
              <div style={{ transform: `translateY(${pushed}px)` }}>
                <TurnHeader turn={pin.turn} stuck />
              </div>
            </div>
          ) : null}
          {rows.length === 0 ? origin : null}
          <div className="vlist" style={{ height: total }}>
            {rows.slice(start, end).map((v, i) => {
              const index = start + i;
              return (
                <div
                  key={v.key}
                  className={`vrow${v.type === 'head' && !v.first && !v.joined ? ' sep' : ''}${v.type === 'row' && v.last ? ' last' : ''}${v.type === 'row' && v.row.kind === 'text' ? ' prose' : ''}${v.type === 'row' && v.row.kind === 'boundary' ? ' bound' : ''}${hit !== null && holdsLine(v, hit) ? ' hit' : ''}`}
                  style={{
                    top: offsets[index] ?? 0,
                    ...(v.type === 'row' ? { paddingTop: v.gap } : {}),
                  }}
                  data-vkey={v.key}
                  data-line={lineOf(v)}
                >
                  {v.type === 'head' ? (
                    <>
                      {v.first ? origin : null}
                      <TurnHeader turn={v.turn} />
                    </>
                  ) : (
                    <RowView row={v.row} />
                  )}
                </div>
              );
            })}
          </div>
          {props.live}
        </div>
      </div>
    </ViewContext>
  );
}
