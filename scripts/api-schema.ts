// The machine-readable client contract (docs/api.md#json-schema): one JSON Schema document built from the
// route table and shared/. `scripts/test/api-docs.test.ts` fails when the committed file differs from what
// this builds, so `pnpm check` catches a schema that changed without regeneration.
//   node scripts/api-schema.ts        writes docs/schema/hoplet-api.schema.json
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { type ConversionConfig, type JsonSchema, toJsonSchema } from '@valibot/to-json-schema';
import { routes } from '../core/src/api/routes.ts';
import * as shared from '../shared/src/index.ts';

const root = path.resolve(import.meta.dirname, '..');

/** Where the artifact is committed, relative to the repository root. */
export const ARTIFACT_PATH = 'docs/schema/hoplet-api.schema.json';

/** The three schemas a route can declare; each is named by an export of shared/, and that is its name in the artifact. */
type Role = 'query' | 'body' | 'response';

export interface RouteEntry {
  method: string;
  path: string;
  auth: string;
  lane: string;
  status: number;
  query: string | null;
  body: string | null;
  response: string;
}

export type BrowserDirection = 'web-to-core' | 'core-to-web';

export interface BrowserMessageEntry {
  type: string;
  direction: BrowserDirection;
  lane: string;
  schema: string;
}

/** Marks a string that `shared/` trims until `settleTrimmed` states its rule; the mark is not in the artifact. */
const TRIMMED = 'x-hoplet-trimmed';

const CONVERSION: ConversionConfig = {
  target: 'draft-2020-12',
  errorMode: 'throw',
  // `trim` has no JSON Schema form, and the length bounds that follow it in a pipe apply to the trimmed text,
  // not to the value that arrives. `trim` only marks the string; `settleTrimmed` writes the rule once the bounds
  // are known.
  overrideAction: ({ valibotAction, jsonSchema }) => {
    if (valibotAction.type !== 'trim') return undefined;
    if (
      jsonSchema.type !== 'string' ||
      jsonSchema.pattern !== undefined ||
      jsonSchema.minLength !== undefined ||
      jsonSchema.maxLength !== undefined
    ) {
      throw new Error('a trim after a pattern, a length bound or a type other than string');
    }
    return { ...jsonSchema, [TRIMMED]: true };
  },
};

/**
 * The pattern of a value whose text, once trimmed, is `min` to `max` characters long (`max` is unbounded when
 * undefined): any whitespace, the trimmed text, any whitespace. `\s` is the whitespace `String.prototype.trim`
 * strips, so a value is valid here exactly when `shared/` accepts it. Lengths are UTF-16 code units.
 */
function trimmedPattern(min: number, max: number | undefined): string {
  if (min < 1 || (max !== undefined && max < min)) {
    throw new Error(`a trimmed string of ${min} to ${max ?? 'any number of'} characters`);
  }
  // The first and the last character of the trimmed text are not whitespace; the ones between are anything.
  const between = `[\\s\\S]{${Math.max(min - 2, 0)},${max === undefined ? '' : max - 2}}\\S`;
  const rest = max === 1 ? '' : min <= 1 ? `(?:${between})?` : between;
  return `^\\s*\\S${rest}\\s*$`;
}

/**
 * Replaces the mark `trim` left with the rule for the string: the pattern of its trimmed text, which takes
 * over the length bounds. A string with no bound but one character says that with `\S`.
 */
function settleTrimmed(node: unknown): unknown {
  if (Array.isArray(node)) return node.map(settleTrimmed);
  if (typeof node !== 'object' || node === null) return node;
  const entries = Object.entries(node).map(([key, value]) => [key, settleTrimmed(value)] as const);
  if (!entries.some(([key]) => key === TRIMMED)) return Object.fromEntries(entries);
  const { pattern, minLength = 0, maxLength } = Object.fromEntries(entries);
  if (pattern !== undefined) throw new Error('a trimmed string with a pattern');
  const simple = maxLength === undefined && minLength === 1;
  const rule = simple
    ? '\\S'
    : trimmedPattern(minLength as number, maxLength as number | undefined);
  return Object.fromEntries(
    entries.flatMap(([key, value]) => {
      if (key === TRIMMED) return [['pattern', rule]];
      return !simple && (key === 'minLength' || key === 'maxLength') ? [] : [[key, value]];
    }),
  );
}

const exported = shared as unknown as Record<string, unknown>;

/** The `query`, `body` and `response` identifiers of each `defineRoute` entry of core/src/api/routes.ts. */
function declaredNames(): Map<string, Partial<Record<Role, string>>> {
  const source = readFileSync(path.join(root, 'core/src/api/routes.ts'), 'utf8');
  const found = new Map<string, Partial<Record<Role, string>>>();
  for (const entry of source.split('defineRoute({').slice(1)) {
    const method = /^\s+method: '(\w+)',$/m.exec(entry)?.[1];
    const routePath = /^\s+path: '([^']+)',$/m.exec(entry)?.[1];
    const names: Partial<Record<Role, string>> = {};
    for (const role of ['query', 'body', 'response'] as const) {
      const name = new RegExp(`^\\s+${role}: (\\w+),$`, 'm').exec(entry)?.[1];
      if (name) names[role] = name;
    }
    found.set(`${method} ${routePath}`, names);
  }
  return found;
}

/** Every route with the names of its schemas; a schema that is not an export of shared/ is an error. */
export function describeRoutes(): RouteEntry[] {
  const declared = declaredNames();
  return routes.map((route) => {
    const key = `${route.method} ${route.path}`;
    const names = declared.get(key) ?? {};
    const nameOf = (role: Role): string | null => {
      const schema = route[role];
      if (!schema) return null;
      const name = names[role];
      if (!name || exported[name] !== schema) {
        throw new Error(
          `${key}: its ${role} schema must be an export of shared/ named in routes.ts`,
        );
      }
      return name;
    };
    const response = nameOf('response');
    if (!response) throw new Error(`${key}: no response schema`);
    return {
      method: route.method,
      path: route.path,
      auth: route.auth,
      lane: route.lane,
      status: route.status,
      query: nameOf('query'),
      body: nameOf('body'),
      response,
    };
  });
}

/** The schema name of a wire message: `session.output` is `SessionOutputMessage`. */
export function messageSchemaName(type: string): string {
  const words = type.split('.').map((word) => word.charAt(0).toUpperCase() + word.slice(1));
  return `${words.join('')}Message`;
}

/**
 * The messages of the browser WebSocket in the order of the wire table. `error` is declared for the agent
 * connection, but the browser socket answers a message it cannot take with it too.
 */
export function describeBrowserMessages(): BrowserMessageEntry[] {
  const types = [...shared.wireTypes('web-to-core', 'core-to-web'), 'error' as const];
  return types.map((type) => {
    const message = shared.WIRE_MESSAGES[type];
    const direction = message.direction === 'web-to-core' ? 'web-to-core' : 'core-to-web';
    return { type, direction, lane: message.lane, schema: messageSchemaName(type) };
  });
}

function convert(schema: unknown, what: string): JsonSchema {
  try {
    const { $schema: _draft, ...converted } = toJsonSchema(
      schema as Parameters<typeof toJsonSchema>[0],
      CONVERSION,
    );
    return settleTrimmed(converted) as JsonSchema;
  } catch (cause) {
    const reason = cause instanceof Error ? cause.message : String(cause);
    throw new Error(`${what}: ${reason}`, { cause });
  }
}

export interface Exclusion {
  schema: string;
  routes: string[];
  reason: string;
}

/** The artifact as an object: the index of routes and messages, the exclusions, and every schema by name. */
export function buildApiSchema() {
  const defs = new Map<string, JsonSchema>();
  const define = (name: string, schema: JsonSchema): void => {
    const existing = defs.get(name);
    if (existing && JSON.stringify(existing) !== JSON.stringify(schema)) {
      throw new Error(`two different schemas are named ${name}`);
    }
    defs.set(name, schema);
  };

  const described = describeRoutes();
  const excluded = new Map<string, Exclusion>();
  for (const [i, route] of described.entries()) {
    const key = `${route.method} ${route.path}`;
    const source = routes[i];
    if (route.body) define(route.body, convert(source?.body, `${key} body ${route.body}`));
    define(route.response, convert(source?.response, `${key} response ${route.response}`));
    if (!route.query) continue;
    // A query string arrives as text, so a query schema may coerce or check it in ways JSON Schema cannot say.
    // Such a schema is listed with the reason, never dropped (docs/api.md#json-schema).
    try {
      define(route.query, convert(source?.query, `${key} query ${route.query}`));
    } catch (error) {
      const entry = excluded.get(route.query) ?? {
        schema: route.query,
        routes: [],
        reason:
          error instanceof Error && error.cause instanceof Error
            ? error.cause.message
            : String(error),
      };
      entry.routes.push(key);
      excluded.set(route.query, entry);
    }
  }

  define('ErrorResponseSchema', convert(shared.ErrorResponseSchema, 'ErrorResponseSchema'));
  const messages = describeBrowserMessages();
  for (const message of messages) {
    const type = message.type as keyof typeof shared.WIRE_MESSAGES;
    define(
      message.schema,
      convert(shared.WIRE_MESSAGES[type].schema, `browser message ${message.type}`),
    );
  }

  return {
    $schema: 'https://json-schema.org/draft/2020-12/schema',
    title: 'Hoplet client API',
    description:
      'Generated from shared/ and the route table by `pnpm schema`; do not edit. Each name under $defs is a schema ' +
      'that docs/api.md names for a route or a message. Parse a body against the schema its route names.',
    protocolVersion: shared.PROTOCOL_VERSION,
    routes: described,
    browserMessages: messages,
    errorResponse: 'ErrorResponseSchema',
    errorStatus: shared.HTTP_ERROR_STATUS,
    excluded: [...excluded.values()],
    $defs: Object.fromEntries([...defs].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))),
  };
}

export function renderApiSchema(): string {
  return `${JSON.stringify(buildApiSchema(), null, 2)}\n`;
}

if (import.meta.main) {
  const file = path.join(root, ARTIFACT_PATH);
  mkdirSync(path.dirname(file), { recursive: true });
  writeFileSync(file, renderApiSchema());
  console.log(`wrote ${ARTIFACT_PATH}`);
}
