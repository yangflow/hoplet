#!/usr/bin/env node
// Generates a synthetic Codex rollout: node scripts/gen-codex-fixture.mjs <out.jsonl> <threadId> <cwd> [turns]
// Line 1 is a session_meta with a ~70 KB base_instructions text, as Codex writes it.
import { writeFileSync } from 'node:fs';

const [out, id, cwd, turnsArg] = process.argv.slice(2);
const turns = Number(turnsArg ?? 50);
let ts = Date.parse('2026-01-01T00:00:00Z');
let ordinal = 0;
const line = (type, payload) => {
  ts += 1000;
  const entry = { timestamp: new Date(ts).toISOString(), ordinal, type, payload };
  ordinal += 1;
  return `${JSON.stringify(entry)}\n`;
};

let body = line('session_meta', {
  id,
  cwd,
  originator: 'codex_cli_rs',
  base_instructions: { text: 'x'.repeat(70_000) },
});
for (let i = 0; i < turns; i++) {
  body += line('response_item', {
    type: 'message',
    role: 'user',
    content: [{ type: 'input_text', text: `codex turn ${i}` }],
  });
  body += line('response_item', {
    type: 'message',
    role: 'assistant',
    content: [{ type: 'output_text', text: `codex answer ${i}` }],
  });
}
writeFileSync(out, body);
