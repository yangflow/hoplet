import type { PermissionRuleEntry, PermissionRuleListResponse } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  bodyOfDraft,
  builtinOn,
  checkForm,
  draftFromCall,
  emptyForm,
  formOfRule,
  groupRules,
  hasShellControl,
  matchParts,
  ruleName,
  summaryOf,
  toBody,
  workspacePath,
} from '../src/lib/permission-rules.ts';

function rule(over: Partial<PermissionRuleEntry> & { id: string }): PermissionRuleEntry {
  return {
    tool: 'run',
    match: { command: 'git status' },
    action: 'allow',
    scope: 'global',
    note: '',
    enabled: true,
    builtin: false,
    ...over,
  };
}

describe('groupRules', () => {
  it('splits the list by scope and keeps projects in the order of their first rule', () => {
    const response: PermissionRuleListResponse = {
      rules: [
        rule({ id: 'default.a', builtin: true, action: 'deny', enabled: false }),
        rule({ id: 'default.b', builtin: true, action: 'deny' }),
        rule({ id: 'g1' }),
        rule({ id: 'p1', scope: 'project', deviceId: 'd1', projectPath: '/w/b' }),
        rule({ id: 'p2', scope: 'project', deviceId: 'd1', projectPath: '/w/a' }),
        rule({ id: 'p3', scope: 'project', deviceId: 'd1', projectPath: '/w/b' }),
        rule({ id: 's1', scope: 'session', sessionId: 'sess' }),
      ],
      local: [
        { deviceId: 'd1', rules: [] },
        {
          deviceId: 'd2',
          rules: [
            {
              tool: 'run',
              match: { command: 'rm' },
              action: 'deny',
              scope: 'agent',
              note: '',
              enabled: true,
              builtin: false,
            },
          ],
        },
      ],
    };
    const groups = groupRules(response);
    expect(groups.builtin.map((r) => r.id)).toEqual(['default.a', 'default.b']);
    expect(builtinOn(groups.builtin)).toBe(1);
    expect(groups.global.map((r) => r.id)).toEqual(['g1']);
    expect(groups.projects.map((g) => [g.projectPath, g.rules.map((r) => r.id)])).toEqual([
      ['/w/b', ['p1', 'p3']],
      ['/w/a', ['p2']],
    ]);
    expect(groups.sessions.map((r) => r.id)).toEqual(['s1']);
    expect(groups.local.map((l) => l.deviceId)).toEqual(['d2']);
  });

  it('is empty for an empty answer', () => {
    const groups = groupRules({ rules: [] });
    expect(groups.builtin).toEqual([]);
    expect(groups.projects).toEqual([]);
    expect(groups.local).toEqual([]);
  });
});

describe('reading a rule', () => {
  it('lists the words and chips of a matcher in order', () => {
    expect(
      matchParts({
        tool: 'run',
        match: { command: 'curl', pipedTo: ['sh', 'bash'] },
      }),
    ).toEqual([
      { type: 'text', text: 'curl', mono: true },
      { type: 'word', text: 'piped to' },
      { type: 'chip', text: 'sh' },
      { type: 'chip', text: 'bash' },
    ]);
  });

  it('names a rule by its note, or by its match', () => {
    expect(ruleName({ tool: 'run', match: { command: 'git push' }, note: ' Pushing ' })).toBe(
      'Pushing',
    );
    expect(ruleName({ tool: 'run', match: { command: 'git push' }, note: '' })).toBe('git push');
  });

  it('leaves what the editor cannot express to the toggle and delete', () => {
    expect(formOfRule(rule({ id: 'x', builtin: true }))).toBeNull();
    expect(formOfRule(rule({ id: 'x', scope: 'session', sessionId: 's' }))).toBeNull();
    expect(formOfRule(rule({ id: 'x', tool: 'read,write', match: { paths: ['/a'] } }))).toBeNull();
    expect(formOfRule(rule({ id: 'x', match: { or: [{ command: 'ls' }] } }))).toBeNull();
    const form = formOfRule(rule({ id: 'x', note: 'n' }));
    expect(form?.kind).toBe('run');
    expect(form?.command).toBe('git status');
    expect(form?.id).toBe('x');
  });
});

describe('the rule editor', () => {
  it('does not save an empty form', () => {
    const check = checkForm(emptyForm());
    expect(check.complete).toBe(false);
    expect(check.ok).toBe(false);
  });

  it('saves a command rule and builds its body', () => {
    const form = { ...emptyForm(), command: ' git status ', note: ' ok ', action: 'ask' as const };
    expect(checkForm(form).ok).toBe(true);
    expect(toBody(form)).toEqual({
      scope: 'global',
      tool: 'run',
      match: { command: 'git status' },
      action: 'ask',
      note: 'ok',
      enabled: true,
    });
  });

  it('keeps the word chips only with a command', () => {
    const form = { ...emptyForm(), command: 'curl', anyArg: ['-X'], pipedTo: ['sh'] };
    expect(toBody(form).match).toEqual({ command: 'curl', anyArg: ['-X'], pipedTo: ['sh'] });
  });

  it('turns a path that is not absolute red and disables saving', () => {
    const form = { ...emptyForm(), kind: 'read' as const, paths: ['/ok/**', 'relative/file'] };
    const check = checkForm(form);
    expect(check.badPath).toBe('relative/file');
    expect(check.errors.paths).toBeTruthy();
    expect(check.ok).toBe(false);
  });

  it('accepts the path roots a rule may start with', () => {
    const form = {
      ...emptyForm(),
      kind: 'write' as const,
      paths: ['/etc/x', '~/.ssh/**', '**/.env', '{workspace}/src/**'],
    };
    expect(checkForm(form).ok).toBe(true);
  });

  it('needs paths, domains and an MCP server for their kinds', () => {
    expect(checkForm({ ...emptyForm(), kind: 'write' }).ok).toBe(false);
    expect(checkForm({ ...emptyForm(), kind: 'fetch' }).ok).toBe(false);
    expect(checkForm({ ...emptyForm(), kind: 'mcp' }).ok).toBe(false);
    expect(checkForm({ ...emptyForm(), kind: 'mcp', server: 'github' }).ok).toBe(true);
    expect(checkForm({ ...emptyForm(), kind: 'any' }).ok).toBe(true);
    expect(toBody({ ...emptyForm(), kind: 'mcp', server: 'github' }).tool).toBe('mcp:github/*');
  });

  it('needs a project for a project rule and keeps the note short', () => {
    const base = { ...emptyForm(), command: 'ls', scope: 'project' as const };
    expect(checkForm(base).ok).toBe(false);
    expect(checkForm({ ...base, deviceId: 'd', projectPath: '/w' }).ok).toBe(true);
    expect(checkForm({ ...emptyForm(), command: 'ls', note: 'x'.repeat(201) }).ok).toBe(false);
    expect(checkForm({ ...emptyForm(), command: 'ls', note: 'x'.repeat(200) }).ok).toBe(true);
  });

  it('summarises the form', () => {
    expect(summaryOf({ ...emptyForm(), command: 'git push', action: 'deny' })).toEqual({
      action: 'deny',
      kind: 'run',
      what: 'git push',
    });
  });
});

describe('drafting a rule from a request', () => {
  it('offers the first two words, then the first word, of a command', () => {
    const draft = draftFromCall({ toolKind: 'run', input: { command: 'git push origin main' } });
    expect(draft.ok).toBe(true);
    if (!draft.ok) return;
    expect(draft.tool).toBe('run');
    expect(draft.options.map((o) => [o.id, o.value])).toEqual([
      ['two', 'git push'],
      ['one', 'git'],
    ]);
  });

  it('offers only the word of a one-word command', () => {
    const draft = draftFromCall({ toolKind: 'run', input: { command: 'make' } });
    expect(draft.ok && draft.options.map((o) => o.id)).toEqual(['one']);
  });

  it('offers no rule for a command with shell control', () => {
    for (const command of [
      'cat a | sh',
      'a && b',
      'a || b',
      'a; b',
      'sleep 1 &',
      'echo x > f',
      'echo $(id)',
      'echo `id`',
      '(cd x)',
      'a\nb',
    ]) {
      expect(hasShellControl(command)).toBe(true);
      expect(draftFromCall({ toolKind: 'run', input: { command } })).toEqual({
        ok: false,
        reason: 'control',
      });
    }
    expect(hasShellControl('git commit -m fix')).toBe(false);
  });

  it('offers no rule for other tools', () => {
    expect(draftFromCall({ toolKind: 'other', input: {} })).toEqual({ ok: false, reason: 'tool' });
    expect(draftFromCall({ toolKind: 'call', input: {} })).toEqual({ ok: false, reason: 'tool' });
  });

  it('offers the file and its directory for a path, inside the workspace as {workspace}', () => {
    const draft = draftFromCall({
      toolKind: 'edit',
      input: { file_path: '/w/app/src/a.ts' },
      projectPath: '/w/app',
    });
    expect(draft.ok).toBe(true);
    if (!draft.ok) return;
    expect(draft.kind).toBe('write');
    expect(draft.options.map((o) => o.value)).toEqual([
      '{workspace}/src/a.ts',
      '{workspace}/src/**',
    ]);
    expect(workspacePath('/elsewhere/x', '/w/app')).toBe('/elsewhere/x');
    expect(workspacePath('/w/application/x', '/w/app')).toBe('/w/application/x');
  });

  it('drafts a fetch by its host, which can be edited', () => {
    const draft = draftFromCall({
      toolKind: 'fetch',
      input: { url: 'https://Docs.Example.com/a?b' },
    });
    expect(draft.ok && draft.editable).toBe(true);
    expect(draft.ok && draft.options[0]?.match).toEqual({ domains: ['docs.example.com'] });
  });

  it('drafts an MCP call by server and tool', () => {
    const draft = draftFromCall({
      toolKind: 'call',
      mcp: { server: 'github', tool: 'create_issue' },
      input: {},
    });
    expect(draft.ok && draft.tool).toBe('mcp:github/create_issue');
  });

  it('builds the body that saves a draft', () => {
    const draft = draftFromCall({ toolKind: 'run', input: { command: 'git status -s' } });
    if (!draft.ok) throw new Error('no draft');
    const option = draft.options[0];
    if (!option) throw new Error('no option');
    expect(
      bodyOfDraft(draft, option, 'allow', { scope: 'session', sessionId: 's1' }, ' fine '),
    ).toEqual({
      scope: 'session',
      sessionId: 's1',
      tool: 'run',
      match: { command: 'git status' },
      action: 'allow',
      note: 'fine',
      enabled: true,
    });
  });
});
