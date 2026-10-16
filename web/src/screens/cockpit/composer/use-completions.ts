import type { CommandsResult } from '@hoplet/shared';
import { useEffect, useState } from 'react';
import { fetchDeviceCommands, fetchSessionCommands, fetchWorkspace } from '../../../lib/api.ts';
import { ApiError } from '../../../lib/errors.ts';
import { onLive } from '../../../lib/live.ts';

/** Where a composer's commands come from: a session, or the directory a draft would start in. */
export type CommandsTarget = { sessionId: string } | { deviceId: string; cwd: string } | null;

const keyOf = (target: CommandsTarget) =>
  !target ? '' : 'sessionId' in target ? target.sessionId : `${target.deviceId}\n${target.cwd}`;

/**
 * The commands of a session or of a draft's directory. A session's list is read again when the core says it
 * changed; until a list arrives there is none and `/` offers nothing.
 */
export function useCommands(
  target: CommandsTarget,
  fixture?: CommandsResult,
): CommandsResult | null {
  const [result, setResult] = useState<CommandsResult | null>(fixture ?? null);
  const key = keyOf(target);
  // biome-ignore lint/correctness/useExhaustiveDependencies: the key stands for the target
  useEffect(() => {
    if (fixture || !target) return;
    let current = true;
    const load = () => {
      const read =
        'sessionId' in target
          ? fetchSessionCommands(target.sessionId)
          : fetchDeviceCommands(target.deviceId, target.cwd);
      read.then(
        (r) => current && setResult(r),
        () => {
          /* the list stays as it was */
        },
      );
    };
    load();
    const stop =
      'sessionId' in target
        ? onLive((e) => {
            if (
              e.kind === 'message' &&
              e.msg.type === 'session.commands.changed' &&
              e.msg.sessionId === target.sessionId
            ) {
              load();
            }
          })
        : () => {};
    return () => {
      current = false;
      stop();
    };
  }, [key, fixture]);
  return result;
}

/** The working-tree paths `@` offers; `offline` when the owner cannot answer. */
export type PathsState =
  | { phase: 'loading' }
  | { phase: 'offline' }
  | { phase: 'ready'; paths: string[] };

/**
 * Reads the owner's working tree once `@` first opens, and again each time it opens afresh. Without a
 * session or with the owner offline there is nothing to read.
 */
export function usePaths(
  sessionId: string | null,
  online: boolean,
  wanted: boolean,
  fixture?: PathsState,
): PathsState {
  const [state, setState] = useState<PathsState>(fixture ?? { phase: 'loading' });
  useEffect(() => {
    if (fixture || !wanted || !sessionId || !online) return;
    let current = true;
    setState({ phase: 'loading' });
    fetchWorkspace(sessionId).then(
      (l) => current && setState({ phase: 'ready', paths: l.paths }),
      (err: unknown) =>
        current &&
        setState(
          err instanceof ApiError && err.code === 'owner-offline'
            ? { phase: 'offline' }
            : { phase: 'ready', paths: [] },
        ),
    );
    return () => {
      current = false;
    };
  }, [sessionId, online, wanted, fixture]);
  if (fixture) return fixture;
  return online ? state : { phase: 'offline' };
}
