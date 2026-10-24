import type {
  Annotation,
  AnnotationCreateRequest,
  ReviewDelivery,
  ReviewSendResult,
} from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  deleteAnnotation,
  fetchAnnotations,
  patchAnnotation,
  postAnnotation,
  postReview,
} from '../../lib/api.ts';
import { errorText } from '../../lib/errors.ts';
import { usePageVisible } from '../../lib/visibility.ts';

/** Another window or device may add or send comments: read them again this often while the page is visible. */
const POLL_MS = 10_000;

export type ReviewRequest = {
  ids: string[];
  note: string;
  delivery: ReviewDelivery;
};

/** Development fixtures: the comments to start with, and what a review of them would read. */
export type AnnotationsFixture = {
  list: Annotation[];
  /** The message the send dialog previews, in place of asking the core. */
  preview?: (list: readonly Annotation[], ids: string[], note: string) => string;
};

export type Annotations = {
  list: readonly Annotation[];
  /** Why the last change failed, until the next one. */
  error: string | null;
  add: (a: Omit<AnnotationCreateRequest, 'id'>) => Promise<boolean>;
  edit: (id: string, body: string) => Promise<boolean>;
  remove: (ids: readonly string[]) => Promise<void>;
  resolve: (id: string, resolved: boolean) => Promise<void>;
  /** The message a review would send, exactly; nothing changes. */
  preview: (r: ReviewRequest, signal?: AbortSignal) => Promise<ReviewSendResult>;
  /** Sends the review; the comments in it are marked sent. Throws what the core answered. */
  send: (r: ReviewRequest & { requestId: string }) => Promise<ReviewSendResult>;
};

/** The session's review comments on its diff (#84): read from the core, changed through it. */
export function useAnnotations(sessionId: string, fixture?: AnnotationsFixture): Annotations {
  const visible = usePageVisible();
  const [list, setList] = useState<readonly Annotation[]>(fixture?.list ?? []);
  const [error, setError] = useState<string | null>(null);
  const changing = useRef(0);

  useEffect(() => {
    if (fixture || !visible) return;
    const abort = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const next = await fetchAnnotations(sessionId, abort.signal);
        // A change in flight answers with the list after it; a read that started before it is stale.
        if (changing.current === 0) setList(next.annotations);
      } catch {
        // keep what is shown; the next read tries again
      }
      if (!abort.signal.aborted) timer = setTimeout(load, POLL_MS);
    };
    void load();
    return () => {
      abort.abort();
      clearTimeout(timer);
    };
  }, [fixture, sessionId, visible]);

  const change = useCallback(
    async (run: () => Promise<{ annotations: Annotation[] }>): Promise<boolean> => {
      changing.current++;
      try {
        setList((await run()).annotations);
        setError(null);
        return true;
      } catch (err) {
        setError(errorText(err));
        return false;
      } finally {
        changing.current--;
      }
    },
    [],
  );

  const local = useCallback((next: (l: readonly Annotation[]) => readonly Annotation[]) => {
    setList(next);
    return Promise.resolve(true);
  }, []);

  const add = useCallback(
    (a: Omit<AnnotationCreateRequest, 'id'>) => {
      const id = crypto.randomUUID();
      if (fixture) {
        const at = Date.now();
        return local((l) => [
          ...l,
          { ...a, id, state: 'pending', createdAt: at, updatedAt: at } as Annotation,
        ]);
      }
      return change(() => postAnnotation(sessionId, { ...a, id }));
    },
    [fixture, sessionId, change, local],
  );

  const edit = useCallback(
    (id: string, body: string) =>
      fixture
        ? local((l) => l.map((a) => (a.id === id ? { ...a, body } : a)))
        : change(() => patchAnnotation(sessionId, id, { body })),
    [fixture, sessionId, change, local],
  );

  const remove = useCallback(
    async (ids: readonly string[]) => {
      if (fixture) {
        await local((l) => l.filter((a) => !ids.includes(a.id)));
        return;
      }
      for (const id of ids) if (!(await change(() => deleteAnnotation(sessionId, id)))) return;
    },
    [fixture, sessionId, change, local],
  );

  const resolve = useCallback(
    async (id: string, resolved: boolean) => {
      const state = resolved ? 'resolved' : 'sent';
      if (fixture) await local((l) => l.map((a) => (a.id === id ? { ...a, state } : a)));
      else await change(() => patchAnnotation(sessionId, id, { state }));
    },
    [fixture, sessionId, change, local],
  );

  const preview = useCallback(
    async (r: ReviewRequest): Promise<ReviewSendResult> => {
      if (fixture) {
        const text = fixture.preview?.(list, r.ids, r.note) ?? '';
        return { text, chars: text.length, limit: 8000, review: 1, sent: false, annotations: [] };
      }
      return postReview(sessionId, { ...r, dryRun: true });
    },
    [fixture, sessionId, list],
  );

  const send = useCallback(
    async (r: ReviewRequest & { requestId: string }): Promise<ReviewSendResult> => {
      if (fixture) {
        const at = Date.now();
        const sent = { review: 1, at, delivery: r.delivery, messageId: r.requestId };
        setList((l) => l.map((a) => (r.ids.includes(a.id) ? { ...a, state: 'sent', sent } : a)));
        return { ...(await preview(r)), sent: true };
      }
      const result = await postReview(sessionId, { ...r, dryRun: false });
      setList(result.annotations);
      return result;
    },
    [fixture, sessionId, preview],
  );

  return { list, error, add, edit, remove, resolve, preview, send };
}
