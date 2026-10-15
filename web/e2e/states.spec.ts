import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { expect, test } from '@playwright/test';

// Every state of the cockpit and the workspace, in both schemes, against the full axe rule set including colour contrast.
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
const screens = { cockpit: stateNames('cockpit'), workspace: stateNames('workspace') };

// What each state shows that axe must have seen: a state that lost its subject would pass for nothing.
const subject: Record<string, Record<string, string>> = {
  cockpit: {
    draft: 'cockpit.draft-row',
    'draft-directory': 'cockpit.draft-add-project',
    'draft-model': 'cockpit.model-menu',
    compose: 'cockpit.thumbs',
    'model-menu': 'cockpit.model-menu',
    'add-project': 'cockpit.add-dialog',
    'add-project-recent': 'cockpit.add-recent',
    'add-project-error': 'cockpit.add-error',
    running: 'cockpit.live-turn',
    'waiting-permission': 'cockpit.permission',
    'failed-turn': 'cockpit.live-error',
    'elided-open': 'cockpit.block-full',
    search: 'cockpit.palette',
    'sidebar-flat': 'cockpit.session-where',
    'sidebar-menu': 'cockpit.machine-popover',
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
    'handoff-path': 'workspace.handoff-path',
    'handoff-busy': 'cockpit.live-turn',
    'handoff-progress': 'workspace.handoff-panel',
    'handoff-failed': 'workspace.handoff-failed',
    'handoff-done': 'workspace.handoff-done',
    'handoff-offline': 'workspace.handoff-snapshot',
  },
};

test('the registries list 26 cockpit and 26 workspace states', () => {
  expect(screens.cockpit).toHaveLength(26);
  expect(screens.workspace).toHaveLength(26);
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
