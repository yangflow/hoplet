import type { ConfigChangeObject, ConfigTakesEffect, SessionConfigChange } from '@hoplet/shared';
import { useCallback, useEffect, useState } from 'react';
import {
  fetchConfigChange,
  fetchConfigChanges,
  postConfigChangeDecision,
  postConfigChangeUndo,
} from './api.ts';
import { errorText } from './errors.ts';
import { t } from './i18n.ts';
import { onLive } from './live.ts';

/** The change with its newer state in place, the newest change first. */
export function withChange(
  list: readonly SessionConfigChange[],
  next: SessionConfigChange,
): SessionConfigChange[] {
  const rest = list.filter((c) => c.changeId !== next.changeId);
  return [next, ...rest].sort((a, b) => b.createdAt - a.createdAt);
}

/**
 * A session's requests to change its own configuration (ADR 0036): loaded once, then kept current by the core's
 * `session.change` messages, which carry the state only, so the change is read again. A fixture replaces both.
 * `touched` holds the changes decided or undone in this window, whose result stays in view after the turn ends;
 * `dismissed` the ones whose refused undo the user put away.
 * `decide` and `undo` resolve to an error message, or null when the core took them.
 */
export function useConfigChanges(
  sessionId: string | null,
  fixture?: readonly SessionConfigChange[],
) {
  const [changes, setChanges] = useState<SessionConfigChange[]>(() => [...(fixture ?? [])]);
  const [touched, setTouched] = useState<ReadonlySet<string>>(
    () => new Set((fixture ?? []).map((c) => c.changeId)),
  );
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(() => new Set());
  const [busy, setBusy] = useState<string | null>(null);
  const locked = fixture !== undefined;

  useEffect(() => {
    if (locked) return;
    setChanges([]);
    setTouched(new Set());
    setDismissed(new Set());
    if (!sessionId) return;
    const abort = new AbortController();
    const load = () => {
      void fetchConfigChanges(sessionId, abort.signal)
        .then((r) => setChanges(r.changes))
        .catch(() => {});
    };
    load();
    const off = onLive((event) => {
      if (event.kind === 'open') load();
      else if (
        event.kind === 'message' &&
        event.msg.type === 'session.change' &&
        event.msg.sessionId === sessionId
      ) {
        void fetchConfigChange(event.msg.changeId, abort.signal)
          .then((next) => setChanges((cur) => withChange(cur, next)))
          .catch(() => {});
      }
    });
    return () => {
      abort.abort();
      off();
    };
  }, [sessionId, locked]);

  const run = useCallback(
    async (changeId: string, act: () => Promise<SessionConfigChange>): Promise<string | null> => {
      setBusy(changeId);
      setTouched((cur) => new Set(cur).add(changeId));
      setDismissed((cur) => {
        const next = new Set(cur);
        next.delete(changeId);
        return next;
      });
      try {
        const next = await act();
        setChanges((cur) => withChange(cur, next));
        return null;
      } catch (err) {
        // A change closed meanwhile, or an undo the core refused, is shown as it is now.
        void fetchConfigChange(changeId)
          .then((next) => setChanges((cur) => withChange(cur, next)))
          .catch(() => {});
        return errorText(err);
      } finally {
        setBusy(null);
      }
    },
    [],
  );

  const decide = useCallback(
    (changeId: string, approve: boolean) =>
      run(changeId, () => postConfigChangeDecision(changeId, approve)),
    [run],
  );
  const undo = useCallback(
    (changeId: string) => run(changeId, () => postConfigChangeUndo(changeId)),
    [run],
  );
  /** Puts away the refusal of an undo in this window; the change itself stays as it is. */
  const dismiss = useCallback((changeId: string) => {
    setDismissed((cur) => new Set(cur).add(changeId));
  }, []);

  return { changes, touched, dismissed, busy, decide, undo, dismiss };
}

export type ConfigChanges = ReturnType<typeof useConfigChanges>;

/** The pending change of a session, if any: the core keeps at most one open per session. */
export function pendingChange(changes: readonly SessionConfigChange[]): SessionConfigChange | null {
  return changes.find((c) => c.state === 'pending') ?? null;
}

/** The path of an object's file, from its diff's headers; null for a setting or a diff without one. */
export function objectPath(object: ConfigChangeObject): string | null {
  if (object.kind === 'config') return null;
  const to = /^\+\+\+ b\/(.+)$/m.exec(object.diff)?.[1];
  if (to) return to;
  return /^--- a\/(.+)$/m.exec(object.diff)?.[1] ?? null;
}

/** The lines a diff adds and removes, without its headers. */
export function diffCounts(diff: string): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const line of diff.split('\n')) {
    if (line.startsWith('+++ ') || line.startsWith('--- ')) continue;
    if (line.startsWith('+')) added += 1;
    else if (line.startsWith('-')) removed += 1;
  }
  return { added, removed };
}

/** A setting's value before and after, from its diff (`-effort: medium` / `+effort: high`). */
export function settingValues(object: ConfigChangeObject): {
  from: string | null;
  to: string | null;
} {
  const value = (sign: string) => {
    const line = object.diff.split('\n').find((l) => l.startsWith(`${sign}${object.name}:`));
    return line === undefined ? null : line.slice(object.name.length + 2).trim();
  };
  return { from: value('-'), to: value('+') };
}

/** How the object is named in the interface: memory by its file, a command as the slash command it is. */
export function objectName(object: ConfigChangeObject): string {
  if (object.kind === 'memory') return objectPath(object)?.split('/').pop() ?? t('Project memory');
  if (object.kind === 'command') return `/${object.name}`;
  if (object.kind === 'config') {
    if (object.name === 'model') return t('Model');
    if (object.name === 'effort') return t('Effort');
  }
  return object.name;
}

/** What kind of object it is, and whether the change makes, replaces or removes it. */
export function objectKind(object: ConfigChangeObject): string {
  const created = object.baseHash === '';
  const removed = object.content === null && object.kind !== 'config';
  switch (object.kind) {
    case 'memory':
      return removed
        ? t('Project memory, removed')
        : created
          ? t('New project memory')
          : t('Project memory');
    case 'skill':
      return removed ? t('Skill, removed') : created ? t('New skill') : t('Skill');
    case 'command':
      return removed ? t('Command, removed') : created ? t('New command') : t('Command');
    default:
      return t('Setting');
  }
}

/** The tag of when one object takes effect. */
export function takesEffectTag(when: ConfigTakesEffect): string {
  if (when === 'now') return t('Now');
  if (when === 'next-turn') return t('Next turn');
  return t('Next start');
}

/** When the change as a whole takes effect, in a line. */
export function takesEffectLine(objects: readonly ConfigChangeObject[]): string {
  const when = new Set(objects.map((o) => o.takesEffect));
  if (when.size === 1) {
    if (when.has('now')) return t('Takes effect now');
    if (when.has('next-turn')) return t('Takes effect from the next turn');
    return t('Takes effect when the session next starts');
  }
  if (!when.has('now'))
    return t('Settings from the next turn, the rest when the session next starts');
  return t('Some now, the rest when the session next starts');
}

/** When the applied change as a whole took effect, in a line. */
export function tookEffectLine(objects: readonly ConfigChangeObject[]): string {
  const when = new Set(objects.map((o) => o.takesEffect));
  if (when.size === 1) {
    if (when.has('now')) return t('Already in effect');
    if (when.has('next-turn')) return t('In effect from the next turn');
    return t('In effect when the session next starts');
  }
  if (!when.has('now'))
    return t('Settings from the next turn, the rest when the session next starts');
  return t('Some now, the rest when the session next starts');
}
