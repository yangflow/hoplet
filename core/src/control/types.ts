import type {
  ClientMessage,
  Command,
  ConfigSetting,
  DeviceDirsResult,
  DeviceInfo,
  DeviceProject,
  PermissionDecision,
  PromptImage,
  Provider,
  ProviderInfo,
  Queue,
  RuntimeCapabilities,
  RuntimeState,
  ServerMessage,
  SessionConfig,
  SessionPermission,
  WorkspaceDiff,
  WorkspaceFile,
  WorkspaceListing,
} from '@hoplet/shared';
import type { BulkQueue, BulkQueueOptions } from './bulk.ts';

/** One agent connection as control sees it: ingest owns the socket, control only talks through this. */
export interface DeviceLink {
  send(msg: ServerMessage): void;
  /** Sends and resolves when the message has been written, rejecting when the connection could not take it. */
  sendAndWait(msg: ServerMessage): Promise<void>;
  isOpen(): boolean;
}

/** The agent messages that answer a request control sent; everything else is sync and stays in ingest. */
export type ControlReply = Exclude<
  ClientMessage,
  { type: 'device.hello' | 'ping' | 'session.append' | 'workspace.snapshot' }
>;

/** What `POST /api/sessions` asks for. */
export interface StartRequest {
  deviceId: string;
  projectPath: string;
  provider: Provider;
  config?: SessionConfig | undefined;
  text: string;
  attachments?: string[] | undefined;
  promptId: string;
}

/** What an agent answers to `runtime.start`, or why there is no answer. */
export type StartAnswer =
  | { ok: true; sessionId: string }
  | { ok: false; error: 'target-offline' | 'timeout' | 'rejected'; reason?: string };

export type StartOutcome =
  | { ok: true; sessionId: string; promptId: string }
  | {
      ok: false;
      error: 'target-offline' | 'invalid-request' | 'timeout' | 'rejected';
      promptId: string;
      reason?: string;
    };

export type PromptOutcome =
  | { ok: true; promptId: string }
  | {
      ok: false;
      error: 'owner-offline' | 'external' | 'starting' | 'busy' | 'timeout' | 'rejected';
      promptId: string;
      reason?: string;
    };

/** A session's runtime state as clients see it. */
export interface RuntimeView {
  state: RuntimeState;
  error?: string;
  /** `capabilities.config` carries the session's values, as the owner reported. */
  capabilities?: RuntimeCapabilities;
}

export type RuntimeCommandOutcome =
  | { ok: true }
  | { ok: false; error: 'owner-offline' | 'not-running' | 'external' };

/** What a steer of a running turn comes to (ADR 0020). */
export type SteerOutcome =
  | { ok: true }
  | {
      ok: false;
      error: 'owner-offline' | 'not-running' | 'steer-unavailable' | 'timeout' | 'rejected';
      reason?: string;
    };

/** The queue after a change, or why the change did not apply: the message changed or went (`conflict`), or the queue is full. */
export type QueueResult = { ok: true; queue: Queue } | { ok: false; error: 'conflict' | 'full' };

/** Converting a queued message to a steer: the queue after it, and how the steer went. */
export type QueueSteerOutcome =
  | { ok: true; queue: Queue; steer: SteerOutcome }
  | { ok: false; error: 'conflict' };

export type PermissionOutcome =
  | { ok: true }
  | { ok: false; error: 'owner-offline' | 'request-closed' | 'invalid-request' };

/** An answer to a permission request: a refusal may explain itself, the approval of a plan names a mode. */
export interface PermissionAnswer {
  decision: PermissionDecision;
  message?: string | undefined;
  mode?: string | undefined;
}

export type WorkspaceOutcome =
  | { ok: true; listing: WorkspaceListing }
  | { ok: false; error: 'owner-offline' | 'timeout' | 'rejected'; reason?: string };

export type WorkspaceFileOutcome =
  | { ok: true; file: WorkspaceFile }
  | { ok: false; error: 'owner-offline' | 'timeout' | 'rejected'; reason?: string };

export type WorkspaceDiffOutcome =
  | { ok: true; diff: WorkspaceDiff }
  | { ok: false; error: 'owner-offline' | 'timeout' | 'rejected'; reason?: string };

/** What `POST /api/sessions/:id/handoff` asks for. */
export interface HandoffRequest {
  sessionId: string;
  targetDeviceId: string;
  targetPath?: string | undefined;
}

export type HandoffOutcome =
  | {
      ok: true;
      ownerDeviceId: string;
      ownerEpoch: number;
      /** The worktree the session continues in on the new owner, and its branch. */
      worktree: string;
      branch: string;
      /** The time of the snapshot an offline handoff installed. */
      snapshotAt?: number;
    }
  | {
      ok: false;
      error:
        | 'session-not-found'
        | 'same-device'
        | 'target-offline'
        | 'owner-offline'
        | 'handoff-in-progress'
        | 'busy'
        | 'no-snapshot'
        | 'no-clone'
        | 'session-changed'
        | 'verify-failed'
        | 'cancelled'
        | 'timeout'
        | 'rejected'
        | 'internal';
      reason?: string;
    };

/** Why a handoff did not complete. */
export type HandoffError = Extract<HandoffOutcome, { ok: false }>['error'];

/** Why a handoff step an agent should answer did not succeed. */
export type StepFailure = { ok: false; error: 'timeout' | 'offline' | 'rejected'; reason?: string };

/** An agent's answer to a handoff step, or why there is none. */
export type StepOutcome<T> = { ok: true; value: T } | StepFailure;

/** What the owner reports when it is ready to hand off. */
export interface PreparedInfo {
  snapshotId: string;
  byteSize: number;
}

/** Where the target installed the session: its worktree and branch. */
export interface InstalledInfo {
  worktree: string;
  branch: string;
}

/** What the target reports about its copy. */
export interface VerifiedInfo {
  sessionBytes: number;
  sessionSha256: string;
  treeHash: string;
}

/** Outcome of device.projects.* / device.sessions.* control round-trips. */
export type DeviceControlOutcome =
  | { ok: true; candidates: string[]; projects: DeviceProject[] }
  | { ok: false; error: 'owner-offline' | 'timeout' | 'rejected'; reason?: string };

/** Outcome of a device.dirs.list round-trip. */
export type DirsOutcome =
  | { ok: true; listing: DeviceDirsResult }
  | { ok: false; error: 'target-offline' | 'timeout' | 'rejected'; reason?: string };

/** Outcome of an attachment.put round-trip: the ID the image is stored under on the device. */
export type AttachmentOutcome =
  | { ok: true; attachmentId: string }
  | { ok: false; error: 'target-offline' | 'timeout' | 'rejected'; reason?: string };

/** Outcome of a device.clone.find round-trip: the clone a handoff would use, or null. */
export type CloneOutcome =
  | { ok: true; path: string | null }
  | { ok: false; error: 'target-offline' | 'timeout' };

/**
 * A moment a user may want to hear about (ADR 0021). A `permission` event is a request that waits for an answer;
 * `expiresAt` is when it is denied, absent for a plan review, which never expires. A `turn-done` event is a turn
 * that went from running to ready. A `failure` is a session start or a handoff that did not complete; a
 * session that failed to start has no ID.
 */
export type ControlEvent =
  | {
      kind: 'permission';
      sessionId: string;
      requestId: string;
      tool: string;
      input: unknown;
      plan: boolean;
      expiresAt?: number;
    }
  | { kind: 'turn-done'; sessionId: string }
  | {
      kind: 'failure';
      source: 'start' | 'handoff';
      sessionId?: string;
      projectPath?: string;
      error: string;
    };

/** The browser WebSockets that have a session open (ADR 0021). */
export interface Viewing {
  /** A browser subscribed to the session. */
  add(sessionId: string): void;
  /** A browser unsubscribed, or its socket closed. */
  remove(sessionId: string): void;
  /** Whether at least one browser is subscribed to the session. */
  isOpen(sessionId: string): boolean;
}

/** What the REST API asks of control. */
export interface Control {
  /** Calls `listener` with every moment that may notify; returns the unsubscribe. */
  onEvent(listener: (event: ControlEvent) => void): () => void;
  /** Which sessions browsers have open; the API keeps it current from its sockets. */
  readonly viewing: Viewing;
  /** The bulk queue: history pages and searches run through it; control requests never do. */
  readonly bulk: BulkQueue;
  /** Calls `listener` with every event agents cause that browsers show (`session.appended`); returns the unsubscribe. */
  subscribe(listener: (msg: ServerMessage) => void): () => void;
  /** Tells every browser something the API changed (`session.updated`). */
  publish(msg: ServerMessage): void;
  ownerOnline(deviceId: string): boolean;
  /** Forwards a prompt to the owner unless its runtime state refuses it; a repeated `promptId` returns the first result. */
  prompt(
    sessionId: string,
    ownerDeviceId: string,
    text: string,
    promptId: string,
    attachments?: string[],
  ): Promise<PromptOutcome>;
  /** Answers an open permission request; the first answer wins. */
  answerPermission(
    sessionId: string,
    ownerDeviceId: string,
    requestId: string,
    answer: PermissionAnswer,
  ): PermissionOutcome;
  interrupt(sessionId: string, ownerDeviceId: string): RuntimeCommandOutcome;
  stop(sessionId: string, ownerDeviceId: string): RuntimeCommandOutcome;
  /** Forwards one setting change (ADR 0018); the caller has checked it against the session's capabilities. */
  config(
    sessionId: string,
    ownerDeviceId: string,
    configId: string,
    value: ConfigSetting,
  ): RuntimeCommandOutcome;
  /** The session's queue of next messages, held by the core (ADR 0020). */
  queueOf(sessionId: string): Queue;
  /** Appends a message to the queue; the session sends it when its turn is over. A repeated `id` changes nothing. */
  queueAdd(
    sessionId: string,
    input: { id: string; text: string; attachments: string[] },
  ): QueueResult;
  /** Replaces a waiting message that is still at `version`. */
  queueEdit(
    sessionId: string,
    id: string,
    version: number,
    input: { text: string; attachments: string[] },
  ): QueueResult;
  queueRemove(sessionId: string, id: string, version: number): QueueResult;
  /** Orders the waiting messages; `ids` names exactly those. */
  queueReorder(sessionId: string, ids: string[]): QueueResult;
  /** Ends a pause: the queue sends again. */
  queueResume(sessionId: string): Queue;
  /** Removes the waiting messages and ends a pause. */
  queueClear(sessionId: string): Queue;
  /** Sends a queued message to the running turn instead of after it. */
  queueSteer(
    sessionId: string,
    ownerDeviceId: string,
    id: string,
    version: number,
  ): Promise<QueueSteerOutcome>;
  /** Forwards a message to the running turn, which takes it at its next tool gap; a repeated `steerId` returns the first result. */
  steer(
    sessionId: string,
    ownerDeviceId: string,
    steerId: string,
    text: string,
    attachments?: string[],
  ): Promise<SteerOutcome>;
  /** The runtime state of a session; `offline` when its owner is not connected. */
  runtimeOf(sessionId: string): RuntimeView;
  /** The vendor of a session, also of one an agent just started; `null` for an unknown session. */
  providerOf(sessionId: string): Provider | null;
  /** The commands and skills the session's owner last reported, or null when it reported none. */
  commandsOf(sessionId: string): Command[] | null;
  /** The permission requests of a session that wait for an answer. */
  openPermissions(sessionId: string): SessionPermission[];
  workspace(sessionId: string, ownerDeviceId: string): Promise<WorkspaceOutcome>;
  /** Reads one file of the owner's working tree; `at` `head` reads it as `HEAD` has it. */
  workspaceFile(
    sessionId: string,
    ownerDeviceId: string,
    path: string,
    at?: 'worktree' | 'head',
  ): Promise<WorkspaceFileOutcome>;
  /** Reads the diff of one changed path of the owner's working tree against `HEAD`. */
  workspaceDiff(
    sessionId: string,
    ownerDeviceId: string,
    path: string,
  ): Promise<WorkspaceDiffOutcome>;
  devices(): DeviceInfo[];
  /**
   * Starts a session in a watched project of a connected device (docs/spec.md#new-session). Resolves when the
   * agent has created and imported the session; a repeated `promptId` returns the first result.
   */
  startSession(request: StartRequest): Promise<StartOutcome>;
  /**
   * Moves a session to another connected device (docs/spec.md#handoff). Resolves when the handoff is over,
   * with the new owner or the reason it failed; a failure leaves owner and cursor unchanged.
   */
  handoff(request: HandoffRequest): Promise<HandoffOutcome>;
  /** Lists the directories of `path` under the browse root of a connected device. */
  listDirs(deviceId: string, path: string): Promise<DirsOutcome>;
  /** Uploads an image for a coming prompt to a connected device on the bulk lane (ADR 0017). */
  putAttachment(deviceId: string, image: PromptImage): Promise<AttachmentOutcome>;
  /** Names the clone among a connected device's watched projects whose remote `origin` is `origin`. */
  findClone(deviceId: string, origin: string): Promise<CloneOutcome>;
  listProjects(deviceId: string): Promise<DeviceControlOutcome>;
  addProject(deviceId: string, path: string): Promise<DeviceControlOutcome>;
  removeProject(deviceId: string, path: string): Promise<DeviceControlOutcome>;
  importSessions(
    deviceId: string,
    path: string,
    sessionIds: string[],
  ): Promise<DeviceControlOutcome>;
  removeSessions(
    deviceId: string,
    path: string,
    sessionIds: string[],
  ): Promise<DeviceControlOutcome>;
}

/** What agent ingestion tells control: which device is on which link, and the answers it relays. */
export interface AgentControl {
  /** The bulk queue: sync appends run through it; control messages are handled on arrival. */
  readonly bulk: BulkQueue;
  /** The link carries `deviceId` from now on; an older link of the same device is replaced. */
  attach(deviceId: string, link: DeviceLink, providers?: ProviderInfo[]): void;
  detach(link: DeviceLink): void;
  /** Hands an event for the browsers to whoever subscribed. */
  publish(msg: ServerMessage): void;
  receive(link: DeviceLink, msg: ControlReply): void;
}

export interface ControlOptions {
  /** Bulk work in flight and waiting; the defaults suit one operator. */
  bulk?: BulkQueueOptions;
  promptTimeoutMs?: number;
  /** How long a new session may take to start on the agent. */
  startTimeoutMs?: number;
  /** The clock prompt deduplication and permission requests expire by. */
  now?: () => number;
  workspaceTimeoutMs?: number;
  /** How long an image upload may wait for the device, behind bulk work. */
  attachmentTimeoutMs?: number;
  /** How long one handoff step waits for an agent's answer. */
  handoffTimeoutMs?: number;
  /** How long the transfer of the snapshot and the session stream to the target may take. */
  transferTimeoutMs?: number;
}
