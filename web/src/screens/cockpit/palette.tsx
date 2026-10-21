import type { GlobalSearchHit, LiveSessionMeta, Template } from '@hoplet/shared';
import { type KeyboardEvent, type ReactNode, useMemo, useState } from 'react';
import { Badge } from '../../components/Badge.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { ProviderMark } from '../../components/provider-marks.tsx';
import { Row } from '../../components/Row.tsx';
import { TextField } from '../../components/TextField.tsx';
import { t } from '../../lib/i18n.ts';
import { filterChoices } from './composer/templates-menu.tsx';
import { displayTitle, isArchived, pathLeaf, sessionPath } from './group.ts';
import { type EntryChoice, entryFields } from './template-entry.tsx';
import { type SearchState, useSearchHits } from './use-search.ts';

const MAX_SESSIONS = 9;
const MAX_LINES = 6;
const MAX_TEMPLATES = 5;

type Entry =
  | { kind: 'session'; id: string; session: LiveSessionMeta; key: string }
  | { kind: 'line'; id: string; hit: GlobalSearchHit; session: LiveSessionMeta | null }
  | { kind: 'template'; id: string; choice: EntryChoice }
  | { kind: 'action'; id: string; action: 'new-session' | 'save-template' | 'settings' };

function Highlight(props: { text: string; query: string }) {
  const q = props.query.trim();
  const at = q ? props.text.toLowerCase().indexOf(q.toLowerCase()) : -1;
  if (at < 0) return props.text;
  return (
    <>
      {props.text.slice(0, at)}
      <span className="hl">{props.text.slice(at, at + q.length)}</span>
      {props.text.slice(at + q.length)}
    </>
  );
}

function Section(props: { label: string; children: ReactNode }) {
  return (
    <div role="presentation">
      <div className="pal-sec" aria-hidden="true">
        {props.label}
      </div>
      {props.children}
    </div>
  );
}

/** The command palette: sessions, matching lines and actions in one list, driven by the keyboard. */
export function Palette(props: {
  sessions: LiveSessionMeta[];
  initialQuery?: string;
  searchFixture?: SearchState;
  onClose: () => void;
  onSelectSession: (id: string) => void;
  onSelectHit: (hit: GlobalSearchHit) => void;
  onNewSession: () => void;
  onSettings: () => void;
  /** The templates to start from; none when the entry is not offered. */
  templates?: readonly EntryChoice[];
  onTemplate?: (template: Template) => void;
  /** Offers "Save the current session as a template…" (a session is open). */
  onSaveTemplate?: () => void;
}) {
  const { sessions, onClose } = props;
  const [query, setQuery] = useState(props.initialQuery ?? '');
  const [active, setActive] = useState(0);
  const { hits, scanning } = useSearchHits(query, props.searchFixture);
  const q = query.trim().toLowerCase();

  const entries = useMemo(() => {
    const byId = new Map(sessions.map((s) => [s.sessionId, s]));
    // Archived sessions are not offered as sessions; their matching lines still name them.
    const matching = sessions
      .filter((s) => !isArchived(s))
      .sort((a, b) => b.updatedAt - a.updatedAt)
      .filter(
        (s) =>
          !q ||
          displayTitle(s).toLowerCase().includes(q) ||
          s.deviceName.toLowerCase().includes(q) ||
          pathLeaf(sessionPath(s)).toLowerCase().includes(q),
      )
      .slice(0, MAX_SESSIONS)
      .map(
        (s, i): Entry => ({
          kind: 'session',
          id: `s-${s.sessionId}`,
          session: s,
          key: `^${i + 1}`,
        }),
      );
    const lines = q
      ? hits.slice(0, MAX_LINES).map(
          (hit): Entry => ({
            kind: 'line',
            id: `l-${hit.sessionId}-${hit.lineNo}`,
            hit,
            session: byId.get(hit.sessionId) ?? null,
          }),
        )
      : [];
    const templates = filterChoices(props.templates ?? [], q)
      .slice(0, MAX_TEMPLATES)
      .map((choice): Entry => ({ kind: 'template', id: `t-${choice.template.id}`, choice }));
    const actions: Entry[] = [
      { kind: 'action', id: 'a-new', action: 'new-session' },
      ...(props.onSaveTemplate
        ? [{ kind: 'action', id: 'a-save', action: 'save-template' } as const]
        : []),
      { kind: 'action', id: 'a-settings', action: 'settings' },
    ];
    return { templates, sessions: matching, lines, actions };
  }, [sessions, hits, q, props.templates, props.onSaveTemplate]);

  const flat: Entry[] = [
    ...entries.templates,
    ...entries.sessions,
    ...entries.lines,
    ...entries.actions,
  ];
  const current = Math.min(active, Math.max(0, flat.length - 1));

  function open(entry: Entry | undefined) {
    if (!entry) return;
    onClose();
    if (entry.kind === 'session') props.onSelectSession(entry.session.sessionId);
    else if (entry.kind === 'line') props.onSelectHit(entry.hit);
    else if (entry.kind === 'template') props.onTemplate?.(entry.choice.template);
    else if (entry.action === 'new-session') props.onNewSession();
    else if (entry.action === 'save-template') props.onSaveTemplate?.();
    else props.onSettings();
  }

  function onKeyDown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const n = flat.length;
      if (n) setActive((current + (e.key === 'ArrowDown' ? 1 : -1) + n) % n);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      open(flat[current]);
    } else if (e.ctrlKey && /^[1-9]$/.test(e.key)) {
      const hit = entries.sessions[Number(e.key) - 1];
      if (hit) {
        e.preventDefault();
        open(hit);
      }
    }
  }

  const rowProps = (entry: Entry) => ({
    id: `pal-${entry.id}`,
    role: 'option',
    'aria-selected': flat[current]?.id === entry.id,
    onMouseMove: () => setActive(flat.indexOf(entry)),
    onClick: () => open(entry),
    'data-testid': `cockpit.palette-${entry.kind}`,
  });

  const activeId = flat[current] ? `pal-${flat[current].id}` : undefined;
  return (
    <Dialog label={t('Search')} size="palette" onClose={onClose} data-testid="cockpit.palette">
      {/* biome-ignore lint/a11y/noStaticElementInteractions: keys are handled for the combobox inside */}
      <div onKeyDown={onKeyDown}>
        <div className="pal-in">
          {Icon.search()}
          <TextField
            bare
            label={t('Search sessions and lines')}
            placeholder={t('Search sessions and lines')}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setActive(0);
            }}
            role="combobox"
            aria-expanded="true"
            aria-controls="pal-list"
            aria-activedescendant={activeId}
            autoComplete="off"
            spellCheck={false}
            data-autofocus
            data-testid="cockpit.palette-input"
          />
        </div>
        <div className="pal-body" id="pal-list" role="listbox" aria-label={t('Results')}>
          {entries.templates.length > 0 ? (
            <Section label={t('Start from a template')}>
              {entries.templates.map((e) =>
                e.kind === 'template' ? (
                  <Row
                    key={e.id}
                    density="menu"
                    leading={Icon.template()}
                    trailing={
                      <span className="pal-where">
                        {e.choice.machine} · {entryFields(e.choice.template)}
                      </span>
                    }
                    {...rowProps(e)}
                  >
                    <Highlight text={e.choice.template.name} query={query} />
                  </Row>
                ) : null,
              )}
            </Section>
          ) : null}
          {entries.sessions.length > 0 ? (
            <Section label={t('Sessions')}>
              {entries.sessions.map((e) =>
                e.kind === 'session' ? (
                  <Row
                    key={e.id}
                    density="menu"
                    leading={<ProviderMark provider={e.session.provider} />}
                    trailing={
                      <>
                        <span className="pal-where">
                          {e.session.deviceName} · {pathLeaf(sessionPath(e.session))}
                        </span>
                        <Badge kind="key">{e.key}</Badge>
                      </>
                    }
                    {...rowProps(e)}
                  >
                    <Highlight text={displayTitle(e.session)} query={query} />
                  </Row>
                ) : null,
              )}
            </Section>
          ) : null}
          {entries.lines.length > 0 || (q && scanning) ? (
            <Section label={t('Lines')}>
              {entries.lines.map((e) =>
                e.kind === 'line' ? (
                  <Row
                    key={e.id}
                    density="menu"
                    muted
                    leading={<ProviderMark provider={e.session?.provider} />}
                    trailing={
                      <span className="pal-where">
                        <span className="pal-ms">
                          {e.session ? displayTitle(e.session) : t('Untitled session')}
                        </span>
                        <span className="pal-ln">
                          {' · '}
                          {t('line {n}').replace('{n}', e.hit.lineNo.toLocaleString())}
                        </span>
                      </span>
                    }
                    {...rowProps(e)}
                  >
                    <Highlight text={e.hit.excerpt} query={query} />
                  </Row>
                ) : null,
              )}
              {scanning ? (
                <div className="pal-scan" aria-hidden="true">
                  <Spinner />
                  {t('Scanning more sessions…')}
                </div>
              ) : null}
            </Section>
          ) : null}
          {entries.actions.length > 0 ? (
            <Section label={t('Actions')}>
              {entries.actions.map((e) =>
                e.kind === 'action' ? (
                  <Row
                    key={e.id}
                    density="menu"
                    leading={
                      e.action === 'new-session'
                        ? Icon.compose()
                        : e.action === 'save-template'
                          ? Icon.template()
                          : Icon.gear()
                    }
                    trailing={
                      e.action === 'save-template' ? undefined : (
                        <Badge kind="key">{e.action === 'new-session' ? '⌘N' : '⌘,'}</Badge>
                      )
                    }
                    {...rowProps(e)}
                  >
                    {e.action === 'new-session'
                      ? t('New session')
                      : e.action === 'save-template'
                        ? t('Save the current session as a template…')
                        : t('Settings')}
                  </Row>
                ) : null,
              )}
            </Section>
          ) : null}
          {flat.length === 0 && !scanning ? <p className="pal-none">{t('No matches')}</p> : null}
        </div>
        <div className="sr-only" role="status">
          {q && scanning ? t('Scanning more sessions…') : ''}
        </div>
        <div className="pal-foot" aria-hidden="true">
          <span>
            <Badge kind="key">↑</Badge>
            <Badge kind="key">↓</Badge>
            {t('to move')}
          </span>
          <span>
            <Badge kind="key">↩</Badge>
            {flat[current]?.kind === 'template' ? t('to start') : t('to open')}
          </span>
          <span>
            <Badge kind="key">esc</Badge>
            {t('to close')}
          </span>
        </div>
      </div>
    </Dialog>
  );
}
