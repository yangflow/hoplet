import {
  type FormEvent,
  type ReactNode,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon } from '../../../components/icons.tsx';
import { t } from '../../../lib/i18n.ts';
import type { ComposerView } from '../runtime.ts';

const MAX_ROWS_PX = 168;

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
 * The composer: a field with a send button, or a bar for the states that block writing. The state line and
 * its action appear only when the state blocks or changes what the user can do.
 */
export function Composer(props: {
  view: ComposerView;
  /** The display name of the session's provider. */
  providerName: string;
  /** Whether a prompt is being sent. */
  busy: boolean;
  error: string | null;
  /** Text to put back in the field (a prompt that was refused), with a counter to say it is new. */
  restore: { text: string; n: number } | null;
  /** Transcription is available (#17); until then the microphone is hidden. */
  voiceAvailable: boolean;
  /** Start in the recording state (fixtures). */
  recording?: boolean;
  onSend: (text: string) => void;
  onStop: () => void;
  onRetry: () => void;
  onHandoff: () => void;
  onDirty: () => void;
}) {
  const { view } = props;
  const [draft, setDraft] = useState('');
  const [recording, setRecording] = useState(props.recording ?? false);
  const [seconds, setSeconds] = useState(props.recording ? 7 : 0);
  const field = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (props.restore) setDraft((d) => (d === '' ? (props.restore as { text: string }).text : d));
  }, [props.restore]);

  useEffect(() => {
    if (!recording || props.recording) return;
    const tick = setInterval(() => setSeconds((s) => s + 1), 1000);
    return () => clearInterval(tick);
  }, [recording, props.recording]);

  // The field grows with its text up to a few lines.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the text is what changes the height
  useLayoutEffect(() => {
    const el = field.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, MAX_ROWS_PX)}px`;
  }, [draft]);

  const text = draft.trim();
  const canSend = view.inputEnabled && text !== '' && !props.busy;

  function submit(e?: FormEvent) {
    e?.preventDefault();
    if (!canSend) return;
    props.onSend(text);
    setDraft('');
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
  return (
    <form className="composer" onSubmit={submit} data-testid="cockpit.composer">
      <textarea
        ref={field}
        className="ph-field"
        rows={1}
        value={draft}
        placeholder={view.placeholder}
        aria-label={view.placeholder}
        disabled={!view.inputEnabled}
        onChange={(e) => {
          setDraft(e.target.value);
          props.onDirty();
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault();
            submit();
          }
        }}
        data-testid="cockpit.composer-input"
      />
      {props.error ? (
        <p className="cerr" role="alert" data-testid="cockpit.composer-error">
          {Icon.alert()}
          {props.error}
        </p>
      ) : null}
      <div className="cf">
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
          <IconButton
            label={t('Send')}
            hint="↩"
            icon={Icon.send()}
            primary
            type="submit"
            disabled={!canSend}
            data-testid="cockpit.send"
          />
        </span>
      </div>
    </form>
  );
}
