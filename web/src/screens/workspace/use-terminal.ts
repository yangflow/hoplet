import { useCallback, useEffect, useRef, useState } from 'react';
import {
  ApiError,
  closeTerminal,
  fetchTerminal,
  openTerminal as postOpenTerminal,
} from '../../lib/api.ts';
import { errorText } from '../../lib/errors.ts';
import { onLive, sendLive } from '../../lib/live.ts';
import {
  createKeyPacer,
  DEFAULT_COLS,
  DEFAULT_ROWS,
  fromBase64,
  type Gate,
  type KeyPacer,
} from '../../lib/terminal.ts';

/** Where a session's terminal stands (ADR 0026). The core and the owner decide; this only follows. */
export type TerminalPhase =
  | { kind: 'checking' }
  | { kind: 'none' }
  | { kind: 'open'; terminalId: string; cols: number; rows: number }
  | { kind: 'closed'; reason: string; exitCode?: number }
  | { kind: 'busy' }
  | { kind: 'off' }
  | { kind: 'failed'; text: string };

/** The emulator the hook writes into: output bytes, and a reset before a replay. */
export interface TerminalSink {
  write(bytes: Uint8Array): void;
  reset(): void;
}

type Pending = { reset: true } | { bytes: Uint8Array };
/** What waits for the emulator to load; the owner replays more on the next attach. */
const PENDING_MAX_BYTES = 1024 * 1024;
const SIZE_DEBOUNCE_MS = 150;

export interface TerminalControl {
  phase: TerminalPhase;
  /** An open request is in flight. */
  opening: boolean;
  open: () => void;
  close: () => void;
  /** Registers the emulator (null when it goes away); what arrived meanwhile is written first. */
  setSink: (sink: TerminalSink | null) => void;
  /** Keystrokes and pastes, paced to the wire limits. */
  sendKeys: (bytes: Uint8Array) => void;
  /** The view changed size; sent when it differs from what the owner has, after a pause. */
  sendSize: (cols: number, rows: number) => void;
}

/**
 * Opens, follows and closes a session's terminal. `gate` is whether one may open; `fixture` is a development
 * state that touches neither the network nor the socket.
 */
export function useTerminal(
  sessionId: string,
  gate: Gate,
  fixture?: TerminalPhase,
): TerminalControl {
  const ready = gate === 'ready';
  const [phase, setPhase] = useState<TerminalPhase>(fixture ?? { kind: 'checking' });
  const [opening, setOpening] = useState(false);
  const phaseRef = useRef(phase);
  phaseRef.current = phase;
  const sink = useRef<TerminalSink | null>(null);
  const pending = useRef<Pending[]>([]);
  const pendingBytes = useRef(0);
  const lastSeq = useRef(-1);
  /** The terminal this window has asked to attach to and not seen end. */
  const attached = useRef<string | null>(null);
  const pacer = useRef<{ id: string; pacer: KeyPacer } | null>(null);
  const sizeSent = useRef({ cols: DEFAULT_COLS, rows: DEFAULT_ROWS });
  const sizeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const deliver = useCallback((item: Pending) => {
    if (sink.current) {
      if ('reset' in item) sink.current.reset();
      else sink.current.write(item.bytes);
      return;
    }
    if ('reset' in item) {
      pending.current = [item];
      pendingBytes.current = 0;
      return;
    }
    pending.current.push(item);
    pendingBytes.current += item.bytes.length;
    while (pendingBytes.current > PENDING_MAX_BYTES && pending.current.length > 1) {
      const dropped = pending.current.shift();
      if (dropped && 'bytes' in dropped) pendingBytes.current -= dropped.bytes.length;
    }
  }, []);

  const attach = useCallback(
    (terminalId: string) => {
      lastSeq.current = -1;
      deliver({ reset: true });
      attached.current = terminalId;
      sendLive({ type: 'terminal.attach', terminalId });
    },
    [deliver],
  );

  // What the core knows of the terminal when the page opens and when the connection comes back.
  useEffect(() => {
    if (fixture) return;
    if (!ready) {
      setPhase((p) =>
        p.kind === 'checking' || p.kind === 'busy' || p.kind === 'failed' ? { kind: 'none' } : p,
      );
      return;
    }
    let live = true;
    const check = (reconnect: boolean) => {
      fetchTerminal(sessionId)
        .then(({ terminal }) => {
          if (!live) return;
          if (terminal) {
            sizeSent.current = { cols: terminal.cols, rows: terminal.rows };
            setPhase({
              kind: 'open',
              terminalId: terminal.terminalId,
              cols: terminal.cols,
              rows: terminal.rows,
            });
            attach(terminal.terminalId);
          } else if (reconnect && phaseRef.current.kind === 'open') {
            attached.current = null;
            setPhase({ kind: 'closed', reason: 'closed' });
          } else {
            setPhase((p) => (p.kind === 'checking' ? { kind: 'none' } : p));
          }
        })
        .catch((err) => {
          if (live)
            setPhase((p) => (p.kind === 'checking' ? { kind: 'failed', text: errorText(err) } : p));
        });
    };
    setPhase((p) => (p.kind === 'open' || p.kind === 'closed' ? p : { kind: 'checking' }));
    if (phaseRef.current.kind !== 'open' && phaseRef.current.kind !== 'closed') check(false);
    const off = onLive((event) => {
      if (event.kind === 'open' && phaseRef.current.kind === 'open') check(true);
    });
    return () => {
      live = false;
      off();
    };
  }, [sessionId, ready, fixture, attach]);

  // What the owner says about the terminal this window follows.
  useEffect(() => {
    if (fixture) return;
    return onLive((event) => {
      if (event.kind !== 'message') return;
      const msg = event.msg;
      const current = phaseRef.current;
      if (current.kind !== 'open') return;
      if (msg.type === 'terminal.attached' && msg.terminalId === current.terminalId) {
        lastSeq.current = -1;
        sizeSent.current = { cols: msg.cols, rows: msg.rows };
        setPhase({ ...current, cols: msg.cols, rows: msg.rows });
      } else if (msg.type === 'terminal.data' && msg.terminalId === current.terminalId) {
        if (msg.seq <= lastSeq.current) return;
        lastSeq.current = msg.seq;
        deliver({ bytes: fromBase64(msg.data) });
      } else if (msg.type === 'terminal.ended' && msg.terminalId === current.terminalId) {
        attached.current = null;
        setPhase({
          kind: 'closed',
          reason: msg.reason,
          ...(msg.exitCode !== undefined ? { exitCode: msg.exitCode } : {}),
        });
      }
    });
  }, [fixture, deliver]);

  // Leaving the tab or the page detaches; the owner closes a terminal nobody follows after 60 seconds.
  useEffect(
    () => () => {
      if (sizeTimer.current) clearTimeout(sizeTimer.current);
      pacer.current?.pacer.clear();
      const id = attached.current;
      if (id) sendLive({ type: 'terminal.detach', terminalId: id });
      attached.current = null;
    },
    [],
  );

  const open = useCallback(() => {
    if (fixture) return;
    setOpening(true);
    postOpenTerminal(sessionId, { cols: DEFAULT_COLS, rows: DEFAULT_ROWS })
      .then((info) => {
        sizeSent.current = { cols: info.cols, rows: info.rows };
        setPhase({ kind: 'open', terminalId: info.terminalId, cols: info.cols, rows: info.rows });
        attach(info.terminalId);
      })
      .catch((err) => {
        const code = err instanceof ApiError ? err.code : null;
        if (code === 'terminal-busy') setPhase({ kind: 'busy' });
        else if (code === 'terminal-unavailable') setPhase({ kind: 'off' });
        else setPhase({ kind: 'failed', text: errorText(err) });
      })
      .finally(() => setOpening(false));
  }, [fixture, sessionId, attach]);

  const close = useCallback(() => {
    const current = phaseRef.current;
    if (fixture || current.kind !== 'open') return;
    closeTerminal(current.terminalId)
      .then(() => {
        attached.current = null;
        setPhase({ kind: 'closed', reason: 'closed' });
      })
      .catch((err) => setPhase({ kind: 'failed', text: errorText(err) }));
  }, [fixture]);

  const setSink = useCallback((next: TerminalSink | null) => {
    sink.current = next;
    if (!next) return;
    const items = pending.current;
    pending.current = [];
    pendingBytes.current = 0;
    for (const item of items) {
      if ('reset' in item) next.reset();
      else next.write(item.bytes);
    }
  }, []);

  const sendKeys = useCallback(
    (bytes: Uint8Array) => {
      const current = phaseRef.current;
      if (fixture || current.kind !== 'open') return;
      if (pacer.current?.id !== current.terminalId) {
        pacer.current?.pacer.clear();
        const terminalId = current.terminalId;
        pacer.current = {
          id: terminalId,
          pacer: createKeyPacer({
            send: (data) => {
              sendLive({ type: 'terminal.keys', terminalId, data });
            },
            now: () => performance.now(),
            setTimer: (run, ms) => setTimeout(run, ms),
            clearTimer: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
          }),
        };
      }
      pacer.current.pacer.push(bytes);
    },
    [fixture],
  );

  const sendSize = useCallback(
    (cols: number, rows: number) => {
      if (fixture) return;
      if (sizeTimer.current) clearTimeout(sizeTimer.current);
      sizeTimer.current = setTimeout(() => {
        const current = phaseRef.current;
        if (current.kind !== 'open') return;
        if (sizeSent.current.cols === cols && sizeSent.current.rows === rows) return;
        if (sendLive({ type: 'terminal.size', terminalId: current.terminalId, cols, rows })) {
          sizeSent.current = { cols, rows };
          setPhase({ ...current, cols, rows });
        }
      }, SIZE_DEBOUNCE_MS);
    },
    [fixture],
  );

  return { phase: fixture ?? phase, opening, open, close, setSink, sendKeys, sendSize };
}
