import {
  type LiveSessionMeta,
  type ServerMessage,
  type SessionRuntimeInfo,
  safeParse,
  WIRE_MESSAGES,
  type WireType,
  wireTypes,
} from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import { TokenGate } from './components/TokenGate.tsx';
import {
  clearToken,
  fetchSessions,
  getToken,
  postNewSession,
  setToken,
  socketUrl,
  UnauthorizedError,
} from './lib/api.ts';
import { errorText } from './lib/errors.ts';
import { emitLive, setLiveSender } from './lib/live.ts';
import { usePageVisible } from './lib/visibility.ts';
import { Cockpit } from './screens/cockpit/index.tsx';
import type { NewSessionRequest } from './screens/cockpit/model.ts';

const BROWSER_MESSAGES = new Set<string>(wireTypes('core-to-web'));
/** Between session-list refreshes caused by activity. */
const REFRESH_MIN_MS = 1000;
const POLL_MS = 30_000;

function sessionInUrl(): string | null {
  return new URLSearchParams(location.search).get('session');
}

/** The selected session lives in the URL (`?session=<id>`), so a reload keeps it. */
function writeSessionUrl(id: string | null, mode: 'push' | 'replace'): void {
  const url = new URL(location.href);
  if (id) url.searchParams.set('session', id);
  else url.searchParams.delete('session');
  if (url.href === location.href) return;
  if (mode === 'push') history.pushState(null, '', url);
  else history.replaceState(null, '', url);
}

export default function App() {
  const [token, setTokenState] = useState<string | null>(getToken());
  const [sessions, setSessions] = useState<LiveSessionMeta[]>([]);
  const [selected, setSelectedState] = useState<string | null>(sessionInUrl);
  const [error, setError] = useState<string | null>(null);
  const [live, setLive] = useState(false);
  const [runtime, setRuntime] = useState<Record<string, SessionRuntimeInfo>>({});
  const visible = usePageVisible();
  const loaded = useRef(false);
  const pending = useRef<string | null>(null);
  const socketRuntime = useRef(new Map<string, { info: SessionRuntimeInfo; at: number }>());
  const lastRefresh = useRef(0);

  const select = useCallback((id: string | null) => {
    setSelectedState(id);
    writeSessionUrl(id, 'push');
  }, []);

  useEffect(() => {
    const onPop = () => setSelectedState(sessionInUrl());
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []);

  const refreshSessions = useCallback(async (): Promise<LiveSessionMeta[] | null> => {
    const startedAt = Date.now();
    lastRefresh.current = startedAt;
    try {
      const { sessions } = await fetchSessions();
      setSessions(sessions);
      setError(null);
      // The list is as fresh as its request: only what the socket said since then is newer.
      for (const [id, entry] of socketRuntime.current) {
        if (entry.at <= startedAt) socketRuntime.current.delete(id);
      }
      setRuntime(Object.fromEntries([...socketRuntime.current].map(([id, e]) => [id, e.info])));
      loaded.current = true;
      setSelectedState((cur) => {
        if (cur && (sessions.some((s) => s.sessionId === cur) || pending.current === cur))
          return cur;
        const next = sessions[0]?.sessionId ?? null;
        writeSessionUrl(next, 'replace');
        return next;
      });
      return sessions;
    } catch (e) {
      if (e instanceof UnauthorizedError) {
        setTokenState(null);
        clearToken();
      } else {
        setError(errorText(e));
      }
      return null;
    }
  }, []);

  // At most one refresh a second answers a burst of activity; what arrives inside that second is answered
  // by one refresh at its end, so a change of ownership or presence is never left to the poll.
  const trailing = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const refreshSoon = useCallback(() => {
    const wait = lastRefresh.current + REFRESH_MIN_MS - Date.now();
    if (wait <= 0) {
      void refreshSessions();
    } else if (trailing.current === undefined) {
      trailing.current = setTimeout(() => {
        trailing.current = undefined;
        void refreshSessions();
      }, wait);
    }
  }, [refreshSessions]);
  useEffect(() => () => clearTimeout(trailing.current), []);

  useEffect(() => {
    if (!token || !visible) return;
    void refreshSessions();
    const timer = setInterval(() => void refreshSessions(), POLL_MS);
    return () => clearInterval(timer);
  }, [token, visible, refreshSessions]);

  useEffect(() => {
    if (!token) return;
    let ws: WebSocket | null = null;
    let closed = false;
    let retry = 1000;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

    const retryLater = () => {
      if (closed) return;
      reconnectTimer = setTimeout(connect, retry);
      retry = Math.min(retry * 2, 15_000);
    };

    const connect = async () => {
      let url: string;
      try {
        url = await socketUrl();
      } catch (err) {
        if (err instanceof UnauthorizedError) {
          setTokenState(null);
          clearToken();
        } else retryLater();
        return;
      }
      if (closed) return;
      const socket = new WebSocket(url);
      ws = socket;
      socket.onmessage = (ev) => {
        let raw: unknown;
        try {
          raw = JSON.parse(String(ev.data));
        } catch {
          return;
        }
        const type = (raw as { type?: unknown } | null)?.type;
        if (typeof type !== 'string' || !BROWSER_MESSAGES.has(type)) return;
        const parsed = safeParse(WIRE_MESSAGES[type as WireType].schema, raw);
        if (!parsed.success) return;
        const msg = parsed.output as ServerMessage;
        if (msg.type === 'session.runtime') {
          const info: SessionRuntimeInfo = {
            state: msg.state,
            ...(msg.capabilities ? { capabilities: msg.capabilities } : {}),
            ...(msg.error !== undefined ? { error: msg.error } : {}),
            ...(msg.options !== undefined ? { options: msg.options } : {}),
          };
          socketRuntime.current.set(msg.sessionId, { info, at: Date.now() });
          setRuntime((cur) => ({ ...cur, [msg.sessionId]: info }));
        } else if (msg.type === 'session.appended' || msg.type === 'device.presence') {
          refreshSoon();
        } else if (msg.type === 'handoff.progress' && msg.step === 'done') {
          // The owner changed: the sidebar and the next handoff need it now, not after the interval.
          void refreshSessions();
        }
        emitLive({ kind: 'message', msg });
      };
      socket.onclose = () => {
        if (ws !== socket) return;
        setLive(false);
        setLiveSender(null);
        emitLive({ kind: 'close' });
        retryLater();
      };
      socket.onopen = () => {
        if (closed) return;
        retry = 1000;
        setLive(true);
        setLiveSender((payload) => socket.send(payload));
        emitLive({ kind: 'open' });
        void refreshSessions();
      };
    };
    void connect();
    return () => {
      closed = true;
      clearTimeout(reconnectTimer);
      setLiveSender(null);
      ws?.close();
    };
  }, [token, refreshSessions, refreshSoon]);

  const create = useCallback(
    async (request: NewSessionRequest) => {
      const started = await postNewSession({
        deviceId: request.deviceId,
        projectPath: request.path,
        provider: request.provider,
        options: request.options,
        text: request.prompt,
        attachments: request.attachments,
      });
      pending.current = started.sessionId;
      select(started.sessionId);
      // The session reaches the list when the agent's import is indexed.
      for (let i = 0; i < 20; i++) {
        const list = await refreshSessions();
        if (list?.some((s) => s.sessionId === started.sessionId)) break;
        await new Promise((r) => setTimeout(r, 500));
      }
      pending.current = null;
    },
    [refreshSessions, select],
  );

  if (!token)
    return (
      <TokenGate
        onSave={(token) => {
          setToken(token);
          setTokenState(token);
        }}
      />
    );

  return (
    <Cockpit
      sessions={sessions}
      selected={selected}
      onSelect={select}
      live={live}
      error={error}
      runtime={runtime}
      onCreateSession={create}
    />
  );
}
