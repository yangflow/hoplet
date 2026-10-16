import type { Queue, QueueItem } from '@hoplet/shared';
import {
  type KeyboardEvent,
  type PointerEvent,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { Badge } from '../../../components/Badge.tsx';
import { Button } from '../../../components/Button.tsx';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon, Spinner } from '../../../components/icons.tsx';
import {
  ApiError,
  deleteQueueItem,
  patchQueueItem,
  postQueueClear,
  postQueueOrder,
  postQueueResume,
  postQueueSteer,
} from '../../../lib/api.ts';
import { errorText } from '../../../lib/errors.ts';
import { t } from '../../../lib/i18n.ts';
import { inOrder, movableIds, moveId, slotAt, type TrayPause, trayPause } from './model.ts';

/** What development fixtures pin: the row being edited, the hovered one, the one being dragged. */
export type TrayFixture = { editing?: string; hovered?: string; dragging?: string };

function Header(props: {
  count: number;
  pause: TrayPause;
  busy: boolean;
  onResume: () => void;
  onClear: () => void;
}) {
  const { count, pause } = props;
  const num = (
    <span className="num" title={t('{n} queued').replace('{n}', String(count))}>
      {count}
    </span>
  );
  if (pause === null) {
    return (
      <div className="qh" data-testid="queue.head">
        <b>{t('Up next')}</b>
        {num}
        <span className="meta">
          <span>{t('One at a time, after this turn')}</span>
        </span>
      </div>
    );
  }
  const why =
    pause === 'permission'
      ? t('waiting for your answer to the permission request')
      : pause === 'error'
        ? t('the turn failed')
        : t('you interrupted the turn');
  return (
    <div className="qh" role="status" data-testid="queue.head" data-pause={pause}>
      {pause === 'permission' ? <span className="need-dot qdot" /> : null}
      {pause === 'error' ? <span className="bad-dot qdot" /> : null}
      <b>{t('Paused')}</b>
      <span>{why}</span>
      <span className="meta">
        {pause === 'permission' ? (
          num
        ) : (
          <>
            <Button
              icon={Icon.play()}
              disabled={props.busy}
              onClick={props.onResume}
              data-testid="queue.resume"
            >
              {t('Resume')}
            </Button>
            <Button
              variant="quiet"
              disabled={props.busy}
              onClick={props.onClear}
              data-testid="queue.clear"
            >
              {t('Clear')}
            </Button>
          </>
        )}
      </span>
    </div>
  );
}

function EditRow(props: {
  item: QueueItem;
  onSave: (text: string) => void;
  onCancel: () => void;
  busy: boolean;
}) {
  const [text, setText] = useState(props.item.text);
  const field = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    const el = field.current;
    el?.focus();
    el?.setSelectionRange(el.value.length, el.value.length);
  }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: the field grows with its text
  useLayoutEffect(() => {
    const el = field.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${el.scrollHeight}px`;
  }, [text]);
  const trimmed = text.trim();
  const save = () => {
    if (trimmed === '' || props.busy) return;
    if (trimmed === props.item.text) props.onCancel();
    else props.onSave(trimmed);
  };
  return (
    <div className="qr edit" data-testid="queue.edit">
      <textarea
        ref={field}
        className="qe-t"
        rows={1}
        value={text}
        aria-label={t('Edit queued message')}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.nativeEvent.isComposing) return;
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            save();
          } else if (e.key === 'Escape') {
            e.preventDefault();
            e.stopPropagation();
            props.onCancel();
          }
        }}
        data-testid="queue.edit-input"
      />
      <div className="qe-f">
        <span className="meta">
          <Badge kind="key">↩</Badge> {t('Save')} <Badge kind="key">Esc</Badge> {t('Cancel')}
        </span>
        <Button variant="quiet" onClick={props.onCancel} data-testid="queue.edit-cancel">
          {t('Cancel')}
        </Button>
        <Button
          variant="primary"
          disabled={trimmed === '' || props.busy}
          onClick={save}
          data-testid="queue.edit-save"
        >
          {t('Save')}
        </Button>
      </div>
    </div>
  );
}

/**
 * The queue of the session as a tray above the composer (ADR 0020): the messages that follow this turn, one at a
 * time. Rows are edited in place, deleted, sent into the running turn and reordered by their grip (pointer, or
 * Alt+Up / Alt+Down); the item being sent is locked. A paused tray says why, and an error or an interrupt waits
 * for Resume or Clear. Every change goes to the core and the answer is the new queue.
 */
export function QueueTray(props: {
  sessionId: string;
  queue: Queue;
  apply: (queue: Queue) => void;
  refresh: () => void;
  /** A permission request waits: the queue is paused until it is answered. */
  waitingPermission: boolean;
  /** The running turn can take a steer now. */
  canSteer: boolean;
  onError: (message: string | null) => void;
  fixture?: TrayFixture;
}) {
  const { queue, fixture } = props;
  const [editing, setEditing] = useState<string | null>(fixture?.editing ?? null);
  const [order, setOrder] = useState<string[] | null>(null);
  const [dragging, setDragging] = useState<string | null>(fixture?.dragging ?? null);
  const [busy, setBusy] = useState(false);
  const list = useRef<HTMLDivElement>(null);
  const orderRef = useRef<string[] | null>(null);

  // The item an editor shows has left the queue (sent, or removed from another window): the editor closes.
  useEffect(() => {
    if (editing !== null && !queue.items.some((i) => i.id === editing && i.state === 'queued')) {
      setEditing(null);
    }
  }, [queue, editing]);

  if (queue.items.length === 0) return null;
  const pause = trayPause(queue, props.waitingPermission);
  const items = order ? inOrder(queue.items, order) : queue.items;
  const firstQueued = items.findIndex((i) => i.state === 'queued');

  /** Runs one change; a stale one (the item was sent meanwhile) says so and reads the queue again. */
  async function run(change: Promise<Queue>): Promise<boolean> {
    setBusy(true);
    props.onError(null);
    try {
      props.apply(await change);
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.code === 'queue-conflict') {
        props.onError(t('That message was already sent, so it can no longer change.'));
      } else props.onError(errorText(err));
      props.refresh();
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function commitOrder(ids: string[]) {
    const same = ids.join() === movableIds(queue.items).join();
    if (!same) await run(postQueueOrder(props.sessionId, ids));
    setOrder(null);
    orderRef.current = null;
  }

  function dragFrom(e: PointerEvent<HTMLSpanElement>, id: string) {
    if (e.button !== 0 || busy) return;
    e.preventDefault();
    const grip = e.currentTarget;
    grip.setPointerCapture?.(e.pointerId);
    setDragging(id);
    const start = movableIds(queue.items);
    orderRef.current = start;
    setOrder(start);
    const move = (ev: globalThis.PointerEvent) => {
      const rows = [...(list.current?.querySelectorAll<HTMLElement>('[data-movable]') ?? [])];
      const middles = rows.map((r) => {
        const box = r.getBoundingClientRect();
        return box.top + box.height / 2;
      });
      const current = orderRef.current ?? start;
      const next = moveId(current, current.indexOf(id), slotAt(middles, ev.clientY));
      if (next.join() !== current.join()) {
        orderRef.current = next;
        setOrder(next);
      }
    };
    const up = () => {
      grip.removeEventListener('pointermove', move);
      grip.removeEventListener('pointerup', up);
      grip.removeEventListener('pointercancel', up);
      setDragging(null);
      void commitOrder(orderRef.current ?? start);
    };
    grip.addEventListener('pointermove', move);
    grip.addEventListener('pointerup', up);
    grip.addEventListener('pointercancel', up);
  }

  function gripKey(e: KeyboardEvent<HTMLSpanElement>, id: string) {
    if (!e.altKey || (e.key !== 'ArrowUp' && e.key !== 'ArrowDown')) return;
    e.preventDefault();
    const ids = movableIds(queue.items);
    const at = ids.indexOf(id);
    const next = moveId(ids, at, at + (e.key === 'ArrowUp' ? -1 : 1));
    if (next.join() !== ids.join()) void run(postQueueOrder(props.sessionId, next));
  }

  return (
    <section
      className={`qtray${pause === 'permission' ? ' need' : pause === 'error' ? ' bad' : ''}`}
      aria-label={t('Queued messages')}
      data-testid="queue.tray"
    >
      <Header
        count={queue.items.length}
        pause={pause}
        busy={busy}
        onResume={() => void run(postQueueResume(props.sessionId))}
        onClear={() => void run(postQueueClear(props.sessionId))}
      />
      <div ref={list}>
        {items.map((item, i) => {
          if (item.id === editing && item.state === 'queued') {
            return (
              <EditRow
                key={item.id}
                item={item}
                busy={busy}
                onCancel={() => setEditing(null)}
                onSave={(text) =>
                  void run(
                    patchQueueItem(props.sessionId, item.id, item.version, text, item.attachments),
                  ).then((ok) => ok && setEditing(null))
                }
              />
            );
          }
          const locked = item.state === 'sending';
          const cls = [
            'qr',
            locked && 'lock',
            dragging === item.id && 'drag',
            fixture?.hovered === item.id && 'hov',
            pause !== null && 'dim',
          ]
            .filter(Boolean)
            .join(' ');
          return (
            <div
              key={item.id}
              className={cls}
              {...(locked ? { 'aria-disabled': true } : { 'data-movable': '' })}
              data-testid="queue.item"
              data-state={item.state}
            >
              {/* biome-ignore lint/a11y/useSemanticElements: a focusable handle that moves its row; a button would not take Alt+arrows as a drag */}
              <span
                className="gr"
                role="button"
                tabIndex={locked ? -1 : 0}
                aria-label={t('Move message {n}').replace('{n}', String(i + 1))}
                title={t('Drag to reorder · Alt ↑ ↓')}
                onPointerDown={locked ? undefined : (e) => dragFrom(e, item.id)}
                onKeyDown={locked ? undefined : (e) => gripKey(e, item.id)}
              >
                {Icon.grip()}
              </span>
              <span className="n">{i + 1}</span>
              <span className="t" title={item.text}>
                {item.text}
                {i === firstQueued ? <span className="tag">{t('Next')}</span> : null}
              </span>
              {locked ? (
                <span className="ac" title={t('Already handed to the agent')}>
                  <Spinner />
                  <span>{t('Sending')}</span>
                </span>
              ) : (
                <span className="ac">
                  <IconButton
                    label={t('Edit')}
                    icon={Icon.pen()}
                    onClick={() => setEditing(item.id)}
                    data-testid="queue.edit-item"
                  />
                  {props.canSteer && pause === null ? (
                    <IconButton
                      label={t('Steer now')}
                      detail={t('Steer now · add to the running turn')}
                      icon={Icon.steer()}
                      disabled={busy}
                      onClick={() =>
                        void run(postQueueSteer(props.sessionId, item.id, item.version))
                      }
                      data-testid="queue.steer-item"
                    />
                  ) : null}
                  <IconButton
                    label={t('Delete')}
                    icon={Icon.trash()}
                    disabled={busy}
                    onClick={() =>
                      void run(deleteQueueItem(props.sessionId, item.id, item.version))
                    }
                    data-testid="queue.delete-item"
                  />
                </span>
              )}
            </div>
          );
        })}
      </div>
    </section>
  );
}
