import type { DeviceInfo, SessionMcpDefaultsResponse, SessionMcpSettings } from '@hoplet/shared';

// Fixtures of the Other sessions screens (design/screens/session-mcp.html) for /__states: the inspector tab and
// Settings. Nothing here is read by the real screens.

const NOW = Date.UTC(2026, 9, 22, 12, 0, 0);

const machine = (deviceId: string, name: string, os: string, sessionMcp: boolean): DeviceInfo => ({
  deviceId,
  name,
  connected: true,
  lastSeen: NOW,
  system: { os, arch: 'arm64' },
  providers: [],
  labels: [],
  sessionMcp,
});

/** Three machines; `miniOn` says whether the mini's agent has `session_mcp` set. */
export function mcpDevices(miniOn = false, macbookOn = true): DeviceInfo[] {
  return [
    machine('dev-macbook', 'MacBook Pro', 'darwin', macbookOn),
    machine('dev-studio', 'studio', 'darwin', true),
    machine('dev-mini', 'mini', 'linux', miniOn),
  ];
}

export const OFF_SETTINGS: SessionMcpSettings = {
  send: false,
  create: false,
  requestHandoff: false,
  autoApproveHandoff: false,
  autoApproveMachines: [],
  visibility: 'project',
  readConfig: true,
  writeConfig: false,
};

/** Send, Start, hand-off and proposing configuration changes on, hand-off approved for studio; reaches the machine. */
export const ON_SETTINGS: SessionMcpSettings = {
  send: true,
  create: true,
  requestHandoff: true,
  autoApproveHandoff: true,
  autoApproveMachines: ['dev-studio'],
  visibility: 'machine',
  readConfig: true,
  writeConfig: true,
};

export const WIDE_SETTINGS: SessionMcpSettings = {
  send: true,
  create: false,
  requestHandoff: true,
  autoApproveHandoff: false,
  autoApproveMachines: [],
  visibility: 'all',
  readConfig: true,
  writeConfig: false,
};

const SEND_ONLY: SessionMcpSettings = { ...OFF_SETTINGS, send: true };

export const MCP_DEFAULTS: SessionMcpDefaultsResponse = {
  global: OFF_SETTINGS,
  projects: [
    { deviceId: 'dev-macbook', path: '/Users/mark/work/hoplet', settings: ON_SETTINGS },
    { deviceId: 'dev-studio', path: '/Users/mark/src/hoplet', settings: SEND_ONLY },
    { deviceId: 'dev-macbook', path: '/Users/mark/work/docs-site', settings: OFF_SETTINGS },
    { deviceId: 'dev-mini', path: '/Users/mark/ops/infra', settings: WIDE_SETTINGS },
  ],
};

export const EMPTY_DEFAULTS: SessionMcpDefaultsResponse = { global: OFF_SETTINGS, projects: [] };

/** The directories each machine offers for a new project default. */
export const MCP_PROJECTS: Record<string, string[]> = {
  'dev-macbook': [
    '/Users/mark/work/hoplet',
    '/Users/mark/work/docs-site',
    '/Users/mark/work/notes',
  ],
  'dev-studio': ['/Users/mark/src/hoplet'],
  'dev-mini': ['/Users/mark/ops/infra'],
};
