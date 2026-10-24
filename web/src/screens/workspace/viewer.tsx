import {
  type Annotation,
  type AnnotationSide,
  MAX_ANNOTATION_SNIPPET_CHARS,
  type WorkspaceChange,
  type WorkspaceDiff,
  type WorkspaceFile,
} from '@hoplet/shared';
import {
  type CSSProperties,
  Fragment,
  type PointerEvent,
  type ReactNode,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { Button } from '../../components/Button.tsx';
import { Tokens } from '../../components/Code.tsx';
import { FileBadge, fileKind } from '../../components/file-types.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { formatBytes } from '../../lib/format.ts';
import { type Language, languageOfPath } from '../../lib/highlight.ts';
import { t } from '../../lib/i18n.ts';
import {
  extendSelection,
  lineOn,
  type Placement,
  pendingOf,
  place,
  rangeOf,
  type Selection,
  shown,
  snippetOf,
  startSelection,
} from './annotations.ts';
import {
  CommentsFoot,
  EditCard,
  NoteCard,
  ReviewBar,
  type ReviewTarget,
  SendReview,
} from './annotations-ui.tsx';
import { Counts, StatusTag } from './lists.tsx';
import { type DiffLine, gutterFor, largestLine, parseDiff, splitPath, unfold } from './model.ts';
import type { Annotations } from './use-annotations.ts';
import type { Loaded, Opened, ViewerContent } from './use-workspace.ts';

/** Review comments in the diff (#84): the session's comments, where they can go, and the fixtures' first state. */
export type DiffReview = {
  notes: Annotations;
  target: ReviewTarget;
  /** Development fixtures: lines picked with a comment being written, resolved ones shown, the send dialog open. */
  initial?: {
    draft?: { side: AnnotationSide; start: number; end: number; body: string };
    showResolved?: boolean;
    send?: { note: string };
  };
};

/** A message in place of a list or a viewer: glyph, one line, one sentence, at most one action. */
export function Quiet(props: {
  icon: ReactNode;
  title?: string;
  text: ReactNode;
  action?: ReactNode;
  status?: boolean;
  testid?: string;
}) {
  return (
    <div
      className="ws-quiet"
      {...(props.status ? { role: 'status' } : {})}
      {...(props.testid ? { 'data-testid': props.testid } : {})}
    >
      {props.icon}
      {props.title ? <b>{props.title}</b> : null}
      <span>{props.text}</span>
      {props.action}
    </div>
  );
}

/** The open path: its header (type, name, status, counts and controls) over its diff, file or image. */
export function Viewer(props: {
  opened: Opened;
  change: WorkspaceChange | undefined;
  content: ViewerContent;
  /** Changed paths in list order, for previous and next. */
  order: readonly string[];
  wrap: boolean;
  online: boolean;
  owner: string;
  onWrap: () => void;
  onMode: (mode: Opened['mode']) => void;
  onStep: (path: string) => void;
  onClose: () => void;
  onLoadFull: () => void;
  review?: DiffReview;
}) {
  const { opened, change, content, order, online, review } = props;
  const [showResolved, setShowResolved] = useState(review?.initial?.showResolved ?? false);
  const [sending, setSending] = useState(review?.initial?.send ?? null);
  const all = review?.notes.list ?? [];
  const here = all.filter((a) => a.path === opened.path);
  const resolvedHere = here.filter((a) => a.state === 'resolved').length;
  const pending = pendingOf(all);
  const { dir, name } = splitPath(opened.path);
  const diffMode = opened.mode === 'diff';
  const file = content.file?.state === 'ok' ? content.file.value : null;
  const diff = content.diff?.state === 'ok' ? content.diff.value : null;
  const text = diffMode ? diff?.kind === 'text' : file?.kind === 'text';
  const at = order.indexOf(opened.path);
  const prev = at > 0 ? order[at - 1] : undefined;
  const next = at >= 0 ? order[at + 1] : undefined;

  return (
    <section className="ws-viewer" aria-label={opened.path} data-testid="workspace.viewer">
      <header className="ws-vh">
        <FileBadge kind={fileKind(name)} />
        <span className="ws-vname" title={opened.path}>
          <b>{name}</b>
          {dir ? <span>{dir}</span> : null}
        </span>
        {change ? <StatusTag status={change.status} /> : null}
        {diffMode && change && !change.binary ? (
          <span className="ws-counts">
            <Counts change={change} />
          </span>
        ) : null}
        <span className="ws-gap" />
        {!diffMode && file ? (
          <span
            className="ws-ro"
            role="img"
            aria-label={t('Read only · {size}').replace('{size}', formatBytes(file.size))}
            title={t('Read only · {size}').replace('{size}', formatBytes(file.size))}
          >
            {Icon.lock()}
          </span>
        ) : null}
        {text ? (
          <IconButton
            label={t('Wrap long lines')}
            hint="⌥Z"
            icon={Icon.wrap()}
            pressed={props.wrap}
            onClick={props.onWrap}
            data-testid="workspace.wrap"
          />
        ) : null}
        {change?.status === 'modified' ? (
          // biome-ignore lint/a11y/useSemanticElements: a segmented control is a group of toggles, not a form fieldset
          <div className="ws-seg" role="group" aria-label={t('Show')}>
            <button
              type="button"
              aria-pressed={diffMode}
              className={diffMode ? 'on' : ''}
              onClick={() => props.onMode('diff')}
              data-testid="workspace.mode-diff"
            >
              {t('Diff')}
            </button>
            <button
              type="button"
              aria-pressed={!diffMode}
              className={diffMode ? '' : 'on'}
              disabled={!online}
              title={
                online ? undefined : t('Files need {name} online').replace('{name}', props.owner)
              }
              onClick={() => props.onMode('file')}
              data-testid="workspace.mode-file"
            >
              {t('File')}
            </button>
          </div>
        ) : null}
        {diffMode ? (
          <span className="ws-nav">
            <IconButton
              label={t('Previous file')}
              hint="⌥↑"
              icon={Icon.arrowUp()}
              disabled={!prev}
              onClick={() => prev && props.onStep(prev)}
              data-testid="workspace.prev"
            />
            <IconButton
              label={t('Next file')}
              hint="⌥↓"
              icon={Icon.arrowDown()}
              disabled={!next}
              onClick={() => next && props.onStep(next)}
              data-testid="workspace.next"
            />
          </span>
        ) : null}
        <IconButton
          label={t('Close')}
          hint="Esc"
          icon={Icon.close()}
          onClick={props.onClose}
          data-testid="workspace.viewer-close"
        />
      </header>
      <section
        className={`ws-vb${props.wrap ? ' wrap' : ''}${review && pending.ids.length > 0 ? ' barred' : ''}`}
        // biome-ignore lint/a11y/noNoninteractiveTabindex: the body scrolls sideways and must be reachable by keyboard
        tabIndex={0}
        aria-label={diffMode ? t('Diff') : t('File')}
        data-testid="workspace.viewer-body"
      >
        {diffMode ? (
          <DiffBody
            path={opened.path}
            content={content}
            change={change}
            online={online}
            owner={props.owner}
            onOpenFile={() => props.onMode('file')}
            onLoadFull={props.onLoadFull}
            {...(review ? { review: { ...review, showResolved } } : {})}
          />
        ) : (
          <FileBody path={opened.path} loaded={content.file} />
        )}
      </section>
      {review && diffMode ? (
        <CommentsFoot
          open={here.length - resolvedHere}
          resolved={resolvedHere}
          showResolved={showResolved}
          onToggle={() => setShowResolved((v) => !v)}
        />
      ) : null}
      {review && pending.ids.length > 0 ? (
        <ReviewBar
          count={pending.ids.length}
          files={pending.files}
          onDiscard={() => void review.notes.remove(pending.ids)}
          onReview={() => setSending({ note: '' })}
        />
      ) : null}
      {review && sending && pending.ids.length > 0 ? (
        <SendReview
          notes={review.notes}
          ids={pending.ids}
          files={pending.files}
          target={review.target}
          initialNote={sending.note}
          onClose={() => setSending(null)}
          onSent={() => setSending(null)}
        />
      ) : null}
    </section>
  );
}

function Pending({ loaded }: { loaded: Loaded<unknown> | undefined }) {
  if (loaded?.state === 'error')
    return <Quiet icon={Icon.alert()} title={t('Could not read this path')} text={loaded.text} />;
  return <Quiet icon={<Spinner />} text={t('Reading…')} status />;
}

function DiffBody(props: {
  path: string;
  content: ViewerContent;
  change: WorkspaceChange | undefined;
  online: boolean;
  owner: string;
  onOpenFile: () => void;
  onLoadFull: () => void;
  review?: DiffReview & { showResolved: boolean };
}) {
  const loaded = props.content.diff;
  if (loaded?.state !== 'ok') return <Pending loaded={loaded} />;
  const diff = loaded.value;
  if (diff.kind === 'binary') return <BinaryDiff diff={diff} content={props.content} />;
  if (diff.diff === '')
    return (
      <Quiet icon={Icon.check()} title={t('No changes')} text={t('This path matches HEAD.')} />
    );
  return (
    <>
      <DiffLines
        key={props.path}
        diff={diff.diff}
        language={languageOfPath(props.path)}
        full={props.content.full}
        canUnfold={props.online && diff.status !== 'deleted'}
        owner={props.owner}
        onLoadFull={props.onLoadFull}
        path={props.path}
        {...(props.review ? { review: props.review } : {})}
      />
      {diff.truncated ? (
        <div className="ws-cut" data-testid="workspace.cut">
          {Icon.info()}
          <span>{t('Cut at 256 KiB of {size}').replace('{size}', formatBytes(diff.size))}</span>
          <span className="ws-gap" />
          {props.online && diff.status !== 'deleted' ? (
            <Button variant="quiet" onClick={props.onOpenFile} data-testid="workspace.open-file">
              {t('Open file')}
            </Button>
          ) : null}
        </div>
      ) : null}
    </>
  );
}

/**
 * Diff rows with folded unchanged regions; a fold opens once the file as it is now has been read. With a review,
 * a line's + picks it (a drag or a shift-click picks more) for a comment, and the comments sit under their lines.
 */
function DiffLines(props: {
  diff: string;
  language: Language;
  full: Loaded<string[]> | undefined;
  canUnfold: boolean;
  owner: string;
  onLoadFull: () => void;
  path: string;
  review?: DiffReview & { showResolved: boolean };
}) {
  const { review } = props;
  const parsed = useMemo(() => parseDiff(props.diff), [props.diff]);
  const [open, setOpen] = useState<ReadonlySet<number>>(new Set());
  const rows = props.full?.state === 'ok' ? unfold(parsed, open, props.full.value) : parsed;
  const gutter = { '--ws-gutter': gutterFor(largestLine(rows)) } as CSSProperties;

  const draft = review?.initial?.draft;
  const [sel, setSel] = useState<Selection | null>(
    draft ? { side: draft.side, anchor: draft.start, head: draft.end } : null,
  );
  const [writing, setWriting] = useState(draft !== undefined);
  const [editing, setEditing] = useState<string | null>(null);
  const dragging = useRef(false);
  useEffect(() => {
    const up = () => {
      if (!dragging.current) return;
      dragging.current = false;
      setWriting(true);
    };
    window.addEventListener('pointerup', up);
    return () => window.removeEventListener('pointerup', up);
  }, []);

  const range = sel ? rangeOf(sel) : null;
  const picked = (row: DiffLine) => {
    const n = sel && range ? lineOn(row, sel.side) : null;
    return n !== null && range !== null && n >= range.start && n <= range.end;
  };
  let pickedLast = -1;
  rows.forEach((row, i) => {
    if (picked(row)) pickedLast = i;
  });

  const cards = new Map<number, { a: Annotation; p: Placement }[]>();
  for (const a of review ? shown(review.notes.list, props.path, review.showResolved) : []) {
    const p = place(rows, a);
    cards.set(p.after, [...(cards.get(p.after) ?? []), { a, p }]);
  }

  function pick(row: DiffLine, e: PointerEvent) {
    if (e.button !== 0) return;
    e.preventDefault();
    if (e.shiftKey && sel) {
      setSel(extendSelection(sel, row));
      setWriting(true);
      return;
    }
    const s = startSelection(row);
    if (!s) return;
    setSel(s);
    setWriting(false);
    dragging.current = true;
  }
  function close() {
    setSel(null);
    setWriting(false);
  }

  const after = (i: number) => {
    const here = cards.get(i) ?? [];
    const out: ReactNode[] = here.map(({ a, p }) =>
      editing === a.id && review ? (
        <EditCard
          key={a.id}
          start={p.startLine}
          end={p.endLine}
          initial={a.body}
          editing
          error={review.notes.error}
          onSave={(body) => review.notes.edit(a.id, body)}
          onCancel={() => setEditing(null)}
        />
      ) : (
        <NoteCard
          key={a.id}
          note={a}
          placement={p}
          onEdit={() => setEditing(a.id)}
          onDelete={() => void review?.notes.remove([a.id])}
          onResolve={(r) => void review?.notes.resolve(a.id, r)}
        />
      ),
    );
    if (review && sel && range && writing && i === pickedLast) {
      out.push(
        <EditCard
          key="new"
          start={range.start}
          end={range.end}
          initial={draft?.body ?? ''}
          editing={false}
          error={review.notes.error}
          onSave={(body) =>
            review.notes.add({
              path: props.path,
              side: sel.side,
              startLine: range.start,
              endLine: range.end,
              snippet: snippetOf(rows, sel.side, range.start, range.end).slice(
                0,
                MAX_ANNOTATION_SNIPPET_CHARS,
              ),
              body,
            })
          }
          onCancel={close}
        />,
      );
    }
    return out;
  };

  return (
    <div className={`ws-diff${review ? ' notes' : ''}`} style={gutter}>
      {after(-1)}
      {rows.map((row, i) => {
        const line = row.kind === 'eq' || row.kind === 'add' || row.kind === 'del';
        return (
          // biome-ignore lint/suspicious/noArrayIndexKey: rows have no identity
          <Fragment key={i}>
            <DiffRow
              row={row}
              language={props.language}
              picked={picked(row)}
              {...(review && line
                ? {
                    onPick: (e: PointerEvent) => pick(row, e),
                    onEnter: () => {
                      if (dragging.current) setSel((s) => s && extendSelection(s, row));
                    },
                  }
                : {})}
            >
              {row.kind === 'fold' ? (
                <button
                  type="button"
                  className="ws-fold"
                  disabled={!props.canUnfold}
                  title={
                    props.canUnfold
                      ? t('Show {n} unchanged lines').replace('{n}', String(row.count))
                      : t('Files need {name} online').replace('{name}', props.owner)
                  }
                  onClick={() => {
                    setOpen((s) => new Set(s).add(row.id));
                    if (props.full?.state !== 'ok') props.onLoadFull();
                  }}
                  data-testid="workspace.fold"
                >
                  <span className="ws-fold-i">
                    {props.full?.state === 'loading' && open.has(row.id) ? (
                      <Spinner />
                    ) : (
                      Icon.down()
                    )}
                  </span>
                  <span>{t('{n} unchanged lines').replace('{n}', String(row.count))}</span>
                </button>
              ) : null}
            </DiffRow>
            {after(i)}
          </Fragment>
        );
      })}
    </div>
  );
}

/** One row of a diff: a hunk header, a note, or a line with its numbers and its sign. */
export function DiffRow(props: {
  row: DiffLine;
  language: Language;
  children: ReactNode;
  /** The line is in the lines picked for a comment. */
  picked?: boolean;
  /** A press on the line's + starts picking lines for a comment; entering the line while pressed extends them. */
  onPick?: (e: PointerEvent) => void;
  onEnter?: () => void;
}) {
  const { row } = props;
  switch (row.kind) {
    case 'fold':
      return <>{props.children}</>;
    case 'hunk':
      return (
        <div className="ws-dl hunk">
          <span className="n" />
          <span className="n" />
          <span className="s" />
          <span className="c">
            @@ {row.range} @@{row.context ? <b> {row.context}</b> : null}
          </span>
        </div>
      );
    case 'note':
      return (
        <div className="ws-dl hunk">
          <span className="n" />
          <span className="n" />
          <span className="s" />
          <span className="c">{row.text}</span>
        </div>
      );
    default:
      return (
        <div
          className={`ws-dl${row.kind === 'eq' ? '' : ` ${row.kind}`}${props.picked ? ' pick' : ''}`}
          {...(props.onEnter ? { onPointerEnter: props.onEnter } : {})}
        >
          {props.onPick ? (
            <button
              type="button"
              className="ws-plus"
              tabIndex={-1}
              aria-label={t('Comment on this line')}
              title={t('Comment on this line')}
              onPointerDown={props.onPick}
              data-testid="workspace.comment-plus"
            >
              {Icon.plus()}
            </button>
          ) : null}
          <span className="n">{row.kind === 'add' ? '' : row.oldNo}</span>
          <span className="n">{row.kind === 'del' ? '' : row.newNo}</span>
          <span className="s">{row.kind === 'add' ? '+' : row.kind === 'del' ? '−' : ' '}</span>
          <span className="c">
            <Tokens line={row.text} language={props.language} />
          </span>
        </div>
      );
  }
}

function FileBody({ path, loaded }: { path: string; loaded: Loaded<WorkspaceFile> | undefined }) {
  if (loaded?.state !== 'ok') return <Pending loaded={loaded} />;
  const file = loaded.value;
  if (file.kind === 'image')
    return (
      <div className="ws-image">
        <ImageView file={file} box="large" />
      </div>
    );
  if (file.kind === 'binary')
    return (
      <Quiet
        icon={Icon.file()}
        title={t('Binary file')}
        text={t('{size} · not shown').replace('{size}', formatBytes(file.size))}
        testid="workspace.binary"
      />
    );
  return <CodeView text={file.text} truncated={file.truncated} size={file.size} path={path} />;
}

function CodeView(props: { text: string; truncated: boolean; size: number; path: string }) {
  const language = languageOfPath(props.path);
  const lines = useMemo(() => {
    const all = props.text.split('\n');
    if (all[all.length - 1] === '') all.pop();
    return all;
  }, [props.text]);
  return (
    <>
      <div
        className="ws-code"
        style={{ '--ws-gutter': gutterFor(lines.length) } as CSSProperties}
        data-testid="workspace.code"
      >
        {lines.map((line, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: lines have no identity
          <div key={i} className="ws-l">
            <span className="n">{i + 1}</span>
            <span className="c">
              <Tokens line={line} language={language} />
            </span>
          </div>
        ))}
      </div>
      {props.truncated ? (
        <div className="ws-cut">
          {Icon.info()}
          <span>{t('Cut at 256 KiB of {size}').replace('{size}', formatBytes(props.size))}</span>
        </div>
      ) : null}
    </>
  );
}

/** An image on the checkerboard, with its pixel size once it has loaded and its byte size. */
function ImageView(props: {
  file: Extract<WorkspaceFile, { kind: 'image' }>;
  box: 'large' | 'side';
}) {
  const { file } = props;
  const [dims, setDims] = useState<string | null>(null);
  return (
    <>
      <div className={`ws-checker ${props.box}`}>
        <img
          src={`data:${file.mediaType};base64,${file.data}`}
          alt={splitPath(file.path).name}
          onLoad={(e) =>
            setDims(`${e.currentTarget.naturalWidth} × ${e.currentTarget.naturalHeight}`)
          }
        />
      </div>
      <span className="ws-cap">
        {props.box === 'large' ? [dims, formatBytes(file.size)].filter(Boolean).join(' · ') : dims}
      </span>
    </>
  );
}

/** A binary change: an image before (at HEAD) and after, or the sizes of any other file. */
function BinaryDiff(props: {
  diff: Extract<WorkspaceDiff, { kind: 'binary' }>;
  content: ViewerContent;
}) {
  const { diff, content } = props;
  if (fileKind(diff.path) !== 'image') {
    const sizes = [diff.oldSize, diff.newSize]
      .map((s) => (s === undefined ? '—' : formatBytes(s)))
      .join(' → ');
    return (
      <Quiet icon={Icon.file()} title={t('Binary file')} text={sizes} testid="workspace.binary" />
    );
  }
  const side = (
    label: string,
    cls: string,
    size: number | undefined,
    loaded: Loaded<WorkspaceFile> | undefined,
  ) => (
    <div className={`ws-side ${cls}`}>
      <div className="ws-side-h">
        <span>{label}</span>
        <span className="ws-meta">{size === undefined ? '' : formatBytes(size)}</span>
      </div>
      {loaded?.state === 'ok' && loaded.value.kind === 'image' ? (
        <ImageView file={loaded.value} box="side" />
      ) : (
        <div className="ws-checker side">
          {loaded?.state === 'loading' ? <Spinner /> : <FileBadge kind="image" />}
        </div>
      )}
    </div>
  );
  return (
    <div className="ws-sides" data-testid="workspace.before-after">
      {diff.oldSize === undefined ? null : side(t('Before'), 'was', diff.oldSize, content.before)}
      {diff.newSize === undefined ? null : side(t('After'), 'now', diff.newSize, content.after)}
    </div>
  );
}
