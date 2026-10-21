import { describe, expect, it } from 'vitest';
import {
  checkLabel,
  gpuMemoryText,
  projectName,
  toolchainsOf,
  withLabel,
  withLabelAt,
  withoutLabel,
} from '../src/lib/capabilities.ts';

describe('toolchainsOf', () => {
  it('lists the found tools in the agent order and names the missing ones', () => {
    const { found, missing } = toolchainsOf({
      toolchains: [
        { name: 'node', version: '22.20.0' },
        { name: 'git', version: '2.51.0' },
      ],
    });
    expect(found.map((x) => x.name)).toEqual(['git', 'node']);
    expect(missing).toEqual([
      'pnpm',
      'npm',
      'bun',
      'python3',
      'go',
      'cargo',
      'rustc',
      'java',
      'docker',
      'gh',
    ]);
  });

  it('keeps a tool this list does not know, after the known ones', () => {
    const { found } = toolchainsOf({
      toolchains: [
        { name: 'zig', version: '0.14.0' },
        { name: 'gh', version: '2.80.0' },
      ],
    });
    expect(found.map((x) => x.name)).toEqual(['gh', 'zig']);
  });
});

describe('projectName and gpuMemoryText', () => {
  it('names a project by the last segment of either kind of path', () => {
    expect(projectName('/Users/me/code/hoplet')).toBe('hoplet');
    expect(projectName('C:\\code\\notes\\')).toBe('notes');
  });
  it('writes a memory size in GB', () => {
    expect(gpuMemoryText(24 * 1024 ** 3)).toBe('24 GB');
    expect(gpuMemoryText(8 * 1024 ** 3)).toBe('8 GB');
    expect(gpuMemoryText(1.5 * 1024 ** 3)).toBe('1.5 GB');
  });
});

describe('checkLabel', () => {
  it('accepts lowercase letters, digits and hyphens, trimmed', () => {
    expect(checkLabel(' ci-runner ', [])).toEqual({ ok: true, label: 'ci-runner' });
    expect(checkLabel('a1', [])).toEqual({ ok: true, label: 'a1' });
  });
  it.each(['', 'GPU box', 'GPU', '-gpu', 'gpu_box', 'gpu box', 'é', 'a'.repeat(33)])(
    'rejects %j',
    (text) => {
      expect(checkLabel(text, [])).toEqual({ ok: false, problem: 'invalid' });
    },
  );
  it('accepts 32 characters', () => {
    expect(checkLabel('a'.repeat(32), []).ok).toBe(true);
  });
  it('rejects a 21st label but accepts one already there', () => {
    const twenty = Array.from({ length: 20 }, (_, i) => `l${i}`);
    expect(checkLabel('extra', twenty)).toEqual({ ok: false, problem: 'full' });
    expect(checkLabel('l3', twenty)).toEqual({ ok: true, label: 'l3' });
  });
});

describe('label lists', () => {
  it('adds at the end without repeating', () => {
    expect(withLabel(['gpu'], 'release')).toEqual(['gpu', 'release']);
    expect(withLabel(['gpu'], 'gpu')).toEqual(['gpu']);
  });
  it('removes', () => {
    expect(withoutLabel(['gpu', 'release'], 'gpu')).toEqual(['release']);
  });
  it('puts a removed label back where it was', () => {
    expect(withLabelAt(['gpu', 'x'], 'release', 1)).toEqual(['gpu', 'release', 'x']);
    expect(withLabelAt(['gpu'], 'release', 9)).toEqual(['gpu', 'release']);
    expect(withLabelAt(['gpu', 'release'], 'release', 0)).toEqual(['gpu', 'release']);
  });
});
