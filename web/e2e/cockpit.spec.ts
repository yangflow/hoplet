import { expect, type Page, test } from '@playwright/test';
import {
  DEVICE,
  LONG_SESSION,
  LONG_TITLE,
  LONG_TURNS,
  NOTES_TITLE,
  SHORT_SESSION,
  SHORT_TITLE,
} from './image-env.ts';

// The real flow in Chromium against the built image: a core in the container, a real agent on the host
// with the scripted fake Claude Code behind it. One page for the whole file: the token is entered once.
test.describe.configure({ mode: 'serial' });

let page: Page;
const row = (title: string) => page.getByTestId('cockpit.session').filter({ hasText: title });

test.beforeAll(async ({ browser }) => {
  page = await browser.newPage({ viewport: { width: 1280, height: 800 }, locale: 'en-US' });
});
test.afterAll(async () => {
  await page.close();
});

test('the token gate unlocks the cockpit', async () => {
  await page.goto(process.env.E2E_URL ?? '');
  await expect(page.getByTestId('gate.input')).toBeVisible();
  await page.getByTestId('gate.input').fill(process.env.E2E_TOKEN ?? '');
  await page.getByTestId('gate.submit').click();
  await expect(page.getByTestId('cockpit.sidebar')).toBeVisible();
  // The core's newest session opens by itself.
  await expect(page.getByTestId('cockpit.topbar')).toBeVisible();
});

test('the sidebar groups the fixture sessions by device and directory', async () => {
  const device = page.getByTestId('cockpit.device');
  await expect(device).toHaveCount(1);
  await expect(device).toContainText(DEVICE);
  await expect(device.getByText('hoplet', { exact: true })).toBeVisible();
  await expect(device.getByText('notes', { exact: true })).toBeVisible();
  await expect(page.getByTestId('cockpit.session')).toHaveCount(3);
  for (const title of [LONG_TITLE, SHORT_TITLE, NOTES_TITLE])
    await expect(row(title)).toBeVisible();
});

test('opening a session renders the tail page and scrolling up loads an earlier one', async () => {
  const pages: string[] = [];
  page.on('request', (r) => {
    const url = new URL(r.url());
    if (
      url.pathname === `/api/sessions/${LONG_SESSION}/lines` &&
      url.searchParams.get('limit') === '80'
    )
      pages.push(url.search);
  });
  await row(LONG_TITLE).click();
  await expect(page).toHaveURL(new RegExp(`session=${LONG_SESSION}`));
  await expect(page.getByTestId('cockpit.topbar')).toContainText(LONG_TITLE);
  const conv = page.getByTestId('cockpit.conversation');
  await expect(conv.getByText(`Answer ${LONG_TURNS - 1}:`)).toBeVisible();
  await expect(conv.getByText('Answer 0:')).toHaveCount(0);
  expect(pages).toEqual(['?start=-80&limit=80']);
  const before = pages.length;
  await expect
    .poll(async () => {
      await conv.evaluate((el) => {
        el.scrollTop = 0;
      });
      return pages.length;
    })
    .toBeGreaterThan(before);
  // Earlier pages keep loading while the top is reached, until the first turn is in the window.
  await expect
    .poll(
      async () => {
        await conv.evaluate((el) => {
          el.scrollTop = 0;
        });
        return await conv.getByText('Follow-up 1:').count();
      },
      { timeout: 30_000 },
    )
    .toBe(1);
  expect(pages.length).toBeGreaterThan(before);
  expect(pages.every((p) => p.endsWith('&limit=80'))).toBe(true);
});

test('the elided block opens in place', async () => {
  await page.goto(`${process.env.E2E_URL}/?session=${LONG_SESSION}`);
  const conv = page.getByTestId('cockpit.conversation');
  await expect(conv.getByText(`Answer ${LONG_TURNS - 1}:`)).toBeVisible();
  const elided = conv.getByTestId('cockpit.elided');
  await expect(elided).toBeVisible();
  await expect(conv.getByTestId('cockpit.block-full')).toHaveCount(0);
  await elided.getByTestId('cockpit.block-open').click();
  await expect(conv.getByTestId('cockpit.block-full').last()).toContainText('BIGBLOCK-END');
  await expect(page).toHaveURL(new RegExp(`session=${LONG_SESSION}`));
});

test('the palette finds a session and opens it', async () => {
  await page.keyboard.press('Meta+k');
  const palette = page.getByTestId('cockpit.palette');
  await expect(palette).toBeVisible();
  await page.getByTestId('cockpit.palette-input').fill('chunk roller');
  const hit = palette.getByTestId('cockpit.palette-session').filter({ hasText: SHORT_TITLE });
  await expect(hit).toBeVisible();
  await hit.click();
  await expect(palette).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`session=${SHORT_SESSION}`));
  await expect(page.getByTestId('cockpit.topbar')).toContainText(SHORT_TITLE);
});

test('the sidebar opens the directory of the session picked from the palette', async () => {
  for (const toggle of await page.getByTestId('cockpit.directory-toggle').all())
    await toggle.click();
  await expect(page.getByTestId('cockpit.session')).toHaveCount(0);
  const pick = async (title: string) => {
    await page.keyboard.press('Meta+k');
    await page.getByTestId('cockpit.palette-input').fill(title);
    await page
      .getByTestId('cockpit.palette')
      .getByTestId('cockpit.palette-session')
      .filter({ hasText: title })
      .click();
    await expect(page.getByTestId('cockpit.topbar')).toContainText(title);
  };
  await pick(NOTES_TITLE);
  await expect(row(NOTES_TITLE)).toBeVisible();
  await expect(page.getByTestId('cockpit.session')).toHaveCount(1);
  await pick(SHORT_TITLE);
  await expect(row(SHORT_TITLE)).toBeVisible();
  await expect(row(NOTES_TITLE)).toBeVisible();
});

test('the top bar holds the title and no machine crumb', async () => {
  await expect(page.getByTestId('cockpit.topbar')).not.toContainText(DEVICE);
});

async function send(text: string) {
  const input = page.getByTestId('cockpit.composer-input');
  await expect(input).toBeEnabled();
  await input.fill(text);
  await page.getByTestId('cockpit.send').click();
}

test('a prompt runs, can be interrupted, and the next turn is stored', async () => {
  const conv = page.getByTestId('cockpit.conversation');
  await send('sleep');
  await expect(page.getByTestId('cockpit.live-turn')).toBeVisible();
  await expect(page.getByTestId('cockpit.interrupt')).toBeVisible();
  await page.getByTestId('cockpit.interrupt').click();
  await expect(page.getByTestId('cockpit.live-turn')).toHaveCount(0);
  await expect(conv.getByText('sleep', { exact: true })).toBeVisible();

  await send('say pong from the double');
  await expect(conv.getByText('pong from the double', { exact: true })).toBeVisible();
  await expect(page.getByTestId('cockpit.live-turn')).toHaveCount(0);
  await expect(page.getByTestId('cockpit.composer-input')).toBeEnabled();
});

test('a permission request is answered with Allow', async () => {
  const conv = page.getByTestId('cockpit.conversation');
  await send('tool echo permitted');
  const card = page.getByTestId('cockpit.permission');
  await expect(card).toBeVisible();
  await expect(card).toContainText('Bash');
  await expect(card).toContainText('echo permitted');
  await card.getByTestId('cockpit.permission-allow').click();
  await expect(card).toHaveCount(0);
  await expect(conv.getByText('DONE', { exact: true })).toBeVisible();
});

test('New session creates a session in a watched directory', async () => {
  await page.getByTestId('cockpit.new-session').click();
  const dialog = page.getByTestId('cockpit.new-dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId('cockpit.new-start')).toBeDisabled();
  await dialog.getByTestId('cockpit.new-prompt').fill('say hello from a new session');
  await dialog.getByTestId('cockpit.new-start').click();
  await expect(dialog).toHaveCount(0);
  const conv = page.getByTestId('cockpit.conversation');
  await expect(conv.getByText('hello from a new session', { exact: true })).toBeVisible();
  await expect(page.getByTestId('cockpit.session')).toHaveCount(4);
  await expect(page).toHaveURL(/session=(?!11111111)[0-9a-f-]{36}/);
});

test('Add project browses the machine and adds a folder', async () => {
  const device = page.getByTestId('cockpit.device');
  await device.getByTestId('cockpit.device-toggle').hover();
  await page.getByTestId('cockpit.add-project').click();
  const dialog = page.getByTestId('cockpit.add-dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId('cockpit.add-submit')).toBeDisabled();
  // The root lists the folders under the agent's browse root; a path outside it is refused in words.
  const projects = dialog.getByTestId('cockpit.add-dir').filter({ hasText: 'projects' });
  await expect(projects).toBeVisible();
  await dialog.getByTestId('cockpit.add-edit').click();
  await dialog.getByTestId('cockpit.add-path').fill('/etc');
  await page.keyboard.press('Enter');
  await expect(dialog.getByTestId('cockpit.add-error')).toContainText(
    `That folder is outside the browse root on ${DEVICE}`,
  );
  await page.keyboard.press('Escape');
  await expect(dialog).toBeVisible();
  // Keyboard: select the row, open it with Enter, go back up with Backspace.
  await projects.click();
  await page.keyboard.press('Enter');
  await expect(dialog.getByTestId('cockpit.add-crumb')).toHaveText(['~', 'projects']);
  await page.keyboard.press('Backspace');
  await expect(dialog.getByTestId('cockpit.add-crumb')).toHaveText(['~']);
  // Mouse: open the folder, then select the repository.
  await projects.dblclick();
  const repo = dialog.getByTestId('cockpit.add-dir').filter({ hasText: 'fixture-repo' });
  await expect(repo).toContainText('git');
  await repo.click();
  await expect(dialog.getByTestId('cockpit.add-submit')).toHaveText('Add fixture-repo');
  await dialog.getByTestId('cockpit.add-submit').click();
  await expect(dialog).toHaveCount(0);
  await expect(device.getByText('fixture-repo', { exact: true })).toBeVisible();
});
