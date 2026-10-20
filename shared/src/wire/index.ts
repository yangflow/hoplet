/**
 * WebSocket messages between the core, the agents and the browsers. Every message carries `v`
 * (`PROTOCOL_VERSION`) and a `type`, belongs to one lane, and, unless it is core-to-web, is mirrored in
 * `agent/internal/sync/protocol.go`; `scripts/test/contract.test.ts` compares both.
 */
import * as v from 'valibot';
import { DeviceProjectSchema, DeviceProjectSessionSchema, DirEntrySchema } from '../catalog.ts';
import {
  CLONE_DETAIL_MAX_BYTES,
  CloneCheckReasonSchema,
  CloneDestinationDetailSchema,
  CloneFailureReasonSchema,
} from '../clone.ts';
import { CommandSchema, MAX_COMMANDS } from '../commands.ts';
import { ToolKindSchema, ToolMcpSchema } from '../conversation.ts';
import {
  DeletionDirtySchema,
  DeletionFileSchema,
  DeletionStepSchema,
  DeletionWorktreeSchema,
} from '../deletion.ts';
import { ErrorCodeSchema } from '../errors.ts';
import {
  AttachmentIdSchema,
  HandoffIdSchema,
  SessionIdSchema,
  SnapshotIdSchema,
  TerminalIdSchema,
} from '../ids.ts';
import { NotifyEventKindSchema } from '../rest/notify.ts';
import { ChangeStatusSchema, WorkspaceChangeSchema } from '../rest/workspace.ts';
import {
  AgentRuntimeStateSchema,
  ConfigOptionSchema,
  ConfigSettingSchema,
  ConfigurationIdSchema,
  ConfigValueSchema,
  PermissionDecisionSchema,
  PlanExitSchema,
  PROMPT_IMAGE_TYPES,
  ProviderConfigurationSchema,
  ProviderEnvVarSchema,
  ProviderInfoSchema,
  RuntimeCapabilitiesSchema,
  RuntimeOutputKindSchema,
  RuntimeStateSchema,
  SessionConfigSchema,
} from '../runtime.ts';
import { ProviderSchema, SessionMetaSchema } from '../session.ts';
import { type Direction, defineWire, type Lane, list } from './define.ts';

export type { Direction, Lane, WireMessage } from './define.ts';
export { defineWire } from './define.ts';

/** What an agent offers beyond its providers; absent means nothing (ADR 0026). */
export const DeviceCapabilitiesSchema = v.object({
  /** The owner enabled the restricted terminal in the agent config. */
  terminal: v.optional(v.boolean()),
});

/** The system the agent runs on, as Go names it: `GOOS` and `GOARCH` verbatim, such as `darwin` and `arm64`. */
export const DeviceSystemSchema = v.object({
  os: v.pipe(v.string(), v.maxLength(32)),
  arch: v.pipe(v.string(), v.maxLength(32)),
});
export type DeviceSystem = v.InferOutput<typeof DeviceSystemSchema>;

/** Bytes of a terminal input or output message, decoded (ADR 0026). */
export const TERMINAL_CHUNK_BYTES = 4096;
/** Output the agent sends before it waits for a `terminal.ack`. */
export const TERMINAL_WINDOW_BYTES = 65536;
export const TERMINAL_MAX_COLS = 500;
export const TERMINAL_MAX_ROWS = 200;

/** Why a terminal was refused or ended. */
export const TerminalReasonSchema = v.picklist([
  'disabled',
  'terminal-busy',
  'turn-running',
  'external',
  'handoff-running',
  'owner-offline',
  'not-a-directory',
  'idle',
  'max-age',
  'detached',
  'turn-started',
  'handoff',
  'deleted',
  'closed',
  'input-flood',
  'exit',
]);
export type TerminalReason = v.InferOutput<typeof TerminalReasonSchema>;

/** Base64 of at most `TERMINAL_CHUNK_BYTES` bytes. */
const TerminalBytes = v.pipe(
  v.string(),
  v.regex(/^[A-Za-z0-9+/]*={0,2}$/),
  v.maxLength(Math.ceil(TERMINAL_CHUNK_BYTES / 3) * 4),
);
const terminalId = TerminalIdSchema;
const TerminalCols = v.pipe(v.number(), v.integer(), v.minValue(1), v.maxValue(TERMINAL_MAX_COLS));
const TerminalRows = v.pipe(v.number(), v.integer(), v.minValue(1), v.maxValue(TERMINAL_MAX_ROWS));

/** Sessions one `ports.watch` names, at most (ADR 0026). */
export const PORTS_WATCH_MAX_SESSIONS = 64;
/** Ports one `session.ports` reports, at most. */
export const SESSION_PORTS_MAX = 16;
/** Characters of an executable name in a port report. */
export const PORT_PROCESS_MAX = 64;

/** Which interfaces a listening socket accepts connections on. */
export const PortAddressSchema = v.picklist(['loopback', 'any', 'other']);
export type PortAddress = v.InferOutput<typeof PortAddressSchema>;

/** One listening TCP port of a process started from a session's directory (ADR 0026). */
export const SessionPortSchema = v.object({
  port: v.pipe(v.number(), v.integer(), v.minValue(1), v.maxValue(65535)),
  address: PortAddressSchema,
  /** The executable's base name; never a command line. */
  process: v.pipe(v.string(), v.maxLength(PORT_PROCESS_MAX)),
});
export type SessionPort = v.InferOutput<typeof SessionPortSchema>;

/** Milliseconds between two `device.resources` reports of an agent. */
export const RESOURCES_INTERVAL_MS = 15_000;
/** Samples the core keeps per machine: one hour at the interval above. */
export const RESOURCES_RING_SIZE = 240;
/** Sessions one `device.resources` reports, at most; the ones holding the most memory. */
export const RESOURCES_SESSIONS_MAX = 64;
/** A machine warns about memory when more than this share is used. */
export const RESOURCES_MEMORY_WARN = 0.9;
/** A machine warns about disk when less than this share of the volume is free. */
export const RESOURCES_DISK_WARN = 0.1;

const Percent = v.pipe(v.number(), v.minValue(0), v.maxValue(100));
const Bytes = v.pipe(v.number(), v.integer(), v.minValue(0));

/**
 * The processes of one session Hoplet started, summed over the process tree: `cpu` is the share of the whole
 * machine's CPU in percent (null while the agent has no earlier reading to compare with), `rss` the resident
 * memory in bytes and `children` the number of descendant processes.
 */
export const SessionResourcesSchema = v.object({
  sessionId: SessionIdSchema,
  cpu: v.nullable(Percent),
  rss: Bytes,
  children: v.pipe(v.number(), v.integer(), v.minValue(0)),
});
export type SessionResources = v.InferOutput<typeof SessionResourcesSchema>;

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
  'clone',
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
      /** The system the agent runs on; absent from an agent that predates it. */
      system: v.optional(DeviceSystemSchema),
      /** The providers this agent runs, with the capabilities of their runtime drivers. */
      providers: v.optional(v.array(ProviderInfoSchema)),
      /** Present when the agent offers more than sessions, such as the terminal. */
      capabilities: v.optional(DeviceCapabilitiesSchema),
      /** The host the inspector links dev servers with: the agent's `preview_host`, else the machine's host name. */
      previewHost: v.optional(v.pipe(v.string(), v.maxLength(255))),
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
      /** The vendor of the source the lines come from; the core stores it when it creates the session and never changes it. */
      provider: ProviderSchema,
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
      /**
       * The ID of the provider configuration the session belongs to (ADR 0028); absent means the vendor's own.
       * The core keeps what the latest append says.
       */
      configuration: v.optional(ConfigurationIdSchema),
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
  /**
   * One writer per session: appends from a non-owner device are rejected. `reason` is `not-owner`, or `deleted`
   * for a session that was deleted (ADR 0023); the agent suppresses the session in both cases.
   */
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

  /** A session was deleted (ADR 0023): drop it from every list. Broadcast to every authenticated connection. */
  'session.deleted': {
    lane: 'control',
    direction: 'core-to-web',
    fields: { sessionId },
  },

  /**
   * The user changed a session's title, pin or archive state. Carries the whole state after the change:
   * a field that is absent is cleared. Broadcast to every authenticated connection; agents ignore it.
   */
  'session.updated': {
    lane: 'control',
    direction: 'core-to-web',
    fields: {
      sessionId,
      titleOverride: v.optional(v.string()),
      pinnedAt: v.optional(v.number()),
      archivedAt: v.optional(v.number()),
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
  /**
   * A message written while the session's turn runs, which the process takes at the turn's next tool gap (ADR 0020).
   * The agent answers with `runtime.steered`, or with `runtime.rejected` carrying `steerId` as `promptId`.
   */
  'runtime.steer': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      sessionId,
      steerId: v.string(),
      text: v.string(),
      attachments: v.optional(list(AttachmentIdSchema)),
    },
  },
  /** The agent wrote the steer to the process. */
  'runtime.steered': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { sessionId, steerId: v.string() },
  },
  /**
   * Sets one setting of a session (ADR 0018). The agent applies it as the driver says (at once, before the next
   * turn or by restarting the process between turns) and reports the session's settings in its next
   * `runtime.state`. The core sends only declared settings; the agent drops one its driver does not declare.
   */
  'runtime.config': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { sessionId, configId: v.string(), value: ConfigSettingSchema },
  },
  /**
   * Answers a permission request. A `deny` may carry `message`, which the process tells the model; an `allow`
   * of a plan exit may carry `mode`, one of the request's `planExit.modes`, to switch to as the plan is approved.
   */
  'runtime.permission.answer': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      sessionId,
      requestId: v.string(),
      decision: PermissionDecisionSchema,
      message: v.optional(v.string()),
      mode: v.optional(v.string()),
    },
  },
  /**
   * Starts a new session in `projectPath`, a watched project of the device: the agent starts the provider's
   * process there with `config`, writes the prompt, imports the session the process creates and answers
   * with `runtime.started`, or with `runtime.start.rejected`.
   */
  'runtime.start': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      requestId,
      projectPath: v.string(),
      provider: ProviderSchema,
      /** The provider configuration to start with (ADR 0028); the vendor's own when absent. */
      configuration: v.optional(ConfigurationIdSchema),
      config: v.optional(SessionConfigSchema),
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
      /** `capabilities.config` carries this session's values. */
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
  /**
   * The process asks to use a tool; answered with `runtime.permission.answer`. `planExit` marks a request to
   * leave a plan (ADR 0019): it has no time limit, and its answer may carry `mode`.
   */
  'runtime.permission': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      sessionId,
      requestId: v.string(),
      tool: v.string(),
      toolUseId: v.optional(v.string()),
      input: v.unknown(),
      planExit: v.optional(PlanExitSchema),
    },
  },
  /**
   * The commands and skills of a session's process, with `cwd`, the directory it runs in. Sent when they
   * first become known and whenever they change (a file of the project or the user changed), and again after
   * a reconnect. At most `MAX_COMMANDS` entries, in the order clients show them.
   */
  'runtime.commands': {
    lane: 'bulk',
    direction: 'device-to-core',
    fields: {
      sessionId,
      cwd: v.pipe(v.string(), v.maxLength(4096)),
      commands: v.pipe(list(CommandSchema), v.maxLength(MAX_COMMANDS)),
    },
  },
  /**
   * A prompt or steer the agent refused (`promptId` is the prompt's or the steer's ID); `reason` is `busy`,
   * `starting`, `external`, `not-running`, `permission`, `unsupported`, `configuration-unavailable` (the session's
   * provider configuration was removed or disabled) or diagnostic text.
   */
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
  /** The commands of a session changed; the browser reads `GET /api/sessions/:id/commands` again. */
  'session.commands': {
    lane: 'control',
    direction: 'core-to-web',
    fields: { sessionId },
  },
  /** The session's queue of next messages changed; the browser reads `GET /api/sessions/:id/queue` again. */
  'session.queue': {
    lane: 'control',
    direction: 'core-to-web',
    fields: { sessionId },
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
  /** A permission request; `toolKind` and `mcp` classify its tool as on `session.output`; `planExit` is as on `runtime.permission`. */
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
      planExit: v.optional(PlanExitSchema),
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
   * `provider` names the session's vendor: the target installs the session file in that vendor's store.
   */
  'handoff.install': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      sessionId,
      handoffId,
      provider: ProviderSchema,
      /** The target's provider configuration the session is installed for; the vendor's own when absent. */
      configuration: v.optional(ConfigurationIdSchema),
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
    fields: { sessionId, handoffId, worktree: v.string(), branch: v.string(), clone: v.string() },
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
   * during `clone`, `sent` bytes of the repository have arrived (`total` only once git reports it); during
   * `transfer`, `sent` of `total` bytes have gone to the target; `done` names the new worktree and branch.
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

  /**
   * A notification for a native client (ADR 0021): sent to browser sockets that declared themselves a native
   * client, of which there is none yet, so the core does not send it today. It carries the payload of every
   * channel and no link.
   */
  notify: {
    lane: 'control',
    direction: 'core-to-web',
    fields: {
      kind: NotifyEventKindSchema,
      sessionId: v.optional(sessionId),
      project: v.string(),
      title: v.optional(v.string()),
      tool: v.optional(v.string()),
      summary: v.optional(v.string()),
      requestId: v.optional(v.string()),
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
  /** Deleting a session (ADR 0023): the agent finds the vendor file and the worktree itself from the ID. */
  'device.session.inspect': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { requestId, sessionId },
  },
  'device.session.inspected': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      requestId,
      vendorFile: v.optional(DeletionFileSchema),
      worktree: v.optional(DeletionWorktreeSchema),
    },
  },
  'device.session.rejected': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, reason: v.string() },
  },
  'device.session.delete': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      requestId,
      sessionId,
      vendorFile: v.boolean(),
      worktree: v.boolean(),
      worktreeDirty: v.boolean(),
    },
  },
  'device.session.deleted': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, steps: list(DeletionStepSchema) },
  },
  /** Stop syncing the session and forget it: the first step of a deletion alone, for a tombstone. */
  'device.session.forget': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { requestId, sessionId },
  },
  'device.session.forgotten': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, sessionId },
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

  // The provider configurations of a machine (ADR 0028)
  /**
   * The full list of the agent's provider configurations: the answer to `device.providers.update`, and sent
   * unasked when the machine's providers.json changes.
   */
  'device.providers': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      requestId: v.optional(requestId),
      providers: list(ProviderInfoSchema),
    },
  },
  /** Changes the name, the enabled flag or the defaults of one configuration; nothing else can change from here. */
  'device.providers.update': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      requestId,
      id: ConfigurationIdSchema,
      name: v.optional(ProviderConfigurationSchema.entries.name),
      enabled: v.optional(v.boolean()),
      defaults: v.optional(SessionConfigSchema),
    },
  },
  'device.providers.rejected': {
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
  // Cloning the repository on a target that has none (ADR 0024). Handled in their own goroutines, never on
  // the ordered handoff queue.
  /** Can `origin` be cloned into `cloneInto` (absent: the agent suggests a destination)? */
  'device.clone.check': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      requestId,
      origin: v.pipe(v.string(), v.maxLength(2048)),
      cloneInto: v.optional(v.pipe(v.string(), v.maxLength(4096))),
    },
  },
  /** `cloneInto` is the destination checked: the one asked for or the suggestion. */
  'device.clone.checked': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      requestId,
      reason: CloneCheckReasonSchema,
      detail: v.optional(CloneDestinationDetailSchema),
      cloneInto: v.string(),
    },
  },
  'device.clone.start': {
    lane: 'control',
    direction: 'core-to-device',
    fields: {
      requestId,
      handoffId,
      origin: v.pipe(v.string(), v.maxLength(2048)),
      cloneInto: v.pipe(v.string(), v.maxLength(4096)),
    },
  },
  /** At most four a second; `total` is absent until git reports it. */
  'device.clone.progress': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, received: v.number(), total: v.optional(v.number()) },
  },
  /** The clone is complete and watched; `path` is its main working tree. */
  'device.clone.done': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { requestId, path: v.string() },
  },
  'device.clone.failed': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      requestId,
      reason: CloneFailureReasonSchema,
      detail: v.optional(v.pipe(v.string(), v.maxLength(CLONE_DETAIL_MAX_BYTES))),
    },
  },
  /** Kills git and removes the directory the clone created; answered by `device.clone.failed` `cancelled`. */
  'device.clone.cancel': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { requestId },
  },

  // Restricted terminal (ADR 0026). Output is credit-controlled: the agent stops reading the PTY after
  // `TERMINAL_WINDOW_BYTES` unacknowledged bytes and resumes on `terminal.ack`.
  'terminal.open': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { terminalId, sessionId, cols: TerminalCols, rows: TerminalRows },
  },
  'terminal.input': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { terminalId, data: TerminalBytes },
  },
  'terminal.resize': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { terminalId, cols: TerminalCols, rows: TerminalRows },
  },
  'terminal.ack': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { terminalId, bytes: v.pipe(v.number(), v.integer(), v.minValue(0)) },
  },
  'terminal.close': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { terminalId, reason: TerminalReasonSchema },
  },
  'terminal.opened': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { terminalId },
  },
  'terminal.output': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      terminalId,
      seq: v.pipe(v.number(), v.integer(), v.minValue(0)),
      data: TerminalBytes,
    },
  },
  'terminal.closed': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { terminalId, reason: TerminalReasonSchema, exitCode: v.optional(v.number()) },
  },
  'terminal.rejected': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { terminalId, reason: TerminalReasonSchema },
  },

  // Dev server ports (ADR 0026). `ports.watch` replaces the set of sessions to scan; an empty set stops the scan.
  'ports.watch': {
    lane: 'control',
    direction: 'core-to-device',
    fields: { sessionIds: v.pipe(list(SessionIdSchema), v.maxLength(PORTS_WATCH_MAX_SESSIONS)) },
  },
  'session.ports': {
    lane: 'control',
    direction: 'device-to-core',
    fields: { sessionId, ports: v.pipe(list(SessionPortSchema), v.maxLength(SESSION_PORTS_MAX)) },
  },

  // The machine's load, every 15 seconds; a report sent while the core is away is dropped.
  'device.resources': {
    lane: 'control',
    direction: 'device-to-core',
    fields: {
      /** When the agent took the reading, in epoch milliseconds. */
      at: v.pipe(v.number(), v.integer(), v.minValue(0)),
      /** CPU in use on the whole machine, percent; null while the agent has no earlier reading to compare with. */
      cpu: v.nullable(Percent),
      memUsed: Bytes,
      memTotal: Bytes,
      /** Free and total bytes of the volume that holds the agent's browse root. */
      diskFree: Bytes,
      diskTotal: Bytes,
      sessions: v.pipe(list(SessionResourcesSchema), v.maxLength(RESOURCES_SESSIONS_MAX)),
    },
  },

  // The browser side of the terminal and the dev server links (ADR 0026). A terminal is opened over REST
  // (`POST /api/sessions/:id/terminal`); these messages attach a browser socket to its stream. The credit
  // window of the agent is the core's business: a browser never acknowledges output.
  /** Attaches this socket to an open terminal: `terminal.attached` and the replay of recent output follow. */
  'terminal.attach': { lane: 'control', direction: 'web-to-core', fields: { terminalId } },
  /** Detaches this socket; the terminal closes `detached` 60 seconds after its last socket left. */
  'terminal.detach': { lane: 'control', direction: 'web-to-core', fields: { terminalId } },
  /** Keystrokes for an attached terminal: base64 of at most `TERMINAL_CHUNK_BYTES` bytes. */
  'terminal.keys': {
    lane: 'control',
    direction: 'web-to-core',
    fields: { terminalId, data: TerminalBytes },
  },
  /** The size of the terminal view changed. */
  'terminal.size': {
    lane: 'control',
    direction: 'web-to-core',
    fields: { terminalId, cols: TerminalCols, rows: TerminalRows },
  },
  /** The socket is attached; the terminal is `cols` x `rows`. */
  'terminal.attached': {
    lane: 'control',
    direction: 'core-to-web',
    fields: { terminalId, sessionId, cols: TerminalCols, rows: TerminalRows },
  },
  /** Output of an attached terminal, in order by `seq`; the first frames after `terminal.attached` replay recent output. */
  'terminal.data': {
    lane: 'control',
    direction: 'core-to-web',
    fields: {
      terminalId,
      seq: v.pipe(v.number(), v.integer(), v.minValue(0)),
      data: TerminalBytes,
    },
  },
  /** The terminal ended, or the id names none (`closed`); a socket receives it once and the terminal is gone. */
  'terminal.ended': {
    lane: 'control',
    direction: 'core-to-web',
    fields: { terminalId, reason: TerminalReasonSchema, exitCode: v.optional(v.number()) },
  },
  /**
   * The ports a session's processes listen on, as its owner last reported, and the host to link them with.
   * Sent to the browsers subscribed to the session when subscribing and on every change; `ports` is empty when
   * the owner went offline. Never stored.
   */
  'session.listening': {
    lane: 'control',
    direction: 'core-to-web',
    fields: {
      sessionId,
      previewHost: v.optional(v.pipe(v.string(), v.maxLength(255))),
      ports: v.pipe(v.array(SessionPortSchema), v.maxLength(SESSION_PORTS_MAX)),
    },
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
export type SessionUpdated = WireMessageOf<'session.updated'>;
export type RuntimePrompt = WireMessageOf<'runtime.prompt'>;
export type RuntimeSteer = WireMessageOf<'runtime.steer'>;
export type RuntimeSteered = WireMessageOf<'runtime.steered'>;
export type RuntimeConfig = WireMessageOf<'runtime.config'>;
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
export type RuntimeCommands = WireMessageOf<'runtime.commands'>;
export type SessionCommands = WireMessageOf<'session.commands'>;
export type SessionQueue = WireMessageOf<'session.queue'>;
export type NotifyMsg = WireMessageOf<'notify'>;
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
export type DeviceSessionInspect = WireMessageOf<'device.session.inspect'>;
export type DeviceSessionInspected = WireMessageOf<'device.session.inspected'>;
export type DeviceSessionRejected = WireMessageOf<'device.session.rejected'>;
export type DeviceSessionDelete = WireMessageOf<'device.session.delete'>;
export type DeviceSessionDeleted = WireMessageOf<'device.session.deleted'>;
export type DeviceSessionForget = WireMessageOf<'device.session.forget'>;
export type DeviceSessionForgotten = WireMessageOf<'device.session.forgotten'>;
export type SessionDeleted = WireMessageOf<'session.deleted'>;
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
export type DeviceCloneCheck = WireMessageOf<'device.clone.check'>;
export type DeviceCloneChecked = WireMessageOf<'device.clone.checked'>;
export type DeviceCloneStart = WireMessageOf<'device.clone.start'>;
export type DeviceCloneProgress = WireMessageOf<'device.clone.progress'>;
export type DeviceCloneDone = WireMessageOf<'device.clone.done'>;
export type DeviceCloneFailed = WireMessageOf<'device.clone.failed'>;
export type DeviceCloneCancel = WireMessageOf<'device.clone.cancel'>;
export type TerminalOpen = WireMessageOf<'terminal.open'>;
export type TerminalInput = WireMessageOf<'terminal.input'>;
export type TerminalResize = WireMessageOf<'terminal.resize'>;
export type TerminalAck = WireMessageOf<'terminal.ack'>;
export type TerminalClose = WireMessageOf<'terminal.close'>;
export type TerminalOpened = WireMessageOf<'terminal.opened'>;
export type TerminalOutput = WireMessageOf<'terminal.output'>;
export type TerminalClosed = WireMessageOf<'terminal.closed'>;
export type TerminalRejected = WireMessageOf<'terminal.rejected'>;
export type TerminalAttach = WireMessageOf<'terminal.attach'>;
export type TerminalDetach = WireMessageOf<'terminal.detach'>;
export type TerminalKeys = WireMessageOf<'terminal.keys'>;
export type TerminalSize = WireMessageOf<'terminal.size'>;
export type TerminalAttached = WireMessageOf<'terminal.attached'>;
export type TerminalData = WireMessageOf<'terminal.data'>;
export type TerminalEnded = WireMessageOf<'terminal.ended'>;
export type SessionListening = WireMessageOf<'session.listening'>;
export type PortsWatch = WireMessageOf<'ports.watch'>;
export type SessionPorts = WireMessageOf<'session.ports'>;
export type DeviceResources = WireMessageOf<'device.resources'>;
export type DeviceProviders = WireMessageOf<'device.providers'>;
export type DeviceProvidersUpdate = WireMessageOf<'device.providers.update'>;
export type DeviceProvidersRejected = WireMessageOf<'device.providers.rejected'>;

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
  WIRE_MESSAGES['runtime.steered'].schema,
  WIRE_MESSAGES['runtime.commands'].schema,
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
  WIRE_MESSAGES['device.session.inspected'].schema,
  WIRE_MESSAGES['device.session.rejected'].schema,
  WIRE_MESSAGES['device.session.deleted'].schema,
  WIRE_MESSAGES['device.session.forgotten'].schema,
  WIRE_MESSAGES['device.dirs'].schema,
  WIRE_MESSAGES['attachment.stored'].schema,
  WIRE_MESSAGES['attachment.rejected'].schema,
  WIRE_MESSAGES['device.dirs.rejected'].schema,
  WIRE_MESSAGES['device.clone'].schema,
  WIRE_MESSAGES['device.clone.checked'].schema,
  WIRE_MESSAGES['device.clone.progress'].schema,
  WIRE_MESSAGES['device.clone.done'].schema,
  WIRE_MESSAGES['device.clone.failed'].schema,
  WIRE_MESSAGES['terminal.opened'].schema,
  WIRE_MESSAGES['terminal.output'].schema,
  WIRE_MESSAGES['terminal.closed'].schema,
  WIRE_MESSAGES['terminal.rejected'].schema,
  WIRE_MESSAGES['session.ports'].schema,
  WIRE_MESSAGES['device.resources'].schema,
  WIRE_MESSAGES['device.providers'].schema,
  WIRE_MESSAGES['device.providers.rejected'].schema,
]);

/** The messages a browser sends. */
export const BrowserMessageSchema = v.variant('type', [
  WIRE_MESSAGES.subscribe.schema,
  WIRE_MESSAGES.unsubscribe.schema,
  WIRE_MESSAGES['terminal.attach'].schema,
  WIRE_MESSAGES['terminal.detach'].schema,
  WIRE_MESSAGES['terminal.keys'].schema,
  WIRE_MESSAGES['terminal.size'].schema,
]);

/** Fields of the nested objects the agent mirrors by name in Go. */
export const WIRE_OBJECTS = {
  SessionMeta: SessionMetaSchema,
  DeviceProject: DeviceProjectSchema,
  DeviceProjectSession: DeviceProjectSessionSchema,
  DirEntry: DirEntrySchema,
  RuntimeCapabilities: RuntimeCapabilitiesSchema,
  ProviderInfo: ProviderInfoSchema,
  ProviderConfiguration: ProviderConfigurationSchema,
  ProviderEnvVar: ProviderEnvVarSchema,
  DeviceCapabilities: DeviceCapabilitiesSchema,
  DeviceSystem: DeviceSystemSchema,
  SessionPort: SessionPortSchema,
  SessionResources: SessionResourcesSchema,
  ConfigOption: ConfigOptionSchema,
  ConfigValue: ConfigValueSchema,
  Command: CommandSchema,
  PlanExit: PlanExitSchema,
  WorkspaceChange: WorkspaceChangeSchema,
  DeletionFile: DeletionFileSchema,
  DeletionDirty: DeletionDirtySchema,
  DeletionWorktree: DeletionWorktreeSchema,
  DeletionStep: DeletionStepSchema,
} as const;
