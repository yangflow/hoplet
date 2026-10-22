import { randomBytes } from 'node:crypto';
import type { Principal } from './principal.ts';

/** How long a ticket can be redeemed (docs/hosting.md#network). */
export const TICKET_TTL_MS = 30_000;

/** Tickets that may wait to be redeemed; minting beyond it forgets the oldest. */
const MAX_OUTSTANDING = 256;

/** Unredeemed tickets one API token may hold, so its tickets never push out the host's. */
const MAX_OUTSTANDING_PER_TOKEN = 16;

export interface Tickets {
  /** A new single-use ticket for one browser WebSocket upgrade; the connection acts as `principal`. */
  mint(principal: Principal): { ticket: string; expiresInSec: number };
  /** The principal the ticket was minted for, once, for a ticket that has not expired; null for anything else. */
  redeem(ticket: string): Principal | null;
  /** An API token was revoked: its outstanding tickets are void and the sockets it opened close. */
  revoke(tokenId: string): void;
  /** Calls `listener` with the ID of each token revoked from now on. */
  onRevoke(listener: (tokenId: string) => void): void;
}

export function createTickets(now: () => number = Date.now): Tickets {
  const issued = new Map<string, { expires: number; principal: Principal }>();
  const listeners: ((tokenId: string) => void)[] = [];
  return {
    mint(principal) {
      const t = now();
      for (const [ticket, { expires }] of issued) {
        if (expires <= t) issued.delete(ticket);
      }
      if (principal.kind === 'token') {
        const own = [...issued].filter(
          ([, e]) => e.principal.kind === 'token' && e.principal.token.id === principal.token.id,
        );
        for (const [ticket] of own.slice(
          0,
          Math.max(0, own.length - MAX_OUTSTANDING_PER_TOKEN + 1),
        )) {
          issued.delete(ticket);
        }
      }
      while (issued.size >= MAX_OUTSTANDING) {
        const oldest = issued.keys().next().value;
        if (oldest === undefined) break;
        issued.delete(oldest);
      }
      const ticket = randomBytes(32).toString('hex');
      issued.set(ticket, { expires: t + TICKET_TTL_MS, principal });
      return { ticket, expiresInSec: TICKET_TTL_MS / 1000 };
    },
    redeem(ticket) {
      const entry = issued.get(ticket);
      issued.delete(ticket);
      return entry && now() < entry.expires ? entry.principal : null;
    },
    revoke(tokenId) {
      for (const [ticket, { principal }] of issued) {
        if (principal.kind === 'token' && principal.token.id === tokenId) issued.delete(ticket);
      }
      for (const listener of listeners) listener(tokenId);
    },
    onRevoke(listener) {
      listeners.push(listener);
    },
  };
}
