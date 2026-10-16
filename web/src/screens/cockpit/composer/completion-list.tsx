import { type Ref, useEffect, useRef } from 'react';
import { Icon, Spinner } from '../../../components/icons.tsx';
import { t } from '../../../lib/i18n.ts';
import { highlight, type Item, sourceLabel } from './completions.ts';

function Marked(props: { name: string; query: string; path?: boolean }) {
  return (
    <>
      {highlight(props.name, props.query, props.path).map((p, i) =>
        p.hit ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: the parts of a name have no identity
          <span key={i} className="cm-hl">
            {p.text}
          </span>
        ) : (
          // biome-ignore lint/suspicious/noArrayIndexKey: the parts of a name have no identity
          <span key={i}>{p.text}</span>
        ),
      )}
    </>
  );
}

/** The id of the option at `index`, for `aria-activedescendant`. */
export const optionId = (id: string, index: number) => `${id}-o${index}`;
export const listId = (id: string) => `${id}-lb`;

/**
 * The list the composer opens at the caret: `/` shows Commands and Skills, `@` Files and folders. The selected
 * row has the selected fill and a left edge, and shows its whole description on a second line; a hovered one
 * a lighter fill. A draft adds a note under the list.
 */
export function CompletionList(props: {
  id: string;
  items: Item[];
  query: string;
  selected: number;
  /** A row to show hovered (development fixtures); the pointer does the same through CSS. */
  hovered?: number | undefined;
  /** The note under the list: what a draft cannot offer yet. */
  note?: string | undefined;
  onPick: (item: Item) => void;
  anchor: Ref<HTMLDivElement>;
}) {
  const { id, items, query, selected } = props;
  const slash = items[0]?.kind === 'slash';
  const groups = slash
    ? [
        { name: t('Commands'), kind: 'command' as const },
        { name: t('Skills'), kind: 'skill' as const },
      ]
        .map((g) => ({
          name: g.name,
          rows: items
            .map((item, i) => ({ item, i }))
            .filter((r) => r.item.kind === 'slash' && r.item.command.kind === g.kind),
        }))
        .filter((g) => g.rows.length > 0)
    : [{ name: t('Files and folders'), rows: items.map((item, i) => ({ item, i })) }];
  const list = useRef<HTMLDivElement>(null);
  // biome-ignore lint/correctness/useExhaustiveDependencies: the selection moves the row into view
  useEffect(() => {
    list.current
      ?.querySelector<HTMLElement>('[aria-selected="true"]')
      ?.scrollIntoView?.({ block: 'nearest' });
  }, [selected]);
  return (
    <div className="cpop cmp" ref={props.anchor} data-testid="cockpit.completions">
      <div
        ref={list}
        className="cm-list"
        role="listbox"
        id={listId(id)}
        aria-label={slash ? t('Commands and skills') : t('Files and folders')}
      >
        {groups.map((g, gi) => (
          // biome-ignore lint/a11y/useSemanticElements: a fieldset would add a border and legend
          <div key={g.name} role="group" aria-labelledby={`${id}-g${gi}`}>
            <div className="menu-label" id={`${id}-g${gi}`}>
              {g.name}
            </div>
            {g.rows.map(({ item, i }) => {
              const oid = optionId(id, i);
              const desc = item.kind === 'slash' ? item.command.description : '';
              // the highlighted row shows its whole description on a second line
              const two = i === selected && desc !== '';
              return (
                // biome-ignore lint/a11y/useKeyWithClickEvents: the field owns the keys; the combobox pattern has focus stay there
                <div
                  key={item.kind === 'slash' ? item.name : item.path}
                  className={`cm-r${i === selected ? ' on' : ''}${two ? ' two' : ''}${i === props.hovered ? ' hov' : ''}`}
                  role="option"
                  id={oid}
                  aria-selected={i === selected}
                  tabIndex={-1}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => props.onPick(item)}
                  data-testid="cockpit.completion"
                >
                  {item.kind === 'slash' ? (
                    <>
                      <div className="cm-l1">
                        <span className="cm-nm">
                          <span className="s">/</span>
                          <Marked name={item.name} query={query} />
                        </span>
                        {item.command.argumentHint ? (
                          <span className="cm-ah">{item.command.argumentHint}</span>
                        ) : null}
                        {two ? null : <span className="cm-ds">{desc}</span>}
                        <span className="cm-src">{sourceLabel(item.command)}</span>
                      </div>
                      {two ? <div className="cm-d2">{desc}</div> : null}
                    </>
                  ) : (
                    <>
                      <span className="lead">{item.dir ? Icon.folder() : Icon.file()}</span>
                      <span className="t">
                        <Marked name={item.path} query={query} path />
                      </span>
                    </>
                  )}
                </div>
              );
            })}
          </div>
        ))}
      </div>
      {props.note ? (
        <div className="cm-note" role="note">
          <span className="cm-ic">{Icon.info()}</span>
          <span>{props.note}</span>
        </div>
      ) : null}
      <div className="cm-f" aria-hidden="true">
        <span>
          <span className="cm-kc">↑</span>
          <span className="cm-kc">↓</span>
          {t('Move')}
        </span>
        <span>
          <span className="cm-kc">⇥</span>
          {t('or')}
          <span className="cm-kc">↩</span>
          {t('Insert')}
        </span>
        <span>
          <span className="cm-kc">Esc</span>
          {t('Close')}
        </span>
      </div>
    </div>
  );
}

/** A completion that has no list: searching, nothing found, or the owner offline. */
export function CompletionStatus(props: {
  kind: 'loading' | 'none' | 'offline';
  text: string;
  anchor: Ref<HTMLDivElement>;
}) {
  const glyph =
    props.kind === 'loading' ? <Spinner /> : props.kind === 'none' ? Icon.search() : Icon.laptop();
  return (
    <div className="cpop cmp sm" ref={props.anchor} data-testid="cockpit.completion-status">
      <div className={`cm-stat${props.kind === 'offline' ? ' bad' : ''}`} role="status">
        <span className="cm-ic">{glyph}</span>
        {props.text}
      </div>
    </div>
  );
}
