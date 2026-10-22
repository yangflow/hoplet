/**
 * Which permission rule decided each tool call of a session (#98, ADR 0029). The core relays `session.rule`
 * with the rule's ID and scope only; the name comes from the rules this window can list: the built-in table
 * needs no request, the user's own and the machines' deny rules are fetched once one is needed.
 */
import {
  BUILTIN_PERMISSION_RULES,
  type PermissionAction,
  type PermissionRuleListResponse,
  type SessionRule,
} from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchPermissionRules } from './api.ts';
import { onLive } from './live.ts';
import { type AppliedRule, ruleName } from './permission-rules.ts';

/** A decision as the core relayed it. */
export type RuleDecision = {
  decision: PermissionAction;
  ruleId: string;
  scope: SessionRule['scope'];
};

/** The rule that decided, named; a rule that is gone since keeps its line under a general name. */
export function appliedRule(
  line: RuleDecision,
  list: PermissionRuleListResponse | null,
): AppliedRule {
  const builtin = BUILTIN_PERMISSION_RULES.find((r) => r.id === line.ruleId);
  if (builtin) {
    return {
      decision: line.decision,
      ruleId: line.ruleId,
      name: ruleName(builtin),
      scope: 'builtin',
    };
  }
  const entry = list?.rules.find((r) => r.id === line.ruleId);
  if (entry) {
    return {
      decision: line.decision,
      ruleId: line.ruleId,
      name: ruleName(entry),
      scope: entry.builtin ? 'builtin' : line.scope === 'agent' ? 'agent' : entry.scope,
    };
  }
  const local = list?.local?.flatMap((l) => l.rules).find((r) => r.id === line.ruleId);
  if (local) {
    return { decision: line.decision, ruleId: line.ruleId, name: ruleName(local), scope: 'agent' };
  }
  return {
    decision: line.decision,
    ruleId: line.ruleId,
    name: '',
    scope: line.scope === 'agent' ? 'agent' : line.scope,
  };
}

/**
 * The rule decisions of one session, by the call's `toolUseId`, as the core tells them (the latest ones again when
 * this window subscribes). `fixture` is the answer itself and nothing is listened to or fetched.
 */
export function useRuleLines(
  sessionId: string,
  fixture?: {
    lines: (RuleDecision & { toolUseId: string })[];
    list?: PermissionRuleListResponse;
    /** The calls the Codex hook did not run for. */
    unenforced?: string[];
  },
) {
  const [lines, setLines] = useState<Map<string, RuleDecision>>(
    () => new Map(fixture?.lines.map((l) => [l.toolUseId, l])),
  );
  const [list, setList] = useState<PermissionRuleListResponse | null>(fixture?.list ?? null);
  const [unenforced, setUnenforced] = useState<ReadonlySet<string>>(
    () => new Set(fixture?.unenforced),
  );
  const locked = fixture !== undefined;

  useEffect(() => {
    if (locked) return;
    setLines(new Map());
    setUnenforced(new Set());
    return onLive((event) => {
      if (event.kind !== 'message') return;
      if (event.msg.type === 'session.rules.unenforced') {
        const { sessionId: id, toolUseId } = event.msg;
        if (id !== sessionId || toolUseId === undefined) return;
        setUnenforced((cur) => new Set(cur).add(toolUseId));
        return;
      }
      if (event.msg.type !== 'session.rule') return;
      const msg = event.msg;
      if (msg.sessionId !== sessionId) return;
      setLines((cur) => {
        const next = new Map(cur);
        next.set(msg.toolUseId, { decision: msg.decision, ruleId: msg.ruleId, scope: msg.scope });
        return next;
      });
    });
  }, [sessionId, locked]);

  // The list is needed once a rule that is not built in has decided and the list does not hold it yet.
  const tried = useRef(new Set<string>());
  const unresolved = [...lines.values()]
    .map((l) => l.ruleId)
    .filter((id) => !BUILTIN_PERMISSION_RULES.some((r) => r.id === id) && !tried.current.has(id));
  const wanted = unresolved.length > 0;
  // biome-ignore lint/correctness/useExhaustiveDependencies: `unresolved` is what `wanted` summarizes
  useEffect(() => {
    if (locked || !wanted) return;
    for (const id of unresolved) tried.current.add(id);
    fetchPermissionRules({})
      .then(setList)
      .catch(() => {
        // Without the list the line names the scope only.
      });
  }, [locked, wanted]);

  const ruleOf = useCallback(
    (toolUseId: string): AppliedRule | undefined => {
      const line = lines.get(toolUseId);
      return line ? appliedRule(line, list) : undefined;
    },
    [lines, list],
  );
  const unenforcedAt = useCallback((toolUseId: string) => unenforced.has(toolUseId), [unenforced]);
  return { ruleOf, unenforcedAt };
}
