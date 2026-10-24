import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const root = new URL('../../', import.meta.url);
const read = (path: string) => readFileSync(new URL(path, root), 'utf8');

const pascal = (kebab: string) =>
  kebab
    .split('-')
    .map((w) => w[0]?.toUpperCase() + w.slice(1))
    .join('');

/** The glyph names docs/design.md#icons lists as the closed set. */
function documented(): string[] {
  const section = /## Icons\n([\s\S]*?)\n## /.exec(read('docs/design.md'))?.[1] ?? '';
  const list = /The set is closed:\s+([\s\S]*?); turned glyphs/.exec(section)?.[1] ?? '';
  return list
    .replace(/\s+/g, ' ')
    .split(', ')
    .map((n) => pascal(n.trim()));
}

describe('the icon wrapper', () => {
  const source = read('web/src/components/icons.tsx');
  const family = ['lucide', 'react'].join('-');
  const imported = new RegExp(`import \\{([^}]*)\\} from '${family}'`).exec(source)?.[1] ?? '';
  const glyphs = imported
    .split(',')
    .map((n) => n.trim())
    .filter(Boolean);

  it('lists 77 glyphs in the design', () => {
    expect(documented()).toHaveLength(77);
  });

  it('imports exactly the glyphs of docs/design.md#icons', () => {
    expect([...glyphs].sort()).toEqual([...documented()].sort());
  });

  it('draws every glyph through the .icon rule at the icon tokens', () => {
    const css = read('web/src/components/components.css');
    expect(css).toMatch(/\.icon\s*\{[^}]*width: var\(--icon-size\)/);
    expect(css).toMatch(/\.icon\s*\{[^}]*stroke-width: var\(--icon-stroke\)/);
    const glyphs = source.slice(0, source.indexOf('export function HopletMark'));
    expect(glyphs).not.toMatch(/strokeWidth|absoluteStrokeWidth|size=/);
  });
});
