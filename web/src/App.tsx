import {
  type LiveSessionMeta,
  type ServerMessage,
  type SessionPatchRequest,
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
  patchSession,
  postNewSession,
  setToken,
  socketUrl,
  UnauthorizedError,
} from './lib/api.ts';
import { errorText } from './lib/errors.ts';
import { emitLive, setLiveSender } from './lib/live.ts';
import { setPageKeysSuspended } from './lib/page-keys.ts';
import { usePageVisible } from './lib/visibility.ts';
import { Cockpit } from './screens/cockpit/index.tsx';
import type { NewSessionRequest } from './screens/cockpit/model.ts';
import { type SectionId, Settings, sectionOf } from './screens/settings/index.tsx';
import {
  type Appearance,
  applyAppearance,
  loadAppearance,
  saveAppearance,
} from './screens/settings/prefs.ts';

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

/** Settings is open when the URL carries `?settings=<section>`. */
function settingsInUrl(): SectionId | null {
  const raw = new URLSearchParams(location.search).get('settings');
  return raw === null ? null : sectionOf(raw);
}

function writeSettingsUrl(section: SectionId | null): void {
  const url = new URL(location.href);
  if (section) url.searchParams.set('settings', section);
  else url.searchParams.delete('settings');
  if (url.href !== location.href) history.pushState(null, '', url);
}

export default function App() {
  const [token, setTokenState] = useState<string | null>(getToken());
  const [sessions, setSessions] = useState<LiveSessionMeta[]>([]);
  const [selected, setSelectedState] = useState<string | null>(sessionInUrl);
  const [settings, setSettings] = useState<SectionId | null>(settingsInUrl);
  const [appearance, setAppearance] = useState<Appearance>(loadAppearance);
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
    const onPop = () => {
      setSelectedState(sessionInUrl());
      setSettings(settingsInUrl());
    };
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, []);

  // The cockpit stays mounted under Settings; focus goes into Settings and comes back to what opened it.
  const opener = useRef<HTMLElement | null>(null);
  const openSettings = useCallback((section: SectionId = 'appearance') => {
    const from = document.activeElement;
    // Switching section inside Settings keeps the first opener.
    if (!opener.current && from instanceof HTMLElement && from !== document.body)
      opener.current = from;
    setSettings(section);
    writeSettingsUrl(section);
  }, []);
  const closeSettings = useCallback(() => {
    setSettings(null);
    writeSettingsUrl(null);
  }, []);
  const isOpen = settings !== null;
  useEffect(() => {
    setPageKeysSuspended(isOpen);
    if (isOpen) return;
    const back = opener.current;
    opener.current = null;
    if (back?.isConnected) back.focus();
  }, [isOpen]);
  const changeAppearance = useCallback((change: Partial<Appearance>) => {
    setAppearance((cur) => ({ ...cur, ...change }));
  }, []);
  const firstAppearance = useRef(true);
  useEffect(() => {
    // The saved choices were applied at boot (and `?scheme=` may override the theme), so only a change acts.
    if (firstAppearance.current) {
      firstAppearance.current = false;
      return;
    }
    saveAppearance(appearance);
    applyAppearance(appearance);
  }, [appearance]);

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
        const next = sessions.find((s) => s.archivedAt === undefined)?.sessionId ?? null;
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
          };
          socketRuntime.current.set(msg.sessionId, { info, at: Date.now() });
          setRuntime((cur) => ({ ...cur, [msg.sessionId]: info }));
        } else if (msg.type === 'session.updated') {
          setSessions((cur) =>
            cur.map((s) => {
              if (s.sessionId !== msg.sessionId) return s;
              const { titleOverride: _t, pinnedAt: _p, archivedAt: _a, ...rest } = s;
              return {
                ...rest,
                ...(msg.titleOverride !== undefined ? { titleOverride: msg.titleOverride } : {}),
                ...(msg.pinnedAt !== undefined ? { pinnedAt: msg.pinnedAt } : {}),
                ...(msg.archivedAt !== undefined ? { archivedAt: msg.archivedAt } : {}),
              };
            }),
          );
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
        config: request.config,
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

  const patch = async (sessionId: string, change: SessionPatchRequest) => {
    const meta = await patchSession(sessionId, change);
    setSessions((cur) => cur.map((s) => (s.sessionId === sessionId ? meta : s)));
  };

  return (
    <>
      {settings && (
        <Settings
          section={settings}
          onSection={openSettings}
          onBack={closeSettings}
          appearance={appearance}
          onAppearance={changeAppearance}
        />
      )}
      <div className="page-host" hidden={isOpen} inert={isOpen}>
        <Cockpit
          onSettings={() => openSettings()}
          onNotifySettings={() => openSettings('notifications')}
          onPatchSession={patch}
          sessions={sessions}
          selected={selected}
          onSelect={select}
          live={live}
          error={error}
          runtime={runtime}
          onCreateSession={create}
        />
      </div>
    </>
  );
}
