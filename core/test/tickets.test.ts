import type { TokenGrant } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import type { Principal } from '../src/api/principal.ts';
import { createTickets } from '../src/api/tickets.ts';

const grant = { abilities: ['read'], allow: [] } as unknown as TokenGrant;
const token = (id: string): Principal => ({
  kind: 'token',
  token: { id, kind: 'scoped', name: id, grant, expiresAt: Number.MAX_SAFE_INTEGER },
});

describe('browser WebSocket tickets', () => {
  it('redeems a ticket once, as the principal it was minted for', () => {
    const tickets = createTickets(() => 0);
    const { ticket } = tickets.mint({ kind: 'host' });
    expect(tickets.redeem(ticket)).toEqual({ kind: 'host' });
    expect(tickets.redeem(ticket)).toBeNull();
  });

  it("keeps an API token's tickets from pushing out the host's", () => {
    const tickets = createTickets(() => 0);
    const host = tickets.mint({ kind: 'host' }).ticket;
    const minted = Array.from({ length: 300 }, () => tickets.mint(token('a')).ticket);
    expect(tickets.redeem(host)).toEqual({ kind: 'host' });
    expect(tickets.redeem(minted[0] ?? '')).toBeNull();
    expect(tickets.redeem(minted[299] ?? '')?.kind).toBe('token');
    const kept = minted.slice(284).filter((t) => tickets.redeem(t) !== null);
    expect(kept).toHaveLength(15);
  });

  it("voids a revoked token's tickets", () => {
    const tickets = createTickets(() => 0);
    const { ticket } = tickets.mint(token('a'));
    tickets.revoke('a');
    expect(tickets.redeem(ticket)).toBeNull();
  });
});
