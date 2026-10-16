import axe from 'axe-core';
import { JSDOM } from 'jsdom';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { states as cockpit } from '../src/screens/cockpit/states.tsx';
import { states as settings } from '../src/screens/settings/states.tsx';
import { states as workspace } from '../src/screens/workspace/states.tsx';

describe.each([
  ['cockpit', cockpit],
  ['workspace', workspace],
  ['settings', settings],
])('%s states axe', (_screen, states) => {
  for (const [name, render] of Object.entries(states)) {
    it(name, async () => {
      const body = renderToStaticMarkup(render());
      const dom = new JSDOM(
        `<!DOCTYPE html><html lang="en"><head><title>hoplet</title></head><body>${body}</body></html>`,
        { pretendToBeVisual: true, url: 'http://localhost/' },
      );
      const { window } = dom;
      const results = await axe.run(window.document.documentElement, {
        rules: { 'color-contrast': { enabled: false } },
      });
      expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
    });
  }
});
