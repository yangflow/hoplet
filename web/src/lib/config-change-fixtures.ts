import type { ConfigChangeObject, SessionConfigChange } from '@hoplet/shared';

// A session's request to change its configuration, as the core serves it (ADR 0036), for /__states and the tests:
// the four objects of design/screens/self-config.html.

const MEMORY: ConfigChangeObject = {
  kind: 'memory',
  name: 'project',
  baseHash: 'a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2',
  newHash: 'b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3',
  content: '',
  diff: [
    '--- a/CLAUDE.md',
    '+++ b/CLAUDE.md',
    '@@ -12,5 +12,7 @@',
    ' ## Working agreements',
    ' ',
    '-Run `pnpm check` after a change.',
    '+Run `pnpm check` before calling a task done.',
    '+After a change in `core/`, also run',
    '+`pnpm --filter @hoplet/core test`.',
    ' ',
    ' Keep pull requests small.',
    '',
  ].join('\n'),
  takesEffect: 'next-start',
};

const SKILL_TEXT = [
  '---',
  'name: ingest-fixtures',
  'description: Rebuild ingest fixtures',
  '---',
  'Run `scripts/fixtures.sh` and diff the',
  'output against `core/test/fixtures/`.',
  '',
].join('\n');

const SKILL: ConfigChangeObject = {
  kind: 'skill',
  name: 'ingest-fixtures',
  baseHash: '',
  newHash: 'c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4',
  content: SKILL_TEXT,
  diff: [
    '--- /dev/null',
    '+++ b/.claude/skills/ingest-fixtures/SKILL.md',
    '@@ -0,0 +1,6 @@',
    ...SKILL_TEXT.trimEnd()
      .split('\n')
      .map((l) => `+${l}`),
    '',
  ].join('\n'),
  newDirectories: ['.claude/skills/ingest-fixtures'],
  takesEffect: 'next-start',
};

const COMMAND: ConfigChangeObject = {
  kind: 'command',
  name: 'triage',
  baseHash: 'd4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5',
  newHash: 'e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6',
  content: '---\ndescription: Triage and label issues\n---\n',
  diff: [
    '--- a/.claude/commands/triage.md',
    '+++ b/.claude/commands/triage.md',
    '@@ -1,3 +1,3 @@',
    ' ---',
    '-description: Triage new issues',
    '+description: Triage and label issues',
    ' ---',
    '',
  ].join('\n'),
  takesEffect: 'next-start',
};

const EFFORT: ConfigChangeObject = {
  kind: 'config',
  name: 'effort',
  baseHash: 'f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7',
  newHash: 'a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8',
  content: 'high',
  diff: '-effort: medium\n+effort: high\n',
  takesEffect: 'next-turn',
};

export const CONFIG_OBJECTS = { memory: MEMORY, skill: SKILL, command: COMMAND, effort: EFFORT };

export const CONFIG_CHANGE_ID = '0f1e2d3c-4b5a-4968-8776-655443322110';

/** The request with the four objects, pending; `over` moves it on. */
export function configChange(
  sessionId: string,
  createdAt: number,
  over: Partial<SessionConfigChange> = {},
): SessionConfigChange {
  return {
    changeId: CONFIG_CHANGE_ID,
    sessionId,
    state: 'pending',
    reason: 'The checks I ran this week need these to be the default for the project.',
    objects: [MEMORY, SKILL, COMMAND, EFFORT],
    createdAt,
    expiresAt: createdAt + 86_400_000,
    decidedAt: null,
    appliedAt: null,
    undoneAt: null,
    undoable: false,
    ...over,
  };
}
