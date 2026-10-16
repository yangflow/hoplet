import type {
  CommandsResult,
  ConfigSetting,
  Entry,
  LiveSessionMeta,
  PromptImage,
  RuntimeCapabilities,
  SessionConfig,
  SessionRuntimeInfo,
} from '@hoplet/shared';
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { Icon } from '../../../components/icons.tsx';
import { providerName } from '../../../components/provider-marks.tsx';
import {
  ApiError,
  fetchLines,
  postAttachment,
  postConfig,
  postInterrupt,
  postPermission,
  postPrompt,
  postStop,
} from '../../../lib/api.ts';
import { errorText } from '../../../lib/errors.ts';
import { t } from '../../../lib/i18n.ts';
import { onLive, subscribeSession } from '../../../lib/live.ts';
import { usePageVisible } from '../../../lib/visibility.ts';
import { Composer } from '../composer/composer.tsx';
import { ModeMenu, modeOption, nextMode } from '../composer/mode-menu.tsx';
import { hasChoices, ModelMenu, withValues } from '../composer/model-menu.tsx';
import { type PathsState, useCommands } from '../composer/use-completions.ts';
import { SessionWhere } from '../composer/where.tsx';
import { type ComposerView, composerView, stateName } from '../runtime.ts';
import { LiveTurnView } from './live-view.tsx';
import { SessionReader } from './reader.ts';
import { Transcript, type TranscriptHandle } from './transcript.tsx';
import { buildTurns } from './turns.ts';
import { type LiveFixture, useLiveTurn } from './use-live-turn.ts';

/** What development fixtures replace: the stored lines, the live turn and a few view settings. */
export type ConversationFixture = {
  entries: Entry[];
  live?: LiveFixture;
  /** The current time, pinned so timers read the same in every capture. */
  now?: number;
  recording?: boolean;
  loadingOlder?: boolean;
  /** New lines to show on the jump pill. */
  unread?: number;
  /** Text in the composer's field. */
  composerText?: string;
  /** What `/` and `@` show: the commands, the paths, the selected row and the hovered one. */
  completions?: {
    commands: CommandsResult;
    paths?: PathsState;
    selected?: number;
    hovered?: number;
  };
  /** The owner is offline but the field is still drawn, with the state line (a fixture of the offline `@`). */
  composerOffline?: boolean;
  /** Images waiting in the composer. */
  composerImages?: PromptImage[];
  /** The session's model menu starts open. */
  modelMenuOpen?: boolean;
  /** The session's permission-mode menu starts open. */
  modeMenuOpen?: boolean;
  scrollTo?: number;
  openBlocks?: string[];
};

/** The current time, ticking every second while something live needs it. */
function useClock(active: boolean, pinned: number | undefined): number {
  const [now, setNow] = useState(() => pinned ?? Date.now());
  useEffect(() => {
    if (!active || pinned !== undefined) return;
    setNow(Date.now());
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, [active, pinned]);
  return pinned ?? now;
}

export function Conversation(props: {
  session: LiveSessionMeta;
  runtime: SessionRuntimeInfo;
  /** This window is connected to the core. */
  live: boolean;
  aroundLine?: number | null;
  /** Keyboard shortcuts of the conversation (Esc interrupts) work only while no overlay is open. */
  shortcuts?: boolean;
  /** The microphone shows only when transcription is available (#17). */
  voiceAvailable?: boolean;
  /** What the session's driver can do, as reported or announced: images, switching the model. */
  capabilities?: RuntimeCapabilities | undefined;
  onHandoff: () => void;
  openDiff?: (path: string) => void;
  fixture?: ConversationFixture;
}) {
  const { session, runtime, live, fixture } = props;
  const aroundLine = props.aroundLine ?? null;
  const sessionId = session.sessionId;
  const provider = providerName(session.provider);
  const state = runtime.state;

  const fixtureEntries = fixture?.entries;
  const reader = useMemo(
    () =>
      fixtureEntries
        ? SessionReader.fixed(fixtureEntries)
        : new SessionReader((start, limit) => fetchLines(sessionId, start, limit)),
    [sessionId, fixtureEntries],
  );
  const snapshot = useSyncExternalStore(reader.subscribe, reader.getSnapshot, reader.getSnapshot);

  useEffect(() => {
    if (!fixture) void reader.open(aroundLine);
  }, [reader, aroundLine, fixture]);
  useEffect(() => {
    if (fixture) return;
    return subscribeSession(sessionId);
  }, [sessionId, fixture]);

  const lt = useLiveTurn({
    sessionId,
    state,
    error: runtime.error,
    snapshot,
    reader,
    fixture: fixture?.live,
  });
  const turn = lt.turn;

  const [atBottom, setAtBottom] = useState(aroundLine === null);
  const atBottomRef = useRef(atBottom);
  const followRef = useRef(aroundLine === null);
  const [readTotal, setReadTotal] = useState(0);
  const transcript = useRef<TranscriptHandle>(null);

  const onViewport = useCallback(
    (view: { line: number; atBottom: boolean }) => {
      atBottomRef.current = view.atBottom;
      followRef.current = view.atBottom;
      setAtBottom(view.atBottom);
      reader.setFocus(view.atBottom ? Number.POSITIVE_INFINITY : view.line);
    },
    [reader],
  );
  useEffect(() => {
    if (atBottom && !snapshot.hasNewer) setReadTotal(snapshot.total);
  }, [atBottom, snapshot.hasNewer, snapshot.total]);

  // New lines: read them while the reader follows the tail; otherwise only count them.
  const visible = usePageVisible();
  useEffect(() => {
    if (fixture) return;
    return onLive((event) => {
      if (event.kind === 'open') {
        if (followRef.current) void reader.pull();
      } else if (event.kind === 'message' && event.msg.type === 'session.appended') {
        if (event.msg.sessionId !== sessionId) return;
        reader.noteTotal(event.msg.totalLines);
        if (followRef.current) void reader.pull();
      }
    });
  }, [reader, sessionId, fixture]);
  useEffect(() => {
    if (visible && !fixture && followRef.current) void reader.pull();
  }, [visible, reader, fixture]);

  const unread =
    fixture?.unread ??
    (atBottom && !snapshot.hasNewer ? 0 : Math.max(0, snapshot.total - readTotal));

  const needOlder = useCallback(() => void reader.loadOlder(), [reader]);
  const needNewer = useCallback(() => void reader.loadNewer(), [reader]);

  // Stored lines from the live turn's boundary on stay hidden until it settles.
  const { turns, base } = useMemo(() => {
    const shown = turn
      ? snapshot.entries.filter((e) => e.lineNo < turn.boundary)
      : snapshot.entries;
    const built = buildTurns(shown, { cwd: session.projectPath });
    const last = built[built.length - 1];
    if (turn?.joined && last) return { turns: built.slice(0, -1), base: last };
    return { turns: built, base: undefined };
  }, [snapshot.entries, turn, session.projectPath]);

  // Sending, interrupting, stopping and answering.
  const [busy, setBusy] = useState(false);
  const [sendError, setSendError] = useState<string | null>(null);
  const [restore, setRestore] = useState<{
    text: string;
    withImages?: boolean;
    n: number;
  } | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [answering, setAnswering] = useState(false);
  const lastPrompt = useRef<{ text: string; attachments: string[] } | null>(null);
  // The config options the session offers. A pick is sent at once; until the runtime state reports that value
  // (or the request failed), the menu shows the picked value.
  const caps = props.capabilities;
  const providers = caps ? [{ provider: session.provider, capabilities: caps }] : [];
  const choosable = caps !== undefined && hasChoices(providers, providers[0]);
  const [picked, setPicked] = useState<SessionConfig>({});
  useEffect(() => {
    const reported = caps?.config ?? [];
    setPicked((cur) => {
      const kept = Object.entries(cur).filter(
        ([id, value]) => reported.find((o) => o.id === id)?.currentValue !== value,
      );
      return kept.length === Object.keys(cur).length ? cur : Object.fromEntries(kept);
    });
  }, [caps]);
  const setConfig = useCallback(
    (id: string, value: ConfigSetting) => {
      setPicked((cur) => ({ ...cur, [id]: value }));
      postConfig(sessionId, id, value).catch((err) => {
        setPicked((cur) => {
          const { [id]: _dropped, ...rest } = cur;
          return rest;
        });
        setSendError(errorText(err));
      });
    },
    [sessionId],
  );
  const shown = withValues(caps?.config, picked);
  const mode = modeOption(shown);
  const cycleMode = useCallback(() => {
    const next = mode ? nextMode(mode) : undefined;
    if (mode && next !== undefined) setConfig(mode.id, next);
  }, [mode, setConfig]);
  const pending = useRef<{ text: string; attachments: string[] } | null>(null);
  const root = useRef<HTMLDivElement>(null);

  const send = useCallback(
    async (text: string, attachments: string[] = []) => {
      setBusy(true);
      setSendError(null);
      setNotice(null);
      lastPrompt.current = { text, attachments };
      pending.current = { text, attachments };
      lt.begin(text);
      followRef.current = true;
      transcript.current?.scrollToBottom();
      try {
        await postPrompt(sessionId, text, attachments);
      } catch (err) {
        pending.current = null;
        lt.cancel();
        setRestore((r) => ({ text, withImages: attachments.length > 0, n: (r?.n ?? 0) + 1 }));
        setSendError(errorText(err));
      } finally {
        setBusy(false);
      }
    },
    [sessionId, lt.begin, lt.cancel],
  );

  // A start that failed gives the prompt back.
  useEffect(() => {
    if (state === 'running') pending.current = null;
    if (state === 'failed' && pending.current) {
      const { text, attachments } = pending.current;
      pending.current = null;
      setRestore((r) => ({ text, withImages: attachments.length > 0, n: (r?.n ?? 0) + 1 }));
    }
    setNotice(null);
  }, [state]);

  const retry = useCallback(() => {
    if (lastPrompt.current) void send(lastPrompt.current.text, lastPrompt.current.attachments);
    else root.current?.querySelector('textarea')?.focus();
  }, [send]);

  const stop = useCallback(() => {
    setSendError(null);
    postStop(sessionId).catch((err: unknown) => setSendError(errorText(err)));
  }, [sessionId]);

  const canInterrupt = runtime.capabilities?.interrupt === true;
  const interrupt = useCallback(() => {
    postInterrupt(sessionId).catch((err: unknown) => setNotice(errorText(err)));
  }, [sessionId]);

  const answer = useCallback(
    async (requestId: string, decision: 'allow' | 'deny') => {
      setAnswering(true);
      setNotice(null);
      try {
        await postPermission(sessionId, requestId, decision);
        lt.answered(requestId);
      } catch (err) {
        // The first answer wins: a closed request is gone for everyone.
        if (err instanceof ApiError && err.code === 'request-closed') lt.answered(requestId);
        setNotice(errorText(err));
      } finally {
        setAnswering(false);
      }
    },
    [sessionId, lt.answered],
  );

  const shortcuts = props.shortcuts ?? true;
  const interruptible =
    (state === 'running' || state === 'waiting-permission') && canInterrupt && live;
  useEffect(() => {
    if (!shortcuts || !interruptible) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented || e.isComposing) return;
      e.preventDefault();
      interrupt();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [shortcuts, interruptible, interrupt]);

  const now = useClock(turn?.phase === 'live' || lt.permissions.length > 0, fixture?.now);

  // Runtime changes and permission requests are announced politely.
  const [announcement, setAnnouncement] = useState(() =>
    state === 'idle' ? '' : stateName(state),
  );
  const firstState = useRef(true);
  useEffect(() => {
    if (firstState.current) {
      firstState.current = false;
      return;
    }
    setAnnouncement(stateName(state));
  }, [state]);
  const firstRequest = lt.permissions[0];
  useEffect(() => {
    if (firstRequest) {
      setAnnouncement(
        t('{provider} asks to use {tool}')
          .replace('{provider}', provider)
          .replace('{tool}', firstRequest.tool),
      );
    }
  }, [firstRequest, provider]);

  // The dock floats over the foot of the column; the transcript keeps clear of it.
  const composerBox = useRef<HTMLDivElement>(null);
  const [dock, setDock] = useState(148);
  useEffect(() => {
    const el = composerBox.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => setDock(el.offsetHeight + 58));
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const commands = useCommands(fixture ? null : { sessionId }, fixture?.completions?.commands);
  const composed = composerView({
    state,
    ownerConnected: session.ownerConnected,
    live,
    deviceName: session.deviceName,
    providerName: provider,
    error: runtime.error,
    turnFailed: turn?.phase === 'failed',
  });
  const view: ComposerView = fixture?.composerOffline
    ? {
        layout: 'field',
        placeholder: t('Owner offline'),
        inputEnabled: false,
        line: {
          text: t('{device} offline').replace('{device}', session.deviceName),
          title: t('Owner offline'),
          glyph: 'off',
          tone: 'plain',
        },
      }
    : composed;

  const frozen = !live;
  const showLive = turn && !snapshot.hasNewer;

  return (
    <div className="conv-wrap" ref={root} data-testid="cockpit.conv" data-state={state}>
      {snapshot.error && !fixture ? (
        <div className="banner-error" role="alert" data-testid="cockpit.read-error">
          {snapshot.error}
        </div>
      ) : null}
      {snapshot.ready && turns.length === 0 && !turn ? (
        <p className="conv-empty" data-testid="cockpit.empty-session">
          {t('This session has no content yet')}
        </p>
      ) : null}
      {!snapshot.ready && !fixture ? (
        <p className="conv-empty" role="status">
          {t('Loading…')}
        </p>
      ) : (
        <Transcript
          sessionId={sessionId}
          turns={turns}
          hasOlder={snapshot.hasOlder}
          hasNewer={snapshot.hasNewer}
          loadingOlder={fixture?.loadingOlder ?? snapshot.loading === 'older'}
          loadingNewer={snapshot.loading === 'newer'}
          onNeedOlder={needOlder}
          onNeedNewer={needNewer}
          onViewport={onViewport}
          focusLine={aroundLine}
          startPinned={aroundLine === null && fixture?.scrollTo === undefined}
          dock={dock}
          {...(props.openDiff ? { openDiff: props.openDiff } : {})}
          {...(fixture?.openBlocks ? { initiallyOpen: fixture.openBlocks } : {})}
          {...(fixture?.scrollTo !== undefined ? { scrollTo: fixture.scrollTo } : {})}
          live={
            showLive ? (
              <LiveTurnView
                turn={turn}
                base={base}
                state={state}
                now={now}
                frozen={frozen}
                permissions={lt.permissions}
                providerName={provider}
                answering={answering}
                notice={notice}
                canInterrupt={canInterrupt}
                onInterrupt={interrupt}
                onAnswer={(id, decision) => void answer(id, decision)}
              />
            ) : null
          }
          ref={transcript}
        />
      )}
      <div className="dock">
        <div className="col">
          {unread > 0 && (fixture || !(atBottom && !snapshot.hasNewer)) ? (
            <div className="pillw">
              <button
                type="button"
                className="pill"
                onClick={() => {
                  if (snapshot.hasNewer) void reader.jumpToTail();
                  else void reader.pull();
                  followRef.current = true;
                  transcript.current?.scrollToBottom();
                }}
                data-testid="cockpit.jump-new"
              >
                <span className="pill-i">{Icon.arrowDown()}</span>
                {t('{n} new').replace('{n}', String(unread))}
              </button>
            </div>
          ) : null}
          <div ref={composerBox}>
            <Composer
              key={sessionId}
              view={view}
              where={
                <SessionWhere
                  deviceName={session.deviceName}
                  online={session.ownerConnected}
                  path={session.projectPath}
                  branch={session.gitBranch}
                />
              }
              providerName={provider}
              busy={busy}
              error={sendError}
              restore={restore}
              voiceAvailable={props.voiceAvailable ?? false}
              completions={{
                commands,
                sessionId,
                online: session.ownerConnected && !fixture?.composerOffline,
                deviceName: session.deviceName,
                ...(fixture?.completions
                  ? {
                      fixture: {
                        ...(fixture.completions.paths ? { paths: fixture.completions.paths } : {}),
                        ...(fixture.completions.selected !== undefined
                          ? { selected: fixture.completions.selected }
                          : {}),
                        ...(fixture.completions.hovered !== undefined
                          ? { hovered: fixture.completions.hovered }
                          : {}),
                      },
                    }
                  : {}),
              }}
              {...(fixture?.recording ? { recording: true } : {})}
              {...(fixture?.composerText ? { initialText: fixture.composerText } : {})}
              onSend={(text, attachments) => void send(text, attachments)}
              acceptsImages={caps?.images === true}
              {...(mode ? { onCycleMode: cycleMode } : {})}
              {...(choosable || mode
                ? {
                    options: (
                      <>
                        {mode ? (
                          <ModeMenu
                            option={mode}
                            onChange={setConfig}
                            {...(fixture?.modeMenuOpen ? { initialOpen: true } : {})}
                          />
                        ) : null}
                        {choosable ? (
                          <ModelMenu
                            providers={providers}
                            provider={session.provider}
                            config={shown}
                            onProvider={() => {}}
                            onChange={setConfig}
                            {...(fixture?.modelMenuOpen ? { initialOpen: true } : {})}
                          />
                        ) : null}
                      </>
                    ),
                  }
                : {})}
              {...(session.ownerDeviceId
                ? {
                    upload: (image: PromptImage) =>
                      postAttachment(session.ownerDeviceId ?? '', image),
                    uploadKey: session.ownerDeviceId,
                  }
                : {})}
              {...(fixture?.composerImages ? { initialImages: fixture.composerImages } : {})}
              onStop={stop}
              onRetry={retry}
              onHandoff={props.onHandoff}
              onDirty={() => setSendError(null)}
            />
          </div>
        </div>
      </div>
      <div className="sr-only" role="status" aria-live="polite" data-testid="cockpit.announce">
        {announcement}
      </div>
    </div>
  );
}
