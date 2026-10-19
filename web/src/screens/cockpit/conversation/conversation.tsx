import type {
  CommandsResult,
  ConfigSetting,
  DeviceSystem,
  Entry,
  LiveSessionMeta,
  PromptImage,
  Queue,
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
  postFreshPermission,
  postInterrupt,
  postPermission,
  postPrompt,
  postQueueAdd,
  postSteer,
  postStop,
} from '../../../lib/api.ts';
import { errorText } from '../../../lib/errors.ts';
import { t } from '../../../lib/i18n.ts';
import { onLive, subscribeSession } from '../../../lib/live.ts';
import { pageKeysSuspended } from '../../../lib/page-keys.ts';
import type { SendMode } from '../../../lib/prefs.ts';
import { loadAppearance } from '../../../lib/prefs.ts';
import { usePageVisible } from '../../../lib/visibility.ts';
import { Composer } from '../composer/composer.tsx';
import { ModeMenu, modeOption, nextMode } from '../composer/mode-menu.tsx';
import { hasChoices, ModelMenu, withValues } from '../composer/model-menu.tsx';
import { type PathsState, useCommands } from '../composer/use-completions.ts';
import { SessionWhere } from '../composer/where.tsx';
import { QueueTray, type TrayFixture } from '../queue/queue-tray.tsx';
import { useQueue } from '../queue/use-queue.ts';
import { type ComposerView, composerView, stateName } from '../runtime.ts';
import { LiveTurnView } from './live-view.tsx';
import { PlanOrigin } from './plan-card.tsx';
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
  /** The queue the tray shows, and how its rows look. */
  queue?: Queue;
  tray?: TrayFixture;
  /** The send menu next to the send button starts open. */
  sendMenuOpen?: boolean;
  /** Messages steered into the live turn that the model has not taken in yet. */
  steering?: { text: string; taken?: boolean }[];
  /** What a send does while a turn runs, when the user's setting is not the default. */
  sendMode?: SendMode;
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
  /** What the session's machine announced about itself, for its mark in the location row. */
  system?: DeviceSystem | null | undefined;
  onHandoff: () => void;
  openDiff?: (path: string) => void;
  /** Opens another session in the cockpit: the ends of a plan approved into a new session. */
  onOpenSession?: (sessionId: string) => void;
  /** A plan was approved into this new session (ADR 0025): the app opens it. */
  onSessionStarted?: (sessionId: string) => void;
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

  const { queue, apply: applyQueue, refresh: refreshQueue } = useQueue(sessionId, fixture?.queue);
  const [steering, setSteering] = useState<string[]>(fixture?.steering?.map((s) => s.text) ?? []);
  // Read at every render so a change made in Settings applies as soon as the overlay closes.
  const sendMode: SendMode = fixture?.sendMode ?? loadAppearance().sendMode;
  // Steers wait for the model to take them in; a turn that ends, or another session, drops them.
  useEffect(() => {
    if (!fixture && state !== 'running' && state !== 'waiting-permission') setSteering([]);
  }, [state, fixture]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: another session starts with no pending steers
  useEffect(() => {
    if (!fixture) setSteering([]);
  }, [sessionId, fixture]);

  // A steer the model has taken in is on disk as a steered entry; until then it waits at the next tool call.
  const steeringShown = steering.map((text, i) => ({
    text,
    taken:
      fixture?.steering?.[i]?.taken ??
      snapshot.entries.some(
        (e) =>
          e.steered === true &&
          e.blocks.some((b) => b.type === 'text' && 'text' in b && b.text.trim() === text.trim()),
      ),
  }));
  const turnOpen = state === 'running' || state === 'waiting-permission';
  const sendDuringTurn = useCallback(
    async (text: string, attachments: string[], mode: SendMode) => {
      setBusy(true);
      setSendError(null);
      try {
        if (mode === 'steer') {
          await postSteer(sessionId, text, attachments);
          setSteering((list) => [...list, text]);
        } else {
          applyQueue(await postQueueAdd(sessionId, crypto.randomUUID(), text, attachments));
        }
      } catch (err) {
        setRestore((r) => ({ text, withImages: attachments.length > 0, n: (r?.n ?? 0) + 1 }));
        setSendError(errorText(err));
      } finally {
        setBusy(false);
      }
    },
    [sessionId, applyQueue],
  );

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
    async (
      requestId: string,
      decision: 'allow' | 'deny',
      plan?: { mode?: string; feedback?: string },
    ) => {
      setAnswering(true);
      setNotice(null);
      // A plan's row names what this window decided; the session stays in its mode when the plan is kept.
      if (plan) {
        const stays = modeOption(withValues(props.capabilities?.config, picked))?.currentValue;
        lt.intend(requestId, {
          outcome: decision === 'allow' ? 'approved' : 'kept',
          ...(decision === 'allow' && plan.mode ? { mode: plan.mode } : {}),
          ...(decision === 'deny' && typeof stays === 'string' ? { mode: stays } : {}),
          ...(plan.feedback ? { feedback: plan.feedback } : {}),
        });
      }
      try {
        await (plan
          ? postPermission(sessionId, requestId, decision, plan)
          : postPermission(sessionId, requestId, decision));
        lt.answered(requestId);
      } catch (err) {
        // The first answer wins: a closed request is gone for everyone.
        if (err instanceof ApiError && err.code === 'request-closed') {
          lt.forget(requestId);
          lt.answered(requestId);
        } else {
          lt.forget(requestId);
        }
        setNotice(errorText(err));
      } finally {
        setAnswering(false);
      }
    },
    [sessionId, lt.answered, lt.intend, lt.forget, props.capabilities, picked],
  );

  // Approving into a new session: the plan stays open until the core has the new session up (ADR 0025).
  const { onSessionStarted } = props;
  const startFresh = useCallback(
    async (requestId: string, mode: string, promptId: string) => {
      lt.intend(requestId, { outcome: 'approved', fresh: true, mode });
      try {
        const started = await postFreshPermission(sessionId, { requestId, mode, promptId });
        onSessionStarted?.(started.sessionId);
      } catch (err) {
        lt.forget(requestId);
        throw err;
      }
    },
    [sessionId, lt.intend, lt.forget, onSessionStarted],
  );

  const shortcuts = props.shortcuts ?? true;
  const interruptible =
    (state === 'running' || state === 'waiting-permission') && canInterrupt && live;
  useEffect(() => {
    if (!shortcuts || !interruptible) return;
    const onKey = (e: KeyboardEvent) => {
      if (pageKeysSuspended() || e.key !== 'Escape' || e.defaultPrevented || e.isComposing) return;
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
        firstRequest.planExit
          ? t('{provider} has a plan').replace('{provider}', provider)
          : t('{provider} asks to use {tool}')
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
    planPending: lt.permissions.some((p) => p.planExit !== undefined),
    canSteer: props.capabilities?.steer === true,
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
          origin={
            session.continuedFrom ? (
              <PlanOrigin from={session.continuedFrom} onOpen={props.onOpenSession} />
            ) : null
          }
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
                onAnswer={(id, decision, plan) => void answer(id, decision, plan)}
                modes={mode}
                decidable={live && session.ownerConnected && !fixture?.composerOffline}
                deviceName={session.deviceName}
                projectPath={session.projectPath}
                onFresh={startFresh}
                continuedIn={session.continuedIn}
                onOpenSession={props.onOpenSession}
                planView={fixture?.live?.plan}
                steering={steeringShown}
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
            <QueueTray
              sessionId={sessionId}
              queue={queue}
              apply={applyQueue}
              refresh={refreshQueue}
              waitingPermission={state === 'waiting-permission'}
              canSteer={state === 'running' && props.capabilities?.steer === true}
              onError={setSendError}
              {...(fixture?.tray ? { fixture: fixture.tray } : {})}
            />
            <Composer
              key={sessionId}
              view={view}
              where={
                <SessionWhere
                  deviceName={session.deviceName}
                  online={session.ownerConnected}
                  system={props.system}
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
              sendMode={sendMode}
              {...(fixture?.sendMenuOpen ? { sendMenuOpen: true } : {})}
              onSend={(text, attachments, mode) =>
                void (turnOpen ? sendDuringTurn(text, attachments, mode) : send(text, attachments))
              }
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
