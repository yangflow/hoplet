import type {
  PermissionRuleBody,
  PermissionRuleEntry,
  PermissionRuleListResponse,
} from '@hoplet/shared';
import { useCallback, useEffect, useState } from 'react';
import {
  deletePermissionRule,
  fetchPermissionRules,
  postPermissionRule,
  putPermissionRule,
} from './api.ts';
import { errorText } from './errors.ts';
import type { Loaded } from './use-usage.ts';

export type PermissionRulesState = Loaded<PermissionRuleListResponse> & {
  /** Saves a new rule (`id` null) or changes one; resolves false and sets `problem` when the core refused. */
  save: (id: string | null, body: PermissionRuleBody) => Promise<boolean>;
  remove: (id: string) => Promise<boolean>;
  /** Switches a rule on or off. Turning a built-in off sends `confirm` equal to its ID. */
  toggle: (rule: PermissionRuleEntry, enabled: boolean) => Promise<boolean>;
  saving: boolean;
  /** The core's reason for the last refused request, or null. */
  problem: string | null;
  clearProblem: () => void;
};

function entryOf(id: string, body: PermissionRuleBody, at: number): PermissionRuleEntry {
  return {
    id,
    tool: body.tool,
    match: body.match,
    action: body.action,
    scope: body.scope,
    note: body.note,
    enabled: body.enabled,
    builtin: false,
    ...(body.deviceId ? { deviceId: body.deviceId } : {}),
    ...(body.projectPath ? { projectPath: body.projectPath } : {}),
    ...(body.sessionId ? { sessionId: body.sessionId } : {}),
    createdAt: at,
    updatedAt: at,
  };
}

/**
 * The permission rules of the host, and their changes. A `fixture` is the answer itself and the changes stay
 * local.
 */
export function usePermissionRules(fixture?: PermissionRuleListResponse): PermissionRulesState {
  const [data, setData] = useState<PermissionRuleListResponse | null>(fixture ?? null);
  const [loading, setLoading] = useState(fixture === undefined);
  const [error, setError] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [again, setAgain] = useState(0);
  const locked = fixture !== undefined;

  // biome-ignore lint/correctness/useExhaustiveDependencies: `again` is the reload after a change
  useEffect(() => {
    if (locked) return;
    const abort = new AbortController();
    fetchPermissionRules({}, abort.signal)
      .then((r) => {
        setData(r);
        setError(null);
        setLoading(false);
      })
      .catch((e) => {
        if (abort.signal.aborted) return;
        setError(errorText(e));
        setLoading(false);
      });
    return () => abort.abort();
  }, [locked, again]);

  const run = useCallback(
    async (local: () => void, remote: () => Promise<unknown>): Promise<boolean> => {
      setProblem(null);
      if (locked) {
        local();
        return true;
      }
      setSaving(true);
      try {
        await remote();
        setAgain((n) => n + 1);
        return true;
      } catch (e) {
        setProblem(errorText(e));
        return false;
      } finally {
        setSaving(false);
      }
    },
    [locked],
  );

  const change = useCallback(
    (fn: (rules: PermissionRuleEntry[]) => PermissionRuleEntry[]) =>
      setData((d) => (d ? { ...d, rules: fn(d.rules) } : d)),
    [],
  );

  const save = useCallback(
    (id: string | null, body: PermissionRuleBody) =>
      run(
        () => {
          const at = Date.now();
          change((rules) =>
            id
              ? rules.map((r) =>
                  r.id === id ? { ...entryOf(id, body, at), createdAt: r.createdAt } : r,
                )
              : [...rules, entryOf(`rule-${at.toString(36)}-${rules.length}`, body, at)],
          );
        },
        () => (id === null ? postPermissionRule(body) : putPermissionRule(id, body)),
      ),
    [run, change],
  );

  const remove = useCallback(
    (id: string) =>
      run(
        () => change((rules) => rules.filter((r) => r.id !== id)),
        () => deletePermissionRule(id),
      ),
    [run, change],
  );

  const toggle = useCallback(
    (rule: PermissionRuleEntry, enabled: boolean) =>
      run(
        () => change((rules) => rules.map((r) => (r.id === rule.id ? { ...r, enabled } : r))),
        () => {
          if (rule.builtin) {
            return putPermissionRule(rule.id, {
              enabled,
              ...(enabled ? {} : { confirm: rule.id }),
            });
          }
          return putPermissionRule(rule.id, {
            scope: rule.scope,
            ...(rule.deviceId ? { deviceId: rule.deviceId } : {}),
            ...(rule.projectPath ? { projectPath: rule.projectPath } : {}),
            ...(rule.sessionId ? { sessionId: rule.sessionId } : {}),
            tool: rule.tool,
            match: rule.match,
            action: rule.action,
            note: rule.note,
            enabled,
          });
        },
      ),
    [run, change],
  );

  return {
    data,
    loading,
    error,
    save,
    remove,
    toggle,
    saving,
    problem,
    clearProblem: useCallback(() => setProblem(null), []),
  };
}
