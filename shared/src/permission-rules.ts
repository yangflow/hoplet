import * as v from 'valibot';
import defaults from './permission-rule-defaults.json' with { type: 'json' };

/**
 * Permission rules (ADR 0029, docs/spec.md#permission-rules). `PermissionRuleSchema` is the JSON the agent
 * reads (`agent/internal/rules`, `Rule`); a field the agent does not know makes it refuse the whole set, so the
 * schemas here refuse unknown fields too. Where a rule applies (a machine and a project path, a session) is the
 * core's business and is not part of this shape.
 */

export const MAX_PERMISSION_RULES = 500;
export const MAX_PERMISSION_NOTE_CHARS = 200;
const MAX_PATTERN_CHARS = 500;
const MAX_LIST = 50;

export const PERMISSION_ACTIONS = ['allow', 'ask', 'deny'] as const;
export const PermissionActionSchema = v.picklist(PERMISSION_ACTIONS);
export type PermissionAction = v.InferOutput<typeof PermissionActionSchema>;

/** `global` applies to every session, `project` to one project path on one machine, `session` to one session. */
export const PERMISSION_SCOPES = ['global', 'project', 'session'] as const;
export const PermissionScopeSchema = v.picklist(PERMISSION_SCOPES);
export type PermissionScope = v.InferOutput<typeof PermissionScopeSchema>;

const Pattern = v.pipe(v.string(), v.minLength(1), v.maxLength(MAX_PATTERN_CHARS));
const PatternList = v.pipe(v.array(Pattern), v.maxLength(MAX_LIST));

/** The fields of one matcher; which are allowed depends on the tool (`permissionRuleProblem`). */
const MatchFields = {
  command: v.optional(v.pipe(v.string(), v.maxLength(MAX_PATTERN_CHARS))),
  anyArg: v.optional(PatternList),
  pipedTo: v.optional(PatternList),
  paths: v.optional(PatternList),
  except: v.optional(PatternList),
  domains: v.optional(PatternList),
};
const MatchLeafSchema = v.strictObject(MatchFields);

/** A matcher. `or` lists alternatives, one level deep; the rule matches when its own fields or any alternative match. */
export const PermissionMatchSchema = v.strictObject({
  ...MatchFields,
  or: v.optional(v.pipe(v.array(MatchLeafSchema), v.maxLength(MAX_LIST))),
});
export type PermissionMatch = v.InferOutput<typeof PermissionMatchSchema>;

/** The tool of a rule: `run`, `read`, `write`, `fetch`, `mcp:<server>/<tool>`, a comma list of those, or `*`. */
export const PermissionToolSchema = v.pipe(v.string(), v.minLength(1), v.maxLength(200));

export const PermissionNoteSchema = v.pipe(v.string(), v.maxLength(MAX_PERMISSION_NOTE_CHARS));

/** The rule as the agent reads it. */
export const PermissionRuleSchema = v.strictObject({
  id: v.pipe(v.string(), v.minLength(1), v.maxLength(80)),
  tool: PermissionToolSchema,
  match: PermissionMatchSchema,
  action: PermissionActionSchema,
  scope: PermissionScopeSchema,
  note: PermissionNoteSchema,
  enabled: v.boolean(),
  builtin: v.boolean(),
});
export type PermissionRule = v.InferOutput<typeof PermissionRuleSchema>;

/** The most deny rules of its own that an agent announces in `device.hello`. */
export const MAX_LOCAL_PERMISSION_RULES = 100;

/**
 * A rule from the agent's `permission_deny` as the agent announces it (ADR 0029): deny only, scope `agent`, always
 * enabled, never a built-in. The core shows it and cannot change it.
 */
export const LocalPermissionRuleSchema = v.strictObject({
  ...PermissionRuleSchema.entries,
  action: v.literal('deny'),
  scope: v.literal('agent'),
  enabled: v.literal(true),
  builtin: v.literal(false),
});
export type LocalPermissionRule = v.InferOutput<typeof LocalPermissionRuleSchema>;

export const LocalPermissionRuleListSchema = v.pipe(
  v.array(LocalPermissionRuleSchema),
  v.maxLength(MAX_LOCAL_PERMISSION_RULES),
);

/** The eight built-in rules of ADR 0029, all enabled; `shared/src/permission-rule-defaults.json` is checked against the agent's table. */
export const BUILTIN_PERMISSION_RULES: readonly PermissionRule[] = v.parse(
  v.array(PermissionRuleSchema),
  defaults,
);

type Family = 'none' | 'run' | 'path' | 'fetch';

function toolFamily(tool: string): Family | string {
  let family: Family = 'none';
  const parts = tool.split(',').map((p) => p.trim());
  for (const part of parts) {
    let next: Family = 'none';
    if (part === '*') {
      if (parts.length > 1) return 'tool * cannot be combined';
    } else if (part === 'run') next = 'run';
    else if (part === 'read' || part === 'write') next = 'path';
    else if (part === 'fetch') next = 'fetch';
    else if (part.startsWith('mcp:')) {
      const [server, name, ...rest] = part.slice(4).split('/');
      if (!server || !name || rest.length > 0) return `tool ${part}: want mcp:<server>/<tool>`;
    } else return `unknown tool ${part}`;
    if (next === 'none') continue;
    if (family !== 'none' && family !== next)
      return 'a rule cannot mix tools with different matchers';
    family = next;
  }
  return family;
}

function validPattern(p: string): boolean {
  return (
    p.startsWith('/') ||
    p === '~' ||
    p.startsWith('~/') ||
    p.startsWith('**') ||
    p.startsWith('{workspace}') ||
    p.startsWith('{hoplet_home}') ||
    p.startsWith('{tmp}')
  );
}

function validDomain(d: string): boolean {
  const stars = d.split('*').length - 1;
  if (stars > 1 || (stars === 1 && !d.startsWith('*.'))) return false;
  const host = d.startsWith('*.') ? d.slice(2) : d;
  if (host === '' || host.startsWith('.') || host.endsWith('.')) return false;
  return /^[a-z0-9._:-]+$/.test(host);
}

type MatchFieldsValue = v.InferOutput<typeof MatchLeafSchema>;

function matchProblem(m: MatchFieldsValue, family: Family): string | null {
  const run = !!m.command || !!m.anyArg?.length || !!m.pipedTo?.length;
  const path = !!m.paths?.length || !!m.except?.length;
  const dom = !!m.domains?.length;
  if ((run && family !== 'run') || (path && family !== 'path') || (dom && family !== 'fetch')) {
    return 'matcher does not fit the tool';
  }
  if ((m.anyArg?.length || m.pipedTo?.length) && !m.command?.trim()) {
    return 'anyArg and pipedTo need a command';
  }
  if (m.except?.length && !m.paths?.length) return 'except needs paths';
  for (const p of [...(m.paths ?? []), ...(m.except ?? [])]) {
    if (!validPattern(p)) return `path ${p} is not absolute`;
  }
  for (const d of m.domains ?? []) if (!validDomain(d)) return `domain ${d}`;
  return null;
}

/**
 * What the schema cannot say, the agent's own checks (`Rule.Validate`): a known tool, a matcher that fits it,
 * absolute path patterns and well-formed domains. Null when the rule is acceptable.
 */
export function permissionRuleProblem(rule: Pick<PermissionRule, 'tool' | 'match'>): string | null {
  const family = toolFamily(rule.tool);
  if (family !== 'none' && family !== 'run' && family !== 'path' && family !== 'fetch')
    return family;
  const { or, ...own } = rule.match;
  const problem = matchProblem(own, family);
  if (problem) return problem;
  for (const alt of or ?? []) {
    const p = matchProblem(alt, family);
    if (p) return p;
  }
  return null;
}
