import type { CommandsResult, PromptImage } from '@hoplet/shared';
import {
  type ClipboardEvent,
  type DragEvent,
  type FormEvent,
  type KeyboardEvent,
  type PointerEvent,
  type ReactNode,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon, Spinner } from '../../../components/icons.tsx';
import { errorText } from '../../../lib/errors.ts';
import { t } from '../../../lib/i18n.ts';
import type { SendMode } from '../../../lib/prefs.ts';
import type { ComposerView } from '../runtime.ts';
import { type Attachment, dataUrl, fromImages, readImage, refusal } from './attachments.ts';
import { xOfIndex } from './caret.ts';
import { CompletionList, CompletionStatus, listId, optionId } from './completion-list.tsx';
import {
  anchorX,
  filterCommands,
  filterPaths,
  type Item,
  insertPick,
  keyAction,
  moveSelection,
  triggerAt,
} from './completions.ts';
import {
  AUTO_MAX_PX,
  clampHeight,
  LINE_PX,
  loadHeight,
  maxFieldHeight,
  saveHeight,
} from './height.ts';
import { SendWhileRunning } from './send-menu.tsx';
import { type PathsState, usePaths } from './use-completions.ts';

/** What the field completes: `/` from the session's commands, `@` from its working tree. */
export type CompletionSources = {
  /** The commands and skills on offer; null until they are read. */
  commands: CommandsResult | null;
  /** The session whose working tree `@` lists; a draft has none and `@` offers nothing. */
  sessionId?: string;
  /** The owner answers: `@` needs it online. */
  online: boolean;
  /** The machine named while paths load and when it is offline. */
  deviceName: string;
  /** Development fixtures: the paths `@` shows, the selected row and the hovered one. */
  fixture?: { paths?: PathsState; selected?: number; hovered?: number };
};

/** Bars of the recording waveform: fixed, since the real level meter arrives with transcription (#17). */
const WAVE = Array.from({ length: 150 }, (_, i) => {
  const h =
    3 +
    Math.round(
      19 *
        Math.abs(
          Math.sin(i * 0.61) *
            Math.cos(i * 0.17 + 1) *
            (0.45 + 0.55 * Math.abs(Math.sin(i * 0.09))),
        ),
    );
  return { h, o: (0.35 + (0.65 * i) / 149).toFixed(2) };
});

function lineGlyph(glyph: 'ready' | 'off' | 'eye' | 'bad'): ReactNode {
  switch (glyph) {
    case 'ready':
      return <span className="ready-dot" />;
    case 'off':
      return <span className="off-dot" />;
    case 'eye':
      return Icon.eye();
    case 'bad':
      return <span className="bad-dot" />;
  }
}

function mmss(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

/** The recording composer: elapsed time, a waveform, discard and stop. Used when voice is available. */
export function RecordingComposer(props: {
  seconds: number;
  onDiscard: () => void;
  onStop: () => void;
}) {
  return (
    <div className="composer" aria-live="polite" data-testid="cockpit.recording">
      <div className="recrow">
        <span className="rdot" role="img" title={t('Recording')} aria-label={t('Recording')} />
        <span className="el">{mmss(props.seconds)}</span>
        <div className="wave" aria-hidden="true">
          {WAVE.map((bar, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: bars have no identity
            <i key={i} style={{ height: bar.h, opacity: bar.o }} />
          ))}
        </div>
      </div>
      <div className="cf">
        <span className="note">
          {Icon.lock()}
          {t('Transcribed on your machines')}
        </span>
        <span className="r">
          <IconButton
            label={t('Discard recording')}
            hint="Esc"
            icon={Icon.close()}
            onClick={props.onDiscard}
            data-testid="cockpit.record-discard"
          />
          <IconButton
            label={t('Stop recording')}
            hint="Space"
            icon={Icon.stopSolid()}
            primary
            onClick={props.onStop}
            data-testid="cockpit.record-stop"
          />
        </span>
      </div>
    </div>
  );
}

/**
 * The composer: where the session runs, a field and a toolbar with send, or a bar for the states that block
 * writing. The state line and its action appear only when the state blocks or changes what the user can do.
 */
export function Composer(props: {
  view: ComposerView;
  /** The first row: where the session runs (`WhereRow`). */
  where?: ReactNode;
  /** The toolbar's choices before dictate and send: the model menu. */
  options?: ReactNode;
  /** What leads the toolbar after the attach button: the configuration chip of a draft. */
  leading?: ReactNode;
  /** Shift+Tab in the field: cycles the permission mode. Absent when the session has no mode to cycle. */
  onCycleMode?: () => void;
  /** The send button's name: Send, or Start in a draft. */
  sendLabel?: string;
  /** Text the field starts with (development fixtures). */
  initialText?: string;
  /** The display name of the session's provider. */
  providerName: string;
  /** Whether a prompt is being sent. */
  busy: boolean;
  error: string | null;
  /**
   * A prompt to put back (one that was refused), with a counter to say it is new; `withImages` gives back the
   * images it was sent with, still uploaded.
   */
  restore: { text: string; withImages?: boolean; n: number } | null;
  /** The driver takes images with a prompt: attach, paste and drop add them. */
  acceptsImages?: boolean;
  /** Uploads an image to the machine the prompt goes to and resolves with its attachment ID (ADR 0017). */
  upload?: (image: PromptImage) => Promise<string>;
  /** The machine uploads go to; when it changes, the images waiting are uploaded again. */
  uploadKey?: string;
  /** Images the composer starts with (development fixtures). */
  initialImages?: PromptImage[];
  /** Transcription is available (#17); until then the microphone is hidden. */
  voiceAvailable: boolean;
  /** Start in the recording state (fixtures). */
  recording?: boolean;
  /** Where `/` and `@` take their lists from; without it the field completes nothing. */
  completions?: CompletionSources;
  /** The way Enter sends while a turn runs (a preference); Alt+Enter sends the other way. */
  sendMode?: SendMode;
  /** The send menu starts open (development fixtures). */
  sendMenuOpen?: boolean;
  /** Sends the text with the attachment IDs of its images, the way chosen when a turn is running. */
  onSend: (text: string, attachments: string[], mode: SendMode) => void;
  onStop: () => void;
  onRetry: () => void;
  onHandoff: () => void;
  onDirty: () => void;
}) {
  const { view } = props;
  const [draft, setDraft] = useState(props.initialText ?? '');
  const [attachments, setAttachments] = useState<Attachment[]>(() =>
    fromImages(props.initialImages ?? []),
  );
  const [attachError, setAttachError] = useState<string | null>(null);
  const picker = useRef<HTMLInputElement>(null);
  // The images of the last prompt sent, given back with it when it is refused.
  const sent = useRef<Attachment[]>([]);
  const [recording, setRecording] = useState(props.recording ?? false);
  const [seconds, setSeconds] = useState(props.recording ? 7 : 0);
  const field = useRef<HTMLTextAreaElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const comboId = useId();
  const popover = useRef<HTMLDivElement>(null);
  // The caret and what is selected in the list; `closed` holds the token Esc closed the list for.
  const [caret, setCaret] = useState((props.initialText ?? '').length);
  const [selected, setSelected] = useState(props.completions?.fixture?.selected ?? 0);
  const [closed, setClosed] = useState<string | null>(null);
  const putCaret = useRef<number | null>(null);
  // A dragged height, remembered by the browser; null while the field sizes itself to its text.
  const [fixed, setFixed] = useState<number | null>(() => loadHeight());
  const fixedRef = useRef(fixed);
  fixedRef.current = fixed;

  useEffect(() => {
    const back = props.restore;
    if (!back) return;
    setDraft((d) => (d === '' ? back.text : d));
    if (back.withImages) setAttachments((a) => (a.length === 0 ? sent.current : a));
  }, [props.restore]);

  /** Uploads one image; its thumbnail shows the upload, a failure says why under the field. */
  function uploadOne(a: Attachment) {
    const upload = props.upload;
    if (!upload) return;
    const patch = (next: Partial<Attachment>) =>
      setAttachments((all) => all.map((x) => (x.id === a.id ? { ...x, ...next } : x)));
    upload({ mediaType: a.mediaType, data: a.data }).then(
      (attachmentId) => patch({ status: 'ready', attachmentId }),
      (err: unknown) => {
        patch({ status: 'failed' });
        setAttachError(errorText(err));
      },
    );
  }

  // A draft that changes machine uploads its images again, to the new one.
  const uploadKey = props.uploadKey;
  const firstKey = useRef(uploadKey);
  // biome-ignore lint/correctness/useExhaustiveDependencies: only a new machine uploads again
  useEffect(() => {
    if (uploadKey === firstKey.current) return;
    firstKey.current = uploadKey;
    setAttachments((all) => {
      const again = all.map((a) => ({ ...a, status: 'uploading' as const }));
      for (const a of again) uploadOne(a);
      return again;
    });
  }, [uploadKey]);

  /** Adds image files, one by one, until a limit refuses one; the reason shows under the field. */
  async function addFiles(files: readonly File[]) {
    setAttachError(null);
    props.onDirty();
    let current = attachments;
    for (const file of files) {
      const read = await readImage(file).catch(() =>
        t('{name} cannot be read as an image').replace('{name}', file.name),
      );
      const why = typeof read === 'string' ? read : refusal(current, read);
      if (why !== null || typeof read === 'string') {
        setAttachError(why);
        break;
      }
      current = [...current, read];
    }
    const added = current.filter((a) => !attachments.includes(a));
    setAttachments(current);
    for (const a of added) uploadOne(a);
  }

  const takesImages = props.acceptsImages === true && view.inputEnabled;
  const imageFiles = (list: FileList | null | undefined) =>
    [...(list ?? [])].filter((f) => f.type.startsWith('image/'));

  function onPaste(e: ClipboardEvent<HTMLTextAreaElement>) {
    const files = imageFiles(e.clipboardData?.files);
    if (!takesImages || files.length === 0) return;
    e.preventDefault();
    void addFiles(files);
  }

  function onDrop(e: DragEvent<HTMLFormElement>) {
    const files = imageFiles(e.dataTransfer?.files);
    if (!takesImages || files.length === 0) return;
    e.preventDefault();
    void addFiles(files);
  }

  useEffect(() => {
    if (!recording || props.recording) return;
    const tick = setInterval(() => setSeconds((s) => s + 1), 1000);
    return () => clearInterval(tick);
  }, [recording, props.recording]);

  /** The tallest the field may be in this window: the conversation keeps its minimum. */
  function maxNow(): number {
    const box = form.current;
    const main = box?.closest('.main');
    if (!box || !main) return AUTO_MAX_PX;
    const top = main.querySelector<HTMLElement>('.top')?.offsetHeight ?? 0;
    const dock = box.closest<HTMLElement>('.dock');
    const chrome = (dock?.offsetHeight ?? box.offsetHeight) - (field.current?.offsetHeight ?? 0);
    return maxFieldHeight(main.clientHeight - top, chrome);
  }

  // The field grows with its text up to 8 lines, unless the user set its height.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the text and the set height change it; maxNow reads the layout
  useLayoutEffect(() => {
    const el = field.current;
    if (!el) return;
    if (fixed !== null) {
      el.style.height = `${clampHeight(fixed, maxNow())}px`;
      return;
    }
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, AUTO_MAX_PX)}px`;
  }, [draft, fixed]);

  function setHeight(px: number | null) {
    setFixed(px);
    saveHeight(px);
  }

  function dragFrom(e: PointerEvent<HTMLSpanElement>) {
    const el = field.current;
    if (!el || e.button !== 0) return;
    e.preventDefault();
    const grip = e.currentTarget;
    const startY = e.clientY;
    const start = el.offsetHeight;
    const max = maxNow();
    grip.setPointerCapture?.(e.pointerId);
    const move = (ev: globalThis.PointerEvent) =>
      setFixed(clampHeight(start + startY - ev.clientY, max));
    const up = () => {
      grip.removeEventListener('pointermove', move);
      grip.removeEventListener('pointerup', up);
      grip.removeEventListener('pointercancel', up);
      saveHeight(fixedRef.current);
    };
    grip.addEventListener('pointermove', move);
    grip.addEventListener('pointerup', up);
    grip.addEventListener('pointercancel', up);
  }

  function gripKey(e: KeyboardEvent<HTMLSpanElement>) {
    const el = field.current;
    if (!el) return;
    const step = { ArrowUp: LINE_PX, ArrowDown: -LINE_PX }[e.key];
    if (step !== undefined) {
      e.preventDefault();
      setHeight(clampHeight(el.offsetHeight + step, maxNow()));
    } else if (e.key === 'Enter' || e.key === 'Escape') {
      e.preventDefault();
      setHeight(null);
    }
  }

  const sources = props.completions;
  const trigger = view.inputEnabled || sources?.fixture?.paths ? triggerAt(draft, caret) : null;
  const mentions = sources?.sessionId !== undefined;
  const wantsPaths = trigger?.kind === 'at' && mentions;
  const paths = usePaths(
    sources?.sessionId ?? null,
    sources?.online ?? false,
    wantsPaths,
    sources?.fixture?.paths,
  );
  const token = trigger ? `${trigger.kind}${trigger.start}` : null;
  const shut = token !== null && token === closed;
  let items: Item[] = [];
  let status: { kind: 'loading' | 'none' | 'offline'; text: string } | null = null;
  if (trigger && !shut && sources) {
    if (trigger.kind === 'slash') {
      if (view.inputEnabled && sources.commands) {
        items = filterCommands(sources.commands.commands, trigger.query);
      }
    } else if (mentions) {
      if (paths.phase === 'offline') {
        status = {
          kind: 'offline',
          text: t('{device} is offline — file mentions need it online').replace(
            '{device}',
            sources.deviceName,
          ),
        };
      } else if (view.inputEnabled && paths.phase === 'loading') {
        status = {
          kind: 'loading',
          text: t('Searching {device}…').replace('{device}', sources.deviceName),
        };
      } else if (view.inputEnabled && paths.phase === 'ready') {
        items = filterPaths(paths.paths, trigger.query);
        if (items.length === 0) {
          status = {
            kind: 'none',
            text: t('No files match “{query}”').replace('{query}', trigger.query),
          };
        }
      }
    }
  }
  const listOpen = items.length > 0;
  const popOpen = listOpen || status !== null;
  const at = Math.min(selected, Math.max(items.length - 1, 0));
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new token or query starts at the first row
  useEffect(() => setSelected(0), [trigger?.kind, trigger?.query]);
  useEffect(() => {
    if (token === null) setClosed(null);
  }, [token]);

  // The list starts at the token, kept inside the composer.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the list's size and the token move it
  useLayoutEffect(() => {
    const pop = popover.current;
    const el = field.current;
    const box = form.current;
    if (!pop || !el || !box || !trigger) return;
    pop.style.setProperty(
      '--ax',
      `${anchorX(xOfIndex(el, trigger.start), box.offsetWidth, pop.offsetWidth)}px`,
    );
  }, [popOpen, trigger?.start, items.length, status?.kind]);

  // After a pick the caret goes behind the inserted text.
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs after each draft change
  useLayoutEffect(() => {
    const to = putCaret.current;
    if (to === null) return;
    putCaret.current = null;
    field.current?.setSelectionRange(to, to);
    setCaret(to);
  }, [draft]);

  function pick(item: Item) {
    if (!trigger) return;
    const next = insertPick(draft, trigger, item);
    putCaret.current = next.caret;
    setDraft(next.text);
    props.onDirty();
    field.current?.focus();
  }

  const text = draft.trim();
  const uploading = attachments.some((a) => a.status !== 'ready');
  const canSend = view.inputEnabled && text !== '' && !props.busy && !uploading;

  // While a turn runs the field stays open and sends one of two ways; a driver that cannot steer only queues.
  const sending = view.sending;
  const steerable = sending?.steer === true;
  const defaultMode: SendMode = steerable ? (props.sendMode ?? 'queue') : 'queue';
  const otherMode: SendMode = defaultMode === 'steer' ? 'queue' : 'steer';

  function submit(e?: FormEvent, mode?: SendMode) {
    e?.preventDefault();
    if (!canSend) return;
    const how = sending ? (mode ?? defaultMode) : 'queue';
    sent.current = attachments;
    props.onSend(
      text,
      attachments.map((a) => a.attachmentId ?? ''),
      how,
    );
    setDraft('');
    setAttachments([]);
    setAttachError(null);
  }

  if (view.layout === 'bar') {
    const line = view.line;
    return (
      <div
        className={`composer cbar${view.action ? '' : ' ro'}`}
        role="status"
        aria-label={view.line?.title}
        data-testid="cockpit.composer"
      >
        <span className="cst" title={line?.title}>
          {line ? lineGlyph(line.glyph) : null}
          {line?.text}
        </span>
        {view.action === 'handoff' ? (
          <IconButton
            label={t('Hand off to another machine')}
            icon={Icon.handoff()}
            primary
            onClick={props.onHandoff}
            data-testid="cockpit.handoff"
          />
        ) : null}
      </div>
    );
  }

  if (recording && props.voiceAvailable) {
    return (
      <RecordingComposer
        seconds={seconds}
        onDiscard={() => {
          setRecording(false);
          setSeconds(0);
        }}
        onStop={() => {
          setRecording(false);
          setSeconds(0);
        }}
      />
    );
  }

  const line = view.line;
  const sendLabel = props.sendLabel ?? t('Send');
  return (
    <form
      ref={form}
      className="composer"
      onSubmit={submit}
      onDragOver={(e) => {
        if (takesImages && e.dataTransfer?.types.includes('Files')) e.preventDefault();
      }}
      onDrop={onDrop}
      data-testid="cockpit.composer"
    >
      {/* biome-ignore lint/a11y/useSemanticElements: a focusable splitter that moves; an hr cannot take focus or a value */}
      <span
        className="grip"
        role="separator"
        aria-orientation="horizontal"
        aria-label={t('Composer height')}
        aria-valuemin={LINE_PX}
        aria-valuemax={Math.max(AUTO_MAX_PX, fixed ?? 0)}
        aria-valuenow={fixed ?? AUTO_MAX_PX}
        title={t('Drag to resize · double-click resets')}
        tabIndex={0}
        onPointerDown={dragFrom}
        onDoubleClick={() => setHeight(null)}
        onKeyDown={gripKey}
        data-testid="cockpit.composer-grip"
      />
      {props.where}
      {attachments.length > 0 ? (
        <ul className="thumbs" aria-label={t('Images')} data-testid="cockpit.thumbs">
          {attachments.map((a) => (
            <li
              key={a.id}
              className={`thumb${a.status === 'ready' ? '' : ` ${a.status}`}`}
              title={
                a.status === 'failed'
                  ? t('{name} was not uploaded').replace('{name}', a.name)
                  : a.name
              }
              aria-busy={a.status === 'uploading'}
              data-status={a.status}
            >
              <img src={dataUrl(a)} alt={a.name} />
              {a.status === 'uploading' ? (
                <span className="thumb-wait" role="img" aria-label={t('Uploading')}>
                  <Spinner />
                </span>
              ) : null}
              <button
                type="button"
                className="thumb-x"
                aria-label={t('Remove {name}').replace('{name}', a.name)}
                title={t('Remove {name}').replace('{name}', a.name)}
                onClick={() => setAttachments((all) => all.filter((x) => x.id !== a.id))}
                data-testid="cockpit.thumb-remove"
              >
                {Icon.close()}
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      <textarea
        ref={field}
        className="ph-field"
        rows={1}
        value={draft}
        placeholder={view.placeholder}
        aria-label={view.placeholder}
        disabled={!view.inputEnabled}
        aria-haspopup="listbox"
        aria-autocomplete="list"
        {...(listOpen
          ? { 'aria-controls': listId(comboId), 'aria-activedescendant': optionId(comboId, at) }
          : {})}
        onChange={(e) => {
          setDraft(e.target.value);
          setCaret(e.target.selectionStart);
          props.onDirty();
        }}
        onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
        onKeyDown={(e) => {
          if (listOpen || status) {
            const act = keyAction(e.key, e.shiftKey, e.nativeEvent.isComposing);
            if (act === 'close') {
              e.preventDefault();
              e.nativeEvent.stopImmediatePropagation();
              setClosed(token);
              return;
            }
            if (listOpen && (act === 'down' || act === 'up')) {
              e.preventDefault();
              setSelected(moveSelection(at, items.length, act));
              return;
            }
            const chosen = items[at];
            if (listOpen && act === 'insert' && chosen) {
              e.preventDefault();
              pick(chosen);
              return;
            }
          }
          if (e.key === 'Tab' && e.shiftKey && props.onCycleMode && !e.nativeEvent.isComposing) {
            e.preventDefault();
            props.onCycleMode();
            return;
          }
          if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault();
            if (!e.altKey) submit();
            else if (sending && (otherMode === 'queue' || steerable)) submit(undefined, otherMode);
          }
        }}
        onPaste={onPaste}
        data-testid="cockpit.composer-input"
      />
      {trigger && status ? (
        <CompletionStatus anchor={popover} kind={status.kind} text={status.text} />
      ) : null}
      {trigger && listOpen ? (
        <CompletionList
          anchor={popover}
          id={comboId}
          items={items}
          query={trigger.query}
          selected={at}
          hovered={sources?.fixture?.hovered}
          note={
            trigger.kind === 'slash' && sources?.commands?.source === 'builtin'
              ? t('Project commands and skills show once the session has started')
              : undefined
          }
          onPick={pick}
        />
      ) : null}
      {props.error || attachError ? (
        <p className="cerr" role="alert" data-testid="cockpit.composer-error">
          {Icon.alert()}
          {props.error ?? attachError}
        </p>
      ) : null}
      <div className="cf">
        {props.acceptsImages ? (
          <>
            <IconButton
              label={t('Attach images')}
              detail={t('Attach images · paste and drop work too')}
              icon={Icon.plus()}
              disabled={!view.inputEnabled}
              onClick={() => picker.current?.click()}
              data-testid="cockpit.attach"
            />
            <input
              ref={picker}
              type="file"
              accept="image/png,image/jpeg,image/gif,image/webp"
              multiple
              hidden
              onChange={(e) => {
                const files = [...(e.target.files ?? [])];
                e.target.value = '';
                void addFiles(files);
              }}
              data-testid="cockpit.attach-input"
            />
          </>
        ) : null}
        {props.leading}
        {line ? (
          <span
            className={`cst${line.tone === 'bad' ? ' bad' : ''}`}
            role="img"
            title={line.title}
            aria-label={line.text ? `${line.text} · ${line.title}` : line.title}
            data-testid="cockpit.composer-state"
          >
            {lineGlyph(line.glyph)}
            {line.text}
          </span>
        ) : null}
        {view.action === 'stop' ? (
          <IconButton
            label={t('Stop {provider}').replace('{provider}', props.providerName)}
            icon={Icon.stop()}
            onClick={props.onStop}
            data-testid="cockpit.stop"
          />
        ) : null}
        {view.action === 'retry' ? (
          <IconButton
            label={t('Retry start')}
            icon={Icon.retry()}
            onClick={props.onRetry}
            data-testid="cockpit.retry"
          />
        ) : null}
        <span className="r">
          {props.options}
          {props.voiceAvailable ? (
            <IconButton
              label={t('Dictate')}
              detail={t('Dictate · transcribed on your machines')}
              icon={Icon.mic()}
              disabled={!view.inputEnabled}
              onClick={() => setRecording(true)}
              data-testid="cockpit.mic"
            />
          ) : null}
          {sending ? (
            <SendWhileRunning
              mode={defaultMode}
              steer={steerable}
              ready={canSend}
              providerName={props.providerName}
              onSend={(mode) => submit(undefined, mode)}
              {...(props.sendMenuOpen ? { initialOpen: true } : {})}
            />
          ) : (
            <IconButton
              label={sendLabel}
              hint="↩"
              icon={Icon.send()}
              primary
              type="submit"
              disabled={!canSend}
              data-testid="cockpit.send"
            />
          )}
        </span>
      </div>
    </form>
  );
}
