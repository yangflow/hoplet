import {
  EXPORT_SUMMARY_MAX_BYTES,
  type ExportConversation,
  type LiveSessionMeta,
} from '@hoplet/shared';
import { type ReactNode, useEffect, useRef, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { TextField } from '../../components/TextField.tsx';
import { fetchSessionExport } from '../../lib/api.ts';
import { downloadBlob } from '../../lib/audit.ts';
import { copyText } from '../../lib/clipboard.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import {
  cutBytes,
  DEFAULT_EXPORT_OPTIONS,
  type ExportOptions,
  type ExportScan,
  exportQuery,
  kindLabel,
  localTimeZone,
  scanExport,
  sizeText,
} from './export.ts';
import { displayTitle } from './group.ts';
import { lastAnswerMarkdown } from './last-answer.ts';
import './export.css';

type Phase = 'options' | 'review' | 'progress' | 'done' | 'failed';

/** Development fixtures for /__states: the phase to show and what the core would have sent. */
export type ExportFixture = {
  phase: Phase;
  options?: Partial<ExportOptions>;
  /** The file's text so far (progress) or whole (review, done). */
  text?: string;
  error?: string;
};

/** The preview shows the head of the file; the rest is in the download. */
const PREVIEW_CHARS = 24 * 1024;

const fill = (text: string, values: Record<string, string | number>) =>
  text.replace(/\{(\w+)\}/g, (m, k: string) => (k in values ? String(values[k]) : m));

function countText(n: number, one: string, many: string): string {
  return fill(n === 1 ? t(one) : t(many), { n });
}

function turnsText(turns: number[]): string {
  if (turns.length === 0) return t('Outside the conversation');
  if (turns.length === 1) return fill(t('Turn {n}'), { n: turns[0] as number });
  const shown = turns.slice(0, 6).join(', ');
  return fill(t('Turns {list}'), { list: turns.length > 6 ? `${shown}…` : shown });
}

/** One checkbox or radio line, styled as the delete dialog's checklist. */
function Choice(props: {
  id: string;
  type: 'checkbox' | 'radio';
  name?: string;
  label: string;
  detail: ReactNode;
  checked: boolean;
  onChange: () => void;
  children?: ReactNode;
}) {
  const { checked, type } = props;
  return (
    <div className="ex-choice">
      <label
        className={`opt${checked ? ' on' : ''}${type === 'radio' ? ' rad' : ''}`}
        data-testid={`cockpit.export-opt-${props.id}`}
      >
        <input
          type={type}
          className="opt-in"
          name={props.name}
          checked={checked}
          onChange={props.onChange}
        />
        <span className="cbx" aria-hidden="true">
          {checked && type === 'checkbox' ? Icon.check() : null}
        </span>
        <span className="opt-t">
          <span className="ol">{props.label}</span>
          <span className="od">{props.detail}</span>
        </span>
      </label>
      {props.children}
    </div>
  );
}

/**
 * Export as Markdown (ADR 0040): the parts of the session the file carries and the strings to mask, a review of
 * what was masked, then the export. The core writes and redacts the file and streams it; the browser holds it
 * until it is complete and saves it then, so a stopped export leaves nothing behind.
 */
export function ExportDialog(props: {
  session: LiveSessionMeta;
  fixture?: ExportFixture;
  onClose: () => void;
  say: (text: string, failed?: boolean) => void;
}) {
  const { session, fixture } = props;
  const title = displayTitle(session);
  const [options, setOptions] = useState<ExportOptions>({
    ...DEFAULT_EXPORT_OPTIONS,
    ...fixture?.options,
  });
  const [phase, setPhase] = useState<Phase>(fixture?.phase ?? 'options');
  const [text, setText] = useState<string | null>(fixture?.text ?? null);
  const [name, setName] = useState('session.md');
  const [progress, setProgress] = useState<ExportScan | null>(
    fixture?.phase === 'progress' && fixture.text ? scanExport(fixture.text) : null,
  );
  const [error, setError] = useState<string | null>(fixture?.error ?? null);
  // The options the text in hand was written with: a preview is reused by Export when they are the same.
  const [madeWith, setMadeWith] = useState<string | null>(() =>
    fixture?.text
      ? JSON.stringify(
          exportQuery({ ...DEFAULT_EXPORT_OPTIONS, ...fixture.options }, localTimeZone()),
        )
      : null,
  );
  const [previewing, setPreviewing] = useState(false);
  const [summaryLoaded, setSummaryLoaded] = useState(fixture !== undefined);
  const abort = useRef<AbortController | null>(null);
  const closed = useRef(false);
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    closed.current = false;
    return () => {
      closed.current = true;
      abort.current?.abort();
    };
  }, []);

  // The summary starts from the last reply; Hoplet writes none of its own.
  useEffect(() => {
    if (summaryLoaded || options.summary === null) return;
    setSummaryLoaded(true);
    lastAnswerMarkdown(session.sessionId).then(
      (answer) => {
        if (closed.current || answer === null) return;
        setOptions((cur) =>
          cur.summary === ''
            ? { ...cur, summary: cutBytes(answer, EXPORT_SUMMARY_MAX_BYTES) }
            : cur,
        );
      },
      () => {},
    );
  }, [summaryLoaded, options.summary, session.sessionId]);

  // The focus follows the phase, as in the delete dialog.
  const first = useRef(true);
  // biome-ignore lint/correctness/useExhaustiveDependencies: moves the focus when the phase changes
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    const dialog = root.current?.closest<HTMLElement>('[role="dialog"]');
    (dialog?.querySelector<HTMLElement>('[data-autofocus]') ?? dialog)?.focus();
  }, [phase]);

  const query = exportQuery(options, localTimeZone());
  const key = JSON.stringify(query);
  const scan = text !== null && phase !== 'progress' ? scanExport(text) : null;
  const ready = text !== null && madeWith === key;

  /** Runs one export, audited by the core; `null` when it was cancelled or failed. */
  async function run(onProgress: boolean): Promise<{ name: string; text: string } | null> {
    abort.current?.abort();
    const controller = new AbortController();
    abort.current = controller;
    let last = 0;
    try {
      const result = await fetchSessionExport(
        session.sessionId,
        query,
        (so) => {
          const now = Date.now();
          if (!onProgress || now - last < 120) return;
          last = now;
          setProgress(scanExport(so));
        },
        controller.signal,
      );
      if (closed.current || controller.signal.aborted) return null;
      setText(result.text);
      setName(result.name);
      setMadeWith(key);
      return result;
    } catch (e) {
      if (closed.current || controller.signal.aborted) return null;
      setError(errorText(e));
      return null;
    }
  }

  async function preview() {
    setError(null);
    setPreviewing(true);
    const result = await run(false);
    if (closed.current) return;
    setPreviewing(false);
    if (result) setPhase('review');
  }

  function save(file: string, body: string) {
    const blob = new Blob([body], { type: 'text/markdown;charset=utf-8' });
    if (!downloadBlob(file, blob)) props.say(t('Could not save the file'), true);
  }

  async function exportNow() {
    setError(null);
    if (ready && text !== null) {
      save(name, text);
      setPhase('done');
      return;
    }
    setProgress(null);
    setPhase('progress');
    const result = await run(true);
    if (closed.current) return;
    if (result) {
      save(result.name, result.text);
      setPhase('done');
    } else if (abort.current?.signal.aborted) {
      setPhase('options');
    } else {
      setPhase('failed');
    }
  }

  function cancelExport() {
    abort.current?.abort();
    setPhase('options');
  }

  const set = (patch: Partial<ExportOptions>) => setOptions((cur) => ({ ...cur, ...patch }));
  const conversation = (value: ExportConversation, label: string, detail: string) => (
    <Choice
      id={`conversation-${value}`}
      type="radio"
      name="export-conversation"
      label={label}
      detail={detail}
      checked={options.conversation === value}
      onChange={() => set({ conversation: value })}
    />
  );

  const warning = (
    <div className="xnote att" data-testid="cockpit.export-warning">
      {Icon.warning()}
      <span>
        {t(
          'Hoplet masks what looks like a credential. It cannot find every secret: read the file before you share it.',
        )}
        {scan && phase === 'options' && ready ? (
          <>
            {' '}
            <button
              type="button"
              className="ex-link"
              onClick={() => setPhase('review')}
              data-testid="cockpit.export-review-link"
            >
              {fill(
                scan.masked === 1 ? t('Review 1 masked value') : t('Review {n} masked values'),
                { n: scan.masked },
              )}
            </button>
          </>
        ) : null}
      </span>
    </div>
  );

  const problem = error ? (
    <div className="xnote bad" role="alert" data-testid="cockpit.export-problem">
      {Icon.alert()}
      <span>{error}</span>
    </div>
  ) : null;

  let body: ReactNode;
  if (phase === 'review' && scan && text !== null) {
    body = (
      <>
        <div className="dlg-h">{t('Masked values')}</div>
        <div className="dlg-sub">
          <span>
            {scan.masked === 0
              ? t('Nothing in the file looks like a credential')
              : t('Masked in the file, never in the session')}
          </span>
        </div>
        <div className="dlg-b">
          {scan.kinds.length > 0 ? (
            <ul className="ex-kinds" data-testid="cockpit.export-kinds">
              {scan.kinds.map((k) => (
                <li key={k.kind}>
                  <span className="ol">{kindLabel(k.kind)}</span>
                  <span className="od">{turnsText(k.turns)}</span>
                  <span className="n">{k.count}</span>
                </li>
              ))}
            </ul>
          ) : null}
          <TextField
            multiline
            label={t('Also mask')}
            showLabel
            placeholder={t('Hostnames, customer names… one per line')}
            rows={3}
            value={options.mask}
            onChange={(e) => set({ mask: e.target.value })}
            data-testid="cockpit.export-mask"
          />
          <section
            className="ex-pre"
            // biome-ignore lint/a11y/noNoninteractiveTabindex: the preview scrolls and must be reachable by keyboard
            tabIndex={0}
            aria-label={t('Preview of the file')}
            data-testid="cockpit.export-preview"
          >
            <pre>{text.length > PREVIEW_CHARS ? `${text.slice(0, PREVIEW_CHARS)}\n…` : text}</pre>
          </section>
        </div>
        <div className="xnote">
          {Icon.shield()}
          <span>
            {t(
              'Masked: tokens and keys Hoplet knows, URL credentials, Bearer headers, private keys, NAME=value secrets and your strings. Not masked: anything else.',
            )}
          </span>
        </div>
        {problem}
        <div className="dlg-f">
          <Button
            variant="quiet"
            onClick={() => setPhase('options')}
            data-testid="cockpit.export-back"
          >
            {t('Back')}
          </Button>
          {ready ? null : (
            <Button
              icon={previewing ? <Spinner /> : Icon.retry()}
              disabled={previewing}
              onClick={() => void preview()}
              data-testid="cockpit.export-update"
            >
              {t('Update preview')}
            </Button>
          )}
          <Button
            variant="primary"
            icon={Icon.fileDown()}
            disabled={previewing}
            onClick={() => void exportNow()}
            data-autofocus=""
            data-testid="cockpit.export-confirm"
          >
            {t('Export')}
          </Button>
        </div>
      </>
    );
  } else if (phase === 'progress') {
    const p = progress;
    const share = p?.total ? Math.min(1, p.turn / Math.max(1, p.total)) : 0;
    body = (
      <>
        <div className="dlg-h">{t('Exporting…')}</div>
        <div className="dlg-b">
          <div className="xs" role="status" data-testid="cockpit.export-progress">
            <span className="run">
              <Spinner />
            </span>
            <div>
              <div className="xl">
                {p?.total
                  ? fill(t('Turn {n} of {total} · {size} written'), {
                      n: p.turn,
                      total: p.total,
                      size: sizeText(p.bytes),
                    })
                  : fill(t('{size} written'), { size: sizeText(p?.bytes ?? 0) })}
              </div>
              <div className="od">
                {t('The file is saved when it is complete. Keep this window open.')}
              </div>
            </div>
          </div>
          <div
            className="ex-bar"
            role="progressbar"
            aria-label={t('Export progress')}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round(share * 100)}
          >
            <span style={{ width: `${share * 100}%` }} />
          </div>
        </div>
        <div className="dlg-f">
          <Button
            variant="quiet"
            onClick={cancelExport}
            data-autofocus=""
            data-testid="cockpit.export-cancel-run"
          >
            {t('Cancel export')}
          </Button>
        </div>
      </>
    );
  } else if (phase === 'done' && scan) {
    body = (
      <>
        <div className="dlg-h">{t('Exported')}</div>
        <div className="dlg-sub">
          <span className="mono" data-testid="cockpit.export-name">
            {name}
          </span>
        </div>
        <dl className="ex-facts dlg-b" data-testid="cockpit.export-facts">
          <dt>{t('Size')}</dt>
          <dd>
            {sizeText(scan.bytes)}
            {scan.total !== null ? ` · ${countText(scan.total, '1 turn', '{n} turns')}` : ''}
          </dd>
          <dt>{t('Masked')}</dt>
          <dd>{countText(scan.masked, '1 value', '{n} values')}</dd>
          <dt>{t('Elided')}</dt>
          <dd>{countText(scan.elided, '1 long output', '{n} long outputs')}</dd>
        </dl>
        {warning}
        <div className="dlg-f">
          <Button
            icon={Icon.copy()}
            onClick={() =>
              void copyText(text ?? '').then((ok) =>
                ok ? props.say(t('Markdown copied')) : props.say(t('Could not copy'), true),
              )
            }
            data-testid="cockpit.export-copy"
          >
            {t('Copy as Markdown')}
          </Button>
          <Button
            variant="primary"
            onClick={props.onClose}
            data-autofocus=""
            data-testid="cockpit.export-done"
          >
            {t('Done')}
          </Button>
        </div>
      </>
    );
  } else if (phase === 'failed') {
    body = (
      <>
        <div className="dlg-h">{t('Export stopped')}</div>
        <div className="xnote bad" role="alert" data-testid="cockpit.export-error">
          {Icon.alert()}
          <span>
            {error ?? t('The export could not finish.')}{' '}
            {t('The partial file was discarded, so nothing incomplete was saved.')}
          </span>
        </div>
        <div className="dlg-f">
          <Button variant="quiet" onClick={props.onClose} data-testid="cockpit.export-close">
            {t('Close')}
          </Button>
          <Button
            variant="primary"
            icon={Icon.retry()}
            onClick={() => void exportNow()}
            data-autofocus=""
            data-testid="cockpit.export-retry"
          >
            {t('Try again')}
          </Button>
        </div>
      </>
    );
  } else {
    body = (
      <>
        <div className="dlg-h">{t('Export as Markdown')}</div>
        <div className="dlg-sub">
          <span className="ex-title">{title}</span>
        </div>
        <div className="dlg-b">
          <div className="ex-sec" id="export-conversation-label">
            {t('Conversation')}
          </div>
          <div role="radiogroup" aria-labelledby="export-conversation-label" className="ex-group">
            {conversation(
              'all',
              t('Everything'),
              t('Your messages, replies and tool calls; long output elided'),
            )}
            {conversation(
              'replies',
              t('Your messages and final replies'),
              t('One reply per turn, no tool calls'),
            )}
            {conversation('none', t('None'), t('Only the parts below'))}
          </div>
          <div className="ex-sec">{t('Also include')}</div>
          <Choice
            id="summary"
            type="checkbox"
            label={t('Summary')}
            detail={t('Starts from the last reply; edit it here')}
            checked={options.summary !== null}
            onChange={() => set({ summary: options.summary === null ? '' : null })}
          >
            {options.summary !== null ? (
              <TextField
                multiline
                label={t('Summary')}
                className="ex-summary"
                rows={4}
                value={options.summary}
                onChange={(e) =>
                  set({ summary: cutBytes(e.target.value, EXPORT_SUMMARY_MAX_BYTES) })
                }
                data-testid="cockpit.export-summary"
              />
            ) : null}
          </Choice>
          <Choice
            id="changes"
            type="checkbox"
            label={t('Changes')}
            detail={t('The net diff against the base; capped at 256 KiB')}
            checked={options.changes}
            onChange={() => set({ changes: !options.changes })}
          />
          <Choice
            id="commands"
            type="checkbox"
            label={t('Commands')}
            detail={t('A table of the commands run, with machine and exit code')}
            checked={options.commands}
            onChange={() => set({ commands: !options.commands })}
          />
          <Choice
            id="machines"
            type="checkbox"
            label={t('Machines and hand-offs')}
            detail={t('The machines the session ran on and where it was handed off')}
            checked={options.machines}
            onChange={() => set({ machines: !options.machines })}
          />
        </div>
        {warning}
        {problem}
        <div className="dlg-f">
          <span className="l">
            <Button
              variant="quiet"
              icon={previewing ? <Spinner /> : Icon.eye()}
              disabled={previewing}
              onClick={() => void preview()}
              data-testid="cockpit.export-preview-button"
            >
              {t('Preview')}
            </Button>
          </span>
          <Button variant="quiet" onClick={props.onClose} data-testid="cockpit.export-cancel">
            {t('Cancel')}
          </Button>
          <Button
            variant="primary"
            icon={Icon.fileDown()}
            disabled={previewing}
            onClick={() => void exportNow()}
            data-autofocus=""
            data-testid="cockpit.export-confirm"
          >
            {t('Export')}
          </Button>
        </div>
      </>
    );
  }

  return (
    <Dialog
      label={t('Export as Markdown')}
      onClose={() => {
        if (phase !== 'progress') props.onClose();
      }}
      className="dx ex"
      data-testid="cockpit.export-dialog"
    >
      <div ref={root}>{body}</div>
    </Dialog>
  );
}
