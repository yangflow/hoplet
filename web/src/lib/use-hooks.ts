import type {
  HookDelivery,
  HookInfo,
  HookListResponse,
  HookSecretResponse,
  HookTestResponse,
  WebhookHookInfo,
} from '@hoplet/shared';
import { useCallback, useEffect, useState } from 'react';
import {
  deleteHook,
  fetchHookDeliveries,
  fetchHooks,
  patchHook,
  postHook,
  postHookTest,
  rotateHookSecret,
} from './api.ts';
import { errorText } from './errors.ts';
import { type HookForm, toCreate, toUpdate } from './hooks.ts';
import type { Loaded } from './use-usage.ts';

/** The secret a fixture hands out. It signs nothing. */
export const FIXTURE_SECRET = 'whsec_EXAMPLE-NOT-A-REAL-SECRET-0000000000000000';

export type HooksState = Loaded<HookListResponse> & {
  /** Adds a webhook; the answer carries the signing secret, shown once. Null when the core refused. */
  create: (form: HookForm) => Promise<HookSecretResponse | null>;
  update: (form: HookForm) => Promise<boolean>;
  remove: (id: string) => Promise<boolean>;
  rotate: (id: string) => Promise<HookSecretResponse | null>;
  toggle: (hook: HookInfo, enabled: boolean) => Promise<boolean>;
  test: (id: string) => Promise<HookTestResponse | null>;
  saving: boolean;
  /** The core's reason for the last refused request, or null. */
  problem: string | null;
  clearProblem: () => void;
};

function webhookOf(form: HookForm, id: string, at: number): WebhookHookInfo {
  return {
    id,
    kind: 'webhook',
    label: form.label.trim(),
    url: form.url.trim().split('?')[0] ?? '',
    events: form.events,
    scope:
      form.scope === 'all'
        ? { kind: 'all' }
        : { kind: 'project', deviceId: form.deviceId, projectPath: form.projectPath },
    summary: form.summary,
    enabled: form.enabled,
    status: form.enabled ? 'active' : 'disabled',
    version: 1,
    createdAt: at,
    updatedAt: at,
    pending: 0,
  };
}

/**
 * The hooks of the host and their changes. A `fixture` is the answer itself and the changes stay local.
 */
export function useHooks(fixture?: HookListResponse): HooksState {
  const [data, setData] = useState<HookListResponse | null>(fixture ?? null);
  const [loading, setLoading] = useState(fixture === undefined);
  const [error, setError] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [again, setAgain] = useState(0);
  const locked = fixture !== undefined;

  // biome-ignore lint/correctness/useExhaustiveDependencies: `again` is the reload after a change
  useEffect(() => {
    if (locked) return;
    let live = true;
    fetchHooks()
      .then((r) => {
        if (!live) return;
        setData(r);
        setError(null);
        setLoading(false);
      })
      .catch((e) => {
        if (!live) return;
        setError(errorText(e));
        setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [locked, again]);

  const run = useCallback(
    async <T>(local: () => T, remote: () => Promise<T>): Promise<T | null> => {
      setProblem(null);
      if (locked) return local();
      setSaving(true);
      try {
        const answer = await remote();
        setAgain((n) => n + 1);
        return answer;
      } catch (e) {
        setProblem(errorText(e));
        return null;
      } finally {
        setSaving(false);
      }
    },
    [locked],
  );

  const change = useCallback(
    (fn: (hooks: HookInfo[]) => HookInfo[]) =>
      setData((d) => (d ? { ...d, hooks: fn(d.hooks) } : d)),
    [],
  );

  const create = useCallback(
    (form: HookForm) =>
      run<HookSecretResponse>(
        () => {
          const at = Date.now();
          const hook = webhookOf(form, `hook-${at.toString(36)}`, at);
          change((hooks) => {
            const scripts = hooks.filter((h) => h.kind === 'script');
            return [...hooks.filter((h) => h.kind === 'webhook'), hook, ...scripts];
          });
          return { hook, secret: FIXTURE_SECRET };
        },
        () => postHook(toCreate(form)),
      ),
    [run, change],
  );

  const update = useCallback(
    async (form: HookForm) => {
      const id = form.id;
      if (id === null) return false;
      const ok = await run<boolean>(
        () => {
          change((hooks) =>
            hooks.map((h) => {
              if (h.id !== id || h.kind !== 'webhook') return h;
              const next = webhookOf(form, id, h.updatedAt);
              return {
                ...next,
                createdAt: h.createdAt,
                status: h.status === 'needs-secret' ? h.status : next.status,
                ...(h.lastDelivery ? { lastDelivery: h.lastDelivery } : {}),
              };
            }),
          );
          return true;
        },
        async () => {
          await patchHook(id, toUpdate(form));
          return true;
        },
      );
      return ok === true;
    },
    [run, change],
  );

  const remove = useCallback(
    async (id: string) =>
      (await run<boolean>(
        () => {
          change((hooks) => hooks.filter((h) => h.id !== id));
          return true;
        },
        async () => {
          await deleteHook(id);
          return true;
        },
      )) === true,
    [run, change],
  );

  const rotate = useCallback(
    (id: string) =>
      run<HookSecretResponse>(
        () => {
          const hook = data?.hooks.find((h) => h.id === id);
          if (hook?.kind !== 'webhook') throw new Error('not a webhook');
          const next: WebhookHookInfo = { ...hook, status: hook.enabled ? 'active' : 'disabled' };
          change((hooks) => hooks.map((h) => (h.id === id ? next : h)));
          return { hook: next, secret: FIXTURE_SECRET };
        },
        () => rotateHookSecret(id),
      ),
    [run, change, data],
  );

  const toggle = useCallback(
    async (hook: HookInfo, enabled: boolean) =>
      (await run<boolean>(
        () => {
          change((hooks) =>
            hooks.map((h) => {
              if (h.id !== hook.id) return h;
              const status = enabled ? 'active' : 'disabled';
              if (h.kind === 'script') return { ...h, enabled, status };
              return { ...h, enabled, status: h.status === 'needs-secret' ? h.status : status };
            }),
          );
          return true;
        },
        async () => {
          await patchHook(hook.id, { enabled });
          return true;
        },
      )) === true,
    [run, change],
  );

  const test = useCallback(
    (id: string) =>
      run<HookTestResponse>(
        () => {
          const hook = data?.hooks.find((h) => h.id === id);
          if (!hook) throw new Error('unknown hook');
          const at = Date.now();
          return {
            hook,
            delivery: {
              id: `dlv-${at.toString(36)}`,
              eventId: 'evt_00000000000000000000000000',
              type: 'hook.test',
              state: 'delivered',
              attempts: 1,
              createdAt: at,
              settledAt: at,
            },
          };
        },
        () => postHookTest(id),
      ),
    [run, data],
  );

  return {
    data,
    loading,
    error,
    create,
    update,
    remove,
    rotate,
    toggle,
    test,
    saving,
    problem,
    clearProblem: useCallback(() => setProblem(null), []),
  };
}

/** The recent deliveries of one hook, asked again when `version` changes. A `fixture` is the answer itself. */
export function useHookDeliveries(
  id: string | null,
  version: number,
  fixture?: HookDelivery[],
): Loaded<HookDelivery[]> {
  const [data, setData] = useState<HookDelivery[] | null>(fixture ?? null);
  const [error, setError] = useState<string | null>(null);
  const locked = fixture !== undefined;

  // biome-ignore lint/correctness/useExhaustiveDependencies: `version` is the reload after a test or a change
  useEffect(() => {
    if (locked || id === null) return;
    const abort = new AbortController();
    setData(null);
    fetchHookDeliveries(id, abort.signal)
      .then((r) => {
        setData(r.deliveries);
        setError(null);
      })
      .catch((e) => {
        if (abort.signal.aborted) return;
        setError(errorText(e));
      });
    return () => abort.abort();
  }, [id, locked, version]);

  return {
    data: id === null ? null : data,
    loading: id !== null && data === null && error === null,
    error,
  };
}
