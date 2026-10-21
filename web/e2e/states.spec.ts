import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { expect, test } from '@playwright/test';

// Every state of the cockpit, the workspace and Settings, in both schemes, against the full axe rule set including colour contrast.
const axeSource = readFileSync(
  createRequire(import.meta.url).resolve('axe-core/axe.min.js'),
  'utf8',
);
/** The states a screen registers: the keys of its `states` object, in order. */
function stateNames(screen: string): string[] {
  const source = readFileSync(
    new URL(`../src/screens/${screen}/states.tsx`, import.meta.url),
    'utf8',
  );
  const states = source.slice(source.indexOf('export const states'));
  return [...states.matchAll(/^ {2}(?:'([\w-]+)'|(\w+)): /gm)].map((m) => m[1] ?? m[2] ?? '');
}
const screens = {
  cockpit: stateNames('cockpit'),
  workspace: stateNames('workspace'),
  settings: stateNames('settings'),
  action: stateNames('action'),
};

// What each state shows that axe must have seen: a state that lost its subject would pass for nothing.
const subject: Record<string, Record<string, string>> = {
  cockpit: {
    draft: 'cockpit.draft-row',
    'draft-directory': 'cockpit.draft-add-project',
    'draft-model': 'cockpit.model-menu',
    'draft-configuration': 'cockpit.configuration-menu',
    'session-configuration': 'cockpit.configuration-tag',
    machines: 'cockpit.device-toggle',
    'machines-draft': 'cockpit.draft-machine',
    'machines-add': 'cockpit.add-machine',
    resources: 'cockpit.resource-warning',
    'resources-menu': 'cockpit.machine-resources',
    compose: 'cockpit.thumbs',
    'model-menu': 'cockpit.model-menu',
    'mode-manual': 'cockpit.mode',
    'mode-menu': 'cockpit.mode-menu',
    'mode-bypass': 'cockpit.mode',
    'mode-plan': 'cockpit.mode',
    'add-project': 'cockpit.add-dialog',
    'add-project-recent': 'cockpit.add-recent',
    'add-project-error': 'cockpit.add-error',
    running: 'cockpit.live-turn',
    'waiting-permission': 'cockpit.permission',
    'queue-menu': 'cockpit.send-options',
    'queue-queued': 'queue.tray',
    'queue-editing': 'queue.edit-input',
    'queue-dragging': 'queue.tray',
    'queue-steered': 'cockpit.steer-pending',
    'queue-paused-permission': 'queue.head',
    'queue-paused-error': 'queue.resume',
    'queue-paused-interrupted': 'queue.resume',
    'plan-pending': 'cockpit.plan',
    'plan-long': 'cockpit.plan-open',
    'plan-modes': 'cockpit.plan-mode-menu',
    'plan-feedback': 'cockpit.plan-feedback',
    'plan-offline': 'cockpit.plan-offline',
    'plan-approved': 'cockpit.plan-resolved',
    'plan-kept': 'cockpit.plan-resolved',
    'plan-elsewhere': 'cockpit.plan-resolved',
    'plan-fresh-menu': 'cockpit.plan-fresh-option',
    'plan-fresh-confirm': 'cockpit.plan-fresh-confirm',
    'plan-fresh-starting': 'cockpit.plan-fresh-starting',
    'plan-fresh-failed': 'cockpit.plan-fresh-error',
    'plan-fresh-done': 'cockpit.plan-fresh-done',
    'plan-fresh-new': 'cockpit.plan-origin',
    'failed-turn': 'cockpit.live-error',
    'elided-open': 'cockpit.block-full',
    search: 'cockpit.palette',
    'sidebar-flat': 'cockpit.session-where',
    'sidebar-menu': 'cockpit.machine-popover',
    'session-usage': 'cockpit.usage-chip',
    'session-usage-open': 'cockpit.usage-popover',
    'session-usage-unknown': 'cockpit.usage-popover',
    'session-actions': 'cockpit.actions-menu',
    'session-actions-copy': 'menu.copy-id',
    'rename-dialog': 'cockpit.rename',
    'session-menu-delete': 'menu.delete',
    delete: 'cockpit.delete-dialog',
    'delete-worktree': 'cockpit.delete-opt-worktree',
    'delete-dirty': 'cockpit.delete-opt-worktree',
    'delete-offline': 'cockpit.delete-offline',
    'delete-running': 'cockpit.delete-running',
    'delete-progress': 'cockpit.delete-steps',
    'delete-partial': 'cockpit.delete-retry',
    pinned: 'cockpit.pinned',
    'pinned-flat': 'cockpit.pinned',
    archived: 'cockpit.archived-page',
    'archived-empty': 'cockpit.archived-empty',
    'copy-toast': 'toast',
    'completions-commands': 'cockpit.completions',
    'completions-filter': 'cockpit.completions',
    'completions-draft': 'cockpit.completions',
    'mention-files': 'cockpit.completions',
    'mention-loading': 'cockpit.completion-status',
    'mention-empty': 'cockpit.completion-status',
    'mention-offline': 'cockpit.completion-status',
    'command-sent': 'cockpit.command-chip',
    'command-output': 'cockpit.command-output',
    'command-compact': 'cockpit.compact-boundary',
  },
  workspace: {
    workspace: 'workspace.files',
    'file-open': 'workspace.code',
    'file-image': 'workspace.viewer',
    changes: 'workspace.changes',
    'tools-menu': 'workspace.tools-menu',
    'diff-open': 'workspace.fold',
    'diff-large': 'workspace.cut',
    'diff-wrap': 'workspace.cut',
    'diff-binary': 'workspace.before-after',
    'file-binary': 'workspace.binary',
    'changes-empty': 'workspace.no-changes',
    'files-truncated': 'workspace.truncated',
    'workspace-loading': 'workspace.loading',
    'workspace-error': 'workspace.error',
    'changes-offline': 'workspace.snapshot',
    'diff-offline': 'workspace.viewer',
    'offline-no-snapshot': 'workspace.no-snapshot',
    'no-git': 'workspace.no-git',
    handoff: 'workspace.handoff-clone',
    'handoff-checking': 'workspace.handoff-panel',
    'handoff-clone': 'workspace.handoff-suggest',
    'handoff-no-access': 'workspace.handoff-no-access',
    'handoff-browse': 'workspace.clone-dialog',
    'handoff-browse-exists': 'workspace.clone-error',
    'handoff-browse-readonly': 'workspace.clone-error',
    'handoff-browse-outside': 'workspace.clone-error',
    'handoff-cloning': 'workspace.handoff-cancel',
    'handoff-clone-failed': 'workspace.handoff-failed',
    'handoff-clone-continue': 'workspace.handoff-panel',
    'terminal-off': 'workspace.terminal-off',
    'terminal-blocked-turn': 'workspace.terminal-blocked-turn-running',
    'terminal-blocked-external': 'workspace.terminal-blocked-external',
    'terminal-blocked-handoff': 'workspace.terminal-blocked-handoff-running',
    'terminal-blocked-offline': 'workspace.terminal-blocked-owner-offline',
    'terminal-ready': 'workspace.terminal-ready',
    'terminal-open': 'workspace.terminal-view',
    'terminal-closed-turn': 'workspace.terminal-closed-turn',
    'terminal-closed-idle': 'workspace.terminal-closed-idle',
    'terminal-closed-offline': 'workspace.terminal-closed-offline',
    'terminal-closed-exit': 'workspace.terminal-closed-generic',
    'terminal-busy': 'workspace.terminal-busy',
    'browser-ports': 'workspace.port',
    'browser-loopback': 'workspace.browser-loopback',
    'browser-empty': 'workspace.browser-empty',
    'browser-offline': 'workspace.browser-offline',
    'handoff-path': 'workspace.handoff-path',
    'handoff-busy': 'cockpit.live-turn',
    'handoff-progress': 'workspace.handoff-panel',
    'handoff-failed': 'workspace.handoff-failed',
    'handoff-done': 'workspace.handoff-done',
    'handoff-offline': 'workspace.handoff-snapshot',
    'handoff-configuration': 'workspace.handoff-configuration',
    'handoff-configuration-pick': 'workspace.handoff-configuration',
    'handoff-configuration-enable': 'workspace.handoff-configuration-enable',
    'handoff-configuration-enabling': 'workspace.handoff-configuration-enable',
    'handoff-configuration-none': 'workspace.handoff-configuration-none',
  },
  settings: {
    settings: 'settings.theme',
    'settings-text': 'settings.preview',
    'settings-text-geist': 'settings.preview',
    'settings-machines': 'settings.machines.tabs',
    'settings-resources': 'settings.resources.sessions',
    'settings-resources-offline': 'settings.resources.stale',
    'settings-thresholds': 'settings.thresholds.summary',
    'settings-thresholds-default': 'settings.thresholds.memory',
    'settings-thresholds-custom': 'settings.thresholds.memory',
    'settings-capabilities': 'settings.capabilities.toolchains',
    'settings-capabilities-mac': 'settings.capabilities.gpu',
    'settings-capabilities-label-add': 'settings.capabilities.input',
    'settings-capabilities-label-remove': 'settings.capabilities.removed',
    'settings-capabilities-label-invalid': 'settings.capabilities.invalid',
    'settings-capabilities-sparse': 'settings.capabilities.toolchains',
    'settings-capabilities-offline': 'settings.capabilities.at',
    'settings-capabilities-none': 'settings.capabilities.none',
    'settings-usage': 'settings.usage.chart',
    'settings-usage-session': 'settings.usage.table.session',
    'settings-usage-machine': 'settings.usage.table.machine',
    'settings-usage-custom': 'settings.usage.first',
    'settings-usage-tokens-only': 'settings.usage.footnote',
    'settings-usage-unknown-model': 'settings.usage.unknown',
    'settings-usage-empty': 'settings.usage.empty',
    'settings-usage-backfill': 'settings.usage.backfill',
    'settings-prices': 'settings.prices.table',
    'settings-prices-edit': 'settings.prices.editor',
    'settings-prices-tokens-only': 'settings.prices.tokens-only',
    mint: 'settings.mint.config',
    'mint-waiting': 'settings.mint.waiting',
    'mint-online': 'settings.mint.online',
    'settings-providers': 'settings.providers.list',
    'settings-providers-edit': 'settings.providers.form',
    'settings-providers-saving': 'settings.providers.form',
    'settings-providers-error': 'settings.providers.error',
    'settings-providers-add': 'settings.providers.add',
    'settings-providers-offline': 'settings.providers.offline',
    'settings-providers-none': 'settings.providers.none',
    'settings-providers-empty': 'settings.providers.empty',
    'settings-notify': 'settings.notify.presets',
    'settings-notify-overrides': 'settings.notify.event.permission',
    'settings-notify-off': 'settings.notify.push',
    'settings-notify-blocked': 'settings.notify.blocked',
    'notify-hook': 'settings.notify.hook.events',
    'notify-hook-new': 'settings.notify.hook.events',
  },
  action: {
    confirm: 'action.card',
    'confirm-plan': 'action.card',
    'confirm-answered': 'action.answered',
    'confirm-expired': 'action.expired',
    'confirm-used': 'action.used',
    'confirm-webui': 'action.webui',
    'hint-blocked': 'notify.hint',
  },
};

test('the registries list 88 cockpit, 56 workspace, 45 settings and 7 action states', () => {
  expect(screens.cockpit).toHaveLength(88);
  expect(screens.workspace).toHaveLength(56);
  expect(screens.settings).toHaveLength(45);
  expect(screens.action).toHaveLength(7);
});

test("a screen's styles stay on that screen: the conversation's tool rows stack", async ({
  page,
}) => {
  await page.goto('/__states/cockpit/plan-approved?scheme=light&lang=en');
  await expect(page.getByTestId('cockpit.plan-resolved').first()).toBeVisible();
  const display = await page
    .locator('.acts')
    .first()
    .evaluate((el) => getComputedStyle(el).display);
  expect(display).toBe('block');
});

for (const [screen, names] of Object.entries(screens)) {
  for (const scheme of ['light', 'dark'] as const) {
    for (const state of names) {
      test(`${screen}/${state} (${scheme}) has no axe violations`, async ({ page }) => {
        await page.goto(`/__states/${screen}/${state}?scheme=${scheme}&lang=en`);
        await expect(page.locator('#root > *').first()).toBeVisible();
        const want = subject[screen]?.[state];
        const shown = want ? page.getByTestId(want) : null;
        if (shown) await expect(shown.first()).toBeVisible();
        await page.evaluate(() => document.fonts.ready);
        await page.addScriptTag({ content: axeSource });
        const violations = await page.evaluate(async () => {
          // @ts-expect-error axe is injected above
          const result = await window.axe.run(document, { resultTypes: ['violations'] });
          return result.violations.map(
            (v: {
              id: string;
              help: string;
              nodes: { target: unknown[]; failureSummary?: string }[];
            }) => ({
              id: v.id,
              help: v.help,
              nodes: v.nodes.map((n) => ({ target: n.target, summary: n.failureSummary })),
            }),
          );
        });
        expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
        if (shown) await expect(shown.first()).toBeVisible();
      });
    }
  }
}
