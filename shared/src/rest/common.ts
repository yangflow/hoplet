import * as v from 'valibot';

/** A string with surrounding whitespace removed, which must not be empty. */
export const TrimmedSchema = v.pipe(v.string(), v.trim(), v.minLength(1));

/**
 * An integer query parameter: absent or not a number reads as `fallback`; the value is truncated and
 * kept within [min, max].
 */
export function queryInt(
  fallback: number,
  min = Number.MIN_SAFE_INTEGER,
  max = Number.MAX_SAFE_INTEGER,
) {
  return v.optional(
    v.pipe(
      v.string(),
      v.transform((raw) => {
        const n = Number(raw);
        return Number.isFinite(n) ? Math.min(Math.max(Math.trunc(n), min), max) : fallback;
      }),
    ),
    String(fallback),
  );
}

/** A search string that holds something besides whitespace; it is used as typed. */
export const QuerySchema = v.pipe(
  v.string(),
  v.check((q) => q.trim().length > 0),
);
