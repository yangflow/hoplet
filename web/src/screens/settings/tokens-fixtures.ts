import type { DeviceInfo, TokenCreateResponse, TokenInfo } from '@hoplet/shared';
import { emptyForm, type TokenForm } from '../../lib/tokens.ts';
import type { TokensFixture } from './tokens.tsx';

// Fixtures for /__states/settings/tokens*: the screens of design/screens/tokens.html, direction A. Nothing here is read by
// the real screen. Times count from NOW, a Thursday, so the rendering does not drift.

const NOW = Date.UTC(2026, 9, 22, 12, 0, 0);
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

function device(deviceId: string, name: string, os: string): DeviceInfo {
  return {
    deviceId,
    name,
    connected: true,
    lastSeen: NOW,
    system: { os, arch: 'arm64' },
    providers: [],
    labels: [],
  };
}

const DEVICES = [
  device('dev-build', 'build-box', 'linux'),
  device('dev-mbp', 'mbp-yang', 'darwin'),
];

const PROJECTS: Record<string, string[]> = {
  'dev-build': ['/srv/hoplet', '/srv/hoplet-docs', '/srv/infra'],
  'dev-mbp': ['/Users/yang/hoplet', '/Users/yang/hoplet-docs'],
};

function token(
  id: string,
  name: string,
  grant: TokenInfo['grant'],
  made: { createdAgo: number; expiresIn: number; usedAgo: number | null },
  state: TokenInfo['state'] = 'active',
  revokedAgo?: number,
): TokenInfo {
  return {
    id,
    kind: 'scoped',
    name,
    grant,
    createdAt: NOW - made.createdAgo,
    expiresAt: NOW + made.expiresIn,
    revokedAt: revokedAgo === undefined ? null : NOW - revokedAgo,
    lastUsedAt: made.usedAgo === null ? null : NOW - made.usedAgo,
    state,
  };
}

const TOKENS: TokenInfo[] = [
  token(
    'tok_release',
    'CI release bot',
    {
      capabilities: ['read', 'send', 'create'],
      allow: [
        { deviceId: 'dev-build', path: '/srv/hoplet' },
        { deviceId: 'dev-build', path: '/srv/hoplet-docs' },
      ],
    },
    { createdAgo: 29 * DAY, expiresIn: 61 * DAY, usedAgo: 4 * MIN },
  ),
  token(
    'tok_phone',
    'Phone shortcut',
    { capabilities: ['read', 'send'] },
    { createdAgo: 84 * DAY, expiresIn: 6 * DAY, usedAgo: 2 * HOUR },
  ),
  token(
    'tok_review',
    'Review agent',
    {
      capabilities: ['read', 'send', 'create', 'answer'],
      allow: [{ deviceId: 'dev-mbp', path: '/Users/yang/hoplet' }],
    },
    { createdAgo: 12 * DAY, expiresIn: 78 * DAY, usedAgo: DAY },
  ),
  token(
    'tok_grafana',
    'Grafana panel',
    { capabilities: ['read'] },
    { createdAgo: 2 * DAY, expiresIn: 28 * DAY, usedAgo: null },
  ),
  token(
    'tok_old',
    'Old laptop script',
    { capabilities: ['read', 'send'] },
    { createdAgo: 120 * DAY, expiresIn: -30 * DAY, usedAgo: 31 * DAY },
    'expired',
  ),
  token(
    'tok_demo',
    'Demo for Sam',
    { capabilities: ['read'] },
    { createdAgo: 20 * DAY, expiresIn: 10 * DAY, usedAgo: 6 * DAY },
    'revoked',
    5 * DAY,
  ),
];

/** The token the reveal shows; the secret is a placeholder. */
const ISSUED: TokenCreateResponse = {
  id: 'tok_new',
  kind: 'scoped',
  name: 'CI release bot',
  grant: {
    capabilities: ['read', 'send', 'create'],
    allow: [{ deviceId: 'dev-build', path: '/srv/hoplet' }],
  },
  createdAt: NOW,
  expiresAt: NOW + 90 * DAY,
  revokedAt: null,
  lastUsedAt: null,
  state: 'active',
  token: 'hpt_placeholder_0000000000000000000000000000',
};

export function filledForm(over: Partial<TokenForm> = {}): TokenForm {
  return {
    ...emptyForm(),
    name: 'CI release bot',
    preset: 'create',
    scope: 'only',
    allow: [{ deviceId: 'dev-build', path: '/srv/hoplet' }],
    ...over,
  };
}

export function tokensFixture(
  opts: Partial<TokensFixture> & { empty?: boolean } = {},
): TokensFixture {
  const { empty, ...rest } = opts;
  return {
    list: { tokens: empty ? [] : TOKENS },
    devices: DEVICES,
    projects: PROJECTS,
    now: NOW,
    origin: 'https://hoplet.example.com',
    ...rest,
  };
}

export const issuedFixture = ISSUED;
