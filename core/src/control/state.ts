import type {
  AgentRuntimeState,
  Command,
  Provider,
  ProviderInfo,
  ServerMessage,
} from '@hoplet/shared';
import type { Store } from '../store/index.ts';
import type { ActiveHandoff } from './handoff.ts';
import type { Waiter } from './pending.ts';
import type { OpenPermission, RuntimeEntry } from './runtime.ts';
import type {
  AttachmentOutcome,
  CloneOutcome,
  ControlEvent,
  DeviceControlOutcome,
  DeviceLink,
  DirsOutcome,
  InstalledInfo,
  PreparedInfo,
  PromptOutcome,
  StartAnswer,
  StartOutcome,
  SteerOutcome,
  StepOutcome,
  VerifiedInfo,
  WorkspaceDiffOutcome,
  WorkspaceFileOutcome,
  WorkspaceOutcome,
} from './types.ts';

/** Everything the control concerns share: who is online, and the requests awaiting an agent's answer. */
export interface ControlState {
  store: Store;
  byDevice: Map<string, DeviceLink>;
  /** Counts how often a device has attached, so handoff can tell a reconnect from an absence. */
  deviceGenerations: Map<string, number>;
  /** Prompts forwarded and not yet taken by the agent, keyed `<sessionId>\n<promptId>`. */
  pendingPrompt: Map<string, Waiter<PromptOutcome>>;
  /** Results of prompts by `<sessionId>\n<promptId>`, repeated for 10 minutes. */
  promptCache: Map<string, { at: number; outcome: Promise<PromptOutcome> }>;
  /** The providers each connected agent announced in `device.hello`. */
  providers: Map<string, ProviderInfo[]>;
  /** Steers forwarded and not yet taken by the agent, keyed `<sessionId>\n<steerId>`. */
  pendingSteer: Map<string, Waiter<SteerOutcome>>;
  /** New sessions forwarded and not yet answered, by request ID. */
  pendingStart: Map<string, Waiter<StartAnswer>>;
  /** Results of new-session requests by `promptId`, repeated for 10 minutes. */
  startCache: Map<string, { at: number; outcome: Promise<StartOutcome> }>;
  /**
   * Sessions an agent started that the store does not know yet: runtime messages of that device are accepted,
   * and the provider they were started with is their vendor.
   */
  startedSessions: Map<string, { deviceId: string; provider: Provider; at: number }>;
  /** The state of each session's process as its owner reported it. */
  runtime: Map<string, RuntimeEntry>;
  /** The commands and skills each session offered in its latest report, with who reported them. */
  commands: Map<string, { deviceId: string; commands: Command[] }>;
  /** Open permission requests by `<sessionId>\n<requestId>`. */
  permissions: Map<string, OpenPermission>;
  /** Hands an event for the browsers to whoever subscribed. */
  publish: (msg: ServerMessage) => void;
  now: () => number;
  /** Hands a moment that may notify to whoever listens. */
  emit: (event: ControlEvent) => void;
  /** Set once the queue exists: a session's runtime state changed (ADR 0020). */
  runtimeChanged: (sessionId: string, state: AgentRuntimeState, error?: string) => void;
  pendingWs: Map<string, Waiter<WorkspaceOutcome>>;
  pendingFile: Map<string, Waiter<WorkspaceFileOutcome>>;
  pendingDiff: Map<string, Waiter<WorkspaceDiffOutcome>>;
  /** The handoff in progress for each session, by session ID. */
  handoffs: Map<string, ActiveHandoff>;
  /** Answers to handoff steps, by handoff ID. */
  pendingPrepared: Map<string, Waiter<StepOutcome<PreparedInfo>>>;
  pendingInstalled: Map<string, Waiter<StepOutcome<InstalledInfo>>>;
  pendingVerified: Map<string, Waiter<StepOutcome<VerifiedInfo>>>;
  pendingDeviceControl: Map<string, Waiter<DeviceControlOutcome>>;
  pendingDirs: Map<string, Waiter<DirsOutcome>>;
  pendingClone: Map<string, Waiter<CloneOutcome>>;
  pendingAttachments: Map<string, Waiter<AttachmentOutcome>>;
  promptTimeoutMs: number;
  startTimeoutMs: number;
  workspaceTimeoutMs: number;
  attachmentTimeoutMs: number;
  handoffTimeoutMs: number;
  transferTimeoutMs: number;
}

export function onlineLink(state: ControlState, deviceId: string): DeviceLink | null {
  const link = state.byDevice.get(deviceId);
  return link?.isOpen() ? link : null;
}
