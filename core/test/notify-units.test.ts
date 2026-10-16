import { createDecipheriv, createECDH, createPublicKey, hkdfSync, verify } from 'node:crypto';
import type { NotifyPreferences } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  channelsFor,
  clip,
  encryptPayload,
  inQuietHours,
  redactText,
  signBody,
  signToken,
  summarize,
  vapidJwt,
  verifyToken,
  webUiOnly,
} from '../src/notify/index.ts';

const b64 = (text: string): Buffer => Buffer.from(text, 'base64url');

describe('redaction', () => {
  it('masks credentials in commands', () => {
    expect(redactText('curl -H "Authorization: Bearer abcdef123456" https://x.test')).not.toContain(
      'abcdef123456',
    );
    expect(redactText('API_TOKEN=hunter2 npm publish')).toBe('API_TOKEN=*** npm publish');
    expect(redactText('git clone https://me:pw123@host.test/r.git')).toBe(
      'git clone https://***@host.test/r.git',
    );
    expect(redactText('curl https://h.test/x?sig=abc123&a=1')).toBe(
      'curl https://h.test/x?sig=***&a=1',
    );
    expect(redactText('echo ghp_abcdefghijklmnopqrstuvwxyz0123')).toBe('echo ***');
  });

  it('keeps ordinary paths', () => {
    const path = '/Users/me/projects/some-very-long-directory-name-here/src/components/Button.tsx';
    expect(redactText(path)).toBe(path);
  });

  it('cuts to the limit on one line', () => {
    const out = clip(`ls\n${'a b '.repeat(60)}`, 80);
    expect([...out].length).toBe(80);
    expect(out).not.toContain('\n');
    expect(out.endsWith('…')).toBe(true);
  });

  it('summarizes a command or a path and nothing for an MCP tool', () => {
    expect(summarize('Bash', { command: 'npm test' }, 80)).toBe('npm test');
    expect(summarize('Edit', { file_path: '/a/b.ts', old_string: 'secret body' }, 80)).toBe(
      '/a/b.ts',
    );
    expect(summarize('mcp__x__y', { command: 'rm -rf /' }, 80)).toBeUndefined();
    expect(summarize('Foo', 'text', 80)).toBeUndefined();
  });
});

describe('web push encryption', () => {
  it('reproduces the RFC 8291 appendix A message', () => {
    const body = encryptPayload(
      Buffer.from('When I grow up, I want to be a watermelon'),
      b64(
        'BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4',
      ),
      b64('BTBZMqHH6r4Tts7J_aSIgg'),
      {
        salt: b64('DGv6ra1nlYgDCS1FRnbzlw'),
        sender: { privateKey: b64('yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw') },
      },
    );
    expect(body.toString('base64url')).toBe(
      'DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN',
    );
  });

  it('decrypts as the receiver with a random sender', () => {
    const ua = createECDH('prime256v1');
    ua.generateKeys();
    const auth = Buffer.alloc(16, 7);
    const plain = Buffer.from(JSON.stringify({ kind: 'permission', project: 'hoplet' }));
    const body = encryptPayload(plain, ua.getPublicKey(), auth);
    const salt = body.subarray(0, 16);
    expect(body.readUInt32BE(16)).toBe(4096);
    const idlen = body[20] as number;
    const sender = body.subarray(21, 21 + idlen);
    const record = body.subarray(21 + idlen);
    const shared = ua.computeSecret(sender);
    const ikm = Buffer.from(
      hkdfSync(
        'sha256',
        shared,
        auth,
        Buffer.concat([Buffer.from('WebPush: info\0'), ua.getPublicKey(), sender]),
        32,
      ),
    );
    const cek = Buffer.from(
      hkdfSync('sha256', ikm, salt, Buffer.from('Content-Encoding: aes128gcm\0'), 16),
    );
    const nonce = Buffer.from(
      hkdfSync('sha256', ikm, salt, Buffer.from('Content-Encoding: nonce\0'), 12),
    );
    const decipher = createDecipheriv('aes-128-gcm', cek, nonce);
    decipher.setAuthTag(record.subarray(record.length - 16));
    const out = Buffer.concat([
      decipher.update(record.subarray(0, record.length - 16)),
      decipher.final(),
    ]);
    expect(out.subarray(0, out.length - 1).equals(plain)).toBe(true);
    expect(out[out.length - 1]).toBe(2);
  });

  it('refuses malformed keys and oversized payloads', () => {
    const ua = createECDH('prime256v1');
    ua.generateKeys();
    expect(() => encryptPayload(Buffer.from('x'), Buffer.alloc(10), Buffer.alloc(16))).toThrow();
    expect(() => encryptPayload(Buffer.from('x'), ua.getPublicKey(), Buffer.alloc(3))).toThrow();
    expect(() => encryptPayload(Buffer.alloc(5000), ua.getPublicKey(), Buffer.alloc(16))).toThrow();
  });
});

describe('vapid jwt', () => {
  it('carries the claims and verifies with the public key', () => {
    const ecdh = createECDH('prime256v1');
    ecdh.generateKeys();
    const vapid = {
      publicKey: ecdh.getPublicKey().toString('base64url'),
      privateKey: ecdh.getPrivateKey().toString('base64url'),
    };
    const now = Date.UTC(2026, 9, 16);
    const jwt = vapidJwt(vapid, 'https://push.example', 'mailto:hoplet@localhost', now);
    const [h, c, s] = jwt.split('.') as [string, string, string];
    expect(JSON.parse(b64(h).toString())).toEqual({ typ: 'JWT', alg: 'ES256' });
    const claims = JSON.parse(b64(c).toString()) as Record<string, unknown>;
    expect(claims).toEqual({
      aud: 'https://push.example',
      exp: Math.floor(now / 1000) + 12 * 3600,
      sub: 'mailto:hoplet@localhost',
    });
    expect(b64(s).length).toBe(64);
    const pub = ecdh.getPublicKey();
    const key = createPublicKey({
      key: {
        kty: 'EC',
        crv: 'P-256',
        x: pub.subarray(1, 33).toString('base64url'),
        y: pub.subarray(33).toString('base64url'),
      },
      format: 'jwk',
    });
    expect(
      verify('sha256', Buffer.from(`${h}.${c}`), { key, dsaEncoding: 'ieee-p1363' }, b64(s)),
    ).toBe(true);
    expect(
      verify('sha256', Buffer.from(`${h}.${c}x`), { key, dsaEncoding: 'ieee-p1363' }, b64(s)),
    ).toBe(false);
  });
});

describe('webhook signature', () => {
  it('is the hex HMAC-SHA-256 of the body', () => {
    // RFC 4231 test case 2
    expect(signBody('Jefe', 'what do ya want for nothing?')).toBe(
      'sha256=5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843',
    );
  });
});

describe('action tokens', () => {
  const key = Buffer.alloc(32, 1).toString('base64url');
  const claims = { sessionId: 's1', requestId: 'r1', expiresAt: 1_000 };

  it('verify with the key that signed them and carry the claims', () => {
    const token = signToken(key, claims);
    const out = verifyToken(key, token);
    expect(out).toMatchObject(claims);
    expect(out?.id).toBeTruthy();
    expect(signToken(key, claims)).not.toBe(token);
  });

  it('are refused under another key, when altered or malformed', () => {
    const token = signToken(key, claims);
    expect(verifyToken(Buffer.alloc(32, 2).toString('base64url'), token)).toBeNull();
    const [payload, mac] = token.split('.') as [string, string];
    const forged = Buffer.from(JSON.stringify({ i: 'x', s: 's1', r: 'r2', e: 9e12 })).toString(
      'base64url',
    );
    expect(verifyToken(key, `${forged}.${mac}`)).toBeNull();
    expect(verifyToken(key, `${payload}.`)).toBeNull();
    expect(verifyToken(key, 'nope')).toBeNull();
    expect(verifyToken(key, `${token}.x`)).toBeNull();
  });
});

describe('webUiOnly', () => {
  const project = '/work/app';
  it('sends dangerous commands to the web UI', () => {
    expect(webUiOnly('Bash', { command: 'rm -rf build' }, project)).toBe(true);
    expect(webUiOnly('Bash', { command: 'cd x && sudo apt update' }, project)).toBe(true);
    expect(webUiOnly('Bash', { command: 'git push origin main' }, project)).toBe(true);
    expect(webUiOnly('Bash', { command: 'git -C . push' }, project)).toBe(true);
    expect(webUiOnly('Bash', { command: 'ls; rm x' }, project)).toBe(true);
  });
  it('allows ordinary commands and lookalikes', () => {
    expect(webUiOnly('Bash', { command: 'npm test' }, project)).toBe(false);
    expect(webUiOnly('Bash', { command: 'git status && git pull' }, project)).toBe(false);
    expect(webUiOnly('Bash', { command: 'cat farm.txt rmdir-notes.md' }, project)).toBe(false);
  });
  it('checks the workspace for file tools', () => {
    expect(webUiOnly('Write', { file_path: '/work/app/src/a.ts' }, project)).toBe(false);
    expect(webUiOnly('Edit', { file_path: 'src/a.ts' }, project)).toBe(false);
    expect(webUiOnly('Edit', { file_path: '/etc/hosts' }, project)).toBe(true);
    expect(webUiOnly('Write', { file_path: '/work/app/../other/x' }, project)).toBe(true);
    expect(webUiOnly('Write', { file_path: '/work/app-two/x' }, project)).toBe(true);
    expect(webUiOnly('Write', {}, project)).toBe(true);
  });
  it('sends every MCP tool to the web UI and lets read tools through', () => {
    expect(webUiOnly('mcp__srv__tool', {}, project)).toBe(true);
    expect(webUiOnly('Read', { file_path: '/etc/hosts' }, project)).toBe(false);
  });
});

describe('decision rules', () => {
  const base: NotifyPreferences = {
    preset: 'needs-me',
    events: {},
    projects: {},
    quietHours: null,
  };
  const at = Date.UTC(2026, 0, 1, 23, 30);
  const quiet = { start: '22:00', end: '07:00', timeZone: 'UTC' };

  it('follows the preset', () => {
    expect(channelsFor(base, 'permission', '/p', at)).toEqual(['push', 'webhook', 'native']);
    expect(channelsFor(base, 'failure', '/p', at)).not.toHaveLength(0);
    expect(channelsFor(base, 'turn-done', '/p', at)).toEqual([]);
    expect(
      channelsFor({ ...base, preset: 'needs-me-and-done' }, 'turn-done', '/p', at),
    ).not.toHaveLength(0);
    expect(channelsFor({ ...base, preset: 'everything' }, 'turn-done', '/p', at)).not.toHaveLength(
      0,
    );
    expect(channelsFor({ ...base, preset: 'off' }, 'permission', '/p', at)).toEqual([]);
  });

  it('lets a project and an event override it', () => {
    const prefs: NotifyPreferences = {
      ...base,
      events: { permission: { channels: ['push'] } },
      projects: {
        '/quiet': { preset: 'off', events: {} },
        '/loud': {
          preset: 'needs-me-and-done',
          events: { 'turn-done': { channels: ['webhook'] } },
        },
        '/mute': { events: { permission: { channels: [] } } },
      },
    };
    expect(channelsFor(prefs, 'permission', '/other', at)).toEqual(['push']);
    expect(channelsFor(prefs, 'plan', '/quiet', at)).toEqual([]);
    expect(channelsFor(prefs, 'turn-done', '/loud', at)).toEqual(['webhook']);
    expect(channelsFor(prefs, 'permission', '/mute', at)).toEqual([]);
  });

  it('holds back what is not needs-you during quiet hours', () => {
    const prefs: NotifyPreferences = { ...base, preset: 'everything', quietHours: quiet };
    expect(channelsFor(prefs, 'turn-done', '/p', at)).toEqual([]);
    expect(channelsFor(prefs, 'permission', '/p', at)).not.toHaveLength(0);
    expect(channelsFor(prefs, 'plan', '/p', at)).not.toHaveLength(0);
    expect(channelsFor(prefs, 'failure', '/p', at)).not.toHaveLength(0);
    expect(channelsFor(prefs, 'turn-done', '/p', Date.UTC(2026, 0, 1, 12, 0))).not.toHaveLength(0);
  });

  it('computes quiet hours on the clock of the zone, across midnight', () => {
    expect(inQuietHours(null, at)).toBe(false);
    expect(inQuietHours(quiet, Date.UTC(2026, 0, 1, 3, 0))).toBe(true);
    expect(inQuietHours(quiet, Date.UTC(2026, 0, 1, 7, 0))).toBe(false);
    expect(inQuietHours(quiet, Date.UTC(2026, 0, 1, 22, 0))).toBe(true);
    expect(
      inQuietHours({ start: '09:00', end: '17:00', timeZone: 'UTC' }, Date.UTC(2026, 0, 1, 12)),
    ).toBe(true);
    expect(
      inQuietHours({ start: '09:00', end: '09:00', timeZone: 'UTC' }, Date.UTC(2026, 0, 1, 9)),
    ).toBe(false);
    // 23:30 UTC is 08:30 in Tokyo
    expect(inQuietHours({ ...quiet, timeZone: 'Asia/Tokyo' }, at)).toBe(false);
    expect(inQuietHours({ ...quiet, timeZone: 'Not/AZone' }, at)).toBe(false);
  });
});
