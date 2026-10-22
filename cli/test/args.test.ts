import { describe, expect, it } from 'vitest';
import { parseArgs } from '../src/args.ts';
import { CliError } from '../src/errors.ts';

function usage(argv: string[]): string {
  try {
    parseArgs(argv);
  } catch (err) {
    if (err instanceof CliError && err.code === 'usage') return err.reason ?? '';
    throw err;
  }
  throw new Error('expected a usage error');
}

describe('parseArgs', () => {
  it('shows help without a command, for help and for --help', () => {
    expect(parseArgs([])).toEqual({ kind: 'help' });
    expect(parseArgs(['help', 'send'])).toEqual({ kind: 'help', topic: 'send' });
    expect(parseArgs(['send', '--help'])).toEqual({ kind: 'help', topic: 'send' });
    expect(parseArgs(['-h'])).toEqual({ kind: 'help' });
  });

  it('reads flags as --flag value and --flag=value, and lists repeat', () => {
    const parsed = parseArgs([
      'new',
      '--device=laptop',
      '--path',
      '/work',
      '--set',
      'a=1',
      '--set=b=2',
      '--wait',
      'do it',
    ]);
    expect(parsed).toEqual({
      kind: 'command',
      name: 'new',
      positionals: ['do it'],
      flags: { device: 'laptop', path: '/work', set: ['a=1', 'b=2'], wait: true },
    });
  });

  it('treats prompt as send, and - and words after -- as words', () => {
    expect(parseArgs(['prompt', 's1', '-'])).toMatchObject({
      name: 'send',
      positionals: ['s1', '-'],
    });
    expect(parseArgs(['send', 's1', '--', '--not-a-flag'])).toMatchObject({
      positionals: ['s1', '--not-a-flag'],
    });
  });

  it('names subcommands', () => {
    expect(parseArgs(['audit', 'verify', '--file', 'x'])).toMatchObject({ name: 'audit verify' });
    expect(parseArgs(['devices'])).toMatchObject({ name: 'devices', positionals: [] });
    expect(parseArgs(['devices', 'add', 'box'])).toMatchObject({
      name: 'devices add',
      positionals: ['box'],
    });
    expect(parseArgs(['tokens', 'revoke', 't1'])).toMatchObject({ name: 'tokens revoke' });
  });

  it('refuses what does not fit', () => {
    expect(usage(['bogus'])).toContain('Unknown command');
    expect(usage(['sessions', '--bogus'])).toContain('Unknown flag');
    expect(usage(['sessions', '--wait'])).toContain('does not belong');
    expect(usage(['wait'])).toContain('Usage');
    expect(usage(['sessions', 'extra'])).toContain('Usage');
    expect(usage(['audit'])).toContain('list|export|verify|head');
    expect(usage(['audit', 'bogus'])).toContain('list|export|verify|head');
    expect(usage(['sessions', '--device'])).toContain('needs a value');
    expect(usage(['sessions', '--json=1'])).toContain('takes no value');
    expect(usage(['devices', 'bogus'])).toContain('Usage');
  });
});
