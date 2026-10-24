import { type KeyboardEvent, type ReactNode, useMemo, useState } from 'react';
import { Badge } from '../../components/Badge.tsx';
import { Button } from '../../components/Button.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { Row } from '../../components/Row.tsx';
import { TextField } from '../../components/TextField.tsx';
import { t } from '../../lib/i18n.ts';
import { keyOf, keysOf } from '../../lib/shortcuts.ts';
import {
  arrange,
  flatten,
  type ListedItem,
  type PaletteItem,
  type PaletteSource,
} from './palette-model.ts';

/** The label with the matched letters marked: the fuzzy positions, or the query's first run in it. */
export function Highlight(props: { text: string; at?: readonly number[] | null; query: string }) {
  const { text } = props;
  let at = props.at;
  if (!at) {
    const q = props.query.trim().toLowerCase();
    const run = q ? text.toLowerCase().indexOf(q) : -1;
    at = run < 0 ? [] : Array.from({ length: q.length }, (_, i) => run + i);
  }
  if (at.length === 0) return <>{text}</>;
  const marked = new Set(at);
  const parts: ReactNode[] = [];
  let i = 0;
  while (i < text.length) {
    const on = marked.has(i);
    let j = i;
    while (j < text.length && marked.has(j) === on) j++;
    const piece = text.slice(i, j);
    parts.push(
      on ? (
        <span key={i} className="hl">
          {piece}
        </span>
      ) : (
        piece
      ),
    );
    i = j;
  }
  return <>{parts}</>;
}

function Section(props: { label: string; aside?: string | undefined; children: ReactNode }) {
  return (
    <div role="presentation">
      <div className="pal-sec" aria-hidden="true">
        {props.label}
        {props.aside ? <span className="pal-aside">{props.aside}</span> : null}
      </div>
      {props.children}
    </div>
  );
}

/** The row offered when nothing matches: start a session with the text as its prompt. */
const NEW_WITH_TEXT = 'new-with-text';

/**
 * The command palette (⌘K): one query over every source, each a section of rows driven by the keyboard. A row
 * that ends work asks in place first; nothing matching offers a new session with the text as its prompt.
 */
export function Palette(props: {
  query: string;
  onQuery: (query: string) => void;
  sources: readonly PaletteSource[];
  onClose: () => void;
  /** Starts a new session with the query as its prompt (⌘↩, and the row shown when nothing matches). */
  onNewWithText?: (text: string) => void;
  /** Opens with this row's confirmation (development fixtures). */
  confirming?: string;
}) {
  const { query, onQuery, onClose } = props;
  const [active, setActive] = useState(0);
  const [confirming, setConfirming] = useState<string | null>(props.confirming ?? null);
  const q = query.trim();

  const sections = useMemo(() => arrange(props.sources, query), [props.sources, query]);
  const busy = sections.some((s) => s.busy);
  const listed = flatten(sections);
  const none = q !== '' && listed.length === 0 && !busy;
  const offerNew = none && props.onNewWithText !== undefined;
  const newItem: ListedItem | null = offerNew
    ? {
        item: {
          id: NEW_WITH_TEXT,
          kind: 'new-with-text',
          label: t('New session with “{text}” as the prompt').replace('{text}', q),
          lead: Icon.compose(),
          keys: keysOf('palette-new'),
          verb: t('to start'),
          run: () => props.onNewWithText?.(q),
        },
        at: null,
      }
    : null;
  const flat = newItem ? [newItem] : listed;
  const current = Math.min(active, Math.max(0, flat.length - 1));
  const confirm = confirming ? flat.find((l) => l.item.id === confirming)?.item : undefined;

  function run(item: PaletteItem | undefined) {
    if (!item) return;
    if (item.confirm && confirming !== item.id) {
      setConfirming(item.id);
      setActive(flat.findIndex((l) => l.item.id === item.id));
      return;
    }
    onClose();
    item.run();
  }

  function onKeyDown(e: KeyboardEvent) {
    if (confirm) {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        setConfirming(null);
      } else if (e.key === 'Enter') {
        e.preventDefault();
        run(confirm);
      } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
      }
      return;
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const n = flat.length;
      if (n) setActive((current + (e.key === 'ArrowDown' ? 1 : -1) + n) % n);
    } else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      if (!q || !props.onNewWithText) return;
      e.preventDefault();
      onClose();
      props.onNewWithText(q);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      run(flat[current]?.item);
    } else if (e.ctrlKey && !e.metaKey && !e.altKey && /^[1-9]$/.test(e.key)) {
      const hit = flat.find((l) => l.key === `^${e.key}`);
      if (hit) {
        e.preventDefault();
        run(hit.item);
      }
    }
  }

  const row = (listedItem: ListedItem) => {
    const { item } = listedItem;
    if (confirm && item.id === confirm.id && item.confirm) {
      return (
        <div
          key={item.id}
          className="pal-conf"
          role="alertdialog"
          aria-label={item.confirm.title}
          data-testid="cockpit.palette-confirm"
        >
          <div className="pal-conf-h">
            <span className="row-lead">{item.lead}</span>
            {item.confirm.title}
          </div>
          <p>{item.confirm.text}</p>
          {item.confirm.list && item.confirm.list.length > 0 ? (
            <ul data-testid="cockpit.palette-confirm-list">
              {item.confirm.list.map((l) => (
                <li key={l.id}>
                  {l.lead}
                  {l.label}
                </li>
              ))}
            </ul>
          ) : null}
          <div className="pal-conf-acts">
            <Button variant="quiet" tabIndex={-1} onClick={() => setConfirming(null)}>
              {t('Cancel')}
              <Badge kind="key">esc</Badge>
            </Button>
            <Button
              variant="danger"
              tabIndex={-1}
              onClick={() => run(item)}
              data-testid="cockpit.palette-confirm-run"
            >
              {item.confirm.action}
              <Badge kind="key">↩</Badge>
            </Button>
          </div>
        </div>
      );
    }
    const keys = listedItem.key ? [listedItem.key] : (item.keys ?? []);
    return (
      <Row
        key={item.id}
        id={`pal-${item.id}`}
        {...(confirm
          ? {}
          : { role: 'option', 'aria-selected': flat[current]?.item.id === item.id })}
        density="menu"
        dim={item.dim ?? false}
        muted={item.muted ?? false}
        leading={item.lead}
        trailing={
          item.meta || keys.length > 0 ? (
            <>
              {item.meta ? <span className="pal-where">{item.meta}</span> : null}
              {keys.map((k) => (
                <Badge key={k} kind="key">
                  {k}
                </Badge>
              ))}
            </>
          ) : undefined
        }
        onMouseMove={() => setActive(flat.indexOf(listedItem))}
        onClick={() => run(item)}
        data-testid={`cockpit.palette-${item.kind}`}
      >
        <Highlight text={item.label} at={listedItem.at} query={query} />
        {q && item.context ? <span className="pal-ctx">{item.context}</span> : null}
      </Row>
    );
  };

  const verb = flat[current]?.item.verb ?? t('to open');
  const activeId = flat[current] ? `pal-${flat[current].item.id}` : undefined;
  return (
    <Dialog
      label={t('Command palette')}
      size="palette"
      onClose={onClose}
      data-testid="cockpit.palette"
    >
      {/* biome-ignore lint/a11y/noStaticElementInteractions: keys are handled for the combobox inside */}
      <div onKeyDown={onKeyDown}>
        <div className="pal-in">
          {Icon.search()}
          <TextField
            bare
            label={t('Search sessions, machines, projects and commands')}
            placeholder={t('Search sessions, machines, projects and commands')}
            value={query}
            onChange={(e) => {
              onQuery(e.target.value);
              setActive(0);
              setConfirming(null);
            }}
            role="combobox"
            aria-expanded={!confirm}
            aria-controls="pal-list"
            aria-activedescendant={confirm ? undefined : activeId}
            autoComplete="off"
            spellCheck={false}
            data-autofocus
            data-testid="cockpit.palette-input"
          />
        </div>
        {/* While a row asks to confirm, the rows around it are shown but not offered: the confirmation owns the keys. */}
        <div
          className="pal-body"
          id="pal-list"
          {...(confirm ? {} : { role: 'listbox', 'aria-label': t('Results') })}
        >
          {sections.map((s) => (
            <Section key={s.id} label={t(s.label)} aside={s.aside}>
              {s.items.map(row)}
              {s.busy ? (
                <div className="pal-scan" aria-hidden="true">
                  <Spinner />
                  {s.busyLabel}
                </div>
              ) : null}
            </Section>
          ))}
          {none ? (
            <div className="pal-none" data-testid="cockpit.palette-none">
              {Icon.search()}
              <b>{t('Nothing matches “{text}”').replace('{text}', q)}</b>
              <p>{t('Sessions, lines, machines, projects and commands were searched.')}</p>
            </div>
          ) : null}
          {newItem ? row(newItem) : null}
        </div>
        <div className="sr-only" role="status">
          {sections
            .filter((s) => s.busy)
            .map((s) => s.busyLabel)
            .join(' ')}
        </div>
        <div className="pal-foot" aria-hidden="true">
          {confirm ? (
            <>
              <span>
                <Badge kind="key">↩</Badge>
                {confirm.confirm?.verb}
              </span>
              <span>
                <Badge kind="key">esc</Badge>
                {t('to go back')}
              </span>
            </>
          ) : (
            <>
              <span>
                <Badge kind="key">↑</Badge>
                <Badge kind="key">↓</Badge>
                {t('to move')}
              </span>
              <span>
                <Badge kind="key">↩</Badge>
                {verb}
              </span>
              {!q && flat.some((l) => l.key) ? (
                <span>
                  <Badge kind="key">⌃1</Badge>
                  {t('to open a session')}
                </span>
              ) : null}
              {q && props.onNewWithText && !none ? (
                <span>
                  <Badge kind="key">{keyOf('palette-new')}</Badge>
                  {t('to start a session with the text')}
                </span>
              ) : null}
              <span>
                <Badge kind="key">esc</Badge>
                {t('to close')}
              </span>
            </>
          )}
        </div>
      </div>
    </Dialog>
  );
}
