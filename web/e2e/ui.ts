// Helpers of the specs that drive the cockpit in Chromium.
import { expect, type Page } from '@playwright/test';
import { HOST_TOKEN, type Stack } from './stack.ts';

/** Opens the cockpit of `stack` and enters the host token at the gate. */
export async function unlock(page: Page, stack: Stack): Promise<void> {
  await page.goto(stack.url);
  await page.getByTestId('gate.input').fill(HOST_TOKEN);
  await page.getByTestId('gate.submit').click();
  await expect(page.getByTestId('cockpit.sidebar')).toBeVisible();
}

/** The sidebar row of a session by its title. */
export const row = (page: Page, title: string) =>
  page.getByTestId('cockpit.session').filter({ hasText: title });

/** Opens a session from the sidebar and waits until its conversation is on screen. */
export async function open(page: Page, title: string): Promise<void> {
  await row(page, title).click();
  await expect(page.getByTestId('cockpit.topbar')).toContainText(title);
  await expect(page.getByTestId('cockpit.conv')).toBeVisible();
}

/** The runtime state the open session shows. */
export const stateOf = (page: Page) => page.getByTestId('cockpit.conv');

export async function send(page: Page, text: string): Promise<void> {
  const input = page.getByTestId('cockpit.composer-input');
  await expect(input).toBeEnabled();
  await input.fill(text);
  await page.getByTestId('cockpit.send').click();
}
