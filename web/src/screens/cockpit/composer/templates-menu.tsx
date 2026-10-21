import type { Template } from '@hoplet/shared';
import { type KeyboardEvent, useEffect, useRef, useState } from 'react';
import { Icon } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { t } from '../../../lib/i18n.ts';
import './templates.css';

/** A template as the picker lists it: the template and the line under its name. */
export type TemplateChoice = { template: Template; detail: string };

/** The choices whose name, prompt or detail contain every word of `query`. */
export function filterChoices<T extends TemplateChoice>(choices: readonly T[], query: string): T[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [...choices];
  return choices.filter((c) => {
    const hay = `${c.template.name} ${c.template.text} ${c.detail}`.toLowerCase();
    return words.every((w) => hay.includes(w));
  });
}

/**
 * The draft's Templates chip and its menu: a find row, the templates with where they run and how many fields
 * they ask for, then "Save the draft as a template…" and "Manage templates…". What a pick does is up to the draft.
 */
export function TemplatesMenu(props: {
  choices: TemplateChoice[];
  loading: boolean;
  /** The draft has a prompt that could be saved. */
  canSave: boolean;
  onPick: (template: Template) => void;
  onSave: () => void;
  onManage: () => void;
  disabled?: boolean;
  /** Starts open (development fixtures). */
  initialOpen?: boolean;
}) {
  const [open, setOpen] = useState(props.initialOpen ?? false);
  const [query, setQuery] = useState('');
  const trigger = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const find = useRef<HTMLInputElement>(null);
  const shown = filterChoices(props.choices, query);

  function close(refocus = true) {
    setOpen(false);
    setQuery('');
    if (refocus) trigger.current?.focus();
  }

  useEffect(() => {
    if (open) find.current?.focus();
  }, [open]);

  function items(): HTMLElement[] {
    return [...(list.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])];
  }

  function onKeyDown(e: KeyboardEvent) {
    const all = items();
    const at = all.indexOf(document.activeElement as HTMLElement);
    if (e.key === 'ArrowDown') all[Math.min(at + 1, all.length - 1)]?.focus();
    else if (e.key === 'ArrowUp') {
      if (at <= 0) find.current?.focus();
      else all[at - 1]?.focus();
    } else if (e.key === 'Home') all[0]?.focus();
    else if (e.key === 'End') all[all.length - 1]?.focus();
    else if (e.key === 'Escape') close();
    else if (e.key === 'Tab') {
      close(false);
      return;
    } else return;
    e.preventDefault();
    e.stopPropagation();
  }

  function pick(template: Template) {
    close(false);
    props.onPick(template);
  }

  return (
    <span className="model-menu tpl-menu">
      <button
        ref={trigger}
        type="button"
        className={`tool-btn${open ? ' open' : ''}`}
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={props.disabled}
        onClick={() => (open ? close() : setOpen(true))}
        data-testid="cockpit.templates"
      >
        {Icon.template()}
        {t('Templates')}
        {Icon.down()}
      </button>
      <Popover open={open} onClose={() => close(false)} className="popover-above popover-wide">
        {/* biome-ignore lint/a11y/noStaticElementInteractions: arrow keys move from the find row into the list */}
        <div className="tpl-find" onKeyDown={onKeyDown}>
          {Icon.search()}
          <input
            ref={find}
            type="search"
            aria-label={t('Find a template')}
            placeholder={t('Find a template')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key !== 'Enter') return;
              e.preventDefault();
              const first = shown[0];
              if (first) pick(first.template);
            }}
            data-testid="cockpit.templates-find"
          />
        </div>
        <div className="popover-sep" aria-hidden="true" />
        <div
          ref={list}
          role="menu"
          aria-label={t('Templates')}
          tabIndex={-1}
          onKeyDown={onKeyDown}
          data-testid="cockpit.templates-menu"
        >
          {shown.map((c) => (
            <button
              key={c.template.id}
              type="button"
              role="menuitem"
              tabIndex={-1}
              className="option tpl-item"
              onClick={() => pick(c.template)}
              data-testid="cockpit.templates-option"
            >
              {Icon.template()}
              <span className="tx">
                <b>{c.template.name}</b>
                <span>{c.detail}</span>
              </span>
            </button>
          ))}
          {shown.length === 0 ? (
            <p className="tpl-empty" data-testid="cockpit.templates-empty">
              {props.loading
                ? t('Loading…')
                : props.choices.length === 0
                  ? t('No templates yet')
                  : t('No template matches')}
            </p>
          ) : null}
          <div className="popover-sep" aria-hidden="true" />
          <button
            type="button"
            role="menuitem"
            tabIndex={-1}
            className="option tpl-one"
            aria-disabled={!props.canSave || undefined}
            onClick={() => {
              if (!props.canSave) return;
              close(false);
              props.onSave();
            }}
            data-testid="cockpit.templates-save"
          >
            {Icon.plus()}
            {t('Save the draft as a template…')}
          </button>
          <button
            type="button"
            role="menuitem"
            tabIndex={-1}
            className="option tpl-one"
            onClick={() => {
              close(false);
              props.onManage();
            }}
            data-testid="cockpit.templates-manage"
          >
            {Icon.gear()}
            {t('Manage templates…')}
          </button>
        </div>
      </Popover>
    </span>
  );
}
