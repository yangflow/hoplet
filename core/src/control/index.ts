// Control: device RPC with timeouts and the handoff steps, between the REST API and agent ingestion
// (docs/architecture.md#components). It never sees a socket; ingest hands it `DeviceLink`s.

import { PROTOCOL_VERSION, type ServerMessage } from '@hoplet/shared';
import { log } from '../log/index.ts';
import type { Store } from '../store/index.ts';
import { createBulkQueue } from './bulk.ts';
import { createDeletion } from './deletion.ts';
import { createFresh } from './fresh.ts';
import { createHandoff } from './handoff.ts';
import { createPorts } from './ports.ts';
import { createQueue } from './queue.ts';
import { receiveReply } from './replies.ts';
import { createResources } from './resources.ts';
import { createRpc } from './rpc.ts';
import { createRules } from './rules.ts';
import { createRuntime } from './runtime.ts';
import { createStart } from './start.ts';
import type { ControlState } from './state.ts';
import { createTerminals } from './terminals.ts';
import type { AgentControl, Control, ControlEvent, ControlOptions, Viewing } from './types.ts';

export { BulkBusyError, type BulkQueue } from './bulk.ts';
export type {
  AgentControl,
  Control,
  ControlEvent,
  ControlOptions,
  ControlReply,
  DeleteSessionOutcome,
  DeletionPreviewOutcome,
  DeviceControlOutcome,
  DeviceLink,
  FreshOutcome,
  FreshRequest,
  HandoffOutcome,
  HandoffRequest,
  PermissionOutcome,
  PromptOutcome,
  QueueResult,
  QueueSteerOutcome,
  RuntimeCommandOutcome,
  RuntimeView,
  StartAnswer,
  StartOutcome,
  StartRequest,
  SteerOutcome,
  TerminalOpenOutcome,
  TerminalSink,
  Terminals,
  Viewing,
  WorkspaceDiffOutcome,
  WorkspaceFileOutcome,
  WorkspaceOutcome,
} from './types.ts';

/** Handoff results that are a refusal of the request or the user's own cancel, not a failure to tell about. */
const HANDOFF_REFUSALS: ReadonlySet<string> = new Set([
  'session-not-found',
  'same-device',
  'handoff-in-progress',
  'busy',
  'cancelled',
]);

const DEFAULT_BULK = { concurrency: 4, maxWaiting: 64 };
const DEFAULT_PROMPT_TIMEOUT_MS = 20_000;
const DEFAULT_START_TIMEOUT_MS = 75_000;
const DEFAULT_WORKSPACE_TIMEOUT_MS = 8_000;
/** An upload waits behind at most the bulk messages already queued for the device. */
const DEFAULT_ATTACHMENT_TIMEOUT_MS = 60_000;
const DEFAULT_HANDOFF_TIMEOUT_MS = 120_000;
const DEFAULT_TRANSFER_TIMEOUT_MS = 600_000;
const DEFAULT_CLONE_TIMEOUT_MS = 600_000;
const DEFAULT_CLONE_STALL_MS = 60_000;
/** A terminal no browser is attached to closes after this long (ADR 0026). */
const DEFAULT_TERMINAL_DETACH_MS = 60_000;

export function createControl(store: Store, options: ControlOptions = {}): Control & AgentControl {
  const listeners = new Set<(msg: ServerMessage) => void>();
  const publish = (msg: ServerMessage): void => {
    for (const listener of listeners) listener(msg);
  };
  const eventListeners = new Set<(event: ControlEvent) => void>();
  const emit = (event: ControlEvent): void => {
    for (const listener of eventListeners) {
      try {
        listener(event);
      } catch (err) {
        log('error', 'control', 'event listener failed', { kind: event.kind, error: String(err) });
      }
    }
  };
  const open = new Map<string, number>();
  const viewing: Viewing = {
    add(sessionId) {
      open.set(sessionId, (open.get(sessionId) ?? 0) + 1);
      ports.sync(sessionId);
    },
    remove(sessionId) {
      const n = (open.get(sessionId) ?? 0) - 1;
      if (n > 0) open.set(sessionId, n);
      else open.delete(sessionId);
      ports.sync(sessionId);
    },
    isOpen: (sessionId) => open.has(sessionId),
  };
  const state: ControlState = {
    store,
    byDevice: new Map(),
    deviceGenerations: new Map(),
    pendingPrompt: new Map(),
    promptCache: new Map(),
    runtime: new Map(),
    commands: new Map(),
    providers: new Map(),
    pendingSteer: new Map(),
    pendingStart: new Map(),
    startCache: new Map(),
    startedSessions: new Map(),
    deviceInfo: new Map(),
    sessionPorts: new Map(),
    resources: new Map(),
    pendingTerminal: new Map(),
    closeTerminals: () => {},
    permissions: new Map(),
    publish,
    now: options.now ?? Date.now,
    emit,
    runtimeChanged: () => {},
    pendingWs: new Map(),
    pendingFile: new Map(),
    pendingDiff: new Map(),
    handoffs: new Map(),
    pendingPrepared: new Map(),
    pendingInstalled: new Map(),
    pendingVerified: new Map(),
    pendingDeviceControl: new Map(),
    pendingProviders: new Map(),
    pendingSession: new Map(),
    pendingDirs: new Map(),
    pendingClone: new Map(),
    pendingCloneCheck: new Map(),
    pendingCloneRun: new Map(),
    pendingAttachments: new Map(),
    promptTimeoutMs: options.promptTimeoutMs ?? DEFAULT_PROMPT_TIMEOUT_MS,
    startTimeoutMs: options.startTimeoutMs ?? DEFAULT_START_TIMEOUT_MS,
    workspaceTimeoutMs: options.workspaceTimeoutMs ?? DEFAULT_WORKSPACE_TIMEOUT_MS,
    attachmentTimeoutMs: options.attachmentTimeoutMs ?? DEFAULT_ATTACHMENT_TIMEOUT_MS,
    handoffTimeoutMs: options.handoffTimeoutMs ?? DEFAULT_HANDOFF_TIMEOUT_MS,
    transferTimeoutMs: options.transferTimeoutMs ?? DEFAULT_TRANSFER_TIMEOUT_MS,
    cloneTimeoutMs: options.cloneTimeoutMs ?? DEFAULT_CLONE_TIMEOUT_MS,
    cloneStallMs: options.cloneStallMs ?? DEFAULT_CLONE_STALL_MS,
  };

  const bulk = createBulkQueue(options.bulk ?? DEFAULT_BULK);
  const runtime = createRuntime(state);
  const queue = createQueue(state, runtime);
  const ownerOf = (sessionId: string): string | null =>
    store.ownerDeviceId(sessionId) ?? state.startedSessions.get(sessionId)?.deviceId ?? null;
  const ports = createPorts(state, open, ownerOf);
  const resources = createResources(state);
  const terminals = createTerminals(
    state,
    runtime.runtimeOf,
    options.terminalDetachMs ?? DEFAULT_TERMINAL_DETACH_MS,
  );
  state.closeTerminals = terminals.closeSession;
  state.runtimeChanged = queue.runtimeChanged;
  const rules = createRules(state);
  const rpc = createRpc(state);
  const handoff = createHandoff(state, rpc.workspace);
  const deletion = createDeletion(state, runtime.runtimeOf);
  const start = createStart(state, rpc.listProjects);
  const fresh = createFresh(state, () => control);

  /** What a connected agent announced besides its providers, for the device list. */
  function deviceExtras(deviceId: string, onlineIds: ReadonlySet<string>) {
    const info = state.deviceInfo.get(deviceId);
    if (!info || !onlineIds.has(deviceId)) return {};
    return {
      terminal: info.terminal,
      ...(info.previewHost !== undefined ? { previewHost: info.previewHost } : {}),
    };
  }

  /** The latest resource report and the warnings it raises; kept while the device is offline. */
  function resourceExtras(deviceId: string, onlineIds: ReadonlySet<string>) {
    return resources.extras(deviceId, onlineIds.has(deviceId));
  }

  const control: Control & AgentControl = {
    bulk,
    viewing,
    onEvent(listener) {
      eventListeners.add(listener);
      return () => eventListeners.delete(listener);
    },
    terminals: terminals.terminals,
    portsOf: ports.of,
    resourcesOf: resources.of,
    syncRules: rules.sync,
    announceRules: rules.announce,
    appliedRulesRevision: rules.appliedRevision,
    attach(deviceId, link, providers, hello) {
      if (state.byDevice.get(deviceId) !== link) {
        // A replaced connection takes its terminals with it.
        terminals.detached([deviceId]);
        state.deviceGenerations.set(deviceId, (state.deviceGenerations.get(deviceId) ?? 0) + 1);
      }
      state.byDevice.set(deviceId, link);
      state.providers.set(deviceId, providers ?? []);
      state.deviceInfo.set(deviceId, {
        terminal: hello?.capabilities?.terminal === true,
        ...(hello?.previewHost !== undefined ? { previewHost: hello.previewHost } : {}),
      });
      runtime.attached(deviceId);
      ports.attached(deviceId);
      rules.attached(deviceId);
      deletion.attached(deviceId);
      publish({ v: PROTOCOL_VERSION, type: 'device.presence', deviceId, connected: true });
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    publish,
    detach(link) {
      const gone: string[] = [];
      for (const [deviceId, attached] of state.byDevice) {
        if (attached !== link) continue;
        state.byDevice.delete(deviceId);
        state.providers.delete(deviceId);
        store.touchDevice(deviceId);
        gone.push(deviceId);
      }
      start.detached(link);
      runtime.detached(gone, link);
      handoff.detached(link);
      terminals.detached(gone);
      ports.detached(gone);
      rules.detached(gone);
      for (const deviceId of gone) state.deviceInfo.delete(deviceId);
      for (const deviceId of gone) {
        publish({ v: PROTOCOL_VERSION, type: 'device.presence', deviceId, connected: false });
      }
    },
    receive(link, msg) {
      if (msg.type === 'device.rules.applied') {
        rules.receive(link, msg.revision, msg.error);
        return;
      }
      receiveReply(state, runtime, terminals, ports, resources, link, msg);
    },
    prompt: runtime.prompt,
    answerPermission: runtime.answerPermission,
    interrupt(sessionId, ownerDeviceId) {
      const result = runtime.interrupt(sessionId, ownerDeviceId);
      if (result.ok) queue.interrupted(sessionId);
      return result;
    },
    queueOf: queue.queueOf,
    queueAdd: queue.queueAdd,
    queueEdit: queue.queueEdit,
    queueRemove: queue.queueRemove,
    queueReorder: queue.queueReorder,
    queueResume: queue.queueResume,
    queueClear: queue.queueClear,
    queueSteer: queue.queueSteer,
    steer: queue.steer,
    stop: runtime.stop,
    config: runtime.config,
    runtimeOf: runtime.runtimeOf,
    providerOf(sessionId) {
      return (
        store.getSession(sessionId)?.provider ??
        state.startedSessions.get(sessionId)?.provider ??
        null
      );
    },
    ownerOf,
    openPermissions: runtime.openPermissions,
    commandsOf: runtime.commandsOf,
    ownerOnline(deviceId) {
      return state.byDevice.get(deviceId)?.isOpen() === true;
    },
    devices() {
      const onlineIds = new Set<string>();
      for (const [id, link] of state.byDevice) if (link.isOpen()) onlineIds.add(id);
      const seen = new Set<string>();
      const out = [];
      for (const d of store.listDevices()) {
        seen.add(d.deviceId);
        out.push({
          deviceId: d.deviceId,
          name: d.name,
          system: d.system,
          ...(d.inventory === undefined ? {} : { inventory: d.inventory }),
          labels: d.labels,
          connected: onlineIds.has(d.deviceId),
          ...(d.lastSeen === undefined ? {} : { lastSeen: d.lastSeen }),
          providers: onlineIds.has(d.deviceId) ? (state.providers.get(d.deviceId) ?? []) : [],
          ...deviceExtras(d.deviceId, onlineIds),
          ...resourceExtras(d.deviceId, onlineIds),
        });
      }
      for (const id of onlineIds) {
        if (!seen.has(id)) {
          out.push({
            deviceId: id,
            name: id,
            system: null,
            labels: [],
            connected: true,
            providers: state.providers.get(id) ?? [],
            ...deviceExtras(id, onlineIds),
            ...resourceExtras(id, onlineIds),
          });
        }
      }
      return out;
    },
    ...rpc,
    deletionPreview: deletion.deletionPreview,
    async deleteSession(sessionId, request) {
      const outcome = await deletion.deleteSession(sessionId, request);
      ports.sync();
      rules.sync(); // the session's rules went with it
      return outcome;
    },
    startSession: start.startSession,
    approvePlanFresh: fresh.approvePlanFresh,
    cancelHandoff: handoff.cancel,
    async handoff(request) {
      const outcome = await handoff.handoff(request);
      ports.sync();
      rules.sync(); // the session's rules follow it to its new owner
      queue.drain(request.sessionId);
      if (!outcome.ok && !HANDOFF_REFUSALS.has(outcome.error)) {
        emit({
          kind: 'failure',
          source: 'handoff',
          sessionId: request.sessionId,
          error: outcome.error,
        });
      }
      return outcome;
    },
  };
  return control;
}
