import type {
  DeviceSystem,
  Provider,
  ProviderInfo,
  RuntimeState,
  SessionConfig,
} from '@hoplet/shared';

export type { ProviderInfo, RuntimeState };

/** What New session submits: `config` holds only the options the user picked. */
export type NewSessionRequest = {
  deviceId: string;
  deviceName: string;
  path: string;
  provider: Provider;
  config: SessionConfig;
  prompt: string;
  /** The IDs of the images uploaded to the device for the first prompt. */
  attachments: string[];
};

/** Where a new session can start: a machine, the providers its agent runs and the directories known on it. */
export type SessionTarget = {
  deviceName: string;
  deviceId: string | null;
  online: boolean;
  system: DeviceSystem | null;
  providers: ProviderInfo[];
  dirs: { path: string; name: string }[];
};
