import type {
  DeviceInfo,
  HookDelivery,
  HookInfo,
  ScriptHookInfo,
  WebhookHookInfo,
} from '@hoplet/shared';
import { emptyHookForm, formOfHook } from '../../lib/hooks.ts';
import { t } from '../../lib/i18n.ts';
import { fill } from '../../lib/templates.ts';
import { FIXTURE_SECRET } from '../../lib/use-hooks.ts';
import type { HooksFixture } from './hooks.tsx';

// Fixtures for /__states/settings/hooks*: the screens of design/screens/hooks.html, direction A. Nothing here is
// read by the real screen. Times are relative to the moment the fixture is built, so "2 min ago" stays true.

const MIN = 60_000;

function device(deviceId: string, name: string, os: string, connected: boolean): DeviceInfo {
  return {
    deviceId,
    name,
    connected,
    lastSeen: Date.now(),
    system: { os, arch: 'arm64' },
    providers: [],
    labels: [],
  };
}

const MACBOOK = device('dev-macbook', 'MacBook Pro', 'darwin', true);
const STUDIO = device('dev-studio', 'studio', 'linux', true);
const MINI = device('dev-mini', 'mini', 'darwin', false);

const PROJECT = '/Users/dev/work/hoplet';

function hook(
  id: string,
  label: string,
  url: string,
  over: Partial<WebhookHookInfo> = {},
): WebhookHookInfo {
  const now = Date.now();
  return {
    id,
    kind: 'webhook',
    label,
    url,
    events: ['session.error'],
    scope: { kind: 'all' },
    summary: false,
    enabled: true,
    status: 'active',
    version: 1,
    createdAt: now - 19 * 24 * 60 * MIN,
    updatedAt: now - 19 * 24 * 60 * MIN,
    pending: 0,
    ...over,
  };
}

function webhooks(): WebhookHookInfo[] {
  const now = Date.now();
  return [
    hook('hook-slack', 'Slack relay', 'https://ops.example.net/hoplet', {
      events: ['turn.completed', 'session.error', 'permission.requested'],
      summary: true,
      lastDelivery: { at: now - 2 * MIN, state: 'delivered' },
    }),
    hook('hook-ci', 'CI trigger', 'https://ci.example.org/api/hooks/hoplet', {
      events: ['handoff.completed', 'session.ended'],
      scope: { kind: 'project', deviceId: MACBOOK.deviceId, projectPath: PROJECT },
      pending: 1,
      lastDelivery: { at: now - 22 * MIN, state: 'pending', error: 'http-503' },
    }),
    hook('hook-pager', 'Pager', 'https://pager.example.com/v1/hoplet', {
      lastDelivery: { at: now - 60 * MIN, state: 'failed', error: 'http-404' },
    }),
    hook('hook-log', 'Log sink', 'https://logs.example.net/ingest', {
      events: [
        'session.started',
        'turn.completed',
        'session.ended',
        'permission.requested',
        'permission.resolved',
        'session.error',
        'handoff.started',
        'handoff.completed',
      ],
      enabled: false,
      status: 'disabled',
    }),
    hook('hook-relay', 'Old relay', 'https://relay.example.org/hook', {
      status: 'needs-secret',
    }),
  ];
}

function script(
  machine: DeviceInfo,
  slug: string,
  command: string,
  over: Partial<ScriptHookInfo> = {},
): ScriptHookInfo {
  return {
    id: `script:${machine.deviceId}:${slug}`,
    kind: 'script',
    label: slug,
    machine: { id: machine.deviceId, name: machine.name, online: machine.connected },
    command,
    events: ['turn.completed'],
    projects: [],
    timeoutMs: 10_000,
    env: [],
    runs: 0,
    failed: 0,
    dropped: 0,
    enabled: true,
    status: 'active',
    updatedAt: Date.now(),
    pending: 0,
    ...over,
  };
}

function scripts(): ScriptHookInfo[] {
  const now = Date.now();
  return [
    script(MACBOOK, 'notify-done', '/Users/yang/bin/hoplet-done.sh', {
      events: ['turn.completed', 'session.ended'],
      projects: ['~/code/hoplet'],
      env: ['SLACK_TOKEN_FILE'],
      runs: 41,
      lastDelivery: { at: now - 26 * MIN, state: 'delivered' },
    }),
    script(MACBOOK, 'pull-branch', '/Users/yang/bin/pull-branch.sh', {
      events: ['handoff.completed'],
      timeoutMs: 30_000,
      runs: 6,
      failed: 1,
      lastDelivery: { at: now - 52 * MIN, state: 'failed', error: 'exit-1' },
    }),
    script(STUDIO, 'desk-light', '/home/yang/.local/bin/desk-light', {
      events: ['permission.requested', 'session.error'],
      timeoutMs: 5000,
      enabled: false,
      status: 'disabled',
    }),
    script(MINI, 'chime', '/Users/yang/bin/chime'),
  ];
}

function deliveries(): HookDelivery[] {
  const now = Date.now();
  const row = (
    n: number,
    ago: number,
    type: string,
    state: HookDelivery['state'],
    attempts: number,
    extra: Partial<HookDelivery> = {},
  ): HookDelivery => ({
    id: `dlv-${n}`,
    eventId: `evt_${String(n).padStart(26, '0')}`,
    type,
    state,
    attempts,
    createdAt: now - ago * MIN,
    ...extra,
  });
  return [
    row(1, 2, 'turn.completed', 'delivered', 1),
    row(2, 3, 'permission.requested', 'delivered', 1),
    row(3, 22, 'handoff.completed', 'pending', 3, {
      error: 'http-503',
      nextAttemptAt: now + 10 * MIN,
    }),
    row(4, 34, 'turn.completed', 'delivered', 2),
    row(5, 52, 'session.error', 'failed', 1, { error: 'http-404' }),
    row(6, 80, 'session.error', 'failed', 9, { error: 'timeout · gave up' }),
    row(7, 97, 'turn.completed', 'dropped', 0, { error: 'queue full' }),
    row(8, 122, 'hook.test', 'delivered', 1),
  ];
}

function stderrRun(): Record<string, HookDelivery[]> {
  const now = Date.now();
  return {
    'script:dev-macbook:pull-branch': [
      {
        id: 'dlv-run-1',
        eventId: 'evt_00000000000000000000000001',
        type: 'handoff.completed',
        state: 'failed',
        attempts: 1,
        error: 'exit-1',
        createdAt: now - 52 * MIN,
        settledAt: now - 52 * MIN,
        run: {
          exitCode: 1,
          durationMs: 1200,
          stderr: 'error: cannot fast-forward origin/main\nhint: run git pull --rebase first',
        },
      },
    ],
  };
}

function testDelivery(over: Partial<HookDelivery> = {}): HookDelivery {
  const now = Date.now();
  return {
    id: 'dlv-test',
    eventId: 'evt_00000000000000000000000099',
    type: 'hook.test',
    state: 'delivered',
    attempts: 1,
    createdAt: now,
    settledAt: now,
    ...over,
  };
}

type Over = Partial<HooksFixture> & { hooks?: HookInfo[] };

/** The fixture of one screen; `over` changes what the screen starts with. */
export function hooksFixture(over: Over = {}): HooksFixture {
  const { hooks, ...rest } = over;
  return {
    list: { hooks: hooks ?? [...webhooks(), ...scripts()] },
    devices: [MACBOOK, STUDIO, MINI],
    projects: {
      [MACBOOK.deviceId]: [PROJECT, '/Users/dev/work/hoplet-site'],
      [STUDIO.deviceId]: ['/home/dev/work/api'],
    },
    deliveries: { 'hook-slack': deliveries(), 'hook-ci': deliveries(), ...stderrRun() },
    ...rest,
  };
}

export const hooksStates = {
  list: () => hooksFixture(),
  deliveries: () => hooksFixture({ open: 'hook-slack' }),
  needsSecret: () => hooksFixture({ open: 'hook-relay' }),
  add: () =>
    hooksFixture({
      form: {
        ...emptyHookForm(),
        label: 'Deploy notifier',
        url: 'https://deploy.example.org/hoplet',
        events: ['session.ended', 'handoff.completed'],
      },
    }),
  edit: () => {
    const f = hooksFixture();
    const ci = f.list.hooks.find((h) => h.id === 'hook-ci');
    return { ...f, form: ci?.kind === 'webhook' ? formOfHook(ci) : emptyHookForm() };
  },
  secret: () =>
    hooksFixture({
      secret: {
        title: fill(t('Hook added · {host}'), { host: 'deploy.example.org' }),
        secret: FIXTURE_SECRET,
        dialog: false,
      },
    }),
  rotateConfirm: () => hooksFixture({ confirm: 'hook-slack' }),
  rotateSecret: () =>
    hooksFixture({
      secret: {
        title: fill(t('New secret · {name}'), { name: 'Slack relay' }),
        secret: FIXTURE_SECRET,
        until: Date.now() + 24 * 60 * MIN,
        dialog: true,
      },
    }),
  test: () => hooksFixture({ test: { id: 'hook-slack', delivery: testDelivery() } }),
  testBad: () =>
    hooksFixture({
      test: {
        id: 'hook-ci',
        delivery: testDelivery({ state: 'failed', error: 'blocked-address' }),
      },
    }),
  scriptRun: () => hooksFixture({ open: 'script:dev-macbook:pull-branch' }),
  empty: () => hooksFixture({ hooks: [] }),
};
