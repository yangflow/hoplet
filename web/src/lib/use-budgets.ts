import type { Budget, BudgetBody, BudgetListResponse } from '@hoplet/shared';
import { useCallback, useEffect, useState } from 'react';
import {
  deleteBudget,
  deleteBudgetAllow,
  fetchBudgets,
  postBudget,
  postBudgetAllow,
  putBudget,
} from './api.ts';
import { errorText } from './errors.ts';
import { BACKFILL_POLL_MS, type Loaded } from './use-usage.ts';

export type BudgetsState = Loaded<BudgetListResponse> & {
  /** Saves a new budget (`id` null) or changes one; resolves false and shows the error when the core refused. */
  save: (id: string | null, body: BudgetBody) => Promise<boolean>;
  remove: (id: string) => Promise<boolean>;
  /** Lifts the pause of a budget until its period ends. */
  allow: (id: string) => Promise<boolean>;
  /** Takes that back. */
  stopAllowing: (id: string) => Promise<boolean>;
  saving: boolean;
  /** The core's reason for the last refused change, or null. */
  problem: string | null;
};

/** `allowed` in the list the way the core would answer, for fixtures that keep their changes locally. */
function lifted(b: Budget, on: boolean): Budget {
  return on
    ? { ...b, state: 'allowed', paused: false }
    : { ...b, state: b.percent > 100 ? 'over' : b.state, paused: b.onLimit === 'pause' };
}

/**
 * The budgets with the spend of their current periods, and their changes. While the core is still counting
 * existing sessions the list is asked again every few seconds. A `fixture` is the answer itself and the changes
 * stay local.
 */
export function useBudgets(fixture?: BudgetListResponse): BudgetsState {
  const [data, setData] = useState<BudgetListResponse | null>(fixture ?? null);
  const [loading, setLoading] = useState(fixture === undefined);
  const [error, setError] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [again, setAgain] = useState(0);
  const locked = fixture !== undefined;
  const backfilling = data?.backfilling === true;

  // biome-ignore lint/correctness/useExhaustiveDependencies: `again` is the poll tick and the reload after a change
  useEffect(() => {
    if (locked) return;
    const abort = new AbortController();
    fetchBudgets(abort.signal)
      .then((r) => {
        setData(r);
        setError(null);
        setLoading(false);
      })
      .catch((e) => {
        if (abort.signal.aborted) return;
        setError(errorText(e));
        setLoading(false);
      });
    return () => abort.abort();
  }, [locked, again]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: `again` re-arms the timer after each poll
  useEffect(() => {
    if (locked || !backfilling) return;
    const timer = setTimeout(() => setAgain((n) => n + 1), BACKFILL_POLL_MS);
    return () => clearTimeout(timer);
  }, [locked, backfilling, again]);

  const run = useCallback(
    async (
      local: (d: BudgetListResponse) => BudgetListResponse,
      remote: () => Promise<unknown>,
    ) => {
      setProblem(null);
      if (locked) {
        setData((d) => (d ? local(d) : d));
        return true;
      }
      setSaving(true);
      try {
        await remote();
        setAgain((n) => n + 1);
        return true;
      } catch (e) {
        setProblem(errorText(e));
        return false;
      } finally {
        setSaving(false);
      }
    },
    [locked],
  );

  const save = useCallback(
    (id: string | null, body: BudgetBody) =>
      run(
        (d) => d,
        () => (id === null ? postBudget(body) : putBudget(id, body)),
      ),
    [run],
  );
  const remove = useCallback(
    (id: string) =>
      run(
        (d) => ({ ...d, budgets: d.budgets.filter((b) => b.id !== id) }),
        () => deleteBudget(id),
      ),
    [run],
  );
  const allow = useCallback(
    (id: string) =>
      run(
        (d) => ({ ...d, budgets: d.budgets.map((b) => (b.id === id ? lifted(b, true) : b)) }),
        () => postBudgetAllow(id),
      ),
    [run],
  );
  const stopAllowing = useCallback(
    (id: string) =>
      run(
        (d) => ({ ...d, budgets: d.budgets.map((b) => (b.id === id ? lifted(b, false) : b)) }),
        () => deleteBudgetAllow(id),
      ),
    [run],
  );

  return { data, loading, error, save, remove, allow, stopAllowing, saving, problem };
}

/**
 * The budgets as the session's chip lists them, asked again when `refresh` changes (the session's line count).
 * A `fixture` is the answer itself. Nothing is shown while the core has not answered or refused.
 */
export function useBudgetList(
  refresh: number,
  fixture?: readonly Budget[],
  enabled = true,
): readonly Budget[] {
  const [list, setList] = useState<readonly Budget[]>(fixture ?? []);
  const locked = fixture !== undefined;

  // biome-ignore lint/correctness/useExhaustiveDependencies: `refresh` only decides when to ask again
  useEffect(() => {
    if (locked || !enabled) return;
    const abort = new AbortController();
    fetchBudgets(abort.signal)
      .then((r) => setList(r.budgets))
      .catch(() => {});
    return () => abort.abort();
  }, [locked, enabled, refresh]);

  return locked ? fixture : list;
}

/**
 * One budget, by the ID a refusal named, for the composer's allow action: its period end and amounts come from
 * the list. null while it loads or when the budget is gone. A `fixture` is the answer itself.
 */
export function useBudget(id: string | null, fixture?: Budget): Budget | null {
  const [budget, setBudget] = useState<Budget | null>(fixture ?? null);
  const locked = fixture !== undefined;

  useEffect(() => {
    if (locked || id === null) return;
    const abort = new AbortController();
    fetchBudgets(abort.signal)
      .then((r) => setBudget(r.budgets.find((b) => b.id === id) ?? null))
      .catch(() => {});
    return () => abort.abort();
  }, [locked, id]);

  if (locked) return fixture;
  return id === null ? null : budget?.id === id ? budget : null;
}
