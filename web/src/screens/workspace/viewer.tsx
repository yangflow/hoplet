import type { WorkspaceChange, WorkspaceDiff, WorkspaceFile } from '@hoplet/shared';
import { type CSSProperties, type ReactNode, useMemo, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Tokens } from '../../components/Code.tsx';
import { FileBadge, fileKind } from '../../components/file-types.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { formatBytes } from '../../lib/format.ts';
import { type Language, languageOfPath } from '../../lib/highlight.ts';
import { t } from '../../lib/i18n.ts';
import { Counts, StatusTag } from './lists.tsx';
import { type DiffLine, gutterFor, largestLine, parseDiff, splitPath, unfold } from './model.ts';
import type { Loaded, Opened, ViewerContent } from './use-workspace.ts';

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
}) {
  const { opened, change, content, order, online } = props;
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
        className={`ws-vb${props.wrap ? ' wrap' : ''}`}
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
          />
        ) : (
          <FileBody path={opened.path} loaded={content.file} />
        )}
      </section>
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
        diff={diff.diff}
        language={languageOfPath(props.path)}
        full={props.content.full}
        canUnfold={props.online && diff.status !== 'deleted'}
        owner={props.owner}
        onLoadFull={props.onLoadFull}
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

/** Diff rows with folded unchanged regions; a fold opens once the file as it is now has been read. */
function DiffLines(props: {
  diff: string;
  language: Language;
  full: Loaded<string[]> | undefined;
  canUnfold: boolean;
  owner: string;
  onLoadFull: () => void;
}) {
  const parsed = useMemo(() => parseDiff(props.diff), [props.diff]);
  const [open, setOpen] = useState<ReadonlySet<number>>(new Set());
  const rows = props.full?.state === 'ok' ? unfold(parsed, open, props.full.value) : parsed;
  const gutter = { '--ws-gutter': gutterFor(largestLine(rows)) } as CSSProperties;
  return (
    <div className="ws-diff" style={gutter}>
      {rows.map((row, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: rows have no identity
        <DiffRow key={i} row={row} language={props.language}>
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
                {props.full?.state === 'loading' && open.has(row.id) ? <Spinner /> : Icon.down()}
              </span>
              <span>{t('{n} unchanged lines').replace('{n}', String(row.count))}</span>
            </button>
          ) : null}
        </DiffRow>
      ))}
    </div>
  );
}

function DiffRow(props: { row: DiffLine; language: Language; children: ReactNode }) {
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
        <div className={`ws-dl${row.kind === 'eq' ? '' : ` ${row.kind}`}`}>
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
