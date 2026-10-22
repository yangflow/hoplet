import { randomUUID } from 'node:crypto';
import { PROTOCOL_VERSION, TERMINAL_WINDOW_BYTES, type TerminalReason } from '@hoplet/shared';
import { log } from '../log/index.ts';
import { askWaiter, settleWaiter } from './pending.ts';
import { type ControlState, onlineLink, type TerminalAnswer } from './state.ts';
import type { Control, DeviceLink, TerminalSink, Terminals } from './types.ts';

/** Terminals one machine may run at once; the agent enforces it too. */
const MAX_PER_DEVICE = 3;
/** Output the core keeps per terminal for a browser that attaches later. */
const REPLAY_BYTES = TERMINAL_WINDOW_BYTES;
/** A sink with more than this waiting is not asked for more output yet (the agent is not acknowledged). */
const ACK_BUFFER_LIMIT = 16 * 1024;
const ACK_RETRY_MS = 50;

interface Chunk {
  seq: number;
  data: string;
  bytes: number;
}

interface Entry {
  terminalId: string;
  sessionId: string;
  deviceId: string;
  link: DeviceLink;
  cols: number;
  rows: number;
  openedAt: number;
  /** False while the owner has not yet answered `terminal.open`. */
  running: boolean;
  sinks: Set<TerminalSink>;
  replay: Chunk[];
  replayBytes: number;
  bytesIn: number;
  bytesOut: number;
  /** Output taken from the agent and not yet acknowledged to it. */
  unacked: number;
  ackTimer?: ReturnType<typeof setTimeout> | undefined;
  detachTimer?: ReturnType<typeof setTimeout> | undefined;
}

/** The answer a refusal or an early end of `terminal.open` comes to. */
function refusal(reason: TerminalReason): Extract<TerminalAnswer, { ok: false }> {
  switch (reason) {
    case 'disabled':
      return { ok: false, error: 'terminal-unavailable' };
    case 'terminal-busy':
      return { ok: false, error: 'terminal-busy' };
    case 'turn-running':
    case 'turn-started':
      return { ok: false, error: 'session-running' };
    case 'external':
      return { ok: false, error: 'external' };
    case 'handoff-running':
    case 'handoff':
      return { ok: false, error: 'handoff-in-progress' };
    case 'owner-offline':
      return { ok: false, error: 'owner-offline' };
    case 'deleted':
      return { ok: false, error: 'session-not-found' };
    default:
      return { ok: false, error: 'rejected', reason };
  }
}

const decodedBytes = (base64: string): number => Buffer.from(base64, 'base64').length;

/** The agent's answers about terminals. */
export type TerminalReply =
  | { type: 'terminal.opened'; terminalId: string }
  | { type: 'terminal.rejected'; terminalId: string; reason: TerminalReason }
  | { type: 'terminal.output'; terminalId: string; seq: number; data: string }
  | {
      type: 'terminal.closed';
      terminalId: string;
      reason: TerminalReason;
      exitCode?: number | undefined;
    };

/**
 * The core side of the restricted terminal (ADR 0026). The agent runs the PTY; the core checks that a terminal
 * may be opened, relays input and output between the browsers attached to it and the agent, paces the agent with
 * `terminal.ack`, and closes the terminal when the session's turn, handoff or deletion or the owner's connection
 * ends it. Input and output pass through unchanged and are never logged or stored; only the replay ring holds
 * the last 64 KiB of output, in memory, until the terminal ends.
 */
export function createTerminals(
  state: ControlState,
  runtimeOf: Control['runtimeOf'],
  detachMs: number,
) {
  const byId = new Map<string, Entry>();
  const bySession = new Map<string, Entry>();

  function infoOf(entry: Entry) {
    return {
      terminalId: entry.terminalId,
      sessionId: entry.sessionId,
      cols: entry.cols,
      rows: entry.rows,
    };
  }

  /** The terminal is open or over already, so a record the log cannot take is told and nothing else happens. */
  function record(input: Parameters<typeof state.store.audit>[0]): void {
    try {
      state.store.audit(input);
    } catch (err) {
      log('error', 'control', 'terminal not recorded', {
        sessionId: input.sessionId ?? '',
        error: err instanceof Error ? err.message : 'unknown',
      });
    }
  }

  /** Removes the terminal, tells the agent unless it ended it, and tells the attached browsers. */
  function finish(
    entry: Entry,
    reason: TerminalReason,
    tellAgent: boolean,
    exitCode?: number,
  ): void {
    if (byId.get(entry.terminalId) !== entry) return;
    byId.delete(entry.terminalId);
    if (bySession.get(entry.sessionId) === entry) bySession.delete(entry.sessionId);
    clearTimeout(entry.ackTimer);
    clearTimeout(entry.detachTimer);
    if (tellAgent) {
      onlineLink(state, entry.deviceId)?.send({
        v: PROTOCOL_VERSION,
        type: 'terminal.close',
        terminalId: entry.terminalId,
        reason,
      });
    }
    if (!entry.running) {
      settleWaiter(state.pendingTerminal, entry.terminalId, refusal(reason));
      return;
    }
    const sinks = [...entry.sinks];
    entry.sinks.clear();
    entry.replay = [];
    for (const sink of sinks) {
      sink.send({
        v: PROTOCOL_VERSION,
        type: 'terminal.ended',
        terminalId: entry.terminalId,
        reason,
        ...(exitCode !== undefined ? { exitCode } : {}),
      });
    }
    record({
      type: 'terminal.closed',
      sessionId: entry.sessionId,
      deviceId: entry.deviceId,
      actor: { kind: 'core' },
      channel: 'core',
      ref: entry.terminalId,
      summary: {
        reason,
        durationMs: state.now() - entry.openedAt,
        bytesIn: entry.bytesIn,
        bytesOut: entry.bytesOut,
        ...(exitCode !== undefined ? { exitCode } : {}),
      },
    });
    // The audit trail: that a terminal ran and for how long, never what was typed or shown (docs/threat-model.md).
    log('info', 'control', 'terminal closed', {
      sessionId: entry.sessionId,
      terminalId: entry.terminalId,
      deviceId: entry.deviceId,
      reason,
      durationMs: state.now() - entry.openedAt,
      bytesIn: entry.bytesIn,
      bytesOut: entry.bytesOut,
      ...(exitCode !== undefined ? { exitCode } : {}),
    });
  }

  function armDetach(entry: Entry): void {
    clearTimeout(entry.detachTimer);
    entry.detachTimer = setTimeout(() => finish(entry, 'detached', true), detachMs);
    entry.detachTimer.unref?.();
  }

  /** Lets the agent send on: at once when nobody is attached, else once every browser has room. */
  function flushAck(entry: Entry): void {
    clearTimeout(entry.ackTimer);
    entry.ackTimer = undefined;
    if (entry.unacked === 0 || byId.get(entry.terminalId) !== entry) return;
    for (const sink of entry.sinks) {
      if (sink.buffered() > ACK_BUFFER_LIMIT) {
        entry.ackTimer = setTimeout(() => flushAck(entry), ACK_RETRY_MS);
        entry.ackTimer.unref?.();
        return;
      }
    }
    const bytes = entry.unacked;
    entry.unacked = 0;
    onlineLink(state, entry.deviceId)?.send({
      v: PROTOCOL_VERSION,
      type: 'terminal.ack',
      terminalId: entry.terminalId,
      bytes,
    });
  }

  const terminals: Terminals = {
    async open(sessionId, ownerDeviceId, cols, rows) {
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return { ok: false, error: 'owner-offline' };
      if (!state.deviceInfo.get(ownerDeviceId)?.terminal) {
        return { ok: false, error: 'terminal-unavailable' };
      }
      const current = runtimeOf(sessionId).state;
      if (current === 'external') return { ok: false, error: 'external' };
      if (state.handoffs.has(sessionId)) return { ok: false, error: 'handoff-in-progress' };
      if (current === 'starting') return { ok: false, error: 'starting' };
      if (current === 'running' || current === 'waiting-permission') {
        return { ok: false, error: 'session-running' };
      }
      let onDevice = 0;
      for (const other of byId.values()) if (other.deviceId === ownerDeviceId) onDevice++;
      if (bySession.has(sessionId) || onDevice >= MAX_PER_DEVICE) {
        return { ok: false, error: 'terminal-busy' };
      }

      const terminalId = randomUUID();
      const entry: Entry = {
        terminalId,
        sessionId,
        deviceId: ownerDeviceId,
        link,
        cols,
        rows,
        openedAt: state.now(),
        running: false,
        sinks: new Set(),
        replay: [],
        replayBytes: 0,
        bytesIn: 0,
        bytesOut: 0,
        unacked: 0,
      };
      byId.set(terminalId, entry);
      bySession.set(sessionId, entry);
      const answer = await askWaiter<TerminalAnswer>(
        state.pendingTerminal,
        terminalId,
        state.promptTimeoutMs,
        { ok: false, error: 'timeout' },
        () =>
          link.send({
            v: PROTOCOL_VERSION,
            type: 'terminal.open',
            terminalId,
            sessionId,
            cols,
            rows,
          }),
        { link, sessionId },
      );
      if (!answer.ok) {
        // No answer: the agent may still open it, so it is told to close.
        if (answer.error === 'timeout') finish(entry, 'closed', true);
        return answer;
      }
      if (byId.get(terminalId) !== entry) return { ok: false, error: 'rejected', reason: 'closed' };
      // Nobody is attached yet: the terminal waits for a browser as it does after the last one left.
      if (entry.sinks.size === 0 && !entry.detachTimer) armDetach(entry);
      record({
        type: 'terminal.opened',
        sessionId,
        deviceId: ownerDeviceId,
        ref: terminalId,
        summary: { cols: entry.cols, rows: entry.rows },
      });
      // The audit trail: that a terminal opened on which session and machine, never its content.
      log('info', 'control', 'terminal opened', { sessionId, terminalId, deviceId: ownerDeviceId });
      return { ok: true, terminal: infoOf(entry) };
    },

    ofSession(sessionId) {
      const entry = bySession.get(sessionId);
      return entry?.running ? infoOf(entry) : null;
    },

    sessionOf(terminalId) {
      const entry = byId.get(terminalId);
      return entry?.running ? entry.sessionId : null;
    },

    attach(terminalId, sink) {
      const entry = byId.get(terminalId);
      if (!entry?.running) return false;
      clearTimeout(entry.detachTimer);
      entry.detachTimer = undefined;
      entry.sinks.add(sink);
      sink.send({ v: PROTOCOL_VERSION, type: 'terminal.attached', ...infoOf(entry) });
      for (const chunk of entry.replay) {
        sink.send({
          v: PROTOCOL_VERSION,
          type: 'terminal.data',
          terminalId,
          seq: chunk.seq,
          data: chunk.data,
        });
      }
      return true;
    },

    detach(terminalId, sink) {
      const entry = byId.get(terminalId);
      if (!entry?.sinks.delete(sink)) return;
      if (entry.sinks.size === 0 && entry.running) armDetach(entry);
    },

    input(terminalId, sink, data) {
      const entry = byId.get(terminalId);
      if (!entry?.running || !entry.sinks.has(sink)) return;
      entry.bytesIn += decodedBytes(data);
      onlineLink(state, entry.deviceId)?.send({
        v: PROTOCOL_VERSION,
        type: 'terminal.input',
        terminalId,
        data,
      });
    },

    resize(terminalId, sink, cols, rows) {
      const entry = byId.get(terminalId);
      if (!entry?.running || !entry.sinks.has(sink)) return;
      entry.cols = cols;
      entry.rows = rows;
      onlineLink(state, entry.deviceId)?.send({
        v: PROTOCOL_VERSION,
        type: 'terminal.resize',
        terminalId,
        cols,
        rows,
      });
    },

    close(terminalId) {
      const entry = byId.get(terminalId);
      if (!entry?.running) return false;
      finish(entry, 'closed', true);
      return true;
    },
  };

  return {
    terminals,
    /** The session's turn starts, it is handed off or deleted: its terminal ends first. */
    closeSession(sessionId: string, reason: TerminalReason): void {
      const entry = bySession.get(sessionId);
      if (entry) finish(entry, reason, true);
    },
    /** The link of these devices is gone: their terminals went with it and are not restored. */
    detached(deviceIds: readonly string[]): void {
      for (const entry of [...byId.values()]) {
        if (deviceIds.includes(entry.deviceId)) finish(entry, 'owner-offline', false);
      }
    },
    receive(link: DeviceLink, msg: TerminalReply): void {
      const entry = byId.get(msg.terminalId);
      if (!entry || entry.link !== link) return;
      switch (msg.type) {
        case 'terminal.opened':
          if (entry.running) return;
          entry.running = true;
          settleWaiter(state.pendingTerminal, entry.terminalId, { ok: true });
          return;
        case 'terminal.rejected':
          if (!entry.running) finish(entry, msg.reason, false);
          return;
        case 'terminal.closed':
          finish(entry, msg.reason, false, msg.exitCode);
          return;
        case 'terminal.output': {
          if (!entry.running) return;
          const bytes = decodedBytes(msg.data);
          entry.bytesOut += bytes;
          entry.replay.push({ seq: msg.seq, data: msg.data, bytes });
          entry.replayBytes += bytes;
          while (entry.replayBytes > REPLAY_BYTES && entry.replay.length > 1) {
            entry.replayBytes -= entry.replay.shift()?.bytes ?? 0;
          }
          for (const sink of entry.sinks) {
            sink.send({
              v: PROTOCOL_VERSION,
              type: 'terminal.data',
              terminalId: entry.terminalId,
              seq: msg.seq,
              data: msg.data,
            });
          }
          entry.unacked += bytes;
          flushAck(entry);
        }
      }
    },
  };
}
