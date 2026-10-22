import type {
  AuditEvent,
  AuditVerifyResponse,
  DeviceInfo,
  LiveSessionMeta,
  ProviderInfo,
} from '@hoplet/shared';
import type { AuditFilters } from '../../lib/audit.ts';
import type { AuditSource, VerifyState } from '../../lib/use-audit.ts';
import type { AuditFixture } from './audit.tsx';

// Fixtures of Settings > Audit (design/screens/audit.html). Nothing here is read by the real screen.

/** Thursday 22 October 2026, 14:40, in the time zone of the browser, so the day groups read the same everywhere. */
const NOW = new Date(2026, 9, 22, 14, 40, 0).getTime();
const at = (day: number, h: number, m: number, s = 0) => new Date(2026, 9, day, h, m, s).getTime();

const HEAD = 1285;
const PROJECT = '/Users/mark/github/repopro/hoplet';
const DOCS = '/Users/mark/github/repopro/docs';
const FIX_INGEST = '8f1c2d3e-4a5b-4c6d-8e7f-90a1b2c3d4e5';
const DOCS_SWEEP = '2b3c4d5e-6f70-4812-9a3b-4c5d6e7f8091';

function claude(): ProviderInfo {
  return {
    provider: 'claude',
    configuration: {
      id: 'claude',
      name: 'Claude Code',
      extends: 'claude',
      enabled: true,
      env: [],
      defaults: {},
    },
    version: '2.1.0',
    tested: true,
    capabilities: { permissions: true, interrupt: true, images: true, steer: true, config: [] },
  };
}

function device(deviceId: string, name: string, os: string): DeviceInfo {
  return {
    deviceId,
    name,
    connected: true,
    lastSeen: NOW,
    system: { os, arch: 'arm64' },
    providers: [claude()],
    labels: [],
  };
}

const DEVICES: DeviceInfo[] = [
  device('dev-macbook', 'MacBook Pro', 'darwin'),
  device('dev-studio', 'studio', 'linux'),
];

function session(
  sessionId: string,
  title: string,
  deviceId: string,
  deviceName: string,
  projectPath: string,
): LiveSessionMeta {
  return {
    sessionId,
    projectPath,
    projectDir: projectPath.replaceAll('/', '-'),
    deviceName,
    ownerDeviceId: deviceId,
    ownerEpoch: 1,
    title,
    provider: 'claude',
    createdAt: at(22, 14, 18),
    updatedAt: at(22, 14, 32),
    lineCount: 240,
    byteSize: 480_000,
    ownerConnected: true,
    runtime: { state: 'idle' },
  };
}

const SESSIONS: LiveSessionMeta[] = [
  session(FIX_INGEST, 'fix-ingest', 'dev-macbook', 'MacBook Pro', PROJECT),
  session(DOCS_SWEEP, 'docs-sweep', 'dev-studio', 'studio', DOCS),
];

/** A stand-in SHA-256: stable, 64 hex characters, different for every seed. */
function hash(seed: number): string {
  let x = (seed * 2_654_435_761) >>> 0;
  let out = '';
  while (out.length < 64) {
    x = (Math.imul(x, 1_664_525) + 1_013_904_223) >>> 0;
    out += x.toString(16).padStart(8, '0');
  }
  return out;
}

type Row = Omit<AuditEvent, 'seq' | 'recordedAt' | 'prevHash' | 'hash'>;

const onMac = { sessionId: FIX_INGEST, deviceId: 'dev-macbook' };
const bySession = { actorKind: 'session', actorId: FIX_INGEST } as const;
const byOperator = { actorKind: 'operator', actorId: 'operator' } as const;
const CURL = 'curl -s -H "Authorization: Bearer ***" https://api.example.net/v1/jobs';

// Newest first; the seq counts down from the head of the chain.
const ROWS: Row[] = [
  {
    at: at(22, 14, 32),
    type: 'tool.call',
    ...onMac,
    ...bySession,
    channel: 'agent',
    ref: null,
    summary: { tool: 'Bash', command: 'pnpm test --filter core' },
  },
  {
    at: at(22, 14, 31),
    type: 'permission.answered',
    ...onMac,
    ...byOperator,
    channel: 'rest',
    ref: null,
    summary: { tool: 'Bash', decision: 'allow', command: 'pnpm test --filter core' },
  },
  {
    at: at(22, 14, 31),
    type: 'permission.requested',
    ...onMac,
    ...bySession,
    channel: 'agent',
    ref: null,
    summary: { tool: 'Bash', command: 'pnpm test --filter core' },
  },
  {
    at: at(22, 14, 29),
    type: 'tool.call',
    ...onMac,
    ...bySession,
    channel: 'agent',
    ref: null,
    summary: {
      tool: 'Edit',
      paths: ['core/src/ingest/append.ts', 'core/src/ingest/append.test.ts'],
      pathCount: 2,
    },
  },
  {
    at: at(22, 14, 24),
    type: 'tool.call',
    ...onMac,
    ...bySession,
    channel: 'agent',
    ref: 'toolu_01HxK3q',
    summary: { tool: 'Bash', command: CURL },
  },
  {
    at: at(22, 14, 20),
    type: 'permission.auto',
    ...onMac,
    actorKind: 'core',
    actorId: 'core',
    channel: 'core',
    ref: null,
    summary: { tool: 'Bash', decision: 'allow', ruleId: 'Run pnpm check', command: 'pnpm check' },
  },
  {
    at: at(22, 14, 20),
    type: 'mode.changed',
    ...onMac,
    ...byOperator,
    channel: 'rest',
    ref: null,
    summary: { option: 'Default', value: 'Accept edits' },
  },
  {
    at: at(22, 14, 18),
    type: 'prompt.sent',
    ...onMac,
    ...byOperator,
    channel: 'rest',
    ref: null,
    summary: { length: 214 },
  },
  {
    at: at(22, 14, 18),
    type: 'session.created',
    ...onMac,
    ...byOperator,
    channel: 'rest',
    ref: null,
    summary: { projectPath: PROJECT },
  },
  {
    at: at(22, 12, 5),
    type: 'audit.exported',
    sessionId: null,
    deviceId: null,
    ...byOperator,
    channel: 'rest',
    ref: null,
    summary: { count: 61, format: 'csv' },
  },
  {
    at: at(21, 17, 44),
    type: 'session.handoff',
    sessionId: DOCS_SWEEP,
    deviceId: 'dev-studio',
    ...byOperator,
    channel: 'rest',
    ref: null,
    summary: { from: 'dev-macbook', to: 'dev-studio', outcome: 'done' },
  },
  {
    at: at(21, 17, 10),
    type: 'config.changed',
    sessionId: null,
    deviceId: null,
    ...byOperator,
    channel: 'rest',
    ref: null,
    summary: { area: 'permission-rules', action: 'created', fields: ['Run pnpm check'] },
  },
  {
    at: at(21, 16, 52),
    type: 'config.changed',
    sessionId: null,
    deviceId: null,
    ...byOperator,
    channel: 'rest',
    ref: null,
    summary: { area: 'webhooks', action: 'created', fields: ['ops.example.net'] },
  },
  {
    at: at(21, 9, 3),
    type: 'device.token.issued',
    sessionId: null,
    deviceId: null,
    ...byOperator,
    channel: 'rest',
    ref: null,
    summary: { name: 'iPhone' },
  },
];

export const AUDIT_EVENTS: AuditEvent[] = ROWS.map((row, i) => {
  const seq = HEAD - i;
  return { ...row, seq, recordedAt: row.at + 7000, prevHash: hash(seq - 1), hash: hash(seq) };
});

/** The record the detail state opens: the tool call with the scrubbed command. */
const OPEN_SEQ = HEAD - 4;

const OK: AuditVerifyResponse = {
  ok: true,
  checked: HEAD,
  head: { seq: HEAD, hash: hash(HEAD) },
  firstBreak: null,
};

const BROKEN: AuditVerifyResponse = {
  ok: false,
  checked: 1031,
  head: { seq: HEAD, hash: hash(HEAD) },
  firstBreak: {
    seq: 1032,
    reason: 'hash-mismatch',
    message: 'The hash of record 1032 does not match its content.',
  },
};

type Options = {
  events?: AuditEvent[];
  next?: number | null;
  verify?: VerifyState;
  loading?: boolean;
  error?: string | null;
  filters?: Partial<AuditFilters>;
  openSeq?: number;
  menuOpen?: boolean;
  toast?: string;
};

function fixture(over: Options = {}): AuditFixture {
  const source: AuditSource = {
    events: over.events ?? AUDIT_EVENTS,
    next: over.next ?? null,
    now: NOW,
    ...(over.verify ? { verify: over.verify } : {}),
    ...(over.loading ? { loading: true } : {}),
    ...(over.error !== undefined ? { error: over.error } : {}),
  };
  return {
    source,
    devices: DEVICES,
    sessions: SESSIONS,
    ...(over.filters ? { filters: over.filters } : {}),
    ...(over.openSeq !== undefined ? { openSeq: over.openSeq } : {}),
    ...(over.menuOpen ? { menuOpen: true } : {}),
    ...(over.toast ? { toast: over.toast } : {}),
  };
}

export const auditPage = () => fixture({ next: HEAD - AUDIT_EVENTS.length });
export const auditFiltered = () =>
  fixture({ filters: { type: 'permission.', q: 'pnpm' }, next: null });
export const auditDetail = () => fixture({ openSeq: OPEN_SEQ, next: HEAD - AUDIT_EVENTS.length });
export const auditVerifyRunning = () => fixture({ verify: { status: 'running' } });
export const auditVerifyOk = () => fixture({ verify: { status: 'done', result: OK } });
export const auditVerifyBroken = () => fixture({ verify: { status: 'done', result: BROKEN } });
export const auditVerifyError = () =>
  fixture({
    verify: { status: 'error', message: 'The audit log could not be read. Try again in a moment.' },
  });
export const auditExport = () => fixture({ menuOpen: true });
export const auditExported = () => fixture({ toast: 'Export saved' });
export const auditNoMatch = () => fixture({ filters: { q: 'terraform' } });
export const auditEmpty = () => fixture({ events: [] });
export const auditLoading = () => fixture({ loading: true, events: [] });
export const auditError = () =>
  fixture({ events: [], error: 'The audit log could not be read. Try again in a moment.' });
