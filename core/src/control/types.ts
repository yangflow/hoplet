import type {
  BudgetRef,
  ClientMessage,
  CloneCheckReason,
  CloneDestinationDetail,
  CloneFailureReason,
  Command,
  ConfigSetting,
  DeleteSessionRequest,
  DeletionPreview,
  DeletionStep,
  DeviceDirsResult,
  DeviceInfo,
  DeviceProject,
  DeviceResourceSample,
  DeviceSessionDeleted,
  DeviceSessionForgotten,
  DeviceSessionInspected,
  DeviceSessionPoint,
  HookEnvelope,
  HookResult,
  HttpErrorCode,
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
  SessionPortsResult,
  SessionResources,
  SessionRule,
  SessionRulesUnenforced,
  TerminalInfo,
  WorkspaceDiff,
  WorkspaceFile,
  WorkspaceListing,
} from '@hoplet/shared';
import type { BulkQueue, BulkQueueOptions } from './bulk.ts';

/** What control holds of a machine's resource reports: the ring, oldest first, and the latest report's sessions. */
export interface ResourcesSnapshot {
  samples: DeviceResourceSample[];
  sessions: SessionResources[];
  /** Each session's readings over the window of `samples`, oldest first, by session ID. */
  history: Map<string, DeviceSessionPoint[]>;
}

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
  /** The provider configuration to start with (ADR 0028); the vendor's own when absent. */
  configuration?: string | undefined;
  config?: SessionConfig | undefined;
  text: string;
  attachments?: string[] | undefined;
  promptId: string;
  /** Where the request came from, told to lifecycle hooks: a plan approved into a new session is `plan`. */
  origin?: 'new' | 'plan' | undefined;
}

/** What an agent answers to `runtime.start`, or why there is no answer. */
export type StartAnswer =
  | { ok: true; sessionId: string }
  | { ok: false; error: 'target-offline' | 'timeout' | 'rejected'; reason?: string };

export type StartOutcome =
  | { ok: true; sessionId: string; promptId: string }
  | {
      ok: false;
      error: 'target-offline' | 'invalid-request' | 'timeout' | 'rejected' | 'over-budget';
      promptId: string;
      reason?: string;
      /** The budget that refused the session (`over-budget`). */
      budget?: BudgetRef;
    };

/** What `POST /api/sessions/:id/permission/fresh` asks for (ADR 0025). */
export interface FreshRequest {
  sessionId: string;
  requestId: string;
  mode: string;
  promptId: string;
}

export type FreshOutcome =
  | { ok: true; sessionId: string; promptId: string }
  | {
      ok: false;
      error:
        | 'session-not-found'
        | 'owner-offline'
        | 'request-closed'
        | 'invalid-request'
        | 'target-offline'
        | 'timeout'
        | 'rejected'
        | 'over-budget';
      promptId: string;
      reason?: string;
      budget?: BudgetRef;
    };

export type PromptOutcome =
  | { ok: true; promptId: string }
  | {
      ok: false;
      error:
        | 'owner-offline'
        | 'external'
        | 'starting'
        | 'busy'
        | 'timeout'
        | 'rejected'
        | 'over-budget';
      promptId: string;
      reason?: string;
      /** The budget that refused the prompt (`over-budget`). */
      budget?: BudgetRef;
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
  /** Who answered, for the audit log: the host (default) or an action link. */
  by?: 'operator' | 'link' | undefined;
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
  /** Clone the repository here on the target first (ADR 0024); exclusive with `targetPath`. */
  cloneInto?: string | undefined;
  /** The target's provider configuration the session is installed for; the vendor's own when absent. */
  configuration?: string | undefined;
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
        | 'clone-failed'
        | 'session-changed'
        | 'verify-failed'
        | 'cancelled'
        | 'timeout'
        | 'rejected'
        | 'internal';
      reason?: string;
      /** The text of a failed `git`, credentials removed; only with `clone-failed`. */
      detail?: string;
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
  /** The clone the worktree belongs to, on the target. */
  clone: string;
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

/** What a provider configuration update changes; a field left out stays. */
export interface ProviderChange {
  id: string;
  name?: string | undefined;
  enabled?: boolean | undefined;
  defaults?: SessionConfig | undefined;
}

/** Outcome of a provider configuration update: the machine's whole list afterwards, or why not. */
export type ProvidersOutcome =
  | { ok: true; providers: ProviderInfo[] }
  | { ok: false; error: 'owner-offline' | 'timeout' | 'rejected'; reason?: string };

/** What the owner agent answered to a device.session.* request. */
export type SessionDeviceOutcome =
  | { ok: true; reply: DeviceSessionInspected | DeviceSessionDeleted | DeviceSessionForgotten }
  | { ok: false; error: 'owner-offline' | 'timeout' | 'rejected'; reason?: string };

/** Outcome of the preview of a deletion. */
export type DeletionPreviewOutcome =
  | { ok: true; preview: DeletionPreview }
  | {
      ok: false;
      error: 'session-not-found' | 'owner-offline' | 'timeout' | 'rejected';
      reason?: string;
    };

/** Outcome of a deletion; `delete-partial` carries the steps that ran, and the core copy is kept. */
export type DeleteSessionOutcome =
  | { ok: true; steps: DeletionStep[] }
  | {
      ok: false;
      error: 'session-not-found' | 'session-running' | 'owner-offline' | 'timeout' | 'rejected';
      reason?: string;
    }
  | { ok: false; error: 'delete-partial'; steps: DeletionStep[] };

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

/** The answer to a device.clone.check round-trip: whether the target can clone the origin, and where. */
export type CloneCheckOutcome =
  | { ok: true; reason: CloneCheckReason; detail?: CloneDestinationDetail; cloneInto: string }
  | { ok: false; error: 'target-offline' | 'timeout' };

/** How a clone on the target ended: where the clone is, or why there is none. */
export type CloneRunOutcome =
  | { ok: true; path: string }
  | {
      ok: false;
      error: 'timeout' | 'offline' | 'failed';
      reason?: CloneFailureReason;
      detail?: string | undefined;
    };

/** What cancelling a handoff comes to: it stops only while the handoff is cloning. */
export type HandoffCancelOutcome = { ok: true } | { ok: false; error: 'handoff-not-running' };

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
  | { kind: 'turn-done'; sessionId: string; turn?: number; durationMs?: number }
  | {
      kind: 'failure';
      source: 'start' | 'handoff';
      sessionId?: string;
      projectPath?: string;
      /** The machine a failed start was meant for. */
      deviceId?: string;
      error: string;
    }
  | LifecycleEvent;

/** Who closed a permission request, as lifecycle hooks name it (ADR 0031). */
export type PermissionActor =
  | { kind: 'user' }
  | { kind: 'action-link' }
  | { kind: 'rule'; id: string }
  | { kind: 'system' };

/**
 * The moments only lifecycle hooks listen to (ADR 0031; notifications ignore them). `deviceId` and `projectPath`
 * are what the emitter knew when the store may not know the session yet or any more.
 */
export type LifecycleEvent =
  | {
      kind: 'started';
      sessionId: string;
      deviceId: string;
      projectPath: string;
      provider: Provider;
      configuration?: string;
      origin: 'new' | 'plan';
    }
  | {
      kind: 'ended';
      sessionId: string;
      reason: 'stopped' | 'idle' | 'deleted';
      deviceId?: string;
      projectPath?: string;
    }
  | {
      kind: 'resolved';
      sessionId: string;
      requestId: string;
      resolution: 'allow' | 'deny' | 'expired' | 'abandoned';
      actor: PermissionActor;
    }
  | { kind: 'runtime-error'; sessionId: string; code: string; reason?: string }
  | {
      kind: 'handoff-started';
      sessionId: string;
      handoffId: string;
      from: string;
      to: string;
    }
  | { kind: 'handoff-done'; sessionId: string; handoffId: string; from: string; to: string };

/** The browser WebSockets that have a session open (ADR 0021). */
export interface Viewing {
  /** A browser subscribed to the session. */
  add(sessionId: string): void;
  /** A browser unsubscribed, or its socket closed. */
  remove(sessionId: string): void;
  /** Whether at least one browser is subscribed to the session. */
  isOpen(sessionId: string): boolean;
}

/** A browser connection attached to a terminal: what control needs to deliver output and pace the agent. */
export interface TerminalSink {
  send(msg: ServerMessage): void;
  /** Bytes the connection has accepted and not yet written; the agent is paced by it. */
  buffered(): number;
}

export type TerminalOpenOutcome =
  | { ok: true; terminal: TerminalInfo }
  | { ok: false; error: HttpErrorCode; reason?: string };

/** The restricted terminals of the owner machines, relayed to browsers (ADR 0026). Nothing here is stored. */
export interface Terminals {
  /** Asks the owner to open a terminal; resolves when it is running or has been refused. */
  open(
    sessionId: string,
    ownerDeviceId: string,
    cols: number,
    rows: number,
  ): Promise<TerminalOpenOutcome>;
  /** The open terminal of a session, or null. */
  ofSession(sessionId: string): TerminalInfo | null;
  /** The session of an open terminal, or null. */
  sessionOf(terminalId: string): string | null;
  /** Attaches the sink and sends it `terminal.attached` and the recent output; false when the terminal is not open. */
  attach(terminalId: string, sink: TerminalSink): boolean;
  /** Detaches the sink; the terminal closes 60 seconds after its last sink left. */
  detach(terminalId: string, sink: TerminalSink): void;
  /** Writes keystrokes (base64) from an attached sink; ignored for a sink that is not attached. */
  input(terminalId: string, sink: TerminalSink, data: string): void;
  resize(terminalId: string, sink: TerminalSink, cols: number, rows: number): void;
  /** Closes the terminal (reason `closed`); false when none is open. */
  close(terminalId: string): boolean;
}

/** What the REST API asks of control. */
export interface Control {
  /** The restricted terminals (ADR 0026). */
  readonly terminals: Terminals;
  /** The ports last reported for a session and the host to link them with (ADR 0026). */
  portsOf(sessionId: string): SessionPortsResult;
  /** The machine's last hour of readings and its sessions' usage from the latest one; empty for a machine that never reported. */
  resourcesOf(deviceId: string): ResourcesSnapshot;
  /**
   * Sends the named devices (every online device when none are named) the permission rules they do not yet have
   * (ADR 0029). The API calls it after a change to the rules, a handoff or a deletion.
   */
  syncRules(deviceIds?: readonly string[]): void;
  /** The revision of the rules the device last applied since it connected; null before it answers. */
  appliedRulesRevision(deviceId: string): number | null;
  /** What the core knows of the rules on a device: the revision it confirmed and when, the last push, and whether a resend is pending. */
  rulesStatus(deviceId: string): {
    applied: number | null;
    appliedAt: number | null;
    pushedAt: number | null;
    retrying: boolean;
  };
  /** Sends a connected device its whole set of rules again; false when it is not connected. */
  pushRulesAgain(deviceId: string): boolean;
  /** Sends a lifecycle event to a script hook on the agent; false when the device is not connected. */
  sendHookEvent(deviceId: string, hookId: string, event: HookEnvelope): boolean;
  /** Calls `listener` with every result an agent reports for a script hook; returns the unsubscribe. */
  onHookResult(listener: (deviceId: string, result: HookResult) => void): () => void;
  /** Calls `listener` with the devices whose link just dropped; returns the unsubscribe. */
  onDevicesGone(listener: (deviceIds: string[]) => void): () => void;
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
  /** The owner device of a session, also of one an agent just started; `null` for an unknown session. */
  ownerOf(sessionId: string): string | null;
  /** The commands and skills the session's owner last reported, or null when it reported none. */
  commandsOf(sessionId: string): Command[] | null;
  /** The permission requests of a session that wait for an answer. */
  openPermissions(sessionId: string): SessionPermission[];
  /** The latest rule decisions of a session, oldest first (ADR 0029). */
  ruleLinesOf(sessionId: string): (SessionRule | SessionRulesUnenforced)[];
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
   * Approves an open plan request into a new session (ADR 0025): starts the session from the plan and, only when
   * that succeeds, records the link, refuses the plan and interrupts the old turn. Nothing changes when the start
   * fails; a repeated `promptId` returns the first result.
   */
  approvePlanFresh(request: FreshRequest): Promise<FreshOutcome>;
  /**
   * Moves a session to another connected device (docs/spec.md#handoff). Resolves when the handoff is over,
   * with the new owner or the reason it failed; a failure leaves owner and cursor unchanged.
   */
  handoff(request: HandoffRequest): Promise<HandoffOutcome>;
  /** Stops a handoff of the session that is in its clone step; the session stays with its owner (ADR 0024). */
  cancelHandoff(sessionId: string): HandoffCancelOutcome;
  /** Lists the directories of `path` under the browse root of a connected device. */
  listDirs(deviceId: string, path: string): Promise<DirsOutcome>;
  /** Uploads an image for a coming prompt to a connected device on the bulk lane (ADR 0017). */
  putAttachment(deviceId: string, image: PromptImage): Promise<AttachmentOutcome>;
  /** Names the clone among a connected device's watched projects whose remote `origin` is `origin`. */
  findClone(deviceId: string, origin: string): Promise<CloneOutcome>;
  /** Asks a connected device whether it can clone `origin` into `cloneInto`, or where it would (ADR 0024). */
  checkClone(deviceId: string, origin: string, cloneInto?: string): Promise<CloneCheckOutcome>;
  /** Changes one provider configuration on a connected device (ADR 0028); resolves with the device's new list. */
  updateProvider(deviceId: string, change: ProviderChange): Promise<ProvidersOutcome>;
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
  /** What deleting the session would remove: asks the owner agent when it is online (ADR 0023). */
  deletionPreview(sessionId: string): Promise<DeletionPreviewOutcome>;
  /** Deletes the session: the core copy always, the vendor file and the worktree when asked for. */
  deleteSession(sessionId: string, request: DeleteSessionRequest): Promise<DeleteSessionOutcome>;
}

/** What an agent announced in `device.hello` besides its providers (ADR 0026). */
export interface DeviceHello {
  capabilities?: { terminal?: boolean | undefined } | undefined;
  previewHost?: string | undefined;
}

/** What agent ingestion tells control: which device is on which link, and the answers it relays. */
export interface AgentControl {
  /** The bulk queue: sync appends run through it; control messages are handled on arrival. */
  readonly bulk: BulkQueue;
  /** The link carries `deviceId` from now on; an older link of the same device is replaced. */
  attach(deviceId: string, link: DeviceLink, providers?: ProviderInfo[], hello?: DeviceHello): void;
  detach(link: DeviceLink): void;
  /** The device said hello: sends it its whole permission rule set (ADR 0029). */
  announceRules(deviceId: string): void;
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
  /** How long a clone on the target may take in all (ADR 0024). */
  cloneTimeoutMs?: number;
  /** How long a clone may go without progress from the target's git. */
  cloneStallMs?: number;
  /** How long a push of permission rules waits for the agent's confirmation before it is sent again. */
  rulesRetryMs?: number;
  /** How long a terminal with no browser attached stays open. */
  terminalDetachMs?: number;
}
