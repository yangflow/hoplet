import { describe, expect, it } from 'vitest';
import {
  closedKindOf,
  createKeyPacer,
  fromBase64,
  gateOf,
  RateLimiter,
  splitChunks,
  terminalTheme,
  toBase64,
} from '../src/lib/terminal.ts';

describe('base64 and chunking', () => {
  it('round-trips arbitrary bytes', () => {
    const bytes = Uint8Array.from({ length: 300 }, (_, i) => i % 256);
    expect([...fromBase64(toBase64(bytes))]).toEqual([...bytes]);
  });
  it('cuts a paste into pieces of at most 4096 bytes, in order', () => {
    const bytes = new Uint8Array(10_000).map((_, i) => i % 251);
    const pieces = splitChunks(bytes);
    expect(pieces.map((p) => p.length)).toEqual([4096, 4096, 1808]);
    expect(pieces.flatMap((p) => [...p])).toEqual([...bytes]);
    expect(splitChunks(new Uint8Array(0))).toEqual([]);
  });
  it('encodes a full piece within the wire limit', () => {
    expect(toBase64(new Uint8Array(4096)).length).toBeLessThanOrEqual(5464);
  });
});

describe('the paste rate limit', () => {
  it('lets 32 KiB through a second and holds the rest until the window moves', () => {
    const limiter = new RateLimiter();
    expect(limiter.wait(0, 32768)).toBe(0);
    limiter.record(0, 32768);
    expect(limiter.wait(10, 1)).toBe(990);
    expect(limiter.wait(1000, 1)).toBe(0);
  });
  it('waits only until enough of the oldest sends have left the window', () => {
    const limiter = new RateLimiter(100, 1000);
    limiter.record(0, 60);
    limiter.record(500, 40);
    expect(limiter.wait(600, 50)).toBe(400);
    expect(limiter.wait(600, 100)).toBe(900);
  });
  it('paces a queue: in order, each message at most 4 KiB, 32 KiB in any second', () => {
    let clock = 0;
    const timers: { at: number; run: () => void }[] = [];
    const sent: { at: number; bytes: number }[] = [];
    const pacer = createKeyPacer({
      send: (b) => sent.push({ at: clock, bytes: fromBase64(b).length }),
      now: () => clock,
      setTimer: (run, ms) => {
        const t = { at: clock + ms, run };
        timers.push(t);
        return t;
      },
      clearTimer: (h) => {
        const i = timers.indexOf(h as (typeof timers)[number]);
        if (i >= 0) timers.splice(i, 1);
      },
    });
    pacer.push(new Uint8Array(200 * 1024));
    while (timers.length > 0) {
      timers.sort((a, b) => a.at - b.at);
      const t = timers.shift() as (typeof timers)[number];
      clock = t.at;
      t.run();
    }
    expect(sent.every((s) => s.bytes <= 4096)).toBe(true);
    expect(sent.reduce((n, s) => n + s.bytes, 0)).toBe(200 * 1024);
    for (const s of sent) {
      const inWindow = sent
        .filter((o) => o.at > s.at - 1000 && o.at <= s.at)
        .reduce((n, o) => n + o.bytes, 0);
      expect(inWindow).toBeLessThanOrEqual(32768);
    }
    expect(pacer.pending()).toBe(0);
  });
});

describe('closed reasons', () => {
  it('maps three reasons to their own copy and the rest to the plain one', () => {
    expect(closedKindOf('turn-started')).toBe('turn');
    expect(closedKindOf('idle')).toBe('idle');
    expect(closedKindOf('owner-offline')).toBe('offline');
    for (const reason of [
      'max-age',
      'detached',
      'handoff',
      'deleted',
      'closed',
      'input-flood',
      'exit',
    ]) {
      expect(closedKindOf(reason)).toBe('generic');
    }
  });
});

describe('the gate', () => {
  const base = { online: true, terminalCapable: true, runtimeState: 'idle', handoffRunning: false };
  it('is ready when nothing blocks', () => {
    expect(gateOf(base)).toBe('ready');
    expect(gateOf({ ...base, runtimeState: 'ready' })).toBe('ready');
  });
  it('checks offline, then the capability, then the session', () => {
    expect(gateOf({ ...base, online: false, terminalCapable: false })).toBe('owner-offline');
    expect(gateOf({ ...base, terminalCapable: false, runtimeState: 'running' })).toBe('off');
    expect(gateOf({ ...base, runtimeState: 'external', handoffRunning: true })).toBe('external');
    expect(gateOf({ ...base, handoffRunning: true, runtimeState: 'running' })).toBe(
      'handoff-running',
    );
    for (const runtimeState of ['running', 'waiting-permission', 'starting']) {
      expect(gateOf({ ...base, runtimeState })).toBe('turn-running');
    }
  });
});

describe('the theme', () => {
  const read = (t: string) => `v(${t})`;
  it('uses the raised surface in light and the surface in dark', () => {
    expect(terminalTheme(read, 'light').background).toBe('v(--color-raised)');
    expect(terminalTheme(read, 'dark').background).toBe('v(--color-surface)');
    expect(terminalTheme(read, 'dark').foreground).toBe('v(--color-text)');
  });
});
