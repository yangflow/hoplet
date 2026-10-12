import { randomBytes } from 'node:crypto';

/** How long a ticket can be redeemed (docs/hosting.md#network). */
export const TICKET_TTL_MS = 30_000;

/** Tickets that may wait to be redeemed; minting beyond it forgets the oldest. */
const MAX_OUTSTANDING = 256;

export interface Tickets {
  /** A new single-use ticket for one browser WebSocket upgrade. */
  mint(): { ticket: string; expiresInSec: number };
  /** True once for a ticket that was minted and has not expired; false for anything else. */
  redeem(ticket: string): boolean;
}

export function createTickets(now: () => number = Date.now): Tickets {
  const expiry = new Map<string, number>();
  return {
    mint() {
      const t = now();
      for (const [ticket, expires] of expiry) {
        if (expires <= t) expiry.delete(ticket);
      }
      while (expiry.size >= MAX_OUTSTANDING) {
        const oldest = expiry.keys().next().value;
        if (oldest === undefined) break;
        expiry.delete(oldest);
      }
      const ticket = randomBytes(32).toString('hex');
      expiry.set(ticket, t + TICKET_TTL_MS);
      return { ticket, expiresInSec: TICKET_TTL_MS / 1000 };
    },
    redeem(ticket) {
      const expires = expiry.get(ticket);
      expiry.delete(ticket);
      return expires !== undefined && now() < expires;
    },
  };
}
