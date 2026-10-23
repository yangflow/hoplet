import { describe, expect, it } from 'vitest';
import { containsCredential, scrubCredentials, scrubText } from '../src/scrub/index.ts';

const R = '[redacted]';

describe('scrubCredentials (what the log masks)', () => {
  const cases: [string, string, string][] = [
    [
      'a bearer credential of any length',
      'Authorization: Bearer abc',
      `Authorization: Bearer ${R}`,
    ],
    ['a bearer credential up to a quote', '"Bearer tok.en-1"', `"Bearer ${R}"`],
    ['a token query parameter', 'GET /x?token=abc123&a=1', `GET /x?token=${R}&a=1`],
    ['a ticket query parameter', 'GET /ws?ticket=t1 HTTP', `GET /ws?ticket=${R} HTTP`],
    [
      'a signature query parameter',
      'https://h.test/x?sig=abc123&a=1',
      `https://h.test/x?sig=${R}&a=1`,
    ],
    ['URL user info', 'https://me:pw123@host.test/r.git', `https://${R}@host.test/r.git`],
    ['a GitHub token', 'ghp_abcdefghijklmnopqrstuvwxyz0123', R],
    ['an AWS key id', 'AKIAABCDEFGHIJKLMNOP', R],
  ];
  it.each(cases)('masks %s', (_name, input, expected) => {
    expect(scrubCredentials(input, R)).toBe(expected);
  });

  it('leaves prose and commands that only name a credential alone', () => {
    for (const line of [
      'authentication failed for device',
      'API_TOKEN=hunter2 npm publish',
      'Token expired',
    ]) {
      expect(scrubCredentials(line, R)).toBe(line);
    }
  });
});

describe('scrubText (what a notification summary masks)', () => {
  const cases: [string, string, string][] = [
    [
      'a bearer header',
      'curl -H "Authorization: Bearer abcdef123456" https://x.test',
      'curl -H "Authorization: *** ***" https://x.test',
    ],
    ['a basic credential', 'Basic dXNlcjpwYXNzd29yZA==', 'Basic ***'],
    ['NAME=value', 'API_TOKEN=hunter2 npm publish', 'API_TOKEN=*** npm publish'],
    ['--name value', 'cli --password hunter2 run', 'cli --password *** run'],
    [
      'URL user info',
      'git clone https://me:pw123@host.test/r.git',
      'git clone https://***@host.test/r.git',
    ],
    [
      'a query credential',
      'curl https://h.test/x?sig=abc123&a=1',
      'curl https://h.test/x?sig=***&a=1',
    ],
    ['a GitHub token', 'echo ghp_abcdefghijklmnopqrstuvwxyz0123', 'echo ***'],
    ['an sk key', 'echo sk-abcdefghijklmnopqrstu', 'echo ***'],
    ['a Slack token', 'xoxb-1234567890-abcdef', '***'],
    ['a JWT', 'eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0.sig', '***'],
    ['a long key-like run with a digit', 'use 0123456789abcdef0123456789abcdef now', 'use *** now'],
  ];
  it.each(cases)('masks %s', (_name, input, expected) => {
    expect(scrubText(input)).toBe(expected);
  });

  it('keeps a long path segment', () => {
    const path = '/repo/0123456789abcdef0123456789abcdef/file.ts';
    expect(scrubText(path)).toBe(path);
  });

  it('takes the mask as an argument', () => {
    expect(scrubText('API_TOKEN=x', R)).toBe(`API_TOKEN=${R}`);
  });
});

describe('containsCredential (what stored configuration must not hold)', () => {
  it('finds a private key block and a credential by its shape', () => {
    expect(containsCredential('-----BEGIN OPENSSH PRIVATE KEY-----\nabc')).toBe(true);
    expect(containsCredential('Authorization: Bearer abc.def')).toBe(true);
  });

  it('lets prose through', () => {
    expect(containsCredential('Run the tests with pnpm check before you commit.')).toBe(false);
    expect(containsCredential('Never put a token in the repository.')).toBe(false);
  });
});
