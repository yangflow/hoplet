// The copy of a project's permission rules to the target of a handoff (ADR 0037): which source rules the target's
// clone lacks, with their paths moved onto it. Pure: the store reads and writes are the caller's.
import type {
  HandoffRule,
  PermissionMatch,
  PermissionRuleBody,
  PermissionRuleEntry,
} from '@hoplet/shared';

/** Where the rules come from: the source machine and the project paths whose rules travel, longest first. */
export interface RuleCopySource {
  deviceId: string;
  roots: readonly string[];
}

/** Where the rules go: the target machine and its clone. */
export interface RuleCopyTarget {
  deviceId: string;
  path: string;
}

export interface RuleCopyPlan {
  /** Every rule as the preview shows it: the copies and the ones already there first, then the target's own. */
  rules: HandoffRule[];
  /** The rules to create on the target. */
  copies: PermissionRuleBody[];
}

/** What the source of a copy is read from. */
interface SourceStore {
  getSession(sessionId: string): { projectPath: string } | null;
  sessionWorktree(sessionId: string): { deviceId: string; clone: string } | null;
}

/**
 * The project a session's rules come from on its owner: the clone its worktree belongs to when Hoplet made one,
 * else its project path. `roots` adds the worktree path itself. Null when the session has no project.
 */
export function ruleSourceOf(
  store: SourceStore,
  sessionId: string,
  ownerDeviceId: string,
): (RuleCopySource & { path: string }) | null {
  const projectPath = store.getSession(sessionId)?.projectPath ?? '';
  const worktree = store.sessionWorktree(sessionId);
  const clone = worktree?.deviceId === ownerDeviceId ? worktree.clone : '';
  const path = clone || projectPath;
  if (!path) return null;
  const roots = [path, ...(projectPath && projectPath !== path ? [projectPath] : [])];
  return { deviceId: ownerDeviceId, roots, path };
}

function trimSeparator(p: string): string {
  return p.length > 1 && (p.endsWith('/') || p.endsWith('\\')) ? p.slice(0, -1) : p;
}

function under(pattern: string, root: string): boolean {
  return pattern === root || pattern.startsWith(`${root}/`) || pattern.startsWith(`${root}\\`);
}

/** A pattern that is a source root or lies below it, moved onto the target; every other pattern as it is. */
function movePattern(pattern: string, roots: readonly string[], target: string): string {
  // A rule pattern is absolute only with `/`; a target that is not cannot take a moved one.
  if (!target.startsWith('/')) return pattern;
  for (const root of roots) {
    if (under(pattern, root)) return target + pattern.slice(root.length);
  }
  return pattern;
}

function moveList(
  list: readonly string[] | undefined,
  roots: readonly string[],
  target: string,
): string[] | undefined {
  return list?.map((p) => movePattern(p, roots, target));
}

function moveLeaf<T extends Omit<PermissionMatch, 'or'>>(
  leaf: T,
  roots: readonly string[],
  target: string,
): T {
  const moved = { ...leaf };
  const paths = moveList(leaf.paths, roots, target);
  const except = moveList(leaf.except, roots, target);
  if (paths) moved.paths = paths;
  if (except) moved.except = except;
  return moved;
}

/** The match with its `paths` and `except` (also in its `or` leaves) moved from the source roots onto the target. */
export function moveMatch(
  match: PermissionMatch,
  roots: readonly string[],
  target: string,
): PermissionMatch {
  const sorted = [...roots].map(trimSeparator).sort((a, b) => b.length - a.length);
  const base = trimSeparator(target);
  const moved = moveLeaf(match, sorted, base);
  if (match.or) moved.or = match.or.map((leaf) => moveLeaf(leaf, sorted, base));
  return moved;
}

/** The JSON of a value with its object keys sorted, so two equal matches give the same text. */
function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value !== null && typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, v]) => v !== undefined)
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
    return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${canonical(v)}`).join(',')}}`;
  }
  return JSON.stringify(value);
}

/** Two rules are the same when their tool, action and match are. */
function sameKey(rule: { tool: string; action: string; match: PermissionMatch }): string {
  return canonical([rule.tool, rule.action, rule.match]);
}

/**
 * Plans the copy: the source machine's enabled project rules under one of the source roots, moved onto the
 * target's clone, against the target's rules of exactly that clone path. A rule the target has is not copied; the
 * target's own rules stay.
 */
export function planRuleCopy(
  entries: readonly PermissionRuleEntry[],
  source: RuleCopySource,
  target: RuleCopyTarget,
): RuleCopyPlan {
  const roots = new Set(source.roots.map(trimSeparator));
  const targetPath = trimSeparator(target.path);
  const fromSource = entries.filter(
    (e) =>
      e.scope === 'project' &&
      e.enabled &&
      e.deviceId === source.deviceId &&
      e.projectPath !== undefined &&
      roots.has(trimSeparator(e.projectPath)),
  );
  const onTarget = entries.filter(
    (e) =>
      e.scope === 'project' &&
      e.deviceId === target.deviceId &&
      e.projectPath !== undefined &&
      trimSeparator(e.projectPath) === targetPath,
  );
  const targetKeys = new Set(onTarget.map(sameKey));
  const sourceKeys = new Set<string>();
  const rules: HandoffRule[] = [];
  const copies: PermissionRuleBody[] = [];
  for (const entry of fromSource) {
    const match = moveMatch(entry.match, [...roots], targetPath);
    const key = sameKey({ tool: entry.tool, action: entry.action, match });
    // The same rule under the clone and under its worktree travels once.
    if (sourceKeys.has(key)) continue;
    sourceKeys.add(key);
    const there = targetKeys.has(key);
    rules.push({
      state: there ? 'same' : 'copy',
      action: entry.action,
      tool: entry.tool,
      match,
      note: entry.note,
    });
    if (!there) {
      copies.push({
        scope: 'project',
        deviceId: target.deviceId,
        projectPath: target.path,
        tool: entry.tool,
        match,
        action: entry.action,
        note: entry.note,
        enabled: true,
      });
    }
  }
  for (const entry of onTarget) {
    if (sourceKeys.has(sameKey(entry))) continue;
    rules.push({
      state: 'target',
      action: entry.action,
      tool: entry.tool,
      match: entry.match,
      note: entry.note,
    });
  }
  return { rules, copies };
}
