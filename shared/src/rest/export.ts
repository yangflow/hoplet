import * as v from 'valibot';

/** The most bytes of the summary an export carries: the query string has to fit the request line. */
export const EXPORT_SUMMARY_MAX_BYTES = 3 * 1024;

/** The most strings the user may add to what an export masks, and the bytes of all of them together. */
export const EXPORT_MASK_MAX_ENTRIES = 20;
export const EXPORT_MASK_MAX_BYTES = 1024;

/** The shortest string an export masks on request: a shorter one would mask parts of ordinary words. */
export const EXPORT_MASK_MIN_CHARS = 3;

/** The most bytes of the net diff an export carries; the files past it are named and counted. */
export const EXPORT_DIFF_MAX_BYTES = 256 * 1024;

/** How much of the conversation an export carries: every turn, the user's messages and final replies, or none. */
export const ExportConversationSchema = v.picklist(['all', 'replies', 'none']);
export type ExportConversation = v.InferOutput<typeof ExportConversationSchema>;

const Flag = v.optional(v.picklist(['true', 'false']), 'false');

function validTimeZone(tz: string): boolean {
  try {
    new Intl.DateTimeFormat('en', { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

/**
 * GET /api/sessions/:id/export: what the Markdown file carries. `summary` is the user's text, written as given
 * (Hoplet writes none); `changes` the net diff against the base, `commands` the table of commands run, `machines`
 * the machines and hand-off points; `mask` the user's own strings to mask, one per line; `tz` the IANA time zone
 * the times are written in (UTC when absent).
 */
export const SessionExportQuerySchema = v.object({
  conversation: v.optional(ExportConversationSchema, 'all'),
  summary: v.optional(v.pipe(v.string(), v.maxBytes(EXPORT_SUMMARY_MAX_BYTES))),
  changes: Flag,
  commands: Flag,
  machines: Flag,
  mask: v.optional(
    v.pipe(
      v.string(),
      v.maxBytes(EXPORT_MASK_MAX_BYTES),
      v.transform((raw) =>
        raw
          .split('\n')
          .map((s) => s.trim())
          .filter((s) => s !== ''),
      ),
      v.maxLength(EXPORT_MASK_MAX_ENTRIES),
      v.check((list) => list.every((s) => [...s].length >= EXPORT_MASK_MIN_CHARS)),
    ),
  ),
  tz: v.optional(v.pipe(v.string(), v.maxLength(64), v.check(validTimeZone))),
});
export type SessionExportQuery = v.InferOutput<typeof SessionExportQuerySchema>;

/**
 * The exported file: Markdown (`text/markdown; charset=utf-8`), streamed as it is written, with front matter
 * whose `turns` says how many turns the conversation section will hold.
 */
export const SessionExportMarkdownSchema = v.string();
