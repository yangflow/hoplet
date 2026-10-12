import * as v from 'valibot';
import { SessionIdSchema } from './ids.ts';

/** The vendor a session belongs to (ADR 0012). Claude Code is the only provider implemented. */
export const ProviderSchema = v.picklist(['claude']);
export type Provider = v.InferOutput<typeof ProviderSchema>;

/**
 * Session index entry as stored. The sync cursor is `byteSize`, the server's received byte count: the
 * agent learns it from `welcome`, reads its local file from that offset and advances on `session.ack`.
 */
export const SessionMetaSchema = v.object({
  sessionId: SessionIdSchema,
  /** Absolute project path decoded from the session lines' cwd (best effort) */
  projectPath: v.string(),
  /** Raw directory name under the vendor's session store */
  projectDir: v.string(),
  deviceName: v.string(),
  /** Current writer: the only device whose appends are accepted. */
  ownerDeviceId: v.optional(v.string()),
  /** Increases with every handoff; an agent that held the session earlier compares it with its own. */
  ownerEpoch: v.number(),
  title: v.string(),
  provider: ProviderSchema,
  createdAt: v.number(),
  updatedAt: v.number(),
  lineCount: v.number(),
  byteSize: v.number(),
});
export type SessionMeta = v.InferOutput<typeof SessionMetaSchema>;
