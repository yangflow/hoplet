import type { SessionMcpDefaultsResponse, SessionMcpSettings } from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  deleteMcpDefault,
  fetchMcpDefaults,
  fetchSessionMcp,
  putMcpDefault,
  putSessionMcp,
} from './api.ts';
import { errorText } from './errors.ts';
import type { Loaded } from './use-usage.ts';

export type SessionMcpState = Loaded<SessionMcpSettings> & {
  /** Saves the settings; they show at once and go back to what the core holds when it refuses. */
  change: (next: SessionMcpSettings) => void;
  /** The core's reason for the last refused change, or null. */
  problem: string | null;
  clearProblem: () => void;
};

/** The settings are normalized the way the core stores them: Start implies Send, proposing implies reading. */
function stored(s: SessionMcpSettings): SessionMcpSettings {
  return {
    ...s,
    send: s.send || s.create,
    readConfig: s.readConfig || s.writeConfig,
    autoApproveHandoff: s.requestHandoff && s.autoApproveHandoff,
    autoApproveMachines: s.requestHandoff ? s.autoApproveMachines : [],
  };
}

/**
 * One session's settings and their changes. A `fixture` is the settings themselves and the changes stay local.
 */
export function useSessionMcp(sessionId: string, fixture?: SessionMcpSettings): SessionMcpState {
  const [data, setData] = useState<SessionMcpSettings | null>(fixture ?? null);
  const [loading, setLoading] = useState(fixture === undefined);
  const [error, setError] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const locked = fixture !== undefined;
  const latest = useRef(data);
  latest.current = data;

  useEffect(() => {
    if (locked) return;
    const abort = new AbortController();
    setLoading(true);
    fetchSessionMcp(sessionId, abort.signal)
      .then((r) => {
        setData(r.settings);
        setError(null);
        setLoading(false);
      })
      .catch((e) => {
        if (abort.signal.aborted) return;
        setError(errorText(e));
        setLoading(false);
      });
    return () => abort.abort();
  }, [locked, sessionId]);

  const change = useCallback(
    (next: SessionMcpSettings) => {
      const before = latest.current;
      setProblem(null);
      setData(stored(next));
      if (locked) return;
      putSessionMcp(sessionId, next)
        .then((r) => setData(r.settings))
        .catch((e) => {
          setData(before);
          setProblem(errorText(e));
        });
    },
    [locked, sessionId],
  );

  const clearProblem = useCallback(() => setProblem(null), []);
  return { data, loading, error, change, problem, clearProblem };
}

export type McpDefaultsState = Loaded<SessionMcpDefaultsResponse> & {
  setGlobal: (settings: SessionMcpSettings) => void;
  /** Sets one project's default, which adds it when it had none. */
  setProject: (deviceId: string, path: string, settings: SessionMcpSettings) => void;
  removeProject: (deviceId: string, path: string) => void;
  problem: string | null;
  clearProblem: () => void;
};

/** The defaults and their changes. A `fixture` is the answer itself and the changes stay local. */
export function useMcpDefaults(fixture?: SessionMcpDefaultsResponse): McpDefaultsState {
  const [data, setData] = useState<SessionMcpDefaultsResponse | null>(fixture ?? null);
  const [loading, setLoading] = useState(fixture === undefined);
  const [error, setError] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const locked = fixture !== undefined;
  const latest = useRef(data);
  latest.current = data;

  useEffect(() => {
    if (locked) return;
    const abort = new AbortController();
    fetchMcpDefaults(abort.signal)
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
  }, [locked]);

  /** Shows `apply` at once, then asks the core; a refusal brings back what was shown before. */
  const run = useCallback(
    (
      apply: (d: SessionMcpDefaultsResponse) => SessionMcpDefaultsResponse,
      send: () => Promise<unknown>,
    ) => {
      const before = latest.current;
      setProblem(null);
      setData((d) => (d ? apply(d) : d));
      if (locked) return;
      send().catch((e) => {
        setData(before);
        setProblem(errorText(e));
      });
    },
    [locked],
  );

  const setGlobal = useCallback(
    (settings: SessionMcpSettings) =>
      run(
        (d) => ({ ...d, global: stored(settings) }),
        () => putMcpDefault(settings),
      ),
    [run],
  );

  const setProject = useCallback(
    (deviceId: string, path: string, settings: SessionMcpSettings) =>
      run(
        (d) => {
          const row = { deviceId, path, settings: stored(settings) };
          const has = d.projects.some((p) => p.deviceId === deviceId && p.path === path);
          return {
            ...d,
            projects: has
              ? d.projects.map((p) => (p.deviceId === deviceId && p.path === path ? row : p))
              : [...d.projects, row],
          };
        },
        () => putMcpDefault(settings, { deviceId, path }),
      ),
    [run],
  );

  const removeProject = useCallback(
    (deviceId: string, path: string) =>
      run(
        (d) => ({
          ...d,
          projects: d.projects.filter((p) => !(p.deviceId === deviceId && p.path === path)),
        }),
        () => deleteMcpDefault({ deviceId, path }),
      ),
    [run],
  );

  const clearProblem = useCallback(() => setProblem(null), []);
  return { data, loading, error, setGlobal, setProject, removeProject, problem, clearProblem };
}
