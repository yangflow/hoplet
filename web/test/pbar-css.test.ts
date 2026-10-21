import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';

const read = (path: string) =>
  readFileSync(new URL(path, new URL('../../', import.meta.url)), 'utf8');

it('styles the budget bar in the stylesheet both Settings and the session popover load', () => {
  const shared = read('web/src/components/components.css');
  expect(shared).toContain('.pbar .pf');
  expect(shared).toContain('.pbar .pt');
  expect(read('web/src/screens/settings/usage.css')).not.toContain('.pbar .pf');
});
