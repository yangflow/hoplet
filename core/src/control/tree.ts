// Trees of work (#81, ADR 0035): the sessions one session started, and those they started. The relation is
// recorded when a session starts (`session_children`) and lasts as long as the session; a chain lists the sessions
// that started it, root first, so the last one is the parent, the first the root, and the chain's length the
// depth below the root. A session no session started has no chain.
/** The creation chains by session. */
export type Chains = ReadonlyMap<string, readonly string[]>;

/** States of a session whose process is up (it counts as a live child). */
export const LIVE_STATES: ReadonlySet<string> = new Set([
  'starting',
  'ready',
  'running',
  'waiting-permission',
]);

/** The root of the tree `sessionId` belongs to: the first session of its chain, or itself. */
export function rootOf(chains: Chains, sessionId: string): string {
  return chains.get(sessionId)?.[0] ?? sessionId;
}

/** The session that started `sessionId`; null when none did. */
export function parentOf(chains: Chains, sessionId: string): string | null {
  const chain = chains.get(sessionId);
  return chain && chain.length > 0 ? (chain[chain.length - 1] as string) : null;
}

/** How many sessions each session started: one scan of the chains. */
export function childCounts(chains: Chains): Map<string, number> {
  const out = new Map<string, number>();
  for (const chain of chains.values()) {
    const parent = chain[chain.length - 1];
    if (parent !== undefined) out.set(parent, (out.get(parent) ?? 0) + 1);
  }
  return out;
}

/** Every descendant of `sessionId` with its depth below it, in pre-order (a parent before its children). */
export function descendantsOf(
  chains: Chains,
  sessionId: string,
): { sessionId: string; depth: number }[] {
  const children = new Map<string, string[]>();
  for (const [id, chain] of chains) {
    const parent = chain[chain.length - 1];
    if (parent === undefined) continue;
    const list = children.get(parent);
    if (list) list.push(id);
    else children.set(parent, [id]);
  }
  const out: { sessionId: string; depth: number }[] = [];
  const seen = new Set<string>([sessionId]);
  const walk = (id: string, depth: number): void => {
    for (const child of children.get(id) ?? []) {
      if (seen.has(child)) continue;
      seen.add(child);
      out.push({ sessionId: child, depth });
      walk(child, depth + 1);
    }
  };
  walk(sessionId, 1);
  return out;
}

/** The sessions below the root `root`, whatever their depth (the root itself is not one of them). */
export function membersOf(chains: Chains, root: string): string[] {
  const out: string[] = [];
  for (const [id, chain] of chains) if (chain[0] === root && id !== root) out.push(id);
  return out;
}
