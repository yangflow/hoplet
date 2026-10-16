import { describe, expect, it } from 'vitest';
import { expiryText, tokenFromHash } from '../src/screens/action/model.ts';

describe('tokenFromHash', () => {
  it('reads a token in the fragment', () => {
    expect(tokenFromHash('#abcdefgh.ijklmnop')).toBe('abcdefgh.ijklmnop');
  });
  it('rejects anything that is not shaped like a token', () => {
    expect(tokenFromHash('')).toBeNull();
    expect(tokenFromHash('#')).toBeNull();
    expect(tokenFromHash('#nodot12345678')).toBeNull();
    expect(tokenFromHash('#abc.def')).toBeNull();
    expect(tokenFromHash('#abcdefgh.ijklmnop&x=1')).toBeNull();
  });
});

describe('expiryText', () => {
  const now = 1_000_000;
  it('rounds up to whole minutes', () => {
    expect(expiryText(now + 7.2 * 60_000, now)).toBe('Link valid for 8 more minutes');
    expect(expiryText(now + 30_000, now)).toBe('Link valid for 1 more minute');
  });
  it('is null when the link has passed or has no end', () => {
    expect(expiryText(now - 1, now)).toBeNull();
    expect(expiryText(undefined, now)).toBeNull();
  });
});
