import type { SessionPort } from '@hoplet/shared';
import { useEffect, useState } from 'react';
import { fetchPorts } from '../../lib/api.ts';
import { errorText } from '../../lib/errors.ts';
import { onLive } from '../../lib/live.ts';

/** The dev servers of a session (ADR 0026): the ports its processes listen on and the host to link them with. */
export type PortsData =
  | { phase: 'loading' }
  | { phase: 'error'; text: string }
  | { phase: 'ready'; ports: SessionPort[]; previewHost?: string };

/** `previewHost` set by the device, else none: no host is ever guessed. */
export function usePorts(sessionId: string, online: boolean, fixture?: PortsData): PortsData {
  const [data, setData] = useState<PortsData>(fixture ?? { phase: 'loading' });

  useEffect(() => {
    if (fixture || !online) return;
    let live = true;
    fetchPorts(sessionId)
      .then((r) => {
        if (live)
          setData({
            phase: 'ready',
            ports: r.ports,
            ...(r.previewHost ? { previewHost: r.previewHost } : {}),
          });
      })
      .catch((err) => {
        if (live) setData({ phase: 'error', text: errorText(err) });
      });
    const off = onLive((event) => {
      if (event.kind !== 'message' || event.msg.type !== 'session.listening') return;
      const msg = event.msg;
      if (msg.sessionId !== sessionId || !live) return;
      setData({
        phase: 'ready',
        ports: msg.ports,
        ...(msg.previewHost ? { previewHost: msg.previewHost } : {}),
      });
    });
    return () => {
      live = false;
      off();
    };
  }, [sessionId, online, fixture]);

  return fixture ?? data;
}
