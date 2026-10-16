import { appendFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { expect, type Page, test } from '@playwright/test';
import { type Agent, Stack, until } from './stack.ts';
import { open, send, stateOf, unlock } from './ui.ts';

// docs/spec.md#remote-control, runtime states: every state the web UI shows is reached through the real
// agent and the scripted fake Claude Code, and the session stays usable after a Deny, a failed turn and a
// crash. A session written from a terminal is `external` and refuses prompts.
test.describe.configure({ mode: 'serial' });

const TOUR = { id: '33333333-4444-4555-8666-777777777701', title: 'Walk the runtime states' };
const OUTSIDE = { id: '33333333-4444-4555-8666-777777777702', title: 'Written from a terminal' };
const BROKEN = { id: '33333333-4444-4555-8666-777777777703', title: 'Start without a Claude Code' };

let stack: Stack;
let agent: Agent;
let broken: Agent;
let page: Page;
let outsideFile = '';

test.beforeAll(async ({ browser }) => {
  stack = await Stack.start(mkdtempSync(join(tmpdir(), 'hoplet-e2e-runtime.')));
  const work = join(stack.tmp, 'work');
  // A cold start takes 2.5 s so that the `starting` state can be seen.
  agent = await stack.agent({
    name: 'e2e-runtime',
    deviceId: 'dev-runtime',
    env: { FAKE_CLAUDE_INIT_DELAY_MS: '2500' },
  });
  agent.seed(join(work, 'tour'), TOUR.id, TOUR.title);
  outsideFile = agent.seed(join(work, 'outside'), OUTSIDE.id, OUTSIDE.title);
  agent.watch(join(work, 'tour'), [TOUR.id]);
  agent.watch(join(work, 'outside'), [OUTSIDE.id]);
  // The fake exits before it answers `initialize`: the process cannot start.
  broken = await stack.agent({
    name: 'e2e-broken',
    deviceId: 'dev-broken',
    env: { FAKE_CLAUDE_EXIT_ON_START: '1' },
  });
  broken.seed(join(work, 'broken'), BROKEN.id, BROKEN.title);
  broken.watch(join(work, 'broken'), [BROKEN.id]);
  agent.start();
  broken.start();
  for (const id of [TOUR.id, OUTSIDE.id, BROKEN.id])
    await until(
      `${id} to sync`,
      60_000,
      async () => ((await stack.session(id))?.lineCount ?? 0) > 0,
    );
  page = await browser.newPage({ viewport: { width: 1280, height: 800 }, locale: 'en-US' });
  await unlock(page, stack);
});

test.afterAll(async () => {
  await page?.close();
  await stack?.stop();
});

test('an idle session shows idle and accepts a prompt', async () => {
  await open(page, TOUR.title);
  await expect(stateOf(page)).toHaveAttribute('data-state', 'idle');
  await expect(page.getByTestId('cockpit.composer-input')).toBeEnabled();
  await expect.poll(async () => (await stack.session(TOUR.id))?.runtime.state).toBe('idle');
});

test('a prompt goes through starting and running, Interrupt leaves it ready and Stop makes it idle', async () => {
  const conv = stateOf(page);
  await send(page, 'sleep');
  await expect(conv).toHaveAttribute('data-state', 'starting');
  await expect(page.getByTestId('cockpit.composer-input')).toBeDisabled();
  await expect(conv).toHaveAttribute('data-state', 'running');
  await page.getByTestId('cockpit.interrupt').click();
  await expect(conv).toHaveAttribute('data-state', 'ready');
  await expect(page.getByTestId('cockpit.composer-state')).toHaveAttribute('title', /Ready/);
  // The session is usable after an interrupt, with the process kept.
  await send(page, 'say usable after the interrupt');
  await expect(conv.getByText('usable after the interrupt', { exact: true })).toBeVisible();
  await expect(conv).toHaveAttribute('data-state', 'ready');
  await page.getByTestId('cockpit.stop').click();
  await expect(conv).toHaveAttribute('data-state', 'idle');
});

test('a permission request waits, Deny is reported to the model and the session stays usable', async () => {
  const conv = stateOf(page);
  await send(page, 'tool echo denied');
  await expect(conv).toHaveAttribute('data-state', 'waiting-permission');
  const card = page.getByTestId('cockpit.permission');
  await expect(card).toContainText('echo denied');
  // The composer stays open to queue a message; steering waits for the answer (ADR 0020).
  await expect(page.getByTestId('cockpit.composer-input')).toBeEnabled();
  await card.getByTestId('cockpit.permission-deny').click();
  await expect(card).toHaveCount(0);
  await expect(conv.getByText('REFUSED', { exact: true })).toBeVisible();
  await expect(conv).toHaveAttribute('data-state', 'ready');
  await send(page, 'say usable after the deny');
  await expect(conv.getByText('usable after the deny', { exact: true })).toBeVisible();
});

test('a failed turn shows failed and the next prompt runs', async () => {
  const conv = stateOf(page);
  await send(page, 'fail');
  await expect(conv).toHaveAttribute('data-state', 'failed');
  await expect.poll(async () => (await stack.session(TOUR.id))?.runtime.error).toContain('boom');
  await expect(page.getByTestId('cockpit.composer-input')).toBeEnabled();
  await send(page, 'say usable after the failure');
  await expect(conv.getByText('usable after the failure', { exact: true })).toBeVisible();
  await expect(conv).toHaveAttribute('data-state', 'ready');
});

test('a crashed process shows failed and the next prompt starts a new one', async () => {
  const conv = stateOf(page);
  await send(page, 'crash');
  await expect(conv).toHaveAttribute('data-state', 'failed');
  await expect.poll(async () => (await stack.session(TOUR.id))?.runtime.error).toContain('exited');
  await send(page, 'say usable after the crash');
  await expect(conv.getByText('usable after the crash', { exact: true })).toBeVisible();
  await expect(conv).toHaveAttribute('data-state', 'ready');
});

test('a process that cannot start shows failed with Retry', async () => {
  await open(page, BROKEN.title);
  const conv = stateOf(page);
  await expect(conv).toHaveAttribute('data-state', 'idle');
  await send(page, 'say never runs');
  await expect(conv).toHaveAttribute('data-state', 'failed');
  await expect(page.getByTestId('cockpit.composer-state')).toContainText('Failed to start');
  await expect(page.getByTestId('cockpit.retry')).toBeVisible();
});

test('a session written from a terminal shows external and refuses prompts', async () => {
  await open(page, OUTSIDE.title);
  const conv = stateOf(page);
  await expect(conv).toHaveAttribute('data-state', 'idle');
  // A terminal appends a turn while no Hoplet process runs for the session.
  const written = (text: string) =>
    `${JSON.stringify({
      type: 'user',
      sessionId: OUTSIDE.id,
      cwd: join(stack.tmp, 'work', 'outside'),
      timestamp: new Date().toISOString(),
      message: { role: 'user', content: text },
    })}\n`;
  appendFileSync(outsideFile, written('typed in a terminal'));
  await expect(conv).toHaveAttribute('data-state', 'external');
  await expect(page.getByTestId('cockpit.composer-input')).toHaveCount(0);
  await expect(page.getByTestId('cockpit.composer')).toContainText('Read only');
  const refused = await stack.prompt(OUTSIDE.id, 'say should be refused');
  expect(refused.status).toBe(409);
  expect(refused.body).toMatchObject({ code: 'external' });
  await expect(conv.getByText('typed in a terminal', { exact: true })).toBeVisible();
  // The refused prompt did not reach the file.
  expect((await stack.session(OUTSIDE.id))?.runtime.state).toBe('external');
});

test('a session whose owner is gone shows offline and comes back when the agent does', async () => {
  await open(page, TOUR.title);
  await agent.kill('SIGKILL');
  const conv = stateOf(page);
  await expect(conv).toHaveAttribute('data-state', 'offline');
  await expect(page.getByTestId('cockpit.composer-input')).toHaveCount(0);
  await expect(page.getByTestId('cockpit.handoff')).toBeVisible();
  const refused = await stack.prompt(TOUR.id, 'say nobody listens');
  expect(refused.status).toBe(409);
  expect(refused.body).toMatchObject({ code: 'owner-offline' });
  agent.start();
  await expect(conv).not.toHaveAttribute('data-state', 'offline');
  await expect(page.getByTestId('cockpit.composer-input')).toBeEnabled();
});
