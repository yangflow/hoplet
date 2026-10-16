import * as v from 'valibot';
import { OPAQUE_ID_PATTERN } from '../ids.ts';
import { PromptAttachmentsSchema } from '../runtime.ts';
import { queryInt, TrimmedSchema } from './common.ts';

/** A session holds at most this many queued messages (ADR 0020). */
export const MAX_QUEUE_ITEMS = 20;

/** The ID of a queued message, chosen by the client; a repeated one is the same message. */
export const QueueItemIdSchema = v.pipe(v.string(), v.regex(OPAQUE_ID_PATTERN));

/** `sending` is the head item on its way to the agent: it can no longer be edited, moved or removed. */
export const QueueItemStateSchema = v.picklist(['queued', 'sending']);
export type QueueItemState = v.InferOutput<typeof QueueItemStateSchema>;

/** Why the queue does not send: its turn failed, or the user interrupted it. Resuming or clearing ends it. */
export const QueuePauseSchema = v.picklist(['error', 'interrupted']);
export type QueuePause = v.InferOutput<typeof QueuePauseSchema>;

export const QueueItemSchema = v.object({
  id: QueueItemIdSchema,
  text: v.string(),
  attachments: PromptAttachmentsSchema,
  /** Increases with every change of the item; an edit names the version it read. */
  version: v.pipe(v.number(), v.integer(), v.minValue(1)),
  state: QueueItemStateSchema,
});
export type QueueItem = v.InferOutput<typeof QueueItemSchema>;

/** The queue of one session, head first; `paused` is absent while it sends. */
export const QueueSchema = v.object({
  items: v.array(QueueItemSchema),
  paused: v.optional(QueuePauseSchema),
  /** The diagnostic text of the failure that paused the queue with `error`. */
  error: v.optional(v.string()),
});
export type Queue = v.InferOutput<typeof QueueSchema>;

/** POST /api/sessions/:id/queue: appends a message; a repeated `id` changes nothing. */
export const QueueAddRequestSchema = v.object({
  id: QueueItemIdSchema,
  text: TrimmedSchema,
  attachments: v.optional(PromptAttachmentsSchema),
});
export type QueueAddRequest = v.InferOutput<typeof QueueAddRequestSchema>;

/** PATCH /api/sessions/:id/queue/:itemId: replaces the text and images of a message not yet sending. */
export const QueueEditRequestSchema = v.object({
  version: v.pipe(v.number(), v.integer(), v.minValue(1)),
  text: TrimmedSchema,
  attachments: v.optional(PromptAttachmentsSchema),
});
export type QueueEditRequest = v.InferOutput<typeof QueueEditRequestSchema>;

/** POST /api/sessions/:id/queue/:itemId/steer: sends the message to the running turn; names the version the client read. */
export const QueueSteerRequestSchema = v.object({
  version: v.pipe(v.number(), v.integer(), v.minValue(1)),
});
export type QueueSteerRequest = v.InferOutput<typeof QueueSteerRequestSchema>;

/** DELETE /api/sessions/:id/queue/:itemId?version=: the version the client read. */
export const QueueVersionQuerySchema = v.object({ version: queryInt(0, 0) });
export type QueueVersionQuery = v.InferOutput<typeof QueueVersionQuerySchema>;

/** POST /api/sessions/:id/queue/order: the IDs of the messages not yet sending, in the new order. */
export const QueueOrderRequestSchema = v.object({
  ids: v.pipe(v.array(QueueItemIdSchema), v.maxLength(MAX_QUEUE_ITEMS)),
});
export type QueueOrderRequest = v.InferOutput<typeof QueueOrderRequestSchema>;

/** POST /api/sessions/:id/steer: a message for the running turn; a repeated `steerId` is the same steer. */
export const SteerRequestSchema = v.object({
  steerId: v.pipe(v.string(), v.minLength(1), v.maxLength(128)),
  text: TrimmedSchema,
  attachments: v.optional(PromptAttachmentsSchema),
});
export type SteerRequest = v.InferOutput<typeof SteerRequestSchema>;

export const SteerResultSchema = v.object({ ok: v.literal(true), steerId: v.string() });
export type SteerResult = v.InferOutput<typeof SteerResultSchema>;
