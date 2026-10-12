import * as v from 'valibot';

/** POST /api/ws-tickets: a single-use credential for one browser WebSocket upgrade (`/ws?ticket=`). */
export const WsTicketResponseSchema = v.object({
  ticket: v.string(),
  /** Seconds the ticket stays valid. */
  expiresInSec: v.number(),
});
export type WsTicketResponse = v.InferOutput<typeof WsTicketResponseSchema>;
