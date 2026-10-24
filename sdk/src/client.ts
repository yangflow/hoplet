import {
  type AuditEventsResponse,
  AuditEventsResponseSchema,
  type AuditVerifyResponse,
  AuditVerifyResponseSchema,
  type DeviceListResponse,
  DeviceListResponseSchema,
  type DeviceMintResult,
  DeviceMintResultSchema,
  type DeviceProjectsResult,
  DeviceProjectsResultSchema,
  ErrorResponseSchema,
  type FullBlock,
  FullBlockSchema,
  type GenericSchema,
  type HandoffCancelResult,
  HandoffCancelResultSchema,
  type HandoffRequest,
  type HandoffResult,
  HandoffResultSchema,
  type HealthResponse,
  HealthResponseSchema,
  type InferOutput,
  type LinesPage,
  LinesPageSchema,
  type LiveSessionMeta,
  LiveSessionMetaSchema,
  type NewSessionRequest,
  NewSessionResultSchema,
  type PermissionRequest,
  PROTOCOL_VERSION,
  type PromptRequest,
  PromptResultSchema,
  type Queue,
  QueueSchema,
  type RuntimeCommandResult,
  RuntimeCommandResultSchema,
  SessionListResponseSchema,
  safeParse,
  type TokenCreateRequest,
  type TokenCreateResponse,
  TokenCreateResponseSchema,
  type TokenListResponse,
  TokenListResponseSchema,
  type TokenRevokeResponse,
  TokenRevokeResponseSchema,
  type WsTicketResponse,
  WsTicketResponseSchema,
} from '@hoplet/shared';
import { HopletError } from './error.ts';
import { EventStream } from './events.ts';
import {
  type Baseline,
  type TurnIo,
  type TurnResult,
  type WaitOptions,
  waitForTurn,
} from './turn.ts';

export type HopletOptions = {
  /** The base URL of the core, e.g. `http://localhost:8787`. */
  url: string;
  /** An API token (`HOPLET_TOKEN`). */
  token: string;
  /** Replaces the global `fetch`. */
  fetch?: typeof fetch;
  /** Replaces the global `WebSocket`. */
  WebSocket?: typeof WebSocket;
};

/** The fields of a prompt the caller chooses; the SDK adds the `promptId`. */
export type PromptInput = Omit<PromptRequest, 'promptId'> & {
  /** Overrides the generated ID. Reuse one only to repeat the same prompt. */
  promptId?: string;
};

export type NewSessionInput = Omit<NewSessionRequest, 'promptId'> & { promptId?: string };

export type PromptSent = {
  promptId: string;
  /** Pass it to `waitForTurn` as `since`. */
  baseline: Baseline;
};

/** What `createSession` returns; `worktree` is set when the session runs in a new worktree. */
export type SessionStarted = PromptSent & {
  sessionId: string;
  worktree?: { path: string; branch: string };
};

export type ListSessionsFilter = {
  /** Only the sessions this device owns. */
  deviceId?: string;
  provider?: LiveSessionMeta['provider'];
  /** Only the sessions in this runtime state. */
  state?: LiveSessionMeta['runtime']['state'];
  /** Only the sessions with this tag; tags are case-insensitive. */
  tag?: string;
};

export type ReadSessionOptions = {
  /**
   * Forward: the first line to read (default 0). Backward: read the pages that end before this line (default:
   * the end of the session).
   */
  start?: number;
  /** Lines per page, 1 to 500. Default 100. */
  limit?: number;
  /** `forward` (default) yields the oldest page first; `backward` the newest. */
  direction?: 'forward' | 'backward';
};

export type SubscribeOptions = {
  /** The sessions to receive the events of; more can be added with `stream.subscribe(id)`. */
  sessionIds?: readonly string[];
};

/** Query filters of the audit log, as `GET /api/audit/events` takes them. */
export type AuditQuery = Record<string, string | number | undefined>;

const SEND_ATTEMPTS = 3;
const RETRY_DELAY_MS = 200;
const MAX_PAGE = 500;

function enc(segment: string): string {
  return encodeURIComponent(segment);
}

function queryString(query: AuditQuery | undefined): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value !== undefined) params.set(key, String(value));
  }
  const text = params.toString();
  return text === '' ? '' : `?${text}`;
}

// A loop, not /\/+$/: that regex backtracks on a long run of slashes.
function trimSlashes(url: string): string {
  let end = url.length;
  while (end > 0 && url[end - 1] === '/') end--;
  return url.slice(0, end);
}

/** The client of one core. All methods reject with a `HopletError`. */
export class Hoplet {
  readonly url: string;
  private readonly token: string;
  private readonly fetchImpl: typeof fetch;
  private readonly WebSocketImpl: typeof WebSocket | undefined;
  private compatible: Promise<void> | null = null;

  constructor(options: HopletOptions) {
    this.url = trimSlashes(options.url);
    this.token = options.token;
    this.fetchImpl = options.fetch ?? ((input, init) => fetch(input, init));
    this.WebSocketImpl = options.WebSocket ?? globalThis.WebSocket;
  }

  // ---- Core ----

  /** The core's health and versions. */
  health(): Promise<HealthResponse> {
    return this.call('GET', '/api/health', HealthResponseSchema, undefined, false);
  }

  // ---- Sessions ----

  async listSessions(filter: ListSessionsFilter = {}): Promise<LiveSessionMeta[]> {
    const { sessions } = await this.call('GET', '/api/sessions', SessionListResponseSchema);
    const tag = filter.tag?.trim().toLowerCase();
    return sessions.filter(
      (s) =>
        (filter.deviceId === undefined || s.ownerDeviceId === filter.deviceId) &&
        (filter.provider === undefined || s.provider === filter.provider) &&
        (filter.state === undefined || s.runtime.state === filter.state) &&
        (tag === undefined || (s.tags ?? []).includes(tag)),
    );
  }

  getSession(sessionId: string): Promise<LiveSessionMeta> {
    return this.call('GET', `/api/sessions/${enc(sessionId)}`, LiveSessionMetaSchema);
  }

  /**
   * Starts a session with its first prompt. The SDK generates the `promptId`; a request lost on the way is
   * repeated with the same one, so the prompt runs once.
   */
  async createSession(input: NewSessionInput): Promise<SessionStarted> {
    const promptId = input.promptId ?? crypto.randomUUID();
    const result = await this.callWithRetry('POST', '/api/sessions', NewSessionResultSchema, {
      ...input,
      promptId,
    });
    return {
      sessionId: result.sessionId,
      promptId: result.promptId,
      baseline: { sessionId: result.sessionId, lineCount: 0, state: 'idle' },
      ...(result.worktree ? { worktree: result.worktree } : {}),
    };
  }

  /**
   * Sends a prompt to a session. The baseline is read before the send; the SDK generates the `promptId` and
   * repeats a request lost on the way with the same one.
   */
  async sendPrompt(sessionId: string, input: PromptInput | string): Promise<PromptSent> {
    const body = typeof input === 'string' ? { text: input } : input;
    const promptId = body.promptId ?? crypto.randomUUID();
    const before = await this.getSession(sessionId);
    const result = await this.callWithRetry(
      'POST',
      `/api/sessions/${enc(sessionId)}/prompt`,
      PromptResultSchema,
      { ...body, promptId },
    );
    return {
      promptId: result.promptId,
      baseline: { sessionId, lineCount: before.lineCount, state: before.runtime.state },
    };
  }

  /**
   * Waits until the turn of the session ends. After `sendPrompt` or `createSession` pass the baseline as
   * `since`; the wait then needs to see the turn start. See `WaitOptions` and `TurnResult`.
   */
  waitForTurn(sessionId: string, options: WaitOptions = {}): Promise<TurnResult> {
    const io: TurnIo = {
      getSession: (id) => this.getSession(id),
      getQueue: (id) => this.getQueue(id),
      openEvents: (id) => {
        try {
          return this.subscribe({ sessionIds: [id] });
        } catch {
          return null;
        }
      },
    };
    return waitForTurn(io, sessionId, options);
  }

  /**
   * Reads the conversation a page at a time (a session is never read whole). Each page holds up to `limit`
   * lines; blocks the core elided are read with `readBlock`.
   */
  async *readSession(
    sessionId: string,
    options: ReadSessionOptions = {},
  ): AsyncGenerator<LinesPage> {
    const limit = Math.min(Math.max(options.limit ?? 100, 1), MAX_PAGE);
    const path = `/api/sessions/${enc(sessionId)}/lines`;
    if (options.direction === 'backward') {
      let upper = options.start;
      for (;;) {
        let start: number;
        let count: number;
        if (upper === undefined) {
          start = -limit;
          count = limit;
        } else {
          start = Math.max(0, upper - limit);
          count = upper - start;
        }
        if (count < 1) return;
        const page = await this.call(
          'GET',
          `${path}${queryString({ start, limit: count })}`,
          LinesPageSchema,
        );
        yield page;
        if (page.start <= 0 || (upper !== undefined && page.start >= upper)) return;
        upper = page.start;
      }
    }
    let start = options.start ?? 0;
    for (;;) {
      const page = await this.call(
        'GET',
        `${path}${queryString({ start, limit })}`,
        LinesPageSchema,
      );
      yield page;
      if (page.end >= page.total || page.end <= page.start) return;
      start = page.end;
    }
  }

  /** The full content of a block the lines elided. */
  readBlock(sessionId: string, line: number, block: number): Promise<FullBlock> {
    return this.call(
      'GET',
      `/api/sessions/${enc(sessionId)}/blocks/${line}/${block}`,
      FullBlockSchema,
    );
  }

  getQueue(sessionId: string): Promise<Queue> {
    return this.call('GET', `/api/sessions/${enc(sessionId)}/queue`, QueueSchema);
  }

  /** Answers a pending permission request (`session.permission` carries its `requestId`). */
  answerPermission(sessionId: string, answer: PermissionRequest): Promise<RuntimeCommandResult> {
    return this.call(
      'POST',
      `/api/sessions/${enc(sessionId)}/permission`,
      RuntimeCommandResultSchema,
      answer,
    );
  }

  interrupt(sessionId: string): Promise<RuntimeCommandResult> {
    return this.call(
      'POST',
      `/api/sessions/${enc(sessionId)}/interrupt`,
      RuntimeCommandResultSchema,
    );
  }

  // ---- Devices ----

  async listDevices(): Promise<DeviceListResponse['devices']> {
    return (await this.call('GET', '/api/devices', DeviceListResponseSchema)).devices;
  }

  /** The watched projects of a device and the sessions in them. */
  listProjects(deviceId: string): Promise<DeviceProjectsResult> {
    return this.call('GET', `/api/devices/${enc(deviceId)}/projects`, DeviceProjectsResultSchema);
  }

  // ---- Events ----

  /**
   * Opens the browser WebSocket with a fresh ticket and yields typed messages. It reconnects with a new ticket
   * and yields `sdk.connected` each time; read the state again when `reconnected` is true. Call `close()` (or
   * leave the `for await`) to end it.
   */
  subscribe(options: SubscribeOptions = {}): EventStream {
    const WebSocketImpl = this.WebSocketImpl;
    if (!WebSocketImpl) {
      throw new HopletError({
        status: 0,
        code: 'network-error',
        reason: 'No WebSocket implementation: pass the `WebSocket` option.',
      });
    }
    return new EventStream({
      WebSocketImpl,
      sessionIds: options.sessionIds ?? [],
      socketUrl: async () => {
        const ticket = await this.call('POST', '/api/ws-tickets', WsTicketResponseSchema);
        return this.socketUrl(ticket);
      },
    });
  }

  private socketUrl(ticket: WsTicketResponse): string {
    const base = this.url.replace(/^http/, 'ws');
    return `${base}/ws?ticket=${enc(ticket.ticket)}`;
  }

  // ---- Host-only calls (a token with the `host` capability) ----

  handoff(sessionId: string, request: HandoffRequest): Promise<HandoffResult> {
    return this.call(
      'POST',
      `/api/sessions/${enc(sessionId)}/handoff`,
      HandoffResultSchema,
      request,
    );
  }

  cancelHandoff(sessionId: string): Promise<HandoffCancelResult> {
    return this.call(
      'POST',
      `/api/sessions/${enc(sessionId)}/handoff/cancel`,
      HandoffCancelResultSchema,
    );
  }

  /** Mints a device token; for an existing `deviceId` it replaces that device's token. */
  mintDevice(name: string, deviceId?: string): Promise<DeviceMintResult> {
    return this.call(
      'POST',
      '/api/devices',
      DeviceMintResultSchema,
      deviceId === undefined ? { name } : { name, deviceId },
    );
  }

  /** One page of the audit log; `query` holds the filters, `after`, `order` and `limit` of the route. */
  auditEvents(query?: AuditQuery): Promise<AuditEventsResponse> {
    return this.call('GET', `/api/audit/events${queryString(query)}`, AuditEventsResponseSchema);
  }

  auditVerify(head?: string): Promise<AuditVerifyResponse> {
    return this.call('GET', `/api/audit/verify${queryString({ head })}`, AuditVerifyResponseSchema);
  }

  /** The audit export as text (`format` is `jsonl` or `csv`). */
  async auditExport(query?: AuditQuery): Promise<string> {
    const res = await this.fetchRaw('GET', `/api/audit/export${queryString(query)}`);
    if (!res.ok) throw await this.errorFrom(res);
    return res.text();
  }

  listTokens(): Promise<TokenListResponse> {
    return this.call('GET', '/api/tokens', TokenListResponseSchema);
  }

  /** Creates a token; the secret is in the response and cannot be read again. */
  createToken(request: TokenCreateRequest): Promise<TokenCreateResponse> {
    return this.call('POST', '/api/tokens', TokenCreateResponseSchema, request);
  }

  revokeToken(tokenId: string): Promise<TokenRevokeResponse> {
    return this.call('DELETE', `/api/tokens/${enc(tokenId)}`, TokenRevokeResponseSchema);
  }

  // ---- Transport ----

  /** Refuses a core that speaks another protocol version; checked once, on first use. */
  private checkCompatible(): Promise<void> {
    this.compatible ??= this.health().then(
      (health) => {
        if (health.protocolVersion !== PROTOCOL_VERSION) {
          throw new HopletError({
            status: 0,
            code: 'protocol-version-mismatch',
            reason: `The core speaks protocol ${health.protocolVersion}, this SDK speaks ${PROTOCOL_VERSION}.`,
          });
        }
      },
      (err: unknown) => {
        // A failed check is tried again on the next call, except a mismatch, which stays.
        if (!(err instanceof HopletError && err.code === 'protocol-version-mismatch')) {
          this.compatible = null;
        }
        throw err;
      },
    );
    return this.compatible;
  }

  private async fetchRaw(method: string, path: string, body?: unknown): Promise<Response> {
    const headers: Record<string, string> = { authorization: `Bearer ${this.token}` };
    const init: RequestInit = { method, headers };
    if (body !== undefined) {
      headers['content-type'] = 'application/json';
      init.body = JSON.stringify(body);
    }
    try {
      return await this.fetchImpl(`${this.url}${path}`, init);
    } catch (cause) {
      throw new HopletError({
        status: 0,
        code: 'network-error',
        reason: cause instanceof Error ? cause.message : String(cause),
        cause,
      });
    }
  }

  private async errorFrom(res: Response): Promise<HopletError> {
    const json: unknown = await res.json().catch(() => null);
    const parsed = safeParse(ErrorResponseSchema, json);
    if (parsed.success) {
      return new HopletError({
        status: res.status,
        code: parsed.output.code,
        reason: parsed.output.reason,
        promptId: parsed.output.promptId,
      });
    }
    // A code a newer core added fails the shared schema; its `code` still passes through.
    const code = (json as { code?: unknown } | null)?.code;
    const reason = (json as { reason?: unknown } | null)?.reason;
    return new HopletError({
      status: res.status,
      code: typeof code === 'string' ? code : 'internal',
      reason: typeof reason === 'string' ? reason : undefined,
    });
  }

  private async call<S extends GenericSchema>(
    method: string,
    path: string,
    schema: S,
    body?: unknown,
    checkVersion = true,
  ): Promise<InferOutput<S>> {
    if (checkVersion) await this.checkCompatible();
    const res = await this.fetchRaw(method, path, body);
    if (!res.ok) throw await this.errorFrom(res);
    const json: unknown = await res.json().catch(() => null);
    const parsed = safeParse(schema, json);
    if (!parsed.success) {
      throw new HopletError({
        status: res.status,
        code: 'invalid-response',
        reason: `${method} ${path} answered something other than the documented shape.`,
      });
    }
    return parsed.output;
  }

  /** A request whose body carries a `promptId`: a lost response is repeated, and the core runs the prompt once. */
  private async callWithRetry<S extends GenericSchema>(
    method: string,
    path: string,
    schema: S,
    body: unknown,
  ): Promise<InferOutput<S>> {
    for (let attempt = 1; ; attempt++) {
      try {
        return await this.call(method, path, schema, body);
      } catch (err) {
        const lost = err instanceof HopletError && err.code === 'network-error';
        if (!lost || attempt >= SEND_ATTEMPTS) throw err;
        await new Promise((resolve) => setTimeout(resolve, RETRY_DELAY_MS * attempt));
      }
    }
  }
}
