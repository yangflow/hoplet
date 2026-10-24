// Changes of sessions to their own configuration (#69, ADR 0036): a session proposes up to five objects (the
// project's memory file, project skills and commands, the options `model` and `effort`); the owner's agent previews
// the files, the user approves or declines the whole request, and only then is anything written. The core holds the
// text and the decision; the files are touched by the owner's agent alone, through the `ConfigFiles` port
// (config-files.ts).
import { createHash, randomUUID } from 'node:crypto';
import {
  CONFIG_CHANGE_PREVIOUS_RETENTION_MS,
  CONFIG_CHANGE_TTL_MS,
  type ConfigChangeObject,
  type ConfigOption,
  configObjectKey,
  configTakesEffect,
  MAX_CONFIG_CHANGE_BYTES,
  PROTOCOL_VERSION,
  type SessionConfigChange,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { containsCredential } from '../scrub/index.ts';
import {
  type AuditInput,
  type PreviousContent,
  publicChange,
  type StoredConfigChange,
} from '../store/index.ts';
import { createAgentConfigFiles } from './config-files.ts';
import type { ControlState } from './state.ts';
import type {
  AuditActor,
  ConfigFileObject,
  Control,
  ControlEvent,
  ControlOptions,
  SessionConfigChanges,
  SessionMcp,
} from './types.ts';

const HOUR = 60 * 60_000;

/**
 * Whether the session's vendor picks up a changed skill or command while it runs (the driver capability
 * `selfConfig.reload` of ADR 0036). No driver reports it yet: the vendor spike could not confirm a rescan of the
 * stream-JSON process, so every file says it takes effect at the next start.
 */
const VENDOR_RELOADS = false;

/** What the module returns to control: the interface the API uses and the hooks control feeds. */
export interface ConfigChangesModule {
  api: SessionConfigChanges;
  onEvent(event: ControlEvent): void;
  /** A device connected: approved changes of its sessions that waited for it are written. */
  deviceAttached(deviceId: string): void;
  sessionGone(sessionId: string): void;
  /** The session moved by a hand-off: its open requests no longer apply. */
  sessionMoved(sessionId: string): void;
}

export function sha256(content: string): string {
  return createHash('sha256').update(content, 'utf8').digest('hex');
}

function bytesOf(content: string | null): number {
  return content === null ? 0 : Buffer.byteLength(content, 'utf8');
}

type Refusal = Extract<Awaited<ReturnType<SessionConfigChanges['propose']>>, { ok: false }>;

function refusal(error: Refusal['error'], reason?: string): Refusal {
  return (reason === undefined ? { ok: false, error } : { ok: false, error, reason }) as Refusal;
}

/** The option a `config` object names, if the driver declares it as a select this module may set. */
function settableOption(
  options: readonly ConfigOption[],
  name: string,
  value: string,
): { option: ConfigOption & { type: 'select' } } | { reason: string } {
  const option = options.find((o) => o.id === name);
  if (option?.type !== 'select') return { reason: 'unknown-option' };
  if (option.category === 'mode' || option.id === 'mode') return { reason: 'mode' };
  const chosen = option.options.find((o) => o.value === value);
  if (!chosen) return { reason: 'unknown-value' };
  if (chosen.risk === 'high') return { reason: 'high-risk' };
  return { option };
}

export function createConfigChanges(
  state: ControlState,
  control: () => Pick<Control, 'ownerOf' | 'runtimeOf' | 'devices' | 'config' | 'providerOf'>,
  sessionMcp: SessionMcp,
  options: ControlOptions,
): ConfigChangesModule {
  const { store } = state;
  const files =
    options.configFiles ??
    createAgentConfigFiles(state, (sessionId) => control().providerOf(sessionId));
  /** The changes being written or undone, which an expiry or a second write must leave alone. */
  const executing = new Set<string>();
  const timers = new Map<string, NodeJS.Timeout>();

  function publish(change: SessionConfigChange): void {
    state.publish({
      v: PROTOCOL_VERSION,
      type: 'session.change',
      sessionId: change.sessionId,
      changeId: change.changeId,
      state: change.state,
    });
  }

  function clearTimer(changeId: string): void {
    const timer = timers.get(changeId);
    if (timer) clearTimeout(timer);
    timers.delete(changeId);
  }

  function arm(change: SessionConfigChange): void {
    clearTimer(change.changeId);
    const delay = Math.max(0, change.expiresAt - state.now());
    const timer = setTimeout(() => expire(change.changeId), delay);
    timer.unref();
    timers.set(change.changeId, timer);
  }

  function audit(
    type: Extract<AuditInput['type'], `config.change.${string}`>,
    change: SessionConfigChange,
    actor: AuditActor | undefined,
    summary: Record<string, unknown> = {},
  ): AuditInput {
    return {
      type,
      at: state.now(),
      sessionId: change.sessionId,
      deviceId: control().ownerOf(change.sessionId),
      ...(actor ? { actor } : {}),
      ref: change.changeId,
      // Which objects, never their content: a record must not hold what the table is allowed to.
      summary: {
        changeId: change.changeId,
        objects: change.objects.map((o) => configObjectKey(o.kind, o.name)),
        ...summary,
      },
    };
  }

  /** Closes a request nobody decided, or that cannot be written any more. */
  function expire(changeId: string): void {
    clearTimer(changeId);
    if (executing.has(changeId)) return;
    try {
      const closed = store.patchConfigChange(
        changeId,
        ['pending', 'approved'],
        { state: 'expired', previous: null },
        null,
      );
      if (closed) publish(publicChange(closed));
    } catch (err) {
      log('error', 'control', 'config change expiry failed', { error: String(err) });
    }
  }

  function expireOpenOf(sessionId: string): void {
    for (const change of store.configChanges(sessionId)) {
      if (change.state === 'pending' || change.state === 'approved') expire(change.changeId);
    }
  }

  function ownerOnline(sessionId: string): string | null {
    const owner = control().ownerOf(sessionId);
    if (owner === null) return null;
    return control()
      .devices()
      .find((d) => d.deviceId === owner)?.connected === true
      ? owner
      : null;
  }

  /** Previews the objects of a proposal and builds the stored form; a refusal says why not. */
  async function preview(
    sessionId: string,
    owner: string,
    input: Parameters<SessionConfigChanges['propose']>[1],
  ): Promise<{ objects: ConfigChangeObject[]; previous: PreviousContent[] } | Refusal> {
    const runtime = control().runtimeOf(sessionId);
    const objects: (ConfigChangeObject | null)[] = input.objects.map(() => null);
    const previous: (PreviousContent | null)[] = input.objects.map(() => null);

    const fileSlots: number[] = [];
    const fileObjects: ConfigFileObject[] = [];
    for (const [i, o] of input.objects.entries()) {
      if (o.kind === 'config') {
        const value = o.content;
        if (value === null) return refusal('invalid-request', 'config');
        if (runtime.state === 'external') return refusal('external');
        const declared = runtime.capabilities?.config ?? [];
        const found = settableOption(declared, o.name, value);
        if ('reason' in found) return refusal('forbidden', found.reason);
        const current = String(found.option.currentValue);
        objects[i] = {
          kind: 'config',
          name: o.name,
          baseHash: '',
          newHash: '',
          content: value,
          diff: `-${o.name}: ${current}\n+${o.name}: ${value}\n`,
          takesEffect: configTakesEffect('config', VENDOR_RELOADS),
        };
        previous[i] = { kind: 'config', name: o.name, content: current };
      } else {
        fileSlots.push(i);
        fileObjects.push({ kind: o.kind, name: o.name, content: o.content });
      }
    }

    if (fileObjects.length > 0) {
      const shown = await files.preview(owner, sessionId, fileObjects);
      if (!shown.ok) return refusal(shown.error, shown.reason);
      for (const [n, i] of fileSlots.entries()) {
        const given = input.objects[i];
        const seen = shown.objects[n];
        if (!given || !seen || seen.kind !== given.kind || seen.name !== given.name) {
          return refusal('rejected', 'preview-mismatch');
        }
        if ((given.baseHash ?? '') !== seen.currentHash) {
          return refusal('stale', configObjectKey(given.kind, given.name));
        }
        objects[i] = {
          kind: given.kind,
          name: given.name,
          baseHash: seen.currentHash,
          newHash: given.content === null ? '' : sha256(given.content),
          content: given.content,
          diff: seen.diff,
          ...(seen.newDirectories?.length ? { newDirectories: seen.newDirectories } : {}),
          takesEffect: configTakesEffect(given.kind, VENDOR_RELOADS),
        };
        if (bytesOf(seen.previous) > MAX_CONFIG_CHANGE_BYTES) return refusal('too-large');
        previous[i] = { kind: given.kind, name: given.name, content: seen.previous };
      }
    }

    return {
      objects: objects.filter((o): o is ConfigChangeObject => o !== null),
      previous: previous.filter((p): p is PreviousContent => p !== null),
    };
  }

  async function propose(
    sessionId: string,
    input: Parameters<SessionConfigChanges['propose']>[1],
  ): ReturnType<SessionConfigChanges['propose']> {
    if (!sessionMcp.settingsOf(sessionId).writeConfig) return refusal('forbidden', 'not-allowed');
    if (control().ownerOf(sessionId) === null) return refusal('not-found');
    if (input.objects.reduce((sum, o) => sum + bytesOf(o.content), 0) > MAX_CONFIG_CHANGE_BYTES) {
      return refusal('too-large');
    }
    // A secret never enters the table, the diff or a notification.
    for (const o of input.objects) {
      if (o.content !== null && containsCredential(o.content)) {
        return refusal('unsafe-content', configObjectKey(o.kind, o.name));
      }
    }
    if (
      store.configChanges(sessionId).some((c) => c.state === 'pending' || c.state === 'approved')
    ) {
      return refusal('forbidden', 'request-open');
    }
    const owner = ownerOnline(sessionId);
    if (owner === null) return refusal('owner-offline');

    const shown = await preview(sessionId, owner, input);
    if ('ok' in shown) return shown;
    // The session may have been moved, or another request opened, while the agent answered.
    if (control().ownerOf(sessionId) !== owner) return refusal('owner-offline');
    if (
      store.configChanges(sessionId).some((c) => c.state === 'pending' || c.state === 'approved')
    ) {
      return refusal('forbidden', 'request-open');
    }

    const at = state.now();
    const created: StoredConfigChange = {
      changeId: randomUUID(),
      sessionId,
      state: 'pending',
      reason: input.reason,
      objects: shown.objects,
      createdAt: at,
      expiresAt: at + CONFIG_CHANGE_TTL_MS,
      decidedAt: null,
      appliedAt: null,
      undoneAt: null,
      undoable: false,
      previous: shown.previous,
    };
    const stored = store.insertConfigChange(
      created,
      audit('config.change.requested', created, { kind: 'session', id: sessionId }),
    );
    const shownChange = publicChange(stored);
    publish(shownChange);
    arm(shownChange);
    return { ok: true, change: shownChange };
  }

  /** Sets the `config` objects to `values`; false when the owner refused one. */
  function setOptions(
    sessionId: string,
    owner: string,
    values: { name: string; value: string }[],
  ): boolean {
    for (const v of values) {
      if (!control().config(sessionId, owner, v.name, v.value).ok) return false;
    }
    return true;
  }

  const configValues = (objects: { kind: string; name: string; content: string | null }[]) =>
    objects
      .filter((o) => o.kind === 'config' && o.content !== null)
      .map((o) => ({ name: o.name, value: o.content as string }));

  /** Writes an approved change; it stays approved when the owner is not there to write it. */
  async function run(change: StoredConfigChange): Promise<void> {
    if (executing.has(change.changeId)) return;
    const owner = ownerOnline(change.sessionId);
    if (owner === null) return;
    executing.add(change.changeId);
    try {
      const previousOptions = (change.previous ?? [])
        .filter((p) => p.kind === 'config' && p.content !== null)
        .map((p) => ({ name: p.name, value: p.content as string }));
      let error: { code: string; reason?: string } | null = null;

      // Options first: they are checked and sent at once, and are put back if the files then fail.
      if (!setOptions(change.sessionId, owner, configValues(change.objects))) {
        error = { code: 'owner-offline' };
        setOptions(change.sessionId, owner, previousOptions);
      } else {
        const fileObjects = change.objects.filter((o) => o.kind !== 'config');
        if (fileObjects.length > 0) {
          const outcome = await files.apply(owner, {
            changeId: change.changeId,
            sessionId: change.sessionId,
            objects: fileObjects.map((o) => ({
              kind: o.kind as ConfigFileObject['kind'],
              name: o.name,
              content: o.content,
              baseHash: o.baseHash,
              newHash: o.newHash,
            })),
          });
          if (!outcome.ok) {
            error = { code: outcome.error, ...(outcome.reason ? { reason: outcome.reason } : {}) };
            setOptions(change.sessionId, owner, previousOptions);
          }
        }
      }

      if (error?.code === 'owner-offline') return; // Stays approved; the owner's next connection tries again.
      const at = state.now();
      const closed = error
        ? store.patchConfigChange(
            change.changeId,
            ['approved'],
            {
              state: 'failed',
              error: error.code,
              errorReason: error.reason ?? null,
              previous: null,
            },
            audit('config.change.failed', change, undefined, { error: error.code }),
          )
        : store.patchConfigChange(
            change.changeId,
            ['approved'],
            { state: 'applied', appliedAt: at },
            audit('config.change.applied', change, undefined),
          );
      if (closed) {
        clearTimer(change.changeId);
        publish(publicChange(closed));
      }
    } catch (err) {
      log('error', 'control', 'config change failed', { error: String(err) });
    } finally {
      executing.delete(change.changeId);
    }
  }

  function decide(
    changeId: string,
    approve: boolean,
    actor?: AuditActor,
  ): ReturnType<SessionConfigChanges['decide']> {
    const current = store.configChange(changeId);
    if (!current) return { ok: false, error: 'not-found' };
    const at = state.now();
    if (current.state !== 'pending') return { ok: false, error: 'request-closed' };
    if (current.expiresAt <= at) {
      expire(changeId);
      return { ok: false, error: 'request-closed' };
    }
    const decided = store.patchConfigChange(
      changeId,
      ['pending'],
      approve
        ? // An approved change whose owner is offline is written when it reconnects, for another 24 hours.
          { state: 'approved', decidedAt: at, expiresAt: at + CONFIG_CHANGE_TTL_MS }
        : { state: 'declined', decidedAt: at, previous: null },
      audit('config.change.decided', current, actor, { approved: approve }),
    );
    if (!decided) return { ok: false, error: 'request-closed' };
    clearTimer(changeId);
    const shown = publicChange(decided);
    publish(shown);
    if (approve) {
      arm(shown);
      void run(decided);
    }
    return { ok: true, change: shown };
  }

  async function undo(
    changeId: string,
    actor?: AuditActor,
  ): ReturnType<SessionConfigChanges['undo']> {
    const current = store.configChange(changeId);
    if (!current) return { ok: false, error: 'not-found' };
    if (current.state !== 'applied' || current.previous === null) {
      return { ok: false, error: 'request-closed' };
    }
    if (executing.has(changeId)) return { ok: false, error: 'busy' };
    const owner = ownerOnline(current.sessionId);
    if (owner === null) return { ok: false, error: 'owner-offline' };
    const previous = current.previous;
    executing.add(changeId);
    try {
      let error: {
        code: 'stale' | 'unsupported' | 'rejected' | 'owner-offline';
        reason?: string;
      } | null = null;
      // Files first: the hash check can refuse them, and then nothing has changed.
      const fileObjects = current.objects.filter((o) => o.kind !== 'config');
      if (fileObjects.length > 0) {
        const outcome = await files.apply(owner, {
          changeId,
          sessionId: current.sessionId,
          restore: true,
          objects: fileObjects.map((o) => {
            const before =
              previous.find((p) => p.kind === o.kind && p.name === o.name)?.content ?? null;
            return {
              kind: o.kind as ConfigFileObject['kind'],
              name: o.name,
              content: before,
              baseHash: o.newHash,
              newHash: before === null ? '' : sha256(before),
            };
          }),
        });
        if (!outcome.ok) {
          const code =
            outcome.error === 'stale' ||
            outcome.error === 'unsupported' ||
            outcome.error === 'owner-offline'
              ? outcome.error
              : 'rejected';
          error = { code, ...(outcome.reason ? { reason: outcome.reason } : {}) };
        }
      }
      if (!error) {
        const values = previous
          .filter((p) => p.kind === 'config' && p.content !== null)
          .map((p) => ({ name: p.name, value: p.content as string }));
        if (!setOptions(current.sessionId, owner, values)) error = { code: 'owner-offline' };
      }

      if (error) {
        if (error.code === 'owner-offline') return { ok: false, error: 'owner-offline' };
        // The change stays applied and says why the undo was refused.
        const kept = store.patchConfigChange(
          changeId,
          ['applied'],
          { state: 'applied', error: error.code, errorReason: error.reason ?? null },
          audit('config.change.failed', current, actor, { phase: 'undo', error: error.code }),
        );
        if (kept) publish(publicChange(kept));
        return { ok: false, error: error.code, ...(error.reason ? { reason: error.reason } : {}) };
      }
      const undone = store.patchConfigChange(
        changeId,
        ['applied'],
        { state: 'undone', undoneAt: state.now(), error: null, errorReason: null, previous: null },
        audit('config.change.undone', current, actor),
      );
      if (!undone) return { ok: false, error: 'request-closed' };
      const shown = publicChange(undone);
      publish(shown);
      return { ok: true, change: shown };
    } finally {
      executing.delete(changeId);
    }
  }

  function prune(): number {
    return store.pruneConfigChangePrevious(state.now() - CONFIG_CHANGE_PREVIOUS_RETENTION_MS);
  }

  const retention = setInterval(() => {
    try {
      prune();
    } catch (err) {
      log('error', 'control', 'config change retention failed', { error: String(err) });
    }
  }, HOUR);
  retention.unref();

  const api: SessionConfigChanges = {
    propose,
    list: (sessionId) => store.configChanges(sessionId).map(publicChange),
    get: (changeId) => {
      const change = store.configChange(changeId);
      return change ? publicChange(change) : null;
    },
    decide,
    undo,
    prune,
    close() {
      clearInterval(retention);
      for (const timer of timers.values()) clearTimeout(timer);
      timers.clear();
    },
  };

  // Requests that were open when the core stopped keep their expiry; the previous content ages out at start too.
  for (const open of store.openConfigChanges()) arm(publicChange(open));
  try {
    prune();
  } catch (err) {
    log('error', 'control', 'config change retention failed', { error: String(err) });
  }

  return {
    api,
    onEvent(event) {
      if (event.kind === 'ended' || event.kind === 'runtime-error') expireOpenOf(event.sessionId);
    },
    deviceAttached(deviceId) {
      for (const open of store.openConfigChanges()) {
        if (open.state === 'approved' && control().ownerOf(open.sessionId) === deviceId) {
          void run(open);
        }
      }
    },
    sessionGone(sessionId) {
      for (const change of store.configChanges(sessionId)) clearTimer(change.changeId);
    },
    sessionMoved: expireOpenOf,
  };
}
