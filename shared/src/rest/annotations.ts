import * as v from 'valibot';
import { OPAQUE_ID_PATTERN } from '../ids.ts';
import { TrimmedSchema } from './common.ts';

// Review comments on the lines of a session's diff (#84): kept by the core, sent to the session as one message.

/** A session holds at most this many comments; resolved ones count until they are deleted. */
export const MAX_ANNOTATIONS = 200;
/** The longest comment, and the longest snippet of the lines it is about. */
export const MAX_ANNOTATION_CHARS = 4000;
export const MAX_ANNOTATION_SNIPPET_CHARS = 4000;
/** The longest note that goes before the comments of a review. */
export const MAX_REVIEW_NOTE_CHARS = 2000;

/** The ID of a comment, chosen by the client; a repeated one is the same comment. */
export const AnnotationIdSchema = v.pipe(v.string(), v.regex(OPAQUE_ID_PATTERN));

/** Which side of the diff the lines are numbered on: `old` is the file at `HEAD`, `new` the working tree. */
export const AnnotationSideSchema = v.picklist(['old', 'new']);
export type AnnotationSide = v.InferOutput<typeof AnnotationSideSchema>;

/** `pending` is not sent yet; `sent` went to the session in a review; `resolved` is a sent comment set aside. */
export const AnnotationStateSchema = v.picklist(['pending', 'sent', 'resolved']);
export type AnnotationState = v.InferOutput<typeof AnnotationStateSchema>;

/** How a review reached the session: as the next prompt, queued behind the running turn, or steered into it. */
export const ReviewDeliverySchema = v.picklist(['prompt', 'queue', 'steer']);
export type ReviewDelivery = v.InferOutput<typeof ReviewDeliverySchema>;

/** The review a comment went out in: its number within the session, when, how, and the message's ID. */
export const AnnotationSentSchema = v.object({
  review: v.pipe(v.number(), v.integer(), v.minValue(1)),
  at: v.number(),
  delivery: ReviewDeliverySchema,
  messageId: v.string(),
});
export type AnnotationSent = v.InferOutput<typeof AnnotationSentSchema>;

/**
 * One comment: the path, the side and the line range it is anchored to, the text of those lines when it was
 * written (`snippet`, which re-anchors it once the lines move), and what the user wrote.
 */
export const AnnotationSchema = v.object({
  id: AnnotationIdSchema,
  path: v.string(),
  side: AnnotationSideSchema,
  startLine: v.pipe(v.number(), v.integer(), v.minValue(1)),
  endLine: v.pipe(v.number(), v.integer(), v.minValue(1)),
  snippet: v.string(),
  body: v.string(),
  state: AnnotationStateSchema,
  createdAt: v.number(),
  updatedAt: v.number(),
  sent: v.optional(AnnotationSentSchema),
});
export type Annotation = v.InferOutput<typeof AnnotationSchema>;

/** GET /api/sessions/:id/annotations: every comment of the session, oldest first. */
export const AnnotationListSchema = v.object({ annotations: v.array(AnnotationSchema) });
export type AnnotationList = v.InferOutput<typeof AnnotationListSchema>;

const LineSchema = v.pipe(v.number(), v.integer(), v.minValue(1), v.maxValue(10_000_000));

/** POST /api/sessions/:id/annotations: adds a pending comment; a repeated `id` returns the stored one. */
export const AnnotationCreateRequestSchema = v.pipe(
  v.object({
    id: AnnotationIdSchema,
    path: v.pipe(TrimmedSchema, v.maxLength(4096)),
    side: AnnotationSideSchema,
    startLine: LineSchema,
    endLine: LineSchema,
    snippet: v.pipe(v.string(), v.maxLength(MAX_ANNOTATION_SNIPPET_CHARS)),
    body: v.pipe(TrimmedSchema, v.maxLength(MAX_ANNOTATION_CHARS)),
  }),
  v.check((r) => r.endLine >= r.startLine, 'endLine before startLine'),
);
export type AnnotationCreateRequest = v.InferOutput<typeof AnnotationCreateRequestSchema>;

/**
 * PATCH /api/sessions/:id/annotations/:annotationId: `body` edits a pending comment; `state` resolves a sent
 * one (`resolved`) or reopens a resolved one (`sent`).
 */
export const AnnotationPatchRequestSchema = v.pipe(
  v.object({
    body: v.optional(v.pipe(TrimmedSchema, v.maxLength(MAX_ANNOTATION_CHARS))),
    state: v.optional(v.picklist(['sent', 'resolved'])),
  }),
  v.check((r) => (r.body === undefined) !== (r.state === undefined), 'one of body or state'),
);
export type AnnotationPatchRequest = v.InferOutput<typeof AnnotationPatchRequestSchema>;

/**
 * POST /api/sessions/:id/annotations/send: composes the pending comments in `ids` (in the stored order) and an
 * optional note into one message. `dryRun` only returns the message; otherwise it goes to the session as a
 * prompt, a queued message or a steer, with `requestId` as its ID, and the comments become `sent`.
 */
export const ReviewSendRequestSchema = v.object({
  ids: v.pipe(v.array(AnnotationIdSchema), v.minLength(1), v.maxLength(MAX_ANNOTATIONS)),
  note: v.optional(v.pipe(v.string(), v.maxLength(MAX_REVIEW_NOTE_CHARS))),
  delivery: ReviewDeliverySchema,
  dryRun: v.optional(v.boolean()),
  requestId: v.pipe(v.string(), v.regex(OPAQUE_ID_PATTERN)),
});
export type ReviewSendRequest = v.InferOutput<typeof ReviewSendRequestSchema>;

/**
 * The message exactly as it goes (or went) to the session, its length against the 8,000-character limit of a
 * prompt, the review's number, and the comments as they are now (unchanged by a dry run).
 */
export const ReviewSendResultSchema = v.object({
  text: v.string(),
  chars: v.number(),
  limit: v.number(),
  review: v.number(),
  sent: v.boolean(),
  annotations: v.array(AnnotationSchema),
});
export type ReviewSendResult = v.InferOutput<typeof ReviewSendResultSchema>;
