/**
 * @hoplet/sdk: a typed client for the Hoplet REST API and the browser WebSocket (ADR 0033). It speaks to the
 * core only through the contract in `shared/`, which the build inlines.
 */

// The REST payloads the methods take and return, so callers need no second import.
export type {
  AuditEventsResponse,
  AuditVerifyResponse,
  DeviceInfo,
  DeviceMintResult,
  DeviceProjectsResult,
  FullBlock,
  HandoffRequest,
  HandoffResult,
  HealthResponse,
  HttpErrorCode,
  LinesPage,
  LiveSessionMeta,
  PermissionRequest,
  Queue,
  RuntimeState,
  ServerMessage,
  TokenCreateRequest,
  TokenCreateResponse,
  TokenInfo,
} from '@hoplet/shared';
export { HTTP_ERROR_STATUS, PROTOCOL_VERSION } from '@hoplet/shared';
export {
  type AuditQuery,
  Hoplet,
  type HopletOptions,
  type ListSessionsFilter,
  type NewSessionInput,
  type PromptInput,
  type PromptSent,
  type ReadSessionOptions,
  type SessionStarted,
  type SubscribeOptions,
} from './client.ts';
export { HopletError, type HopletErrorCode, type SdkErrorCode } from './error.ts';
export { type ConnectedEvent, EventStream, type HopletEvent } from './events.ts';
export type { Baseline, TurnOutcome, TurnResult, WaitOptions } from './turn.ts';
