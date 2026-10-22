import {
  DEFAULT_RESOURCE_THRESHOLDS,
  type ResourceThresholds,
  ResourceThresholdsSchema,
  safeParse,
} from '@hoplet/shared';
import { auditedChange } from './audit.ts';
import type { StoreContext } from './layout.ts';

/** The stored thresholds; the defaults while none are stored or the stored ones are unreadable. */
export function resourceThresholds(ctx: StoreContext): ResourceThresholds {
  const row = ctx.db.prepare('SELECT json FROM resource_thresholds WHERE id = 1').get() as
    | { json: string }
    | undefined;
  if (!row) return structuredClone(DEFAULT_RESOURCE_THRESHOLDS);
  try {
    const parsed = safeParse(ResourceThresholdsSchema, JSON.parse(row.json));
    if (parsed.success) return parsed.output;
  } catch {
    // Unreadable thresholds are the defaults.
  }
  return structuredClone(DEFAULT_RESOURCE_THRESHOLDS);
}

export function setResourceThresholds(ctx: StoreContext, thresholds: ResourceThresholds): void {
  const before = resourceThresholds(ctx) as unknown as Record<string, unknown>;
  const after = thresholds as unknown as Record<string, unknown>;
  const fields = Object.keys(after).filter(
    (key) => JSON.stringify(before[key]) !== JSON.stringify(after[key]),
  );
  auditedChange(
    ctx,
    () =>
      ctx.db
        .prepare(
          'INSERT INTO resource_thresholds(id, json) VALUES(1, ?) ON CONFLICT(id) DO UPDATE SET json = excluded.json',
        )
        .run(JSON.stringify(thresholds)),
    () => ({
      type: 'config.changed',
      summary: {
        area: 'resource-thresholds',
        action: 'updated',
        fields,
        previous: Object.fromEntries(fields.map((key) => [key, before[key]])),
        current: Object.fromEntries(fields.map((key) => [key, after[key]])),
      },
    }),
  );
}
