// docs/api.md and docs/schema/hoplet-api.schema.json against the route table and shared/: adding, removing or
// changing a route, a browser message, an error code or a schema without them fails `pnpm check`.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { routes } from '../../core/src/api/routes.ts';
import {
  HandoffRequestSchema,
  HTTP_ERROR_CODES,
  HTTP_ERROR_STATUS,
  NewSessionRequestSchema,
  safeParse,
} from '../../shared/src/index.ts';
import {
  ARTIFACT_PATH,
  buildApiSchema,
  describeBrowserMessages,
  describeRoutes,
  renderApiSchema,
} from '../api-schema.ts';

const root = path.resolve(import.meta.dirname, '../..');
const doc = readFileSync(path.join(root, 'docs/api.md'), 'utf8');

/** The rows of every table whose header starts with `first`, as trimmed cells. */
function tableRows(first: string): string[][] {
  const rows: string[][] = [];
  let inTable = false;
  for (const line of doc.split('\n')) {
    const cells = line.startsWith('|')
      ? line
          .split('|')
          .slice(1, -1)
          .map((c) => c.trim())
      : null;
    if (!cells) {
      inTable = false;
    } else if (cells[0] === first) {
      inTable = true;
    } else if (inTable && !/^-+$/.test(cells[0] ?? '')) {
      rows.push(cells);
    }
  }
  return rows;
}

const names = (cell: string | undefined): string[] =>
  [...(cell ?? '').matchAll(/`([^`]+)`/g)].map((m) => m[1] as string);

const routeKey = (r: { method: string; path: string }): string => `${r.method} ${r.path}`;

describe('docs/api.md REST routes', () => {
  const rows = tableRows('Method');

  it('lists every route of the route table, and no other', () => {
    const documented = rows.map((cells) => `${cells[0]} ${names(cells[1])[0]}`);
    expect(new Set(documented).size, 'a route is listed twice').toBe(documented.length);
    expect(documented.sort()).toEqual(routes.map(routeKey).sort());
  });

  it('states the authentication, lane and schema names of each route', () => {
    for (const route of describeRoutes()) {
      const cells = rows.find((r) => `${r[0]} ${names(r[1])[0]}` === routeKey(route));
      const [, , auth, lane, query, body, response] = cells ?? [];
      const key = routeKey(route);
      expect(auth, key).toBe(route.auth);
      expect(lane, key).toBe(route.lane);
      expect(names(query)[0] ?? null, `${key} query`).toBe(route.query);
      expect(names(body)[0] ?? null, `${key} body`).toBe(route.body);
      expect(names(response)[0], `${key} response`).toBe(route.response);
      if (route.status !== 200) expect(response, key).toContain(String(route.status));
    }
  });

  it('lists only error codes of shared/', () => {
    for (const cells of rows) {
      for (const code of names(cells[7])) {
        expect(HTTP_ERROR_CODES as readonly string[], `${cells[0]} ${cells[1]}`).toContain(code);
      }
    }
  });

  it('mentions no route outside the route table', () => {
    const known = new Set(routes.map(routeKey));
    const knownPaths = new Set(routes.map((r) => r.path));
    for (const span of doc.matchAll(
      /`((?:GET |POST |PUT |PATCH |DELETE )?\/api\/[^`\s?]*)[^`]*`/g,
    )) {
      const [method, spanPath] = (span[1] as string).includes(' ')
        ? (span[1] as string).split(' ')
        : [undefined, span[1]];
      // The prefix `/api/` and the wildcard `/api/*` name the whole API, not a route.
      if (spanPath === '/api/*' || spanPath === '/api/') continue;
      if (method) expect(known, span[0]).toContain(`${method} ${spanPath}`);
      else expect(knownPaths, span[0]).toContain(spanPath);
    }
    for (const found of doc.matchAll(/\/api\/[\w:/-]+/g)) {
      expect(knownPaths, found[0]).toContain(found[0]);
    }
  });
});

describe('docs/api.md native clients', () => {
  const section = /^## Native clients$([\s\S]*?)^## /m.exec(doc)?.[1] ?? '';

  it('says how a client authenticates, connects the WebSocket and checks the protocol version', () => {
    for (const fact of [
      'Authorization: Bearer <host token>',
      '`POST /api/ws-tickets`',
      '`GET /api/health`',
      '`coreVersion`',
      '`protocolVersion`',
      'Keychain',
    ]) {
      expect(section, fact).toContain(fact);
    }
  });
});

describe('docs/api.md error codes', () => {
  it('lists every error code of a REST response with its HTTP status', () => {
    const documented = Object.fromEntries(
      tableRows('Code').map((cells) => [names(cells[0])[0], Number(cells[1])]),
    );
    expect(documented).toEqual(HTTP_ERROR_STATUS);
  });
});

describe('docs/api.md browser WebSocket', () => {
  const arrows = { 'web-to-core': 'browser → core', 'core-to-web': 'core → browser' };

  it('lists every message of the browser socket with its direction, lane and schema', () => {
    const rows = tableRows('Type');
    const messages = describeBrowserMessages();
    expect(rows.map((cells) => names(cells[0])[0]).sort()).toEqual(
      messages.map((m) => m.type).sort(),
    );
    for (const message of messages) {
      const cells = rows.find((r) => names(r[0])[0] === message.type);
      expect(cells?.[1], message.type).toBe(arrows[message.direction]);
      expect(cells?.[2], message.type).toBe(message.lane);
      expect(names(cells?.[3])[0], message.type).toBe(message.schema);
    }
  });
});

describe('the JSON Schema file', () => {
  it('is what shared/ and the route table produce; run `pnpm schema` when it is not', () => {
    expect(readFileSync(path.join(root, ARTIFACT_PATH), 'utf8')).toBe(renderApiSchema());
  });

  it('holds a schema for every body, response and message that docs/api.md names', () => {
    const { $defs } = buildApiSchema();
    const named = [
      ...describeRoutes().flatMap((r) => [r.body, r.response]),
      ...describeBrowserMessages().map((m) => m.schema),
      'ErrorResponseSchema',
    ].filter((name): name is string => name !== null);
    for (const name of named) expect(Object.keys($defs), name).toContain(name);
  });

  it('explains in docs/api.md each schema it leaves out', () => {
    const { excluded } = buildApiSchema();
    expect(excluded.length).toBeGreaterThan(0);
    for (const entry of excluded) {
      expect(doc, entry.schema).toContain(`| \`${entry.schema}\` |`);
      expect(entry.reason, entry.schema).not.toBe('');
    }
  });

  it('puts every query schema either in the file or in the exclusions', () => {
    const { $defs, excluded } = buildApiSchema();
    const left = new Set(excluded.map((e) => e.schema));
    for (const route of describeRoutes()) {
      if (route.query)
        expect(route.query in $defs || left.has(route.query), route.query).toBe(true);
    }
  });
});

describe('the JSON Schema of a trimmed string', () => {
  interface StringSchema {
    type?: string;
    minLength?: number;
    maxLength?: number;
    pattern?: string;
  }
  // A JSON Schema validator checks the value as it arrives; shared/ trims it first and then checks its length.
  const accepts = (schema: StringSchema, value: string): boolean =>
    schema.type === 'string' &&
    [...value].length >= (schema.minLength ?? 0) &&
    [...value].length <= (schema.maxLength ?? Number.POSITIVE_INFINITY) &&
    (schema.pattern === undefined || new RegExp(schema.pattern, 'u').test(value));

  const { $defs } = buildApiSchema();
  const property = (def: string, name: string): StringSchema =>
    ($defs[def] as { properties: Record<string, StringSchema> }).properties[name] as StringSchema;

  const cases: [string, string, boolean][] = [
    ['one character', 'x', true],
    ['nothing', '', false],
    ['only whitespace', ' \t\n\u00a0\ufeff\u2028', false],
    ['whitespace before the bound', `${' '.repeat(4096)}x`, true],
    ['whitespace after the bound', `x${' '.repeat(4096)}`, true],
    ['whitespace on both sides', `${' '.repeat(5000)}x${' '.repeat(5000)}`, true],
    ['whitespace that trim strips', '\u00a0\ufeffx\u2028', true],
    ['a character that trim keeps', '\u0085', true],
    ['the bound', 'x'.repeat(4096), true],
    ['the bound inside whitespace', ` ${'x'.repeat(4096)}\n`, true],
    ['one over the bound', 'x'.repeat(4097), false],
    ['one over the bound inside whitespace', ` ${'x'.repeat(4097)} `, false],
    ['whitespace inside, at the bound', `x${' '.repeat(4094)}y`, true],
    ['whitespace inside, one over the bound', `x${' '.repeat(4095)}y`, false],
  ];

  it.each([
    ['HandoffRequestSchema', 'targetPath', HandoffRequestSchema.entries.targetPath],
    ['NewSessionRequestSchema', 'projectPath', NewSessionRequestSchema.entries.projectPath],
  ])('bounds the trimmed text of %s.%s, not the value that arrives', (def, name, schema) => {
    const artifact = property(def, name);
    expect(artifact.maxLength, 'a bound on the untrimmed value').toBeUndefined();
    for (const [what, value, accepted] of cases) {
      expect(safeParse(schema, value).success, `shared/ and ${what}`).toBe(accepted);
      expect(accepts(artifact, value), `the file and ${what}`).toBe(accepted);
    }
  });

  it('accepts what shared/ accepts where a trimmed string has no upper bound', () => {
    const artifact = property('HandoffRequestSchema', 'targetDeviceId');
    for (const [what, value] of cases) {
      const schema = HandoffRequestSchema.entries.targetDeviceId;
      expect(accepts(artifact, value), what).toBe(safeParse(schema, value).success);
    }
  });

  it('leaves no trace of the generator in the file', () => {
    expect(renderApiSchema()).not.toContain('x-hoplet');
  });
});
