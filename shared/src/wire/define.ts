import * as v from 'valibot';
import { PROTOCOL_VERSION } from '../version.ts';

/**
 * The two lanes of the agent connection (docs/architecture.md#principles). Control work (prompts,
 * listings, handoff steps, presence) is never queued behind bulk work (session bytes, workspace transfer).
 */
export type Lane = 'control' | 'bulk';

export type Direction = 'device-to-core' | 'core-to-device' | 'core-to-web' | 'web-to-core';

interface Definition {
  readonly lane: Lane;
  readonly direction: Direction;
  readonly fields: v.ObjectEntries;
}

export interface WireMessage<K extends string, D extends Definition> {
  readonly type: K;
  readonly lane: D['lane'];
  readonly direction: D['direction'];
  /** `v` and `type` are part of every message; `fields` are the rest. */
  readonly schema: v.ObjectSchema<
    {
      readonly v: v.LiteralSchema<typeof PROTOCOL_VERSION, undefined>;
      readonly type: v.LiteralSchema<K, undefined>;
    } & D['fields'],
    undefined
  >;
}

/** Builds the table of messages keyed by wire `type`, adding the envelope (`v`, `type`) to each. */
export function defineWire<const T extends Record<string, Definition>>(
  definitions: T,
): { readonly [K in keyof T & string]: WireMessage<K, T[K]> } {
  const table: Record<string, unknown> = {};
  for (const [type, def] of Object.entries(definitions)) {
    table[type] = {
      type,
      lane: def.lane,
      direction: def.direction,
      schema: v.object({
        v: v.literal(PROTOCOL_VERSION),
        type: v.literal(type),
        ...def.fields,
      }),
    };
  }
  return table as { readonly [K in keyof T & string]: WireMessage<K, T[K]> };
}

/** The Go agent encodes empty slices as `null`; both read as an empty list. */
export function list<const S extends v.GenericSchema>(item: S) {
  return v.pipe(
    v.nullable(v.array(item)),
    v.transform((items) => items ?? []),
  );
}
