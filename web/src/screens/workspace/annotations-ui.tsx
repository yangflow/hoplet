import type { Annotation, ReviewDelivery, ReviewSendResult } from '@hoplet/shared';
import { type KeyboardEvent, useEffect, useRef, useState } from 'react';
import { Badge } from '../../components/Badge.tsx';
import { Button } from '../../components/Button.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { TextField } from '../../components/TextField.tsx';
import { ApiError } from '../../lib/api.ts';
import { copyText } from '../../lib/clipboard.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import type { Placement } from './annotations.ts';
import { formatCount } from './model.ts';
import type { Annotations, ReviewRequest } from './use-annotations.ts';

// The parts of a review in the diff (#84, design/screens/diff-annotations.html, direction A): a comment being
// written, the comments in the diff, the count at the viewer's foot, the bar of pending comments and the dialog
// that sends them.

/** `Line 42` or `Lines 42–44`. */
export function linesLabel(start: number, end: number): string {
  return start === end
    ? t('Line {n}').replace('{n}', String(start))
    : t('Lines {a}–{b}').replace('{a}', String(start)).replace('{b}', String(end));
}

/** A comment being written or edited: ⌘↵ or Ctrl+↵ adds it, Esc cancels. */
export function EditCard(props: {
  start: number;
  end: number;
  initial: string;
  editing: boolean;
  /** Why the last change failed. */
  error?: string | null;
  onSave: (body: string) => Promise<boolean>;
  onCancel: () => void;
}) {
  const [body, setBody] = useState(props.initial);
  const [saving, setSaving] = useState(false);
  const [tried, setTried] = useState(false);
  const empty = body.trim() === '';
  async function save() {
    if (empty || saving) return;
    setSaving(true);
    setTried(true);
    const ok = await props.onSave(body.trim());
    setSaving(false);
    if (ok) props.onCancel();
  }
  function onKeyDown(e: KeyboardEvent) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      void save();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      props.onCancel();
    }
  }
  return (
    <div className="ws-ann edit" data-testid="workspace.comment-edit">
      <div className="ws-ann-h">
        <span className="tag acc">
          {Icon.comment()}
          {linesLabel(props.start, props.end)}
        </span>
      </div>
      <TextField
        multiline
        label={t('Comment')}
        value={body}
        rows={3}
        maxLength={4000}
        autoFocus
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={onKeyDown}
        data-testid="workspace.comment-field"
      />
      {props.error && tried ? (
        <p className="ws-ann-err" role="alert">
          {props.error}
        </p>
      ) : null}
      <div className="ws-ann-f">
        <span className="ws-ann-m">
          <Badge kind="key">⌘↵</Badge>
          {props.editing ? t('to save') : t('to add')}
          <Badge kind="key">Esc</Badge>
          {t('to cancel')}
        </span>
        <Button variant="quiet" onClick={props.onCancel}>
          {t('Cancel')}
        </Button>
        <Button
          variant="primary"
          disabled={empty || saving}
          onClick={() => void save()}
          data-testid="workspace.comment-save"
        >
          {props.editing ? t('Save') : t('Add comment')}
        </Button>
      </div>
    </div>
  );
}

/** A comment in the diff: pending ones edit and delete, sent ones resolve, resolved ones fold to a line. */
export function NoteCard(props: {
  note: Annotation;
  placement: Placement;
  onEdit: () => void;
  onDelete: () => void;
  onResolve: (resolved: boolean) => void;
}) {
  const { note: a, placement: p } = props;
  const lines = linesLabel(p.startLine, p.endLine);
  if (a.state === 'resolved')
    return (
      <div className="ws-ann fold" data-testid="workspace.comment-resolved">
        <span className="ws-ann-ok">{Icon.check()}</span>
        <span className="tag line">{lines}</span>
        <span className="ws-ann-b">{a.body}</span>
        <Button
          variant="quiet"
          onClick={() => props.onResolve(false)}
          data-testid="workspace.comment-reopen"
        >
          {t('Reopen')}
        </Button>
      </div>
    );
  const changed = p.fit === 'changed';
  const sent = a.state === 'sent' && a.sent;
  return (
    <div
      className={`ws-ann${sent ? ' sent' : ''}`}
      data-testid={sent ? 'workspace.comment-sent' : 'workspace.comment'}
    >
      <div className="ws-ann-h">
        <span className="tag">
          {Icon.comment()}
          {lines}
        </span>
        {sent ? (
          <span className="tag line">
            {Icon.send()}
            {t('Sent in review {n}').replace('{n}', String(sent.review))}
          </span>
        ) : (
          <span className="ws-ann-m">{t('Pending')}</span>
        )}
        {changed ? (
          <span className="tag line" data-testid="workspace.comment-changed">
            {t('Lines changed')}
          </span>
        ) : null}
        <span className="ws-gap" />
        {sent ? null : (
          <>
            <IconButton label={t('Edit')} icon={Icon.pen()} onClick={props.onEdit} />
            <IconButton
              label={t('Delete')}
              icon={Icon.trash()}
              onClick={props.onDelete}
              data-testid="workspace.comment-delete"
            />
          </>
        )}
      </div>
      <div className="ws-ann-b">{a.body}</div>
      {changed && a.snippet ? <div className="ws-snip">{a.snippet}</div> : null}
      {sent ? (
        <div className="ws-ann-f">
          <span className="ws-ann-m">
            {changed
              ? t('Shown with the code as sent; these lines have changed since.')
              : p.fit === 'moved'
                ? t('The lines moved since the review was sent.')
                : null}
          </span>
          <Button
            icon={Icon.check()}
            onClick={() => props.onResolve(true)}
            data-testid="workspace.comment-resolve"
          >
            {t('Resolve')}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

/** The viewer's foot while the file has comments: how many are open and resolved, and the resolved ones' toggle. */
export function CommentsFoot(props: {
  open: number;
  resolved: number;
  showResolved: boolean;
  onToggle: () => void;
}) {
  if (props.open + props.resolved === 0) return null;
  return (
    <div className="ws-vfoot" data-testid="workspace.comments-foot">
      <span>
        {t('{open} open · {resolved} resolved')
          .replace('{open}', formatCount(props.open))
          .replace('{resolved}', formatCount(props.resolved))}
      </span>
      {props.resolved > 0 ? (
        <Button variant="quiet" onClick={props.onToggle} data-testid="workspace.show-resolved">
          {props.showResolved ? t('Hide resolved') : t('Show resolved')}
        </Button>
      ) : null}
    </div>
  );
}

/** The pending comments of every file, over the foot of the viewer. */
export function ReviewBar(props: {
  count: number;
  files: number;
  onDiscard: () => void;
  onReview: () => void;
}) {
  return (
    <div className="ws-rbar" data-testid="workspace.review-bar">
      <span className="ws-rbar-l">
        {Icon.comment()}
        <b className="ws-rbar-c">
          {(props.count === 1 ? t('{n} pending comment') : t('{n} pending comments')).replace(
            '{n}',
            formatCount(props.count),
          )}
        </b>
        <span>
          {(props.files === 1 ? t('in {n} file') : t('in {n} files')).replace(
            '{n}',
            formatCount(props.files),
          )}
        </span>
      </span>
      <Button variant="quiet" onClick={props.onDiscard} data-testid="workspace.review-discard">
        {t('Discard')}
      </Button>
      <Button
        variant="primary"
        icon={Icon.send()}
        onClick={props.onReview}
        data-testid="workspace.review-open"
      >
        {t('Review and send…')}
      </Button>
    </div>
  );
}

/** What the session can take now: a prompt, or a queued message and perhaps a steer while a turn runs. */
export type ReviewTarget = {
  /** Why it cannot be sent at all (the owner is offline, the session runs outside Hoplet), or null. */
  blocked: string | null;
  busy: boolean;
  canSteer: boolean;
};

const PREVIEW_DELAY_MS = 250;

/**
 * Send review: an optional note, the message exactly as the core composes it, and how it goes. The message is
 * the core's dry run of the same request, so what is shown is what is sent.
 */
export function SendReview(props: {
  notes: Annotations;
  ids: string[];
  files: number;
  target: ReviewTarget;
  initialNote?: string;
  onClose: () => void;
  onSent: (result: ReviewSendResult) => void;
}) {
  const { notes, ids, target } = props;
  const [note, setNote] = useState(props.initialNote ?? '');
  const [preview, setPreview] = useState<ReviewSendResult | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [sending, setSending] = useState<ReviewDelivery | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const requestId = useRef(crypto.randomUUID());
  const key = ids.join(',');

  // biome-ignore lint/correctness/useExhaustiveDependencies: `key` stands for the ids
  useEffect(() => {
    const abort = new AbortController();
    const timer = setTimeout(() => {
      const request: ReviewRequest = { ids, note, delivery: 'prompt' };
      notes.preview(request, abort.signal).then(
        (r) => {
          if (abort.signal.aborted) return;
          setPreview(r);
          setPreviewError(null);
        },
        (err: unknown) => {
          if (!abort.signal.aborted) setPreviewError(errorText(err));
        },
      );
    }, PREVIEW_DELAY_MS);
    return () => {
      abort.abort();
      clearTimeout(timer);
    };
  }, [key, note, notes.preview]);

  const over = preview !== null && preview.chars > preview.limit;
  const ready = preview !== null && !over && sending === null;

  async function send(delivery: ReviewDelivery) {
    setSending(delivery);
    setError(null);
    try {
      const result = await notes.send({ ids, note, delivery, requestId: requestId.current });
      props.onSent(result);
    } catch (err) {
      setError(
        err instanceof ApiError && err.code === 'busy'
          ? t('A turn started. Queue the review, or send it when the turn ends.')
          : errorText(err),
      );
      // A refused request can be tried again under a new ID; a sent one repeats under the same.
      if (err instanceof ApiError && err.status < 500) requestId.current = crypto.randomUUID();
    } finally {
      setSending(null);
    }
  }

  const count = preview?.chars ?? 0;
  const limit = preview?.limit ?? 8000;
  return (
    <Dialog
      label={t('Send review')}
      onClose={props.onClose}
      size="wide"
      className="ws-send"
      data-testid="workspace.send-review"
    >
      <div className="dlg-h">{t('Send review')}</div>
      <div className="ws-send-sub">
        {(ids.length === 1 ? t('{n} comment') : t('{n} comments')).replace(
          '{n}',
          formatCount(ids.length),
        )}{' '}
        {(props.files === 1 ? t('in {n} file') : t('in {n} files')).replace(
          '{n}',
          formatCount(props.files),
        )}
        {' · '}
        {t('sent as the next turn')}
      </div>
      <div className="dlg-b">
        <div className="ws-send-l">
          <span>{t('Note')}</span>
          <span>{t('Optional, goes first')}</span>
        </div>
        <TextField
          multiline
          label={t('Note')}
          value={note}
          rows={2}
          maxLength={2000}
          data-autofocus=""
          onChange={(e) => setNote(e.target.value)}
          data-testid="workspace.review-note"
        />
        <div className="ws-send-l">
          <span>{t('Message')}</span>
          <span className={over ? 'over' : ''} data-testid="workspace.review-count">
            {t('{n} / {limit} characters')
              .replace('{n}', formatCount(count))
              .replace('{limit}', formatCount(limit))}
          </span>
        </div>
        <pre
          className="ws-compose"
          // biome-ignore lint/a11y/noNoninteractiveTabindex: the message scrolls and must be reachable by keyboard
          tabIndex={0}
          data-testid="workspace.review-message"
        >
          {preview ? preview.text : previewError ? previewError : <Spinner />}
        </pre>
        {target.busy && !target.blocked ? (
          <p className="ws-send-n" role="note">
            {Icon.clock()}
            <span>
              {target.canSteer
                ? t(
                    'A turn is running. Queue sends the review when it ends; Steer now adds it to the running turn.',
                  )
                : t('A turn is running. Queue sends the review when it ends.')}
            </span>
          </p>
        ) : null}
        {target.blocked ? (
          <p className="ws-send-n" role="note" data-testid="workspace.review-blocked">
            {Icon.info()}
            <span>{target.blocked}</span>
          </p>
        ) : null}
        {over ? (
          <p className="ws-send-n bad" role="alert" data-testid="workspace.review-over">
            {Icon.alert()}
            <span>
              {t(
                'Too long for one message. Send the comments of fewer files, or shorten the snippets; the rest stay pending.',
              )}
            </span>
          </p>
        ) : null}
        {error ? (
          <p className="ws-send-n bad" role="alert" data-testid="workspace.review-error">
            {Icon.alert()}
            <span>{error}</span>
          </p>
        ) : null}
      </div>
      <div className="dlg-f ws-send-f">
        <Button
          variant="quiet"
          icon={copied ? Icon.check() : Icon.copy()}
          disabled={!preview}
          onClick={() => {
            if (preview) void copyText(preview.text).then(setCopied);
          }}
          className="ws-send-copy"
          data-testid="workspace.review-copy"
        >
          {copied ? t('Copied') : t('Copy')}
        </Button>
        <Button onClick={props.onClose}>{t('Cancel')}</Button>
        {target.busy ? (
          <>
            {target.canSteer ? (
              <Button
                icon={Icon.steer()}
                disabled={!ready || target.blocked !== null}
                onClick={() => void send('steer')}
                data-testid="workspace.review-steer"
              >
                {t('Steer now')}
              </Button>
            ) : null}
            <Button
              variant="primary"
              icon={sending === 'queue' ? <Spinner /> : Icon.queue()}
              disabled={!ready || target.blocked !== null}
              onClick={() => void send('queue')}
              data-testid="workspace.review-queue"
            >
              {t('Queue')}
            </Button>
          </>
        ) : (
          <Button
            variant="primary"
            icon={sending ? <Spinner /> : Icon.send()}
            disabled={!ready || target.blocked !== null}
            onClick={() => void send('prompt')}
            data-testid="workspace.review-send"
          >
            {t('Send review')}
          </Button>
        )}
      </div>
    </Dialog>
  );
}
