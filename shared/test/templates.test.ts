import { describe, expect, it } from 'vitest';
import { fillTemplate, parseTemplate } from '../src/templates.ts';

const parse = (text: string) => {
  const parsed = parseTemplate(text);
  if (!parsed.ok) throw new Error(`refused: ${parsed.problem} at ${parsed.position}`);
  return parsed;
};

describe('parseTemplate', () => {
  it('finds placeholders in the order of first use', () => {
    const parsed = parse('Fix {{issue}} in {{path:src/}} then {{issue}} {{tone:calm}}');
    expect(parsed.placeholders.map((p) => p.name)).toEqual(['issue', 'path', 'tone']);
    expect(parsed.parts[0]).toEqual({ kind: 'text', text: 'Fix ' });
    expect(parsed.parts[1]).toEqual({ kind: 'field', name: 'issue' });
  });

  it('lists names once, with the first default', () => {
    const parsed = parse('{{a:one}} {{b}} {{a:two}} {{c:}}');
    expect(parsed.placeholders).toEqual([
      { name: 'a', default: 'one' },
      { name: 'b' },
      { name: 'c', default: '' },
    ]);
  });

  it('keeps text without placeholders as one piece', () => {
    expect(parse('plain {text} here').parts).toEqual([{ kind: 'text', text: 'plain {text} here' }]);
  });

  it('turns \\{{ into a literal {{ and leaves other backslashes', () => {
    const parsed = parse('use \\{{name}} and a\\b {{x}}');
    expect(parsed.placeholders).toEqual([{ name: 'x' }]);
    expect(fillTemplate(parsed, { x: '1' })).toEqual({ ok: true, text: 'use {{name}} and a\\b 1' });
  });

  it('ends a default at the first }} and allows single braces and colons in it', () => {
    const parsed = parse('{{a:x:{y}}} z');
    expect(parsed.placeholders).toEqual([{ name: 'a', default: 'x:{y' }]);
  });

  it('refuses a {{ that starts no placeholder, with its offset', () => {
    expect(parseTemplate('ab {{ }}')).toEqual({ ok: false, problem: 'syntax', position: 3 });
    expect(parseTemplate('{{Upper}}')).toEqual({ ok: false, problem: 'syntax', position: 0 });
    expect(parseTemplate('{{1a}}')).toMatchObject({ ok: false, problem: 'syntax' });
    expect(parseTemplate('x {{open')).toEqual({ ok: false, problem: 'syntax', position: 2 });
    expect(parseTemplate('{{a:no end')).toMatchObject({ ok: false, problem: 'syntax' });
    expect(parseTemplate('{{a:two\nlines}}')).toMatchObject({ ok: false, problem: 'syntax' });
  });

  it('limits the name and the default', () => {
    expect(parseTemplate(`{{${'a'.repeat(32)}}}`).ok).toBe(true);
    expect(parseTemplate(`{{${'a'.repeat(33)}}}`)).toMatchObject({ ok: false, problem: 'syntax' });
    expect(parseTemplate(`{{a:${'d'.repeat(200)}}}`).ok).toBe(true);
    expect(parseTemplate(`{{a:${'d'.repeat(201)}}}`)).toMatchObject({
      ok: false,
      problem: 'syntax',
    });
  });

  it('limits the distinct placeholders to 20', () => {
    const names = (n: number) => Array.from({ length: n }, (_, i) => `{{p${i}}}`).join(' ');
    expect(parseTemplate(names(20)).ok).toBe(true);
    expect(parseTemplate(`${names(20)} {{p0}}`).ok).toBe(true);
    expect(parseTemplate(names(21))).toMatchObject({ ok: false, problem: 'too-many' });
  });
});

describe('fillTemplate', () => {
  it('fills every use of a name', () => {
    expect(fillTemplate(parse('{{a}}-{{a}}-{{b:z}}'), { a: 'x' })).toEqual({
      ok: true,
      text: 'x-x-z',
    });
  });

  it('prefers a typed value to the default, the empty string included', () => {
    const parsed = parse('[{{a:def}}]');
    expect(fillTemplate(parsed, { a: 'typed' })).toEqual({ ok: true, text: '[typed]' });
    expect(fillTemplate(parsed, { a: '' })).toEqual({ ok: true, text: '[]' });
    expect(fillTemplate(parsed, {})).toEqual({ ok: true, text: '[def]' });
  });

  it('names the values that are missing, an empty one included', () => {
    const parsed = parse('{{a}} {{b}} {{c:d}}');
    expect(fillTemplate(parsed, { a: '' })).toEqual({
      ok: false,
      missing: ['a', 'b'],
      unknown: [],
    });
  });

  it('names the values no placeholder takes', () => {
    expect(fillTemplate(parse('{{a}}'), { a: '1', zzz: '2' })).toEqual({
      ok: false,
      missing: [],
      unknown: ['zzz'],
    });
  });

  it('never reads a value as a template', () => {
    expect(fillTemplate(parse('{{a}} {{b:x}}'), { a: '{{b:y}} \\{{' })).toEqual({
      ok: true,
      text: '{{b:y}} \\{{ x',
    });
  });
});
