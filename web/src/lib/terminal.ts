import { TERMINAL_CHUNK_BYTES } from '@hoplet/shared';

/**
 * The terminal's logic without a screen (ADR 0026): how keystrokes are cut and paced, why a terminal is closed or
 * cannot be opened, and the emulator's colours. The Mac client reuses it as it is.
 */

/**
 * Paste speed limit: bytes sent in any one second. Half the owner's 64 KiB/s, so frames that the network delays
 * and delivers together never reach the owner faster than it allows (it closes the terminal with `input-flood`).
 */
export const KEY_BYTES_PER_SECOND = 32 * 1024;
export const RATE_WINDOW_MS = 1000;
/** The size a terminal opens with before the view has been measured. */
export const DEFAULT_COLS = 90;
export const DEFAULT_ROWS = 32;

/** Cuts bytes into pieces of at most `max` bytes, in order. */
export function splitChunks(bytes: Uint8Array, max = TERMINAL_CHUNK_BYTES): Uint8Array[] {
  const out: Uint8Array[] = [];
  for (let at = 0; at < bytes.length; at += max) out.push(bytes.subarray(at, at + max));
  return out;
}

export function toBase64(bytes: Uint8Array): string {
  let binary = '';
  for (let at = 0; at < bytes.length; at += 0x2000) {
    binary += String.fromCharCode(...bytes.subarray(at, at + 0x2000));
  }
  return btoa(binary);
}

export function fromBase64(text: string): Uint8Array {
  const binary = atob(text);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}

/** A sliding window: at most `limit` bytes in any `windowMs`. */
export class RateLimiter {
  private sent: { at: number; bytes: number }[] = [];
  private readonly limit: number;
  private readonly windowMs: number;

  constructor(limit = KEY_BYTES_PER_SECOND, windowMs = RATE_WINDOW_MS) {
    this.limit = limit;
    this.windowMs = windowMs;
  }

  /** How long to wait before `bytes` may go out at `now`; 0 means now. */
  wait(now: number, bytes: number): number {
    this.sent = this.sent.filter((s) => now - s.at < this.windowMs);
    let total = this.sent.reduce((sum, s) => sum + s.bytes, 0);
    if (total + bytes <= this.limit) return 0;
    for (const s of this.sent) {
      total -= s.bytes;
      if (total + bytes <= this.limit) return Math.max(1, s.at + this.windowMs - now);
    }
    return this.windowMs;
  }

  /** Records that `bytes` went out at `now`. */
  record(now: number, bytes: number): void {
    this.sent.push({ at: now, bytes });
  }
}

export interface KeyPacerDeps {
  send: (base64: string) => void;
  now: () => number;
  setTimer: (run: () => void, ms: number) => unknown;
  clearTimer: (handle: unknown) => void;
}

export interface KeyPacer {
  /** Queues keystrokes; they leave in order, in pieces of at most one message, within the rate limit. */
  push(bytes: Uint8Array): void;
  /** Drops what is still queued. */
  clear(): void;
  pending(): number;
}

export function createKeyPacer(deps: KeyPacerDeps, limiter = new RateLimiter()): KeyPacer {
  const queue: Uint8Array[] = [];
  let timer: unknown = null;
  const drain = () => {
    timer = null;
    while (queue.length > 0) {
      const next = queue[0] as Uint8Array;
      const wait = limiter.wait(deps.now(), next.length);
      if (wait > 0) {
        timer = deps.setTimer(drain, wait);
        return;
      }
      queue.shift();
      limiter.record(deps.now(), next.length);
      deps.send(toBase64(next));
    }
  };
  return {
    push(bytes) {
      for (const piece of splitChunks(bytes)) queue.push(piece);
      if (timer === null) drain();
    },
    clear() {
      queue.length = 0;
      if (timer !== null) deps.clearTimer(timer);
      timer = null;
    },
    pending: () => queue.reduce((sum, q) => sum + q.length, 0),
  };
}

/** How a closed terminal is explained: three reasons have their own copy, every other reason the plain one. */
export type ClosedKind = 'turn' | 'idle' | 'offline' | 'generic';

export function closedKindOf(reason: string): ClosedKind {
  if (reason === 'turn-started') return 'turn';
  if (reason === 'idle') return 'idle';
  if (reason === 'owner-offline') return 'offline';
  return 'generic';
}

/** What keeps a terminal from opening, in the order it is checked; `ready` when nothing does. */
export type Gate =
  | 'ready'
  | 'off'
  | 'owner-offline'
  | 'external'
  | 'handoff-running'
  | 'turn-running';

export function gateOf(input: {
  online: boolean;
  terminalCapable: boolean;
  runtimeState: string;
  handoffRunning: boolean;
}): Gate {
  if (!input.online) return 'owner-offline';
  if (!input.terminalCapable) return 'off';
  if (input.runtimeState === 'external') return 'external';
  if (input.handoffRunning) return 'handoff-running';
  if (
    input.runtimeState === 'running' ||
    input.runtimeState === 'waiting-permission' ||
    input.runtimeState === 'starting'
  ) {
    return 'turn-running';
  }
  return 'ready';
}

/** The emulator's theme from design tokens; `read` returns a token's value, such as `--color-text`. */
export function terminalTheme(read: (token: string) => string, scheme: 'light' | 'dark') {
  const surface = scheme === 'light' ? read('--color-raised') : read('--color-surface');
  const text = read('--color-text');
  return {
    background: surface,
    foreground: text,
    cursor: text,
    cursorAccent: surface,
    selectionBackground: read('--color-selected'),
    black: surface,
    red: read('--color-danger'),
    green: read('--color-success'),
    yellow: read('--color-attention-text'),
    blue: read('--color-syntax-type'),
    magenta: read('--color-syntax-keyword'),
    cyan: read('--color-syntax-function'),
    white: read('--color-text-2'),
    brightBlack: read('--color-text-3'),
    brightWhite: text,
  };
}
