import type {
  TokenCreateRequest,
  TokenCreateResponse,
  TokenInfo,
  TokenListResponse,
} from '@hoplet/shared';
import { MAX_ACTIVE_TOKENS } from '@hoplet/shared';
import { useCallback, useEffect, useState } from 'react';
import { deleteToken, fetchTokens, postToken } from './api.ts';
import { ApiError, errorText } from './errors.ts';
import { t } from './i18n.ts';
import type { Loaded } from './use-usage.ts';

export type TokensState = Loaded<TokenListResponse> & {
  /**
   * Issues a token. The answer carries the secret, which the caller shows once; nothing here keeps it. null when
   * the core refused, with the reason in `problem`.
   */
  issue: (body: TokenCreateRequest) => Promise<TokenCreateResponse | null>;
  /** Revokes a token; false when the core refused. */
  revoke: (id: string) => Promise<boolean>;
  busy: boolean;
  /** The core's reason for the last refused change, or null. */
  problem: string | null;
  clearProblem: () => void;
};

/** The copy of a refusal: the limit of active tokens has its own words. */
export function issueProblem(err: unknown): string {
  if (err instanceof ApiError && err.code === 'too-large') {
    return t(
      'The most Hoplet allows, {n} tokens, are active. Revoke one before issuing another.',
    ).replace('{n}', String(MAX_ACTIVE_TOKENS));
  }
  return errorText(err);
}

/** A token as the list shows it, from the answer that issued it: the secret is left behind. */
export function infoOf(issued: TokenCreateResponse): TokenInfo {
  const { token: _secret, ...info } = issued;
  return info;
}

/**
 * The API tokens and their changes. A `fixture` is the list itself and the changes stay local; its issued token
 * has a placeholder secret.
 */
export function useTokens(fixture?: TokenListResponse): TokensState {
  const [data, setData] = useState<TokenListResponse | null>(fixture ?? null);
  const [loading, setLoading] = useState(fixture === undefined);
  const [error, setError] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [again, setAgain] = useState(0);
  const locked = fixture !== undefined;

  // biome-ignore lint/correctness/useExhaustiveDependencies: `again` is the reload after a change
  useEffect(() => {
    if (locked) return;
    const abort = new AbortController();
    fetchTokens(abort.signal)
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

  const issue = useCallback(
    async (body: TokenCreateRequest): Promise<TokenCreateResponse | null> => {
      setProblem(null);
      if (locked) {
        const now = Date.now();
        const made: TokenCreateResponse = {
          id: `tok_${now.toString(16)}`,
          kind: 'scoped',
          name: body.name,
          grant: body.grant,
          createdAt: now,
          expiresAt: now + body.expiresInDays * 86_400_000,
          revokedAt: null,
          lastUsedAt: null,
          state: 'active',
          token: 'hpt_placeholder',
        };
        setData((d) => ({ tokens: [infoOf(made), ...(d?.tokens ?? [])] }));
        return made;
      }
      setBusy(true);
      try {
        const made = await postToken(body);
        setAgain((n) => n + 1);
        return made;
      } catch (e) {
        setProblem(issueProblem(e));
        return null;
      } finally {
        setBusy(false);
      }
    },
    [locked],
  );

  const revoke = useCallback(
    async (id: string): Promise<boolean> => {
      setProblem(null);
      if (locked) {
        setData((d) =>
          d
            ? {
                tokens: d.tokens.map((k) =>
                  k.id === id ? { ...k, state: 'revoked', revokedAt: Date.now() } : k,
                ),
              }
            : d,
        );
        return true;
      }
      setBusy(true);
      try {
        await deleteToken(id);
        setAgain((n) => n + 1);
        return true;
      } catch (e) {
        setProblem(errorText(e));
        return false;
      } finally {
        setBusy(false);
      }
    },
    [locked],
  );

  const clearProblem = useCallback(() => setProblem(null), []);
  return { data, loading, error, issue, revoke, busy, problem, clearProblem };
}
