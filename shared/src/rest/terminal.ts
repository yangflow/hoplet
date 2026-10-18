import * as v from 'valibot';
import { TerminalIdSchema } from '../ids.ts';
import { SessionPortSchema, TERMINAL_MAX_COLS, TERMINAL_MAX_ROWS } from '../wire/index.ts';

const Cols = v.pipe(v.number(), v.integer(), v.minValue(1), v.maxValue(TERMINAL_MAX_COLS));
const Rows = v.pipe(v.number(), v.integer(), v.minValue(1), v.maxValue(TERMINAL_MAX_ROWS));

/** POST /api/sessions/:id/terminal: opens the restricted terminal of the session's owner machine (ADR 0026). */
export const TerminalOpenRequestSchema = v.object({ cols: Cols, rows: Rows });
export type TerminalOpenRequest = v.InferOutput<typeof TerminalOpenRequestSchema>;

/** An open terminal; the stream is attached on the browser WebSocket with `terminal.attach`. */
export const TerminalInfoSchema = v.object({
  terminalId: TerminalIdSchema,
  sessionId: v.string(),
  cols: Cols,
  rows: Rows,
});
export type TerminalInfo = v.InferOutput<typeof TerminalInfoSchema>;

/** GET /api/sessions/:id/terminal: the session's open terminal, or none. */
export const TerminalStateSchema = v.object({ terminal: v.nullable(TerminalInfoSchema) });
export type TerminalState = v.InferOutput<typeof TerminalStateSchema>;

/** DELETE /api/terminals/:id */
export const TerminalCloseResultSchema = v.object({ ok: v.literal(true) });
export type TerminalCloseResult = v.InferOutput<typeof TerminalCloseResultSchema>;

/** GET /api/sessions/:id/ports: the ports last reported for the session, with the host to link them with. */
export const SessionPortsResultSchema = v.object({
  previewHost: v.optional(v.string()),
  ports: v.array(SessionPortSchema),
});
export type SessionPortsResult = v.InferOutput<typeof SessionPortsResultSchema>;
