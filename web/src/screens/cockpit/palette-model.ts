import type { ReactNode } from 'react';

/**
 * The command palette's list (docs/spec.md#command-palette-and-keyboard-shortcuts): sources give the entries of
 * one kind each (sessions, machines, projects, commands, lines…), the palette matches them against the query and
 * lists each source as a section. A new kind of entry is a new source; nothing here knows the kinds.
 */

/** A confirmation an entry asks for in place before it runs (Stop): what ends, what stays, what goes with it. */
export type PaletteConfirm = {
  title: string;
  text: string;
  /** What goes with it, one line each (the child sessions a stop also stops). */
  list?: readonly { id: string; label: string; lead?: ReactNode }[];
  /** The confirming button's label. */
  action: string;
  /** The footer's word for Enter while it asks ("to stop"). */
  verb: string;
};

export type PaletteItem = {
  id: string;
  /** Names the row for tests: `cockpit.palette-<kind>`. */
  kind: string;
  /** What the row shows and the query is matched on, letters highlighted. */
  label: string;
  /** Matched as well when the label does not match, never highlighted (a session's machine and directory). */
  keywords?: readonly string[];
  lead?: ReactNode;
  /** Shown after the label when a query is typed, where the section heading no longer names it. */
  context?: string;
  /** Shown at the row's end before its keys. */
  meta?: ReactNode;
  keys?: readonly string[];
  /** Listed only when a query is typed. */
  queryOnly?: boolean;
  /** Reached with ⌃1–⌃9 in the order the list shows them. */
  numbered?: boolean;
  dim?: boolean;
  muted?: boolean;
  /** The footer's word for Enter on this row; "to open" when absent. */
  verb?: string;
  confirm?: PaletteConfirm;
  run: () => void;
};

export type PaletteSource = {
  id: string;
  /** The section heading with no query and with one; sections of one heading next to each other merge. */
  label: string;
  queryLabel?: string;
  /** Shown at the heading's end with no query (the open session's title over its commands). */
  aside?: string;
  items: readonly PaletteItem[];
  /** `fuzzy`: the palette matches and ranks the items; `given`: the source matched them itself and they show as given. */
  match?: 'fuzzy' | 'given';
  /** With no query or only with one; both when absent. */
  when?: 'empty' | 'query';
  /** The most rows the section shows: one for no query and one for a query. */
  limit?: number;
  queryLimit?: number;
  /** The source is still looking (a search over the lines). */
  busy?: boolean;
  busyLabel?: string;
};

export type Match = { score: number; at: readonly number[] };

const WORD = /[\s\-_/.·:]/;

/**
 * Whether the query's letters appear in the text in order, case aside. A run of the query inside the text ranks
 * above scattered letters, at the start or at a word above elsewhere; `at` are the matched positions.
 */
export function fuzzy(text: string, query: string): Match | null {
  const q = query.trim().toLowerCase();
  if (!q) return { score: 0, at: [] };
  const lower = text.toLowerCase();
  const run = lower.indexOf(q);
  if (run >= 0) {
    const atWord = run === 0 || WORD.test(lower[run - 1] ?? '');
    return {
      score: (run === 0 ? 3000 : atWord ? 2000 : 1000) - run,
      at: Array.from({ length: q.length }, (_, i) => run + i),
    };
  }
  const at: number[] = [];
  let j = 0;
  for (let i = 0; i < lower.length && j < q.length; i++) {
    if (lower[i] === q[j]) {
      at.push(i);
      j++;
    }
  }
  if (j < q.length) return null;
  const first = at[0] ?? 0;
  const spread = (at[at.length - 1] ?? 0) - first;
  const words = at.filter((i) => i === 0 || WORD.test(lower[i - 1] ?? '')).length;
  return { score: 500 + words * 50 - spread - first, at };
}

export type ListedItem = { item: PaletteItem; at: readonly number[] | null; key?: string };
export type Section = {
  id: string;
  label: string;
  aside?: string;
  items: ListedItem[];
  busy?: boolean;
  busyLabel?: string;
};

const MAX_NUMBERED = 9;

/** The sections the palette lists for a query, in the sources' order, with ⌃1–⌃9 given to the numbered rows. */
export function arrange(sources: readonly PaletteSource[], query: string): Section[] {
  const q = query.trim();
  const sections: Section[] = [];
  let numbered = 0;
  for (const source of sources) {
    if ((source.when === 'empty' && q) || (source.when === 'query' && !q)) continue;
    const limit = (q ? source.queryLimit : source.limit) ?? Number.POSITIVE_INFINITY;
    let items: ListedItem[];
    if (!q || source.match === 'given') {
      items = source.items
        .filter((item) => !(item.queryOnly && !q))
        .map((item) => ({ item, at: null }));
    } else {
      items = source.items
        .map((item, order) => {
          const own = fuzzy(item.label, q);
          if (own) return { item, at: own.at, score: own.score, order };
          const other = (item.keywords ?? [])
            .map((k) => fuzzy(k, q))
            .reduce<Match | null>(
              (best, m) => (m && (!best || m.score > best.score) ? m : best),
              null,
            );
          return other ? { item, at: [], score: other.score / 2, order } : null;
        })
        .filter((x): x is NonNullable<typeof x> => x !== null)
        .sort((a, b) => b.score - a.score || a.order - b.order)
        .map(({ item, at }) => ({ item, at }));
    }
    items = items
      .slice(0, limit)
      .map((listed) =>
        listed.item.numbered && numbered < MAX_NUMBERED
          ? { ...listed, key: `^${++numbered}` }
          : listed,
      );
    const label = q ? (source.queryLabel ?? source.label) : source.label;
    const previous = sections[sections.length - 1];
    if (previous && previous.label === label && q) {
      previous.items.push(...items);
      continue;
    }
    if (items.length === 0 && !source.busy) continue;
    sections.push({
      id: source.id,
      label,
      ...(!q && source.aside ? { aside: source.aside } : {}),
      items,
      ...(source.busy ? { busy: true, busyLabel: source.busyLabel ?? '' } : {}),
    });
  }
  return sections;
}

/** The rows of the sections in the order the arrows move through them. */
export function flatten(sections: readonly Section[]): ListedItem[] {
  return sections.flatMap((s) => s.items);
}
