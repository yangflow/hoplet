import type { Hoplet, HopletOptions } from '@hoplet/sdk';
import type { Flags } from './args.ts';

/** Where the command writes; each call is a piece of text, so streamed output needs no line breaks of its own. */
export type CliIo = {
  stdout: (text: string) => void;
  stderr: (text: string) => void;
};

/** What the command needs from its surroundings; tests replace each part. */
export type Deps = {
  env: Record<string, string | undefined>;
  io: CliIo;
  fetch?: typeof fetch;
  WebSocket?: typeof WebSocket;
  /** Replaces `new Hoplet(options)`. */
  createClient?: (options: HopletOptions) => Hoplet;
  /** Reads a token file; `mode` is the file's permission bits. */
  readFile?: (path: string) => { text: string; mode: number };
  /** Reads standard input to its end. */
  readStdin?: () => Promise<string>;
  /** Asks the person at the terminal; absent when there is none. */
  ask?: (question: string) => Promise<string>;
  /** Aborted by Ctrl-C. */
  signal?: AbortSignal;
};

export type Context = {
  client: Hoplet;
  flags: Flags;
  json: boolean;
  deps: Deps;
  /** Prints one JSON document (a line). */
  doc: (value: unknown) => void;
  /** Prints a line of text. */
  line: (text: string) => void;
  /** Prints a line on standard error. */
  note: (text: string) => void;
};

/** The exit code of each turn result (ADR 0033); 1 is any error and 3 belongs to `audit verify`. */
export const EXIT_OF_RESULT = {
  ok: 0,
  failed: 4,
  ended: 5,
  blocked: 6,
  timeout: 7,
} as const;
