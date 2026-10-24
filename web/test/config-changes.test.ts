import type { SessionMcpSettings } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import en from '../src/i18n/en.json';
import zh from '../src/i18n/zh-Hans.json';
import { CONFIG_OBJECTS, configChange } from '../src/lib/config-change-fixtures.ts';
import {
  diffCounts,
  objectKind,
  objectName,
  objectPath,
  pendingChange,
  settingValues,
  takesEffectLine,
  tookEffectLine,
  withChange,
} from '../src/lib/config-changes.ts';
import { withReadConfig, withWriteConfig } from '../src/lib/session-mcp.ts';

const SESSION = '8f1c2d3e-4a5b-4c6d-8e7f-90a1b2c3d4e5';
const { memory, skill, command, effort } = CONFIG_OBJECTS;

describe('config change helpers', () => {
  it('keeps the newest change first and replaces a change by its id', () => {
    const older = configChange(SESSION, 1_000, { changeId: 'a', state: 'declined' });
    const newer = configChange(SESSION, 2_000, { changeId: 'b' });
    const list = withChange([older], newer);
    expect(list.map((c) => c.changeId)).toEqual(['b', 'a']);
    const applied = withChange(list, { ...newer, state: 'applied' });
    expect(applied).toHaveLength(2);
    expect(applied[0]?.state).toBe('applied');
    expect(pendingChange(list)?.changeId).toBe('b');
    expect(pendingChange(applied)).toBeNull();
  });

  it('reads the path of a file from its diff, and none for a setting', () => {
    expect(objectPath(memory)).toBe('CLAUDE.md');
    expect(objectPath(skill)).toBe('.claude/skills/ingest-fixtures/SKILL.md');
    expect(objectPath(effort)).toBeNull();
    expect(objectPath({ ...command, diff: '--- a/.claude/commands/old.md\n+++ /dev/null\n' })).toBe(
      '.claude/commands/old.md',
    );
  });

  it('counts added and removed lines without the headers', () => {
    expect(diffCounts(memory.diff)).toEqual({ added: 3, removed: 1 });
    expect(diffCounts(skill.diff)).toEqual({ added: 6, removed: 0 });
  });

  it("reads a setting's values from its diff", () => {
    expect(settingValues(effort)).toEqual({ from: 'medium', to: 'high' });
    expect(settingValues({ ...effort, diff: '+effort: high\n' })).toEqual({
      from: null,
      to: 'high',
    });
  });

  it('names and kinds the objects', () => {
    expect(objectName(memory)).toBe('CLAUDE.md');
    expect(objectName({ ...memory, diff: '' })).toBe('Project memory');
    expect(objectName(command)).toBe('/triage');
    expect(objectName(effort)).toBe('Effort');
    expect(objectName(skill)).toBe('ingest-fixtures');
    expect(objectKind(skill)).toBe('New skill');
    expect(objectKind(memory)).toBe('Project memory');
    expect(objectKind({ ...command, content: null })).toBe('Command, removed');
    expect(objectKind(effort)).toBe('Setting');
  });

  it('says when the change takes effect and when it took effect', () => {
    expect(takesEffectLine([memory, skill])).toBe('Takes effect when the session next starts');
    expect(takesEffectLine([effort])).toBe('Takes effect from the next turn');
    expect(takesEffectLine([{ ...skill, takesEffect: 'now' }])).toBe('Takes effect now');
    expect(takesEffectLine([memory, effort])).toBe(
      'Settings from the next turn, the rest when the session next starts',
    );
    expect(takesEffectLine([{ ...skill, takesEffect: 'now' }, memory])).toBe(
      'Some now, the rest when the session next starts',
    );
    expect(tookEffectLine([{ ...skill, takesEffect: 'now' }])).toBe('Already in effect');
    expect(tookEffectLine([effort])).toBe('In effect from the next turn');
    expect(tookEffectLine([memory])).toBe('In effect when the session next starts');
  });
});

describe('configuration switches of session MCP', () => {
  const base = { readConfig: false, writeConfig: false } as SessionMcpSettings;

  it('proposing changes turns reading on, and reading off turns proposing off', () => {
    const on = withWriteConfig(base, true);
    expect(on).toMatchObject({ readConfig: true, writeConfig: true });
    expect(withWriteConfig(on, false)).toMatchObject({ readConfig: true, writeConfig: false });
    expect(withReadConfig(on, false)).toMatchObject({ readConfig: false, writeConfig: false });
    expect(withReadConfig(base, true)).toMatchObject({ readConfig: true, writeConfig: false });
  });
});

describe('config change strings', () => {
  it('has every string in both languages', () => {
    const keys = [
      'Review changes',
      'Decided as a whole',
      'Changes applied',
      'Changes undone',
      'Changes declined',
      'Change request expired',
      'Changes not applied',
      'Undo did not run',
      'Change request',
      'Approve all {n}',
      'Read configuration',
      'Propose configuration changes',
      'Propose changes',
      '{provider} proposes {n} changes to this project',
      'Takes effect when the session next starts',
    ];
    for (const key of keys) {
      expect(en, key).toHaveProperty([key], key);
      expect(zh, key).toHaveProperty([key]);
    }
  });
});
