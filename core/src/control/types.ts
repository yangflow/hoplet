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
  SessionConfigChange,
  SessionConfigChangeCreate,
  SessionHandoffRequest,
  SessionMcpProjectDefault,
  SessionMcpSettings,
  SessionPermission,
  SessionPortsResult,
  SessionResources,
  SessionRule,
  SessionRulesUnenforced,
  TerminalInfo,
  TokenGrant,
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

/**
 * Who an action is recorded under in the audit log when it is not the host: the token that made the call, or, for
 * a session's own token (ADR 0034), the session.
 */
export type AuditActor = { kind: 'token'; id: string } | { kind: 'session'; id: string };

/** The limits on what sessions do to other sessions (ADR 0034); each is per source session. */
export interface SessionLimits {
  /** Prompts to other sessions per minute. */
  promptsPerMinute: number;
  /** Prompts to other sessions per hour. */
  promptsPerHour: number;
  /** Prompts to one target per minute. */
  perTargetPerMinute: number;
  /** Prompts from one source that one target has not yet finished a turn on. */
  unansweredPerTarget: number;
  /** Sessions one session starts per hour. */
  startsPerHour: number;
  /** Sessions one session started that are still live (ADR 0035). */
  liveChildren: number;
  /** How many sessions deep below the root a tree may reach: a root's child is depth 1. */
  childDepth: number;
  /** Live sessions in one tree below its root. */
  liveTree: number;
  /** How long a child may run unless its parent asks for less or more, in milliseconds. */
  childTimeMs: number;
  /** The most a parent may ask a child to run, in milliseconds. */
  childTimeMaxMs: number;
  /** Input plus output tokens (cache reads excluded) one tree may spend below its root. */
  treeTokens: number;
  /** Hand-off requests one session may have approved automatically per hour. */
  autoApprovedHandoffsPerHour: number;
  /** Hand-off requests one session may make per hour, the approved ones and the rest. */
  handoffRequestsPerHour: number;
}

/** Why a session may not prompt another; `forbidden` carries the reason. */
export type SessionGuardRefusal =
  | { ok: false; error: 'self' | 'loop-limit' | 'depth-limit' | 'children-limit' }
  | { ok: false; error: 'forbidden'; reason: string }
  | { ok: false; error: 'budget-exceeded'; budget?: BudgetRef }
  | { ok: false; error: 'rate-limited'; retryAfter: number };

/** The verdict on one session prompting another. When allowed, `settle` tells whether the prompt was accepted. */
export type SessionGuard = { ok: true; settle(accepted: boolean): void } | SessionGuardRefusal;

/**
 * The verdict on one session starting another. When allowed, `config` is the setting the new session starts with
 * (its permission mode filled in or checked), `chain` the provenance it gets, and `settle` tells whether the
 * start went through.
 */
export type SessionStartGuard =
  | {
      ok: true;
      config: SessionConfig;
      chain: string[];
      /** The sessions that started the one that asks, root first: what the new session's creation chain extends. */
      lineage: string[];
      /** How long the new session may run, in milliseconds. */
      timeLimitMs: number;
      settle(accepted: boolean): void;
    }
  | SessionGuardRefusal;

/** What a minted session token comes to. */
export interface SessionTokenMint {
  secret: string;
  expiresAt: number;
}

/** Sessions that direct sessions (#67, ADR 0034). */
export interface SessionMcp {
  /** The settings of a session; resolved from the project default, else the global default, else read only. */
  settingsOf(sessionId: string): SessionMcpSettings;
  /** Replaces a session's settings (the host's change); the grant of its token follows at the next request. */
  setSettings(sessionId: string, settings: SessionMcpSettings): SessionMcpSettings;
  globalDefault(): SessionMcpSettings;
  projectDefaults(): SessionMcpProjectDefault[];
  setGlobalDefault(settings: SessionMcpSettings): SessionMcpSettings;
  setProjectDefault(
    deviceId: string,
    path: string,
    settings: SessionMcpSettings,
  ): SessionMcpSettings;
  /** Removes a project's default; false when there was none. */
  removeProjectDefault(deviceId: string, path: string): boolean;
  /**
   * What the session's token may do now, looked up from its current settings; null when the session is gone.
   * `answer` is never part of it.
   */
  grantOf(sessionId: string): TokenGrant | null;
  /** Mints the token of the session's process; null when the session is unknown or its grant would hold `answer`. */
  mintToken(sessionId: string): SessionTokenMint | null;
  /** Ends every token of the session. */
  revokeTokens(sessionId: string): void;
  /** Checks `source` prompting `target` against the mode rule, the chain and the limits, and reserves the prompt. */
  guardPrompt(source: string, target: string): SessionGuard;
  /**
   * Checks `source` starting a session with `request`: the chain, the limits on starts and live children, and a
   * permission mode that is not more permissive than the source's (filled in when the request names none).
   */
  guardStart(
    source: string,
    request: Pick<
      StartRequest,
      'deviceId' | 'projectPath' | 'provider' | 'configuration' | 'config'
    > & {
      /** How long the new session may run, in milliseconds; the default when absent. */
      timeLimitMs?: number | undefined;
    },
  ): SessionStartGuard;
  /** The same checks without the chain and the limits, for an interrupt; null when allowed. */
  guardInterrupt(source: string, target: string): SessionGuardRefusal | null;
  /** Stops the sweep and the reconnection timers. */
  close(): void;
}

/** Why a session's hand-off request was not made. */
export type SessionHandoffRefusal =
  | { ok: false; error: 'not-found' | 'same-device' | 'target-offline' | 'loop-limit' }
  | { ok: false; error: 'forbidden'; reason: string }
  | { ok: false; error: 'rate-limited'; retryAfter: number };

/** Hand-off requests of sessions (#67, ADR 0034). */
export interface SessionHandoffRequests {
  /**
   * `sessionId` asks to be handed off to `targetDeviceId`: pending until the user answers, or approved at once when
   * the session's settings and the hourly limit allow. The handoff itself waits for the end of the turn.
   */
  request(
    sessionId: string,
    targetDeviceId: string,
    reason: string,
  ): { ok: true; request: SessionHandoffRequest } | SessionHandoffRefusal;
  /** A session's requests, newest first. */
  list(sessionId: string): SessionHandoffRequest[];
  get(requestId: string): SessionHandoffRequest | null;
  /** The user's answer to a pending request; `request-closed` once it was answered, expired or moved on. */
  decide(
    requestId: string,
    approve: boolean,
    actor?: AuditActor,
  ):
    | { ok: true; request: SessionHandoffRequest }
    | { ok: false; error: 'not-found' | 'request-closed' };
  /** Stops the expiry timers. */
  close(): void;
}

/** An object of a change request that is a file of the project (a `config` object is not). */
export interface ConfigFileObject {
  kind: 'memory' | 'skill' | 'command';
  name: string;
  /** The proposed content; null deletes the file. */
  content: string | null;
}

/** What the owner's agent says about the files of a proposal before the user sees it (ADR 0036). */
export type ConfigFilePreview =
  | {
      ok: true;
      objects: {
        kind: ConfigFileObject['kind'];
        name: string;
        /** SHA-256 (hex) of the file as it is now; empty when it does not exist. */
        currentHash: string;
        /** The file's content now; null when it does not exist. */
        previous: string | null;
        /** A unified diff of the file as the agent read it against the proposed content. */
        diff: string;
      }[];
    }
  | {
      ok: false;
      error: 'owner-offline' | 'unsupported' | 'unsafe-content' | 'rejected' | 'timeout';
      reason?: string;
    };

/** What the owner's agent says about writing the files of an approved change. */
export type ConfigFileApply =
  | { ok: true }
  | {
      ok: false;
      error: 'owner-offline' | 'unsupported' | 'unsafe-content' | 'stale' | 'timeout' | 'failed';
      reason?: string;
    };

/**
 * Where a change request meets the files of a project (ADR 0036): the owner's agent previews the files of a
 * proposal and writes them after approval. The core never reads or writes a project file itself.
 */
export interface ConfigFiles {
  preview(
    deviceId: string,
    sessionId: string,
    objects: ConfigFileObject[],
  ): Promise<ConfigFilePreview>;
  /** Writes all objects or none; each is written only if the file still has `baseHash`. */
  apply(
    deviceId: string,
    input: {
      changeId: string;
      sessionId: string;
      objects: (ConfigFileObject & { baseHash: string; newHash: string })[];
    },
  ): Promise<ConfigFileApply>;
}

/** Why a session's change request was not made. */
export type SessionConfigChangeRefusal =
  | {
      ok: false;
      error:
        | 'not-found'
        | 'owner-offline'
        | 'unsupported'
        | 'unsafe-content'
        | 'stale'
        | 'too-large'
        | 'timeout'
        | 'rejected'
        | 'external';
      reason?: string;
    }
  | { ok: false; error: 'forbidden' | 'invalid-request'; reason: string };

/** Changes of sessions to their own configuration (#69, ADR 0036). */
export interface SessionConfigChanges {
  /**
   * `sessionId` proposes changes to its own configuration: previewed by the owner's agent, then `pending` until the
   * user decides. Nothing is written before that.
   */
  propose(
    sessionId: string,
    input: SessionConfigChangeCreate,
  ): Promise<{ ok: true; change: SessionConfigChange } | SessionConfigChangeRefusal>;
  /** A session's requests, newest first. */
  list(sessionId: string): SessionConfigChange[];
  get(changeId: string): SessionConfigChange | null;
  /**
   * The user's answer to a pending request; `request-closed` once it was answered or has expired. An approval
   * returns at once with the request `approved`; it is written when the owner is online.
   */
  decide(
    changeId: string,
    approve: boolean,
    actor?: AuditActor,
  ):
    | { ok: true; change: SessionConfigChange }
    | { ok: false; error: 'not-found' | 'request-closed' };
  /** Undoes an applied change; the files are written back only if they still have the hash the change produced. */
  undo(
    changeId: string,
    actor?: AuditActor,
  ): Promise<
    | { ok: true; change: SessionConfigChange }
    | {
        ok: false;
        error:
          | 'not-found'
          | 'request-closed'
          | 'busy'
          | 'owner-offline'
          | 'stale'
          | 'unsupported'
          | 'rejected';
        reason?: string;
      }
  >;
  /** Drops the previous content of changes applied more than 30 days ago; returns how many lost it. */
  prune(): number;
  /** Stops the timers. */
  close(): void;
}

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
  /** Run the session in a new git worktree; `branch` names it, absent the agent generates one. */
  worktree?: { branch?: string | undefined } | undefined;
  /** Where the request came from, told to lifecycle hooks: a plan approved into a new session is `plan`. */
  origin?: 'new' | 'plan' | undefined;
  /** The token that asked, for the audit log; the host when absent. */
  actor?: AuditActor | undefined;
  /**
   * Set when a session started this one (ADR 0034): the agent names it before the prompt, the core records the
   * provenance `chain`. ADR 0035: `lineage` is the creation chain of the starter (root first), which the new
   * session's own extends, and `timeLimitMs` how long it may run; the tree watcher enforces it.
   */
  source?:
    | { sessionId: string; chain: string[]; lineage: string[]; timeLimitMs: number }
    | undefined;
}

/** What an agent answers to `runtime.start`, or why there is no answer. */
export type StartAnswer =
  | { ok: true; sessionId: string; worktree?: StartedWorktree }
  | { ok: false; error: 'target-offline' | 'timeout' | 'rejected'; reason?: string };

/** The worktree an agent made for a new session. */
export interface StartedWorktree {
  clone: string;
  path: string;
  branch: string;
}

export type StartOutcome =
  | { ok: true; sessionId: string; promptId: string; worktree?: StartedWorktree }
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
  /** The token that asked, for the audit log; the host when absent. */
  actor?: AuditActor | undefined;
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
  /** The token that answered, for the audit log; the host when absent. */
  actor?: AuditActor | undefined;
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
  /** A session asked the user to hand it off to `targetName` (ADR 0034); a request approved automatically is not one. */
  | { kind: 'handoff-requested'; sessionId: string; requestId: string; targetName: string }
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
    actor?: AuditActor,
  ): Promise<PromptOutcome>;
  /** Answers an open permission request; the first answer wins. */
  answerPermission(
    sessionId: string,
    ownerDeviceId: string,
    requestId: string,
    answer: PermissionAnswer,
  ): PermissionOutcome;
  interrupt(sessionId: string, ownerDeviceId: string, actor?: AuditActor): RuntimeCommandOutcome;
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
    input: { id: string; text: string; attachments: string[]; actor?: AuditActor | undefined },
  ): QueueResult;
  /** Replaces a waiting message that is still at `version`. */
  queueEdit(
    sessionId: string,
    id: string,
    version: number,
    input: { text: string; attachments: string[]; actor?: AuditActor | undefined },
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
    actor?: AuditActor,
  ): Promise<QueueSteerOutcome>;
  /** Forwards a message to the running turn, which takes it at its next tool gap; a repeated `steerId` returns the first result. */
  steer(
    sessionId: string,
    ownerDeviceId: string,
    steerId: string,
    text: string,
    attachments?: string[],
    actor?: AuditActor,
  ): Promise<SteerOutcome>;
  /** The runtime state of a session; `offline` when its owner is not connected. */
  runtimeOf(sessionId: string): RuntimeView;
  /** The vendor of a session, also of one an agent just started; `null` for an unknown session. */
  providerOf(sessionId: string): Provider | null;
  /** The owner device of a session, also of one an agent just started; `null` for an unknown session. */
  ownerOf(sessionId: string): string | null;
  /** Sessions that direct sessions: settings, grants, session tokens, provenance and limits (ADR 0034). */
  readonly sessionMcp: SessionMcp;
  /** Requests of sessions to be handed off to another machine (ADR 0034). */
  readonly handoffRequests: SessionHandoffRequests;
  /** Changes of sessions to their own configuration (ADR 0036). */
  readonly configChanges: SessionConfigChanges;
  /** Looks at the trees of work once for sessions past a limit and stops them (ADR 0035); the timer calls it every 30 seconds. */
  checkTrees(): void;
  /** The project directory of a session, also of one an agent just started; `null` for an unknown session. */
  projectOf(sessionId: string): string | null;
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
  capabilities?: { terminal?: boolean | undefined; sessionMcp?: boolean | undefined } | undefined;
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
  /** Where change requests meet project files; the default answers `unsupported` (ADR 0036). */
  configFiles?: ConfigFiles;
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
  /** Overrides of the limits on sessions that prompt other sessions (ADR 0034). */
  sessionLimits?: Partial<SessionLimits>;
  /** How long an agent may stay disconnected before its sessions' tokens are revoked. */
  sessionReconnectMs?: number;
  /** Schedules the tree watcher (ADR 0035); `fn` runs every `ms` until the returned function is called. */
  treeEvery?: (fn: () => void, ms: number) => () => void;
}
