#!/usr/bin/env node
import { readFileSync, statSync } from 'node:fs';
import { createInterface } from 'node:readline/promises';
import { runCli } from './run.ts';

const controller = new AbortController();
process.on('SIGINT', () => controller.abort());

const interactive = process.stdin.isTTY === true;

const code = await runCli(process.argv.slice(2), {
  env: process.env,
  io: {
    stdout: (text) => {
      process.stdout.write(text);
    },
    stderr: (text) => {
      process.stderr.write(text);
    },
  },
  signal: controller.signal,
  readFile: (path) => ({ text: readFileSync(path, 'utf8'), mode: statSync(path).mode }),
  readStdin: async () => {
    const chunks: Buffer[] = [];
    for await (const chunk of process.stdin) chunks.push(chunk as Buffer);
    return Buffer.concat(chunks).toString('utf8');
  },
  ...(interactive
    ? {
        ask: async (question: string) => {
          const rl = createInterface({ input: process.stdin, output: process.stderr });
          try {
            return await rl.question(question);
          } finally {
            rl.close();
          }
        },
      }
    : {}),
});
process.exit(code);
