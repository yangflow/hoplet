import type { GlobalSearchHit, LiveSessionMeta, Template } from '@hoplet/shared';
import { useMemo, useState } from 'react';
import { Icon } from '../../components/icons.tsx';
import { MachineIcon } from '../../components/MachineIcon.tsx';
import { ProviderMark } from '../../components/provider-marks.tsx';
import type { Kid } from '../../lib/children.ts';
import { t } from '../../lib/i18n.ts';
import { keyOf, keysOf } from '../../lib/shortcuts.ts';
import { systemOf } from '../../lib/system.ts';
import type { DraftWhere } from './draft.tsx';
import {
  type DeviceGroup,
  displayTitle,
  isArchived,
  isPinned,
  pathLeaf,
  sessionPath,
} from './group.ts';
import { Palette } from './palette.tsx';
import type { PaletteItem, PaletteSource } from './palette-model.ts';
import { type EntryChoice, entryFields } from './template-entry.tsx';
import { type SearchState, useSearchHits } from './use-search.ts';

const RECENT = 3;
const MAX_SESSIONS = 9;
const MAX_LINES = 6;
const MAX_TEMPLATES = 5;
const MAX_MACHINES = 5;
const MAX_PROJECTS = 5;

/** Where a session runs, as the palette's rows show it: machine · directory. */
function where(machine: string, rest: string) {
  return (
    <>
      <span className="pal-ms">{machine}</span>
      <span className="pal-ln">
        {' · '}
        {rest}
      </span>
    </>
  );
}

function sessionItem(s: LiveSessionMeta, run: (id: string) => void): PaletteItem {
  const dir = pathLeaf(sessionPath(s));
  return {
    id: `s-${s.sessionId}`,
    kind: 'session',
    label: displayTitle(s),
    keywords: [s.deviceName, dir],
    lead: <ProviderMark provider={s.provider} />,
    meta: where(s.deviceName, dir),
    numbered: true,
    dim: !s.ownerConnected,
    run: () => run(s.sessionId),
  };
}

/** The sessions, newest first: the three most recent with no query, any of them for a query. */
export function sessionSources(
  sessions: readonly LiveSessionMeta[],
  onSelect: (id: string) => void,
): PaletteSource[] {
  const items = sessions
    .filter((s) => !isArchived(s))
    .sort((a, b) => b.updatedAt - a.updatedAt)
    .map((s) => sessionItem(s, onSelect));
  return [
    { id: 'recent', label: 'Recent', when: 'empty', limit: RECENT, items },
    { id: 'sessions', label: 'Sessions', when: 'query', queryLimit: MAX_SESSIONS, items },
  ];
}

export function templateSource(
  choices: readonly EntryChoice[],
  onTemplate: (template: Template) => void,
): PaletteSource {
  return {
    id: 'templates',
    label: 'Start from a template',
    limit: RECENT,
    queryLimit: MAX_TEMPLATES,
    items: choices.map((c) => ({
      id: `t-${c.template.id}`,
      kind: 'template',
      label: c.template.name,
      keywords: [c.machine, c.detail, c.template.text],
      lead: Icon.template(),
      meta: where(c.machine, entryFields(c.template)),
      verb: t('to start'),
      run: () => onTemplate(c.template),
    })),
  };
}

/** The lines of every session that hold the query; the core searched them, so they show as it answered. */
export function lineSource(
  search: SearchState,
  sessions: readonly LiveSessionMeta[],
  onHit: (hit: GlobalSearchHit) => void,
): PaletteSource {
  const byId = new Map(sessions.map((s) => [s.sessionId, s]));
  return {
    id: 'lines',
    label: 'Lines',
    when: 'query',
    match: 'given',
    busy: search.scanning,
    busyLabel: t('Scanning more sessions…'),
    items: search.hits.slice(0, MAX_LINES).map((hit) => {
      const s = byId.get(hit.sessionId) ?? null;
      return {
        id: `l-${hit.sessionId}-${hit.lineNo}`,
        kind: 'line',
        label: hit.excerpt,
        lead: <ProviderMark provider={s?.provider} />,
        muted: true,
        meta: where(
          s ? displayTitle(s) : t('Untitled session'),
          t('line {n}').replace('{n}', hit.lineNo.toLocaleString()),
        ),
        run: () => onHit(hit),
      };
    }),
  };
}

/** The machines: a row opens Settings › Machines on the machine's tab. */
export function machineSource(
  groups: readonly DeviceGroup[],
  onMachine: (deviceId: string) => void,
): PaletteSource {
  return {
    id: 'machines',
    label: 'Machines',
    when: 'query',
    queryLimit: MAX_MACHINES,
    items: groups.flatMap((g) => {
      const deviceId = g.deviceId;
      if (deviceId === null) return [];
      const n = g.dirs.reduce((sum, d) => sum + d.sessions.length, 0);
      return [
        {
          id: `m-${deviceId}`,
          kind: 'machine',
          label: g.name,
          lead: <MachineIcon kind={systemOf(g.system?.os)} online={g.online} />,
          meta: (
            <span className="pal-ms">
              {g.online
                ? t(n === 1 ? '{n} session' : '{n} sessions').replace('{n}', String(n))
                : t('Offline')}
            </span>
          ),
          dim: !g.online,
          run: () => onMachine(deviceId),
        },
      ];
    }),
  };
}

/**
 * The projects: one row per directory name, with the machines that have it. A row starts a session there, on
 * the open session's machine when it has the project, else on the first online one.
 */
export function projectSource(
  groups: readonly DeviceGroup[],
  near: string | null,
  onStart: (where: DraftWhere) => void,
): PaletteSource {
  const byName = new Map<string, { deviceName: string; path: string; online: boolean }[]>();
  for (const g of groups)
    for (const d of g.dirs) {
      const list = byName.get(d.name) ?? [];
      list.push({ deviceName: g.name, path: d.path, online: g.online });
      byName.set(d.name, list);
    }
  return {
    id: 'projects',
    label: 'Projects',
    when: 'query',
    queryLimit: MAX_PROJECTS,
    items: [...byName].map(([name, places]) => {
      const at =
        places.find((p) => p.deviceName === near && p.online) ??
        places.find((p) => p.online) ??
        places[0];
      return {
        id: `p-${name}`,
        kind: 'project',
        label: name,
        keywords: places.map((p) => p.path),
        lead: Icon.folderOpen(),
        meta: <span className="pal-ms">{places.map((p) => p.deviceName).join(', ')}</span>,
        verb: t('to start a session'),
        run: () => at && onStart({ deviceName: at.deviceName, path: at.path }),
      };
    }),
  };
}

/** What the commands act on and how: the open session and the cockpit's own actions. */
export type PaletteCommands = {
  /** The open session; its commands are listed when there is one. */
  session: LiveSessionMeta | null;
  /** A turn is running or waiting, so Stop is offered. */
  running: boolean;
  /** The live sessions below it, which a stop also stops. */
  stopsWith: readonly Kid[];
  workspaceOpen: boolean;
  sidebarShown: boolean;
  stop: (session: LiveSessionMeta, cascade: boolean) => void;
  handoff: () => void;
  rename: (session: LiveSessionMeta) => void;
  tags: (session: LiveSessionMeta) => void;
  patch: (session: LiveSessionMeta, change: { pinned?: boolean; archived?: boolean }) => void;
  remove: (session: LiveSessionMeta) => void;
  saveTemplate?: (session: LiveSessionMeta) => void;
  workspace: () => void;
  tool: (tool: 'changes' | 'files') => void;
  adjacent: (step: -1 | 1) => void;
  newSession: () => void;
  sidebar: () => void;
  shortcuts: () => void;
  settings: () => void;
};

function command(
  id: string,
  label: string,
  lead: PaletteItem['lead'],
  run: () => void,
  more: Partial<PaletteItem> = {},
): PaletteItem {
  return { id: `c-${id}`, kind: 'command', label, lead, run, ...more };
}

/** The commands on the open session (This session) and the general ones; with a query both are Commands. */
export function commandSources(c: PaletteCommands): PaletteSource[] {
  const s = c.session;
  const sources: PaletteSource[] = [];
  if (s) {
    const title = displayTitle(s);
    const ctx = { context: title };
    const pinned = isPinned(s);
    const archived = isArchived(s);
    const kids = c.stopsWith;
    sources.push({
      id: 'this-session',
      label: 'This session',
      queryLabel: 'Commands',
      aside: title,
      items: [
        ...(c.running
          ? [
              command('stop', t('Stop'), Icon.stop(), () => c.stop(s, kids.length > 0), {
                ...ctx,
                confirm: {
                  title: t('Stop “{title}”?').replace('{title}', title),
                  text: `${t(
                    'The turn on {machine} ends now. The transcript and the worktree stay; you can reply to start again.',
                  ).replace('{machine}', s.deviceName)}${
                    kids.length === 0
                      ? ''
                      : ` ${t(
                          kids.length === 1
                            ? 'Its child session stops with it:'
                            : 'Its {n} child sessions stop with it:',
                        ).replace('{n}', String(kids.length))}`
                  }`,
                  list: kids.map((k) => ({
                    id: k.id,
                    label: `${k.title} · ${k.machine}`,
                    lead: <ProviderMark />,
                  })),
                  action: t('Stop'),
                  verb: t('to stop'),
                },
              }),
            ]
          : []),
        command('handoff', t('Hand off to another machine…'), Icon.handoff(), c.handoff, ctx),
        command('rename', t('Rename…'), Icon.pen(), () => c.rename(s), {
          ...ctx,
          keys: keysOf('rename'),
          queryOnly: true,
        }),
        command(
          'pin',
          pinned ? t('Unpin') : t('Pin'),
          pinned ? Icon.unpin() : Icon.pin(),
          () => c.patch(s, { pinned: !pinned }),
          { ...ctx, keys: keysOf('pin') },
        ),
        command('tags', t('Tags…'), Icon.tag(), () => c.tags(s), {
          ...ctx,
          keys: keysOf('tags'),
          queryOnly: true,
        }),
        command(
          'archive',
          archived ? t('Restore') : t('Archive'),
          archived ? Icon.restore() : Icon.archive(),
          () => c.patch(s, { archived: !archived }),
          { ...ctx, keys: keysOf('archive') },
        ),
        command(
          'workspace',
          c.workspaceOpen ? t('Hide the workspace') : t('Show the workspace'),
          Icon.panel(),
          c.workspace,
          { ...ctx, keys: keysOf('workspace'), queryOnly: true },
        ),
        command('changes', t('Changes'), Icon.branch(), () => c.tool('changes'), {
          ...ctx,
          keys: keysOf('changes'),
          queryOnly: true,
        }),
        command('files', t('Files'), Icon.folder(), () => c.tool('files'), {
          ...ctx,
          keys: keysOf('files'),
          queryOnly: true,
        }),
        ...(c.saveTemplate
          ? [
              command(
                'save-template',
                t('Save the current session as a template…'),
                Icon.template(),
                () => c.saveTemplate?.(s),
                { ...ctx, queryOnly: true },
              ),
            ]
          : []),
        command('delete', t('Delete…'), Icon.trash(), () => c.remove(s), {
          ...ctx,
          queryOnly: true,
        }),
      ],
    });
  }
  sources.push({
    id: 'general',
    label: 'General',
    queryLabel: 'Commands',
    items: [
      command('new-session', t('New session'), Icon.compose(), c.newSession, {
        keys: keysOf('new-session'),
      }),
      command(
        'previous-session',
        t('Previous session in the sidebar'),
        Icon.arrowUp(),
        () => c.adjacent(-1),
        { keys: [keysOf('adjacent-session')[0] ?? ''], queryOnly: true },
      ),
      command(
        'next-session',
        t('Next session in the sidebar'),
        Icon.arrowDown(),
        () => c.adjacent(1),
        { keys: [keysOf('adjacent-session')[1] ?? ''], queryOnly: true },
      ),
      command(
        'sidebar',
        c.sidebarShown ? t('Hide the sidebar') : t('Show the sidebar'),
        Icon.sidebar(),
        c.sidebar,
        { keys: keysOf('sidebar'), queryOnly: true },
      ),
      command('shortcuts', t('Keyboard shortcuts'), Icon.keyboard(), c.shortcuts, {
        keys: [keyOf('shortcuts')],
      }),
      command('settings', t('Settings'), Icon.gear(), c.settings, { keys: keysOf('settings') }),
    ],
  });
  return sources;
}

/** The cockpit's palette: its sources in the order the list shows them, and the search over the lines. */
export function CockpitPalette(props: {
  sessions: LiveSessionMeta[];
  groups: readonly DeviceGroup[];
  initialQuery?: string;
  searchFixture?: SearchState;
  /** Opens with this row's confirmation (development fixtures). */
  confirming?: string;
  templates?: readonly EntryChoice[];
  onTemplate?: (template: Template) => void;
  commands: PaletteCommands;
  onClose: () => void;
  onSelectSession: (id: string) => void;
  onSelectHit: (hit: GlobalSearchHit) => void;
  onMachine: (deviceId: string) => void;
  onStartIn: (where: DraftWhere) => void;
  onNewWithText: (text: string) => void;
}) {
  const [query, setQuery] = useState(props.initialQuery ?? '');
  const search = useSearchHits(query, props.searchFixture);
  const { sessions, groups, templates, onTemplate, commands } = props;
  const { onSelectSession, onSelectHit, onMachine, onStartIn } = props;
  const near = commands.session?.deviceName ?? null;
  const fixed = useMemo(
    () => ({
      sessions: sessionSources(sessions, onSelectSession),
      templates: templates && onTemplate ? [templateSource(templates, onTemplate)] : [],
      machines: machineSource(groups, onMachine),
      projects: projectSource(groups, near, onStartIn),
      commands: commandSources(commands),
    }),
    [
      sessions,
      onSelectSession,
      templates,
      onTemplate,
      groups,
      onMachine,
      near,
      onStartIn,
      commands,
    ],
  );
  const sources = useMemo(
    () => [
      ...fixed.templates,
      ...fixed.sessions,
      fixed.machines,
      fixed.projects,
      ...fixed.commands,
      lineSource(search, sessions, onSelectHit),
    ],
    [fixed, search, sessions, onSelectHit],
  );
  return (
    <Palette
      query={query}
      onQuery={setQuery}
      sources={sources}
      onClose={props.onClose}
      onNewWithText={props.onNewWithText}
      {...(props.confirming ? { confirming: props.confirming } : {})}
    />
  );
}
