import { MAX_SESSION_TAGS, MAX_TAG_CHARS, TAG_PATTERN } from '@hoplet/shared';
import { type KeyboardEvent, useEffect, useId, useRef, useState } from 'react';
import { Icon } from '../../components/icons.tsx';
import { t } from '../../lib/i18n.ts';

/** A tag as the core keeps it: trimmed and lower case. Null when it is not a tag. */
export function normalTag(text: string): string | null {
  const tag = text.trim().toLowerCase();
  return tag.length > 0 && tag.length <= MAX_TAG_CHARS && TAG_PATTERN.test(tag) ? tag : null;
}

/** What the editor offers for a query: the existing tags that match and are not on the session, then a new one. */
export function tagOptions(
  query: string,
  tags: readonly string[],
  existing: readonly { tag: string; count: number }[],
): { tag: string; count?: number; create?: true }[] {
  const q = query.trim().toLowerCase();
  const matches = existing
    .filter((e) => !tags.includes(e.tag) && e.tag.includes(q))
    .map((e) => ({ tag: e.tag, count: e.count }));
  const tag = normalTag(q);
  const create = tag && !tags.includes(tag) && !existing.some((e) => e.tag === tag);
  return create ? [...matches, { tag, create: true }] : matches;
}

/**
 * The tag editor under the top bar's tags (docs/spec.md#organizing-sessions): a combobox whose chips are the
 * session's tags. Enter adds the active option, Backspace in an empty field removes the last tag, Esc closes.
 * Every change goes to the core at once.
 */
export function TagEditor(props: {
  tags: readonly string[];
  /** Every tag in use, with how many sessions carry it. */
  existing: readonly { tag: string; count: number }[];
  /** The text in the field to start with (development fixtures). */
  initialQuery?: string;
  onAdd: (tag: string) => void;
  onRemove: (tag: string) => void;
  onClose: (restoreFocus: boolean) => void;
}) {
  const { tags, existing } = props;
  const [query, setQuery] = useState(props.initialQuery ?? '');
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const id = useId();
  const full = tags.length >= MAX_SESSION_TAGS;
  const options = full ? [] : tagOptions(query, tags, existing);
  const at = Math.min(active, Math.max(options.length - 1, 0));
  const invalid = query.trim() !== '' && normalTag(query) === null;

  useEffect(() => {
    inputRef.current?.focus();
  }, []);
  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      const host = boxRef.current?.parentElement;
      if (host && !host.contains(e.target as Node)) props.onClose(false);
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [props.onClose]);

  const add = (tag: string) => {
    props.onAdd(tag);
    setQuery('');
    setActive(0);
  };

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.nativeEvent.isComposing) return;
    if (e.key === 'ArrowDown' && options.length > 0) setActive((at + 1) % options.length);
    else if (e.key === 'ArrowUp' && options.length > 0)
      setActive((at - 1 + options.length) % options.length);
    else if (e.key === 'Enter') {
      const pick = options[at];
      if (pick) add(pick.tag);
    } else if (e.key === 'Backspace' && query === '' && tags.length > 0) {
      props.onRemove(tags[tags.length - 1] as string);
    } else if (e.key === 'Escape') props.onClose(true);
    else return;
    e.preventDefault();
    e.stopPropagation();
  }

  const optionId = (i: number) => `${id}-option-${i}`;
  return (
    <div
      ref={boxRef}
      className="popover tag-pop"
      role="dialog"
      aria-label={t('Tags')}
      data-testid="cockpit.tag-editor"
    >
      <div className="tag-field">
        {tags.map((tag) => (
          <span key={tag} className="tag" data-testid="cockpit.tag-editor-chip">
            {tag}
            <button
              type="button"
              className="tag-x"
              aria-label={t('Remove {tag}').replace('{tag}', tag)}
              title={t('Remove {tag}').replace('{tag}', tag)}
              onClick={() => props.onRemove(tag)}
            >
              {Icon.close()}
            </button>
          </span>
        ))}
        <input
          ref={inputRef}
          role="combobox"
          aria-label={t('Add tag')}
          aria-expanded={options.length > 0}
          aria-controls={`${id}-list`}
          aria-autocomplete="list"
          {...(options.length > 0 ? { 'aria-activedescendant': optionId(at) } : {})}
          aria-invalid={invalid || undefined}
          value={query}
          maxLength={MAX_TAG_CHARS}
          disabled={full}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={onKeyDown}
          data-testid="cockpit.tag-input"
        />
      </div>
      <div id={`${id}-list`} role="listbox" aria-label={t('Existing tags')}>
        {options.some((o) => !o.create) ? (
          <div className="menu-label" aria-hidden="true">
            {t('Existing tags')}
          </div>
        ) : null}
        {options.map((o, i) => (
          // biome-ignore lint/a11y/useKeyWithClickEvents: the combobox owns the keys (aria-activedescendant)
          <div
            key={o.create ? `new:${o.tag}` : o.tag}
            id={optionId(i)}
            role="option"
            tabIndex={-1}
            aria-selected={i === at}
            className={`menu-item${i === at ? ' on' : ''}`}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => add(o.tag)}
            data-testid="cockpit.tag-option"
          >
            <span className="menu-check">{o.create ? Icon.plus() : Icon.tag()}</span>
            {o.create ? t('Create “{q}”').replace('{q}', o.tag) : o.tag}
            {o.count !== undefined ? <span className="tag-count">{o.count}</span> : null}
          </div>
        ))}
      </div>
      <p className={`menu-note${invalid || full ? ' warn' : ''}`} role="status">
        {full
          ? t('A session takes up to {n} tags.').replace('{n}', String(MAX_SESSION_TAGS))
          : t(
              'Letters, digits and hyphens, up to 32. Enter adds, Backspace removes the last, Esc closes.',
            )}
      </p>
    </div>
  );
}
