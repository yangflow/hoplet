import type { Provider, ProviderInfo, RuntimeState, StartOptions } from '@hoplet/shared';

export type { ProviderInfo, RuntimeState };

/** The states the sidebar shows as a glyph; `idle` and `offline` show the last-activity time instead. */
export type GlyphState = Exclude<RuntimeState, 'idle' | 'offline'>;

export function glyphState(state: RuntimeState): GlyphState | null {
  return state === 'idle' || state === 'offline' ? null : state;
}

/** What New session submits: the options hold only what the provider's capabilities declare. */
export type NewSessionRequest = {
  deviceId: string;
  deviceName: string;
  path: string;
  provider: Provider;
  options: StartOptions;
  prompt: string;
};

/** Where a new session can start: a machine, the providers its agent runs and the directories known on it. */
export type SessionTarget = {
  deviceName: string;
  deviceId: string | null;
  online: boolean;
  providers: ProviderInfo[];
  dirs: { path: string; name: string }[];
};
