import type { FullBlock } from '@hoplet/shared';
import { useEffect, useMemo, useRef, useState } from 'react';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { fetchBlock } from '../../lib/api.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { formatBytes } from './format.ts';
import { linesOf, OutputWell } from './OutputWell.tsx';
import type { Elided } from './turns.ts';
import { useView } from './view-context.tsx';

/** The text a full block shows when it is opened. */
export function blockText(block: FullBlock): string {
  switch (block.type) {
    case 'text':
    case 'thinking':
      return block.text;
    case 'tool-result':
      return block.content;
    case 'tool-call':
      return JSON.stringify(block.input ?? null, null, 2);
    case 'image':
      return block.url ?? block.mediaType;
    case 'other':
      return block.text ?? block.label ?? block.kind;
  }
}

const CACHE_BYTES = 8 * 1024 * 1024;
const fetched = new Map<string, string>();

/** Puts the text of a block where `loadBlock` finds it (development fixtures). */
export function rememberBlock(sessionId: string, line: number, block: number, text: string): void {
  remember(`${sessionId}:${line}:${block}`, text);
}

function cachedBlock(sessionId: string, line: number, block: number): string | undefined {
  return fetched.get(`${sessionId}:${line}:${block}`);
}
let fetchedBytes = 0;

function remember(key: string, text: string): void {
  if (text.length > CACHE_BYTES) return;
  fetched.set(key, text);
  fetchedBytes += text.length;
  for (const [oldest, value] of fetched) {
    if (fetchedBytes <= CACHE_BYTES || oldest === key) break;
    fetched.delete(oldest);
    fetchedBytes -= value.length;
  }
}

/** Fetches an elided block of a session once; opened blocks are kept up to 8 MiB. */
export async function loadBlock(sessionId: string, line: number, block: number): Promise<string> {
  const key = `${sessionId}:${line}:${block}`;
  const known = fetched.get(key);
  if (known !== undefined) return known;
  const text = blockText(await fetchBlock(sessionId, line, block));
  remember(key, text);
  return text;
}

const PREVIEW_LINES = 3;

/**
 * An elided block: a preview in the output well with an open button showing the size; opened, the whole block
 * in place. `load` fetches it; the opened state is kept while the row is unmounted by the virtual list.
 */
export function ElidedPanel(props: {
  id: string;
  bytes: number;
  preview: string;
  load: () => Promise<string>;
  /** The text, when it is already at hand. */
  cached?: string | undefined;
  /** Sits flush left, not under a tool row. */
  solo?: boolean;
}) {
  const { id, bytes, preview, load, solo } = props;
  const view = useView();
  const open = view.isOpen(`blk:${id}`);
  const [text, setText] = useState<string | null>(props.cached ?? null);
  const [state, setState] = useState<'idle' | 'loading' | 'error'>('idle');
  const [error, setError] = useState('');
  const loadRef = useRef(load);
  loadRef.current = load;

  useEffect(() => {
    if (!open || text !== null) return;
    let cancelled = false;
    setState('loading');
    loadRef
      .current()
      .then((full) => {
        if (cancelled) return;
        setText(full);
        setState('idle');
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setError(errorText(e));
        setState('error');
        view.setOpen(`blk:${id}`, false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, text, id, view]);

  const size = formatBytes(bytes);
  const full = useMemo(() => (open && text !== null ? linesOf(text) : null), [open, text]);
  if (full) {
    return (
      <OutputWell
        lines={full}
        numbered
        {...(solo ? { solo } : {})}
        label={t('Block content')}
        data-testid="cockpit.elided-open"
        scrollTestId="cockpit.block-full"
        actions={
          <IconButton
            label={t('Close')}
            hint="Esc"
            icon={Icon.close()}
            onClick={() => view.setOpen(`blk:${id}`, false)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') view.setOpen(`blk:${id}`, false);
            }}
            data-testid="cockpit.block-close"
          />
        }
      />
    );
  }
  return (
    <OutputWell
      lines={linesOf(preview).slice(0, PREVIEW_LINES)}
      peek
      {...(solo ? { solo } : {})}
      copyText={preview}
      label={t('Block content')}
      data-testid="cockpit.elided"
    >
      <button
        type="button"
        className="wopen"
        aria-label={`${t('Open the whole block')} · ${size}`}
        title={`${t('Open the whole block')} · ${size}`}
        disabled={state === 'loading'}
        onClick={() => view.setOpen(`blk:${id}`, true)}
        data-testid="cockpit.block-open"
      >
        {state === 'loading' ? <Spinner /> : null}
        <span>{t('Open {size}').replace('{size}', size)}</span>
      </button>
      {state === 'error' ? (
        <p className="elided-error" role="alert">
          {error}
        </p>
      ) : null}
    </OutputWell>
  );
}

/** An elided block of a stored line, fetched from the core when opened. */
export function StoredElided({ elided, solo }: { elided: Elided; solo?: boolean }) {
  const view = useView();
  return (
    <ElidedPanel
      id={`${elided.line}:${elided.block}`}
      bytes={elided.bytes}
      preview={elided.preview}
      load={() => loadBlock(view.sessionId, elided.line, elided.block)}
      cached={cachedBlock(view.sessionId, elided.line, elided.block)}
      {...(solo ? { solo } : {})}
    />
  );
}
