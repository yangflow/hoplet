import { describe, expect, it } from 'vitest';
import { BUILTIN_PERMISSION_RULES, permissionRuleProblem } from '../src/permission-rules.ts';

describe('the built-in permission rules', () => {
  it('are the eight of ADR 0029, global, enabled and acceptable to the agent', () => {
    expect(BUILTIN_PERMISSION_RULES.map((r) => r.id)).toEqual([
      'default.secret-files',
      'default.credential-stores',
      'default.hoplet-and-provider-config',
      'default.write-outside-workspace',
      'default.force-push',
      'default.destructive-git',
      'default.recursive-delete-and-sudo',
      'default.download-and-run',
    ]);
    for (const rule of BUILTIN_PERMISSION_RULES) {
      expect(rule).toMatchObject({ scope: 'global', enabled: true, builtin: true });
      expect(permissionRuleProblem(rule), rule.id).toBeNull();
    }
  });
});

describe('permissionRuleProblem', () => {
  it('accepts a matcher that fits the tool', () => {
    expect(permissionRuleProblem({ tool: '*', match: {} })).toBeNull();
    expect(
      permissionRuleProblem({ tool: 'run', match: { command: 'git', anyArg: ['-f'] } }),
    ).toBeNull();
    expect(permissionRuleProblem({ tool: 'mcp:s/t', match: {} })).toBeNull();
  });

  it('names what is wrong', () => {
    expect(permissionRuleProblem({ tool: 'nope', match: {} })).toMatch(/unknown tool/);
    expect(permissionRuleProblem({ tool: 'run', match: { paths: ['/a'] } })).toMatch(
      /does not fit/,
    );
    expect(permissionRuleProblem({ tool: 'run', match: { anyArg: ['x'] } })).toMatch(
      /need a command/,
    );
    expect(permissionRuleProblem({ tool: 'read', match: { paths: ['a'] } })).toMatch(
      /not absolute/,
    );
    expect(permissionRuleProblem({ tool: 'fetch', match: { domains: ['*'] } })).toMatch(/domain/);
    expect(permissionRuleProblem({ tool: 'run', match: { or: [{ paths: ['/a'] }] } })).toMatch(
      /does not fit/,
    );
  });
});
