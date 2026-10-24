import { describe, expect, it } from 'vitest';
import {
  containsCredential,
  redactForExport,
  scrubCredentials,
  scrubText,
} from '../src/scrub/index.ts';

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

describe('redactForExport (what an exported session masks)', () => {
  const cases: [string, string, string][] = [
    ['an API key', 'key sk-abcdefghijklmnopqrstu here', 'key [redacted:api-key] here'],
    ['a GitHub token', 'ghp_abcdefghijklmnopqrstuv', '[redacted:github-token]'],
    [
      'a Hoplet token after its name',
      'HOPLET_TOKEN=hpt_abcdefghijklmnopqrstuv',
      'HOPLET_TOKEN=[redacted:hoplet-token]',
    ],
    ['a Slack token', 'xoxb-1234567890-abcdef', '[redacted:slack-token]'],
    ['an AWS key', 'AKIAABCDEFGHIJKLMNOP', '[redacted:aws-key]'],
    ['a JWT', 'eyJhbGciOiJI.eyJzdWIiOiIx.sig', '[redacted:jwt]'],
    [
      'URL user info',
      'https://me:pa55@example.com/x',
      'https://[redacted:url-credential]@example.com/x',
    ],
    [
      'a bearer token',
      'Authorization: Bearer abc.def',
      'Authorization: Bearer [redacted:bearer-token]',
    ],
    [
      'a basic credential',
      'Authorization: Basic dXNlcjpwYXNz1',
      'Authorization: Basic [redacted:basic-auth]',
    ],
    ['a query credential', '/cb?token=abc123&x=1', '/cb?token=[redacted:query-credential]&x=1'],
    ['a named secret', 'DB_PASSWORD=hunter22', 'DB_PASSWORD=[redacted:named-secret]'],
    ['a quoted named secret', 'api_key: "my value"', 'api_key: "[redacted:named-secret]"'],
    ['a flag', '--auth-token s3cretvalue', '--auth-token [redacted:named-secret]'],
    [
      'a private key block',
      '-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----',
      '[redacted:private-key]',
    ],
  ];
  for (const [name, input, output] of cases) {
    it(`masks ${name}`, () => {
      const r = redactForExport(input);
      expect(r.text).toBe(output);
      expect(r.masked).toBe(1);
    });
  }

  it("masks the user's strings without regard to case, the longest first", () => {
    const r = redactForExport('Acme Corp and acme corp-internal', [
      'acme corp',
      'Acme Corp-internal',
    ]);
    expect(r.text).toBe('[redacted:custom] and [redacted:custom]');
    expect(r.masked).toBe(2);
  });

  it('never masks a marker again', () => {
    const once = redactForExport('x sk-abcdefghijklmnopqrstu', ['redacted', 'api-key']).text;
    expect(once).toBe('x [redacted:api-key]');
  });

  it('lets prose, hashes and paths through', () => {
    for (const text of [
      'Never put a token in the repository; the auth flow is in core/src/auth.',
      'commit 4f9c2a1e8b7d6c5f4e3d2c1b0a9f8e7d6c5b4a39 changed core/src/api/session-export.ts',
      'The password field is empty.',
    ]) {
      expect(redactForExport(text)).toEqual({ text, masked: 0 });
    }
  });
});
