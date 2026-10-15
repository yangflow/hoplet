/**
 * WebSocket messages between the core, the agents and the browsers. Every message carries `v`
 * (`PROTOCOL_VERSION`) and a `type`, belongs to one lane, and, unless it is core-to-web, is mirrored in
 * `agent/internal/sync/protocol.go`; `scripts/test/contract.test.ts` compares both.
 */
import * as v from 'valibot';
import { DeviceProjectSchema, DeviceProjectSessionSchema, DirEntrySchema } from '../catalog.ts';
import { ToolKindSchema, ToolMcpSchema } from '../conversation.ts';
import { ErrorCodeSchema } from '../errors.ts';
import { AttachmentIdSchema, HandoffIdSchema, SessionIdSchema, SnapshotIdSchema } from '../ids.ts';
import { ChangeStatusSchema, WorkspaceChangeSchema } from '../rest/workspace.ts';
import {
  AgentRuntimeStateSchema,
  PermissionDecisionSchema,
  PROMPT_IMAGE_TYPES,
  ProviderInfoSchema,
  RuntimeCapabilitiesSchema,
  RuntimeOutputKindSchema,
  RuntimeStateSchema,
  StartOptionsSchema,
} from '../runtime.ts';
import { ProviderSchema, SessionMetaSchema } from '../session.ts';
import { type Direction, defineWire, type Lane, list } from './define.ts';

export type { Direction, Lane, WireMessage } from './define.ts';
export { defineWire } from './define.ts';

export const EncodingSchema = v.picklist(['plain', 'gzip+base64']);
export type Encoding = v.InferOutput<typeof EncodingSchema>;

/** How the bytes of a handoff chunk are carried: base64, or base64 of their gzip. */
export const ChunkEncodingSchema = v.picklist(['base64', 'gzip+base64']);
export type ChunkEncoding = v.InferOutput<typeof ChunkEncodingSchema>;

/** The parts of a handoff transfer: the stored session stream and the workspace snapshot archive. */
export const HandoffPartSchema = v.picklist(['snapshot', 'session']);
export type HandoffPart = v.InferOutput<typeof HandoffPartSchema>;

/** The steps a handoff reports to browsers. */
export const HandoffStepSchema = v.picklist([
  'prepare',
  'transfer',
  'install',
  'verify',
  'commit',
  'done',
  'failed',
]);
export type HandoffStep = v.InferOutput<typeof HandoffStepSchema>;

/** The steps of a handoff an agent can fail. */
export const HandoffAgentStepSchema = v.picklist(['prepare', 'install', 'verify']);
export type HandoffAgentStep = v.InferOutput<typeof HandoffAgentStepSchema>;

/** A workspace snapshot travels in chunks of at most this many bytes (docs/architecture.md#workspace-snapshots). */
export const SNAPSHOT_CHUNK_BYTES = 1024 * 1024;
/** The largest snapshot archive the core stores: 64 MiB compressed (docs/hosting.md#resources). */
export const SNAPSHOT_MAX_BYTES = 64 * 1024 * 1024;
/** Base64 characters that carry `SNAPSHOT_CHUNK_BYTES`. */
export const SNAPSHOT_CHUNK_CHARS = Math.ceil(SNAPSHOT_CHUNK_BYTES / 3) * 4;

/** The largest image a workspace file read returns whole: 4 MiB, and the base64 characters that carry it. */
export const WORKSPACE_IMAGE_BYTES = 4 * 1024 * 1024;
export const WORKSPACE_IMAGE_CHARS = Math.ceil(WORKSPACE_IMAGE_BYTES / 3) * 4;

/** A handoff chunk is gzip and base64 of at most `SNAPSHOT_CHUNK_BYTES` bytes; this bounds its characters. */
export const HANDOFF_CHUNK_CHARS = SNAPSHOT_CHUNK_CHARS + 4096;

const sessionId = SessionIdSchema;
const handoffId = HandoffIdSchema;
const requestId = v.string();
const cursor = v.pipe(v.number(), v.integer(), v.minValue(0));

export const WIRE_MESSAGES = defineWire({
  // Session and connection
  'device.hello': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      deviceId: v.string(),
      name: v.string(),
      version: v.string(),
      /** The providers this agent runs, with the capabilities of their runtime drivers. */
      providers: v.optional(v.array(ProviderInfoSchema)),
    },
  },
  welcome: {
    lane: 'control',
    direction: 'core-to-device',
    fields: { serverTime: v.number(), sessions: v.array(SessionMetaSchema) },
  },
  ping: { lane: 'control', direction: 'device-to-core', fields: { t: v.optional(v.number()) } },
  pong: { lane: 'control', direction: 'core-to-device', fields: { t: v.optional(v.number()) } },
  error: {
    lane: 'control',
    direction: 'core-to-device',
    fields: { code: ErrorCodeSchema, message: v.string(), sessionId: v.optional(v.string()) },
  },

  // Sync: raw JSONL lines from `byteStart` (\n-joined; base64 of gzip when `encoding` is gzip+base64)
  'session.append': {
    lane: 'bulk',
    direction: 'device-to-core',
    fields: {
      sessionId,
      projectDir: v.string(),
      filePath: v.string(),
      byteStart: cursor,
      encoding: EncodingSchema,
      lines: v.string(),
      /**
       * Live one-line title from the agent. Updates the store index only; never rewrites chunk bytes.
       * May arrive with an empty batch so already-synced sessions catch up after a title change.
       */
      title: v.optional(v.string()),
    },
  },
  'session.ack': {
    lane: 'bulk',
    direction: 'core-to-device',
    fields: { sessionId, byteSize: v.number(), lineCount: v.number() },
  },
  /** The server cursor does not match `byteStart`; the agent resends from `byteSize`. */
  'session.resync': {
    lane: 'bulk',
    direction: 'core-to-device',
    fields: { sessionId, byteSize: v.number() },
  },
  /** One writer per session: appends from a non-owner device are rejected. */
  'session.rejected': {
    lane: 'bulk',
    direction: 'core-to-device',
    fields: { sessionId, reason: v.string(), ownerDeviceId: v.optional(v.string()) },
  },
  /** Broadcast to every authenticated connection; agents ignore it. */
  'session.appended': {
    lane: 'control',
    direction: 'core-to-web',
    fields: {
      sessionId,
      appendedLines: v.number(),
      totalLines: v.number(),
      byteSize: v.number(),
      updatedAt: v.number(),
      isNew: v.optional(v.boolean()),
    },
  },

  /**
   * One chunk of the workspace snapshot of a session, uploaded after every completed turn. Chunks of one
   * snapshot arrive in order (`seq` from 0) and the last has `last` set; `data` is base64 of at most
   * `SNAPSHOT_CHUNK_BYTES` bytes of the archive. The metadata repeats in every chunk and must not change.
   */
  'workspace.snapshot': {
    lane: 'bulk',
    direction: 'device-to-core',
    fields: {
      sessionId,
      snapshotId: SnapshotIdSchema,
      createdAt: cursor,
      baseCommit: v.pipe(v.string(), v.maxLength(64)),
      originUrl: v.pipe(v.string(), v.maxLength(2048)),
      treeHash: v.pipe(v.string(), v.maxLength(64)),
      byteSize: cursor,
      sha256: v.pipe(v.string(), v.maxLength(64)),
      seq: cursor,
      last: v.boolean(),
      data: v.pipe(v.string(), v.maxLength(SNAPSHOT_CHUNK_CHARS)),
    },
  },

  // Remote control (ADR 0008). Prompts, answers and runtime events are control work.
  'runtime.prompt': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      sessionId,
      promptId: v.string(),
      text: v.string(),
      attachments: v.optional(list(AttachmentIdSchema)),
    },
  },
  'runtime.permission.answer': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { sessionId, requestId: v.string(), decision: PermissionDecisionSchema },
  },
  /**
   * Starts a new session in `projectPath`, a watched project of the device: the agent starts the provider's
   * process there with `options`, writes the prompt, imports the session the process creates and answers
   * with `runtime.started`, or with `runtime.start.rejected`.
   */
  'runtime.start': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      requestId,
      projectPath: v.string(),
      provider: ProviderSchema,
      options: StartOptionsSchema,
      text: v.string(),
      attachments: v.optional(list(AttachmentIdSchema)),
      promptId: v.string(),
    },
  },
  /** The session the agent started is imported and syncs from now on. */
  'runtime.started': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, sessionId },
  },
  'runtime.start.rejected': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, reason: v.string() },
  },
  'runtime.interrupt': { lane: 'control', direction: 'core-to-device', fields: { sessionId } },
  'runtime.stop': { lane: 'control', direction: 'core-to-device', fields: { sessionId } },
  /** The state of a session's process, with what its runtime driver can do. `error` accompanies `failed`. */
  'runtime.state': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      sessionId,
      state: AgentRuntimeStateSchema,
      error: v.optional(v.string()),
      capabilities: RuntimeCapabilitiesSchema,
    },
  },
  /** Streamed content of the running turn; transient, the session file is the record. */
  'runtime.output': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      sessionId,
      kind: RuntimeOutputKindSchema,
      text: v.optional(v.string()),
      tokens: v.optional(v.number()),
      tool: v.optional(v.string()),
      toolUseId: v.optional(v.string()),
      input: v.optional(v.unknown()),
      isError: v.optional(v.boolean()),
      truncated: v.optional(v.boolean()),
    },
  },
  /** The process asks to use a tool; answered with `runtime.permission.answer`. */
  'runtime.permission': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      sessionId,
      requestId: v.string(),
      tool: v.string(),
      toolUseId: v.optional(v.string()),
      input: v.unknown(),
    },
  },
  /** A prompt the agent refused; `reason` is `busy`, `starting`, `external` or diagnostic text. */
  'runtime.rejected': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { sessionId, promptId: v.string(), reason: v.string() },
  },

  // Browser WebSocket: subscriptions and the runtime events of the core
  /** A device connected or disconnected; sessions it owns change between `offline` and what it reports. */
  'device.presence': {
    lane: 'control',
    direction: 'core-to-web',
    fields: { deviceId: v.string(), connected: v.boolean() },
  },
  subscribe: { lane: 'control', direction: 'web-to-core', fields: { sessionId } },
  unsubscribe: { lane: 'control', direction: 'web-to-core', fields: { sessionId } },
  'session.runtime': {
    lane: 'control',
    direction: 'core-to-web',
    fields: {
      sessionId,
      state: RuntimeStateSchema,
      error: v.optional(v.string()),
      capabilities: v.optional(RuntimeCapabilitiesSchema),
    },
  },
  /**
   * Delivered only to browsers subscribed to the session. A `tool-call` carries the kind of its tool (and
   * the MCP server and tool) as the session provider's mapping classifies it.
   */
  'session.output': {
    lane: 'control',
    direction: 'core-to-web',
    fields: {
      sessionId,
      kind: RuntimeOutputKindSchema,
      text: v.optional(v.string()),
      tokens: v.optional(v.number()),
      tool: v.optional(v.string()),
      toolKind: v.optional(ToolKindSchema),
      mcp: v.optional(ToolMcpSchema),
      toolUseId: v.optional(v.string()),
      input: v.optional(v.unknown()),
      isError: v.optional(v.boolean()),
      truncated: v.optional(v.boolean()),
    },
  },
  /** A permission request; `toolKind` and `mcp` classify its tool as on `session.output`. */
  'session.permission': {
    lane: 'control',
    direction: 'core-to-web',
    fields: {
      sessionId,
      requestId: v.string(),
      tool: v.string(),
      toolKind: v.optional(ToolKindSchema),
      mcp: v.optional(ToolMcpSchema),
      toolUseId: v.optional(v.string()),
      input: v.unknown(),
    },
  },

  // Workspace listing, file and diff reads (docs/spec.md#workspace). Requests and the listing are control;
  // the answers that carry file content or a diff are bulk, so a large file never delays a prompt.
  'session.workspace.list': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { sessionId, requestId },
  },
  /**
   * `git` is false for a directory without a commit; it then has no paths and no changes. `origin` is the URL
   * of the repository's remote `origin`, empty without one.
   */
  'session.workspace': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      sessionId,
      requestId,
      git: v.boolean(),
      commit: v.string(),
      origin: v.string(),
      paths: list(v.string()),
      truncated: v.optional(v.boolean()),
      changes: list(WorkspaceChangeSchema),
    },
  },
  /** Answers any workspace request of the same `requestId` that fails. */
  'session.workspace.rejected': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { sessionId, requestId, reason: v.string() },
  },
  /** `at` is `worktree` when absent; `head` reads the file as `HEAD` has it. */
  'session.workspace.file.read': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      sessionId,
      requestId,
      path: v.string(),
      at: v.optional(v.picklist(['worktree', 'head'])),
    },
  },
  /**
   * One file: `text` (at most 256 KiB, `truncated` beyond) for `kind` text, `mediaType` and the whole file as
   * base64 `data` (at most 4 MiB before encoding) for `kind` image, only `size` for `kind` binary.
   */
  'session.workspace.file': {
    lane: 'bulk',
    direction: 'device-to-core',
    fields: {
      sessionId,
      requestId,
      path: v.string(),
      kind: v.picklist(['text', 'image', 'binary']),
      size: cursor,
      text: v.optional(v.string()),
      truncated: v.optional(v.boolean()),
      mediaType: v.optional(
        v.picklist(['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/svg+xml']),
      ),
      data: v.optional(v.pipe(v.string(), v.maxLength(WORKSPACE_IMAGE_CHARS))),
    },
  },
  'session.workspace.diff.read': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { sessionId, requestId, path: v.string() },
  },
  /**
   * One changed path's diff against `HEAD`: for `kind` text the hunks (`diff`, at most 256 KiB, `truncated`
   * beyond) and the full `size`; for `kind` binary the sizes of the sides that exist.
   */
  'session.workspace.diff': {
    lane: 'bulk',
    direction: 'device-to-core',
    fields: {
      sessionId,
      requestId,
      path: v.string(),
      status: ChangeStatusSchema,
      kind: v.picklist(['text', 'binary']),
      diff: v.optional(v.string()),
      truncated: v.optional(v.boolean()),
      size: v.optional(cursor),
      oldSize: v.optional(cursor),
      newSize: v.optional(cursor),
    },
  },

  // Handoff (docs/spec.md#handoff): the data transfer is bulk; every step and answer is control
  'handoff.prepare': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { sessionId, handoffId },
  },
  /** The owner stopped the session's process, took a fresh snapshot and sent it; `byteSize` ends the session file. */
  'handoff.prepared': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { sessionId, handoffId, snapshotId: SnapshotIdSchema, byteSize: cursor },
  },
  /**
   * Starts an installation on the target. The snapshot archive and the session stream follow as
   * `handoff.data` chunks; the sizes and checksums are what the target holds the received bytes to.
   */
  'handoff.install': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      sessionId,
      handoffId,
      targetPath: v.optional(v.pipe(v.string(), v.maxLength(4096))),
      snapshotBytes: cursor,
      snapshotSha256: v.pipe(v.string(), v.maxLength(64)),
      sessionBytes: cursor,
      sessionSha256: v.pipe(v.string(), v.maxLength(64)),
    },
  },
  'handoff.data': {
    lane: 'bulk',
    direction: 'core-to-device',
    fields: {
      sessionId,
      handoffId,
      part: HandoffPartSchema,
      seq: cursor,
      last: v.boolean(),
      encoding: ChunkEncodingSchema,
      data: v.pipe(v.string(), v.maxLength(HANDOFF_CHUNK_CHARS)),
    },
  },
  /** The target installed the worktree, the session file and the project entry; it names the worktree and branch. */
  'handoff.installed': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { sessionId, handoffId, worktree: v.string(), branch: v.string() },
  },
  'handoff.verify': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { sessionId, handoffId },
  },
  /** What the target holds: the size and SHA-256 of the installed session file and the tree hash of the worktree. */
  'handoff.verified': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      sessionId,
      handoffId,
      sessionBytes: cursor,
      sessionSha256: v.pipe(v.string(), v.maxLength(64)),
      treeHash: v.pipe(v.string(), v.maxLength(64)),
    },
  },
  /** A step an agent could not do; whatever it had installed is already removed. */
  'handoff.failed': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { sessionId, handoffId, step: HandoffAgentStepSchema, reason: v.string() },
  },
  /**
   * Ownership moved: `ownerDeviceId` is the owner from now on, `ownerEpoch` its epoch and `byteSize` the
   * cursor. The new owner resumes synchronization there; the old owner stops controlling the session.
   */
  'handoff.commit': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      sessionId,
      handoffId,
      ownerDeviceId: v.string(),
      ownerEpoch: cursor,
      byteSize: cursor,
    },
  },
  /** The handoff failed before the commit: the target removes what it installed. */
  'handoff.abort': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { sessionId, handoffId, reason: v.string() },
  },
  /**
   * Progress of a handoff for browsers. `snapshotAt` is the time of the snapshot an offline handoff uses;
   * during `transfer`, `sent` of `total` bytes have gone to the target; `done` names the new worktree and branch.
   */
  'handoff.progress': {
    lane: 'control',
    direction: 'core-to-web',
    fields: {
      sessionId,
      handoffId,
      step: HandoffStepSchema,
      targetDeviceId: v.string(),
      snapshotAt: v.optional(v.number()),
      sent: v.optional(v.number()),
      total: v.optional(v.number()),
      worktree: v.optional(v.string()),
      branch: v.optional(v.string()),
      reason: v.optional(v.string()),
    },
  },

  // Device projects and imported sessions
  'device.projects.list': { lane: 'control', direction: 'core-to-device', fields: { requestId } },
  'device.projects.add': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { requestId, path: v.string() },
  },
  'device.projects.remove': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { requestId, path: v.string() },
  },
  'device.sessions.import': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { requestId, path: v.string(), sessionIds: v.array(sessionId) },
  },
  'device.sessions.remove': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { requestId, path: v.string(), sessionIds: v.array(sessionId) },
  },
  'device.projects': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, candidates: list(v.string()), projects: list(DeviceProjectSchema) },
  },
  'device.projects.rejected': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, reason: v.string() },
  },

  // Directory browsing under the agent's browse root
  /**
   * An image for a coming prompt (ADR 0017), in chunks of at most `SNAPSHOT_CHUNK_BYTES` like a snapshot: `seq`
   * counts from 0, `last` marks the final chunk, after which the device answers once.
   */
  'attachment.put': {
    lane: 'bulk',
    direction: 'core-to-device',
    fields: {
      requestId,
      attachmentId: AttachmentIdSchema,
      mediaType: v.picklist(PROMPT_IMAGE_TYPES),
      seq: cursor,
      last: v.boolean(),
      data: v.pipe(v.string(), v.maxLength(SNAPSHOT_CHUNK_CHARS)),
    },
  },
  'attachment.stored': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, attachmentId: AttachmentIdSchema },
  },
  'attachment.rejected': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, reason: v.string() },
  },

  'device.dirs.list': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { requestId, path: v.string() },
  },
  'device.dirs': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      requestId,
      path: v.string(),
      parent: v.nullable(v.string()),
      root: v.string(),
      entries: list(DirEntrySchema),
      truncated: v.boolean(),
    },
  },
  'device.dirs.rejected': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, reason: v.string() },
  },

  // The clone a handoff would use: a watched project whose remote `origin` is `origin`
  'device.clone.find': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { requestId, origin: v.pipe(v.string(), v.maxLength(2048)) },
  },
  /** `path` is the clone's main working tree, null when no watched project has that `origin`. */
  'device.clone': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, path: v.nullable(v.string()) },
  },
});

type Table = typeof WIRE_MESSAGES;
export type WireType = keyof Table;
export type WireMessageOf<K extends WireType> = Table[K] extends {
  schema: infer S extends v.GenericSchema;
}
  ? v.InferOutput<S>
  : never;
type InDirection<D extends string> = {
  [K in WireType]: Table[K]['direction'] extends D ? WireMessageOf<K> : never;
}[WireType];

/** device → core */
export type ClientMessage = InDirection<'device-to-core'>;
/** browser → core */
export type BrowserMessage = InDirection<'web-to-core'>;
/** core → device or browser */
export type ServerMessage = InDirection<'core-to-device' | 'core-to-web'>;

export type DeviceHello = WireMessageOf<'device.hello'>;
export type Welcome = WireMessageOf<'welcome'>;
export type PingMsg = WireMessageOf<'ping'>;
export type PongMsg = WireMessageOf<'pong'>;
export type ErrorMsg = WireMessageOf<'error'>;
export type SessionAppend = WireMessageOf<'session.append'>;
export type SessionAck = WireMessageOf<'session.ack'>;
export type SessionResync = WireMessageOf<'session.resync'>;
export type SessionRejected = WireMessageOf<'session.rejected'>;
export type WorkspaceSnapshot = WireMessageOf<'workspace.snapshot'>;
export type SessionAppended = WireMessageOf<'session.appended'>;
export type RuntimePrompt = WireMessageOf<'runtime.prompt'>;
export type RuntimePermissionAnswer = WireMessageOf<'runtime.permission.answer'>;
export type RuntimeStart = WireMessageOf<'runtime.start'>;
export type RuntimeStarted = WireMessageOf<'runtime.started'>;
export type RuntimeStartRejected = WireMessageOf<'runtime.start.rejected'>;
export type RuntimeInterrupt = WireMessageOf<'runtime.interrupt'>;
export type RuntimeStop = WireMessageOf<'runtime.stop'>;
export type RuntimeStateMsg = WireMessageOf<'runtime.state'>;
export type RuntimeOutputMsg = WireMessageOf<'runtime.output'>;
export type RuntimePermissionMsg = WireMessageOf<'runtime.permission'>;
export type RuntimeRejected = WireMessageOf<'runtime.rejected'>;
export type SubscribeMsg = WireMessageOf<'subscribe'>;
export type UnsubscribeMsg = WireMessageOf<'unsubscribe'>;
export type DevicePresence = WireMessageOf<'device.presence'>;
export type SessionRuntime = WireMessageOf<'session.runtime'>;
export type SessionOutput = WireMessageOf<'session.output'>;
export type SessionPermission = WireMessageOf<'session.permission'>;
export type SessionWorkspaceList = WireMessageOf<'session.workspace.list'>;
export type SessionWorkspace = WireMessageOf<'session.workspace'>;
export type SessionWorkspaceRejected = WireMessageOf<'session.workspace.rejected'>;
export type SessionWorkspaceFileRead = WireMessageOf<'session.workspace.file.read'>;
export type SessionWorkspaceFile = WireMessageOf<'session.workspace.file'>;
export type SessionWorkspaceDiffRead = WireMessageOf<'session.workspace.diff.read'>;
export type SessionWorkspaceDiff = WireMessageOf<'session.workspace.diff'>;
export type HandoffPrepare = WireMessageOf<'handoff.prepare'>;
export type HandoffPrepared = WireMessageOf<'handoff.prepared'>;
export type HandoffInstall = WireMessageOf<'handoff.install'>;
export type HandoffData = WireMessageOf<'handoff.data'>;
export type HandoffInstalled = WireMessageOf<'handoff.installed'>;
export type HandoffVerify = WireMessageOf<'handoff.verify'>;
export type HandoffVerified = WireMessageOf<'handoff.verified'>;
export type HandoffFailed = WireMessageOf<'handoff.failed'>;
export type HandoffCommit = WireMessageOf<'handoff.commit'>;
export type HandoffAbort = WireMessageOf<'handoff.abort'>;
export type HandoffProgress = WireMessageOf<'handoff.progress'>;
export type DeviceProjectsList = WireMessageOf<'device.projects.list'>;
export type DeviceProjectsAdd = WireMessageOf<'device.projects.add'>;
export type DeviceProjectsRemove = WireMessageOf<'device.projects.remove'>;
export type DeviceSessionsImport = WireMessageOf<'device.sessions.import'>;
export type DeviceSessionsRemove = WireMessageOf<'device.sessions.remove'>;
export type DeviceProjects = WireMessageOf<'device.projects'>;
export type DeviceProjectsRejected = WireMessageOf<'device.projects.rejected'>;
export type AttachmentPut = WireMessageOf<'attachment.put'>;
export type AttachmentStored = WireMessageOf<'attachment.stored'>;
export type AttachmentRejected = WireMessageOf<'attachment.rejected'>;
export type DeviceDirsList = WireMessageOf<'device.dirs.list'>;
export type DeviceDirs = WireMessageOf<'device.dirs'>;
export type DeviceDirsRejected = WireMessageOf<'device.dirs.rejected'>;
export type DeviceCloneFind = WireMessageOf<'device.clone.find'>;
export type DeviceClone = WireMessageOf<'device.clone'>;

/** Types of the messages in `direction`, in table order. */
export function wireTypes(...directions: Direction[]): WireType[] {
  return (Object.keys(WIRE_MESSAGES) as WireType[]).filter((type) =>
    directions.includes(WIRE_MESSAGES[type].direction),
  );
}

export function laneOf(type: WireType): Lane {
  return WIRE_MESSAGES[type].lane;
}

/** The messages an agent sends, as one schema discriminated by `type`. */
export const ClientMessageSchema = v.variant('type', [
  WIRE_MESSAGES['device.hello'].schema,
  WIRE_MESSAGES.ping.schema,
  WIRE_MESSAGES['session.append'].schema,
  WIRE_MESSAGES['workspace.snapshot'].schema,
  WIRE_MESSAGES['runtime.state'].schema,
  WIRE_MESSAGES['runtime.output'].schema,
  WIRE_MESSAGES['runtime.permission'].schema,
  WIRE_MESSAGES['runtime.rejected'].schema,
  WIRE_MESSAGES['runtime.started'].schema,
  WIRE_MESSAGES['runtime.start.rejected'].schema,
  WIRE_MESSAGES['session.workspace'].schema,
  WIRE_MESSAGES['session.workspace.rejected'].schema,
  WIRE_MESSAGES['session.workspace.file'].schema,
  WIRE_MESSAGES['session.workspace.diff'].schema,
  WIRE_MESSAGES['handoff.prepared'].schema,
  WIRE_MESSAGES['handoff.installed'].schema,
  WIRE_MESSAGES['handoff.verified'].schema,
  WIRE_MESSAGES['handoff.failed'].schema,
  WIRE_MESSAGES['device.projects'].schema,
  WIRE_MESSAGES['device.projects.rejected'].schema,
  WIRE_MESSAGES['device.dirs'].schema,
  WIRE_MESSAGES['attachment.stored'].schema,
  WIRE_MESSAGES['attachment.rejected'].schema,
  WIRE_MESSAGES['device.dirs.rejected'].schema,
  WIRE_MESSAGES['device.clone'].schema,
]);

/** The messages a browser sends. */
export const BrowserMessageSchema = v.variant('type', [
  WIRE_MESSAGES.subscribe.schema,
  WIRE_MESSAGES.unsubscribe.schema,
]);

/** Fields of the nested objects the agent mirrors by name in Go. */
export const WIRE_OBJECTS = {
  SessionMeta: SessionMetaSchema,
  DeviceProject: DeviceProjectSchema,
  DeviceProjectSession: DeviceProjectSessionSchema,
  DirEntry: DirEntrySchema,
  RuntimeCapabilities: RuntimeCapabilitiesSchema,
  ProviderInfo: ProviderInfoSchema,
  RuntimeStartOptions: StartOptionsSchema,
  WorkspaceChange: WorkspaceChangeSchema,
} as const;
