/**
 * The logic behind the permission rule screens (#98, ADR 0029): grouping the list by scope, the rule editor's
 * form with its validation, and drafting a rule from a permission request. No React and no network.
 */
import {
  type LocalPermissionRule,
  MAX_PERMISSION_NOTE_CHARS,
  type PermissionAction,
  type PermissionMatch,
  type PermissionRuleBody,
  type PermissionRuleEntry,
  type PermissionRuleListResponse,
  permissionRuleProblem,
  type ToolKind,
  type ToolMcp,
} from '@hoplet/shared';

/** The kinds of tool the editor offers. */
export const RULE_KINDS = ['run', 'read', 'write', 'fetch', 'mcp', 'any'] as const;
export type RuleKind = (typeof RULE_KINDS)[number];

/** A rule applied to a call: what the conversation line says and links to. */
export type AppliedRule = {
  decision: PermissionAction;
  ruleId: string;
  /** The rule's note, or the line that names its match. */
  name: string;
  scope: 'builtin' | 'global' | 'project' | 'session';
};

// -- Grouping -------------------------------------------------------------------------------------------------

export type ProjectGroup = {
  key: string;
  deviceId: string;
  projectPath: string;
  rules: PermissionRuleEntry[];
};

export type RuleGroups = {
  builtin: PermissionRuleEntry[];
  global: PermissionRuleEntry[];
  projects: ProjectGroup[];
  sessions: PermissionRuleEntry[];
  local: { deviceId: string; rules: LocalPermissionRule[] }[];
};

/** The list split by where a rule applies; projects keep the order in which their first rule was made. */
export function groupRules(response: PermissionRuleListResponse): RuleGroups {
  const out: RuleGroups = { builtin: [], global: [], projects: [], sessions: [], local: [] };
  for (const rule of response.rules) {
    if (rule.builtin) out.builtin.push(rule);
    else if (rule.scope === 'global') out.global.push(rule);
    else if (rule.scope === 'session') out.sessions.push(rule);
    else if (rule.scope === 'project' && rule.deviceId && rule.projectPath) {
      const key = `${rule.deviceId}\u0000${rule.projectPath}`;
      let group = out.projects.find((g) => g.key === key);
      if (!group) {
        group = { key, deviceId: rule.deviceId, projectPath: rule.projectPath, rules: [] };
        out.projects.push(group);
      }
      group.rules.push(rule);
    }
  }
  out.local = (response.local ?? []).filter((l) => l.rules.length > 0);
  return out;
}

/** How many built-in rules are on. */
export function builtinOn(rules: readonly PermissionRuleEntry[]): number {
  return rules.filter((r) => r.enabled).length;
}

// -- Reading a rule -------------------------------------------------------------------------------------------

export type MatchPart =
  | { type: 'text'; text: string; mono?: boolean }
  | { type: 'word'; text: string }
  | { type: 'chip'; text: string; except?: boolean };

/** The kind of tool a rule names, or `other` when the editor cannot show it (a list of tools). */
export function kindOfTool(tool: string): RuleKind | 'other' {
  if (tool === '*') return 'any';
  if (tool === 'run' || tool === 'read' || tool === 'write' || tool === 'fetch') return tool;
  if (tool.startsWith('mcp:') && !tool.includes(',')) return 'mcp';
  return 'other';
}

/** The tools a rule names, for the icons of a row. */
export function toolsOf(tool: string): RuleKind[] {
  if (tool === '*') return ['any'];
  const out: RuleKind[] = [];
  for (const part of tool.split(',').map((p) => p.trim())) {
    const kind = part.startsWith('mcp:') ? 'mcp' : part;
    if ((RULE_KINDS as readonly string[]).includes(kind) && !out.includes(kind as RuleKind)) {
      out.push(kind as RuleKind);
    }
  }
  return out;
}

/** `server › tool` of an `mcp:` tool. */
export function mcpLabel(tool: string): string {
  const [server = '', name = ''] = tool.slice(4).split('/');
  return `${server} › ${name}`;
}

/** What a row shows of a rule's matcher, in order. */
export function matchParts(rule: { tool: string; match: PermissionMatch }): MatchPart[] {
  const { match } = rule;
  const parts: MatchPart[] = [];
  const kind = kindOfTool(rule.tool);
  if (kind === 'any') return [{ type: 'text', text: '*', mono: true }];
  if (kind === 'mcp') return [{ type: 'text', text: mcpLabel(rule.tool), mono: true }];
  const leaf = (m: PermissionMatch) => {
    if (m.command) parts.push({ type: 'text', text: m.command, mono: true });
    if (m.anyArg?.length) {
      parts.push({ type: 'word', text: 'with' });
      for (const a of m.anyArg) parts.push({ type: 'chip', text: a });
    }
    if (m.pipedTo?.length) {
      parts.push({ type: 'word', text: 'piped to' });
      for (const a of m.pipedTo) parts.push({ type: 'chip', text: a });
    }
    for (const p of m.paths ?? []) parts.push({ type: 'chip', text: p });
    for (const p of m.except ?? []) parts.push({ type: 'chip', text: p, except: true });
    for (const d of m.domains ?? []) parts.push({ type: 'chip', text: d });
  };
  leaf(match);
  for (const alt of match.or ?? []) {
    if (parts.length > 0) parts.push({ type: 'word', text: 'or' });
    leaf(alt);
  }
  return parts;
}

/** The name of a rule: its note, or its match written out. */
export function ruleName(rule: { tool: string; match: PermissionMatch; note: string }): string {
  if (rule.note.trim()) return rule.note.trim();
  return matchParts(rule)
    .filter((p) => p.type !== 'word' || p.text)
    .map((p) => p.text)
    .join(' ');
}

// -- The editor's form ----------------------------------------------------------------------------------------

export type RuleForm = {
  /** The rule being edited, or null for a new one. */
  id: string | null;
  kind: RuleKind;
  command: string;
  anyArg: string[];
  pipedTo: string[];
  paths: string[];
  domains: string[];
  server: string;
  tool: string;
  action: PermissionAction;
  scope: 'global' | 'project';
  deviceId: string;
  projectPath: string;
  note: string;
  enabled: boolean;
};

export function emptyForm(): RuleForm {
  return {
    id: null,
    kind: 'run',
    command: '',
    anyArg: [],
    pipedTo: [],
    paths: [],
    domains: [],
    server: '',
    tool: '',
    action: 'allow',
    scope: 'global',
    deviceId: '',
    projectPath: '',
    note: '',
    enabled: true,
  };
}

/**
 * The form of an existing rule, or null when the editor cannot express it: alternatives (`or`), exceptions, a list of
 * tools, or a session rule. Those rows can be switched and deleted, not edited.
 */
export function formOfRule(rule: PermissionRuleEntry): RuleForm | null {
  if (rule.builtin || rule.scope === 'session') return null;
  const kind = kindOfTool(rule.tool);
  if (kind === 'other') return null;
  if (rule.match.or?.length || rule.match.except?.length) return null;
  const form = emptyForm();
  form.id = rule.id;
  form.kind = kind;
  form.action = rule.action;
  form.note = rule.note;
  form.enabled = rule.enabled;
  form.command = rule.match.command ?? '';
  form.anyArg = [...(rule.match.anyArg ?? [])];
  form.pipedTo = [...(rule.match.pipedTo ?? [])];
  form.paths = [...(rule.match.paths ?? [])];
  form.domains = [...(rule.match.domains ?? [])];
  if (kind === 'mcp') {
    const [server = '', name = ''] = rule.tool.slice(4).split('/');
    form.server = server;
    form.tool = name === '*' ? '' : name;
  }
  if (rule.scope === 'project') {
    form.scope = 'project';
    form.deviceId = rule.deviceId ?? '';
    form.projectPath = rule.projectPath ?? '';
  }
  return form;
}

export type FormField = 'command' | 'paths' | 'domains' | 'server' | 'note' | 'project';

export type FormCheck = {
  /** The reason a field is wrong, by field. */
  errors: Partial<Record<FormField, string>>;
  /** Every part a rule of this kind needs is filled in. */
  complete: boolean;
  /** The first path pattern that is not absolute, for the hint under the field. */
  badPath: string | null;
  /** The first domain that is not well formed. */
  badDomain: string | null;
  /** The editor can save: complete and nothing wrong. */
  ok: boolean;
};

const trimmed = (xs: readonly string[]) => xs.map((x) => x.trim()).filter((x) => x !== '');

/** The tool string of a form. */
export function toolOfForm(form: RuleForm): string {
  switch (form.kind) {
    case 'mcp':
      return `mcp:${form.server.trim()}/${form.tool.trim() || '*'}`;
    case 'any':
      return '*';
    default:
      return form.kind;
  }
}

/** The matcher of a form: only the fields its kind has. */
export function matchOfForm(form: RuleForm): PermissionMatch {
  const match: PermissionMatch = {};
  if (form.kind === 'run') {
    const command = form.command.trim();
    if (command) match.command = command;
    const anyArg = trimmed(form.anyArg);
    const pipedTo = trimmed(form.pipedTo);
    if (command && anyArg.length) match.anyArg = anyArg;
    if (command && pipedTo.length) match.pipedTo = pipedTo;
  } else if (form.kind === 'read' || form.kind === 'write') {
    const paths = trimmed(form.paths);
    if (paths.length) match.paths = paths;
  } else if (form.kind === 'fetch') {
    const domains = trimmed(form.domains);
    if (domains.length) match.domains = domains;
  }
  return match;
}

export function checkForm(form: RuleForm): FormCheck {
  const errors: FormCheck['errors'] = {};
  let badPath: string | null = null;
  let badDomain: string | null = null;
  let complete = true;
  if (form.kind === 'run' && form.command.trim() === '') complete = false;
  if ((form.kind === 'read' || form.kind === 'write') && trimmed(form.paths).length === 0) {
    complete = false;
  }
  if (form.kind === 'fetch' && trimmed(form.domains).length === 0) complete = false;
  if (form.kind === 'mcp') {
    const server = form.server.trim();
    if (server === '') complete = false;
    else if (server.includes('/') || form.tool.includes('/')) {
      errors.server = 'server';
    }
  }
  if (form.kind === 'read' || form.kind === 'write') {
    for (const p of trimmed(form.paths)) {
      const problem = permissionRuleProblem({ tool: form.kind, match: { paths: [p] } });
      if (problem) {
        badPath = p;
        errors.paths = problem;
        break;
      }
    }
  }
  if (form.kind === 'fetch') {
    for (const d of trimmed(form.domains)) {
      const problem = permissionRuleProblem({ tool: 'fetch', match: { domains: [d] } });
      if (problem) {
        badDomain = d;
        errors.domains = problem;
        break;
      }
    }
  }
  if (form.note.length > MAX_PERMISSION_NOTE_CHARS) errors.note = 'note';
  if (form.scope === 'project' && (form.deviceId === '' || form.projectPath === '')) {
    complete = false;
  }
  if (Object.keys(errors).length === 0 && complete) {
    // The last word is the shared check: a form that passes everything above must pass it too.
    const problem = permissionRuleProblem({ tool: toolOfForm(form), match: matchOfForm(form) });
    if (problem) {
      complete = false;
    }
  }
  const ok = complete && Object.keys(errors).length === 0;
  return { errors, complete, badPath, badDomain, ok };
}

/** The request body for a form. */
export function toBody(form: RuleForm): PermissionRuleBody {
  return {
    scope: form.scope,
    ...(form.scope === 'project' ? { deviceId: form.deviceId, projectPath: form.projectPath } : {}),
    tool: toolOfForm(form),
    match: matchOfForm(form),
    action: form.action,
    note: form.note.trim(),
    enabled: form.enabled,
  };
}

/** The parts of the sentence the editor shows under the form; the screen turns them into words. */
export type Summary = {
  action: PermissionAction;
  kind: RuleKind;
  /** The command, the paths, the domains or `server › tool`. */
  what: string;
};

export function summaryOf(form: RuleForm): Summary {
  let what = '';
  if (form.kind === 'run') what = form.command.trim();
  else if (form.kind === 'read' || form.kind === 'write') what = trimmed(form.paths).join(', ');
  else if (form.kind === 'fetch') what = trimmed(form.domains).join(', ');
  else if (form.kind === 'mcp') what = `${form.server.trim()} › ${form.tool.trim() || '*'}`;
  return { action: form.action, kind: form.kind, what };
}

// -- Drafting a rule from a request ---------------------------------------------------------------------------

export type DraftOption = {
  /** Stable key: `two`, `one`, `file`, `dir`, `host`. */
  id: string;
  /** What the option matches: the words, the path or the host. */
  value: string;
  /** The match the saved rule gets. */
  match: PermissionMatch;
};

export type Draft =
  | {
      ok: true;
      kind: 'run' | 'read' | 'write' | 'fetch' | 'mcp';
      /** The rule's tool. */
      tool: string;
      /** The request as the card shows it. */
      request: string;
      options: DraftOption[];
      /** The host of a fetch can be edited. */
      editable: boolean;
    }
  | { ok: false; reason: 'control' | 'tool' };

/** A shell construct no rule can allow (ADR 0029): pipe, list, background, redirection, substitution, subshell, newline. */
export function hasShellControl(command: string): boolean {
  return /[|&;<>`\n\r]|\$\(|\(/.test(command);
}

function pathOfInput(input: unknown): string | undefined {
  if (!input || typeof input !== 'object') return undefined;
  const rec = input as Record<string, unknown>;
  for (const k of ['file_path', 'path', 'target_file', 'notebook_path']) {
    const v = rec[k];
    if (typeof v === 'string' && v !== '') return v;
  }
  return undefined;
}

function stringOf(input: unknown, key: string): string | undefined {
  if (!input || typeof input !== 'object') return undefined;
  const v = (input as Record<string, unknown>)[key];
  return typeof v === 'string' && v !== '' ? v : undefined;
}

/** A path inside the project as `{workspace}/…`, any other path as it is. */
export function workspacePath(path: string, projectPath: string | undefined): string {
  if (projectPath) {
    const root = projectPath.replace(/\/+$/, '');
    if (path === root) return '{workspace}';
    if (path.startsWith(`${root}/`)) return `{workspace}${path.slice(root.length)}`;
  }
  return path;
}

/** The first `count` words of a command, as the rule's `command`. */
function firstWords(command: string, count: number): string {
  return command.trim().split(/\s+/).slice(0, count).join(' ');
}

/**
 * The rule a permission request can be drafted into: what the "Always allow…" buttons offer, or why they are not
 * offered. `toolKind` is the call's kind; `projectPath` turns a path inside the project into `{workspace}/…`.
 */
export function draftFromCall(call: {
  toolKind: ToolKind;
  mcp?: ToolMcp | undefined;
  input: unknown;
  projectPath?: string | undefined;
}): Draft {
  const { toolKind, input } = call;
  if (toolKind === 'run') {
    const command = stringOf(input, 'command');
    if (!command) return { ok: false, reason: 'tool' };
    if (hasShellControl(command)) return { ok: false, reason: 'control' };
    const words = command.trim().split(/\s+/);
    const options: DraftOption[] = [];
    if (words.length >= 2) {
      const two = firstWords(command, 2);
      options.push({ id: 'two', value: two, match: { command: two } });
    }
    const one = firstWords(command, 1);
    options.push({ id: 'one', value: one, match: { command: one } });
    return {
      ok: true,
      kind: 'run',
      tool: 'run',
      request: command.trim(),
      options,
      editable: false,
    };
  }
  if (toolKind === 'read' || toolKind === 'search' || toolKind === 'list' || toolKind === 'view') {
    return pathDraft('read', input, call.projectPath);
  }
  if (toolKind === 'edit' || toolKind === 'write') {
    return pathDraft('write', input, call.projectPath);
  }
  if (toolKind === 'fetch') {
    const raw = stringOf(input, 'url');
    if (!raw) return { ok: false, reason: 'tool' };
    let host: string;
    try {
      host = new URL(raw).hostname.toLowerCase();
    } catch {
      return { ok: false, reason: 'tool' };
    }
    if (host === '') return { ok: false, reason: 'tool' };
    return {
      ok: true,
      kind: 'fetch',
      tool: 'fetch',
      request: raw,
      options: [{ id: 'host', value: host, match: { domains: [host] } }],
      editable: true,
    };
  }
  if (toolKind === 'call' && call.mcp?.server && call.mcp.tool) {
    const tool = `mcp:${call.mcp.server}/${call.mcp.tool}`;
    return {
      ok: true,
      kind: 'mcp',
      tool,
      request: mcpLabel(tool),
      options: [{ id: 'tool', value: mcpLabel(tool), match: {} }],
      editable: false,
    };
  }
  return { ok: false, reason: 'tool' };
}

function pathDraft(kind: 'read' | 'write', input: unknown, projectPath: string | undefined): Draft {
  const path = pathOfInput(input) ?? stringOf(input, 'pattern');
  if (!path || !(path.startsWith('/') || path.startsWith('~')))
    return { ok: false, reason: 'tool' };
  const file = workspacePath(path, projectPath);
  const options: DraftOption[] = [{ id: 'file', value: file, match: { paths: [file] } }];
  const slash = file.lastIndexOf('/');
  if (slash > 0) {
    const dir = `${file.slice(0, slash)}/**`;
    options.push({ id: 'dir', value: dir, match: { paths: [dir] } });
  }
  return { ok: true, kind, tool: kind, request: path, options, editable: false };
}

/** The body that saves a draft option: a session rule of `sessionId`, or a project rule of the session's project. */
export function bodyOfDraft(
  draft: Extract<Draft, { ok: true }>,
  option: DraftOption,
  action: 'allow' | 'deny',
  where:
    | { scope: 'session'; sessionId: string }
    | { scope: 'project'; deviceId: string; projectPath: string },
  note: string,
): PermissionRuleBody {
  return {
    ...where,
    tool: draft.tool,
    match: option.match,
    action,
    note: note.trim(),
    enabled: true,
  };
}
