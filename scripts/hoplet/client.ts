/** Host-token HTTP client for core REST (prompt, interrupt, handoff, devices). */
import { randomUUID } from 'node:crypto';
import {
  DeviceListResponseSchema,
  DeviceMintResultSchema,
  ErrorResponseSchema,
  type GenericSchema,
  HandoffCancelResultSchema,
  HandoffResultSchema,
  type HttpErrorCode,
  type InferOutput,
  PromptResultSchema,
  RuntimeCommandResultSchema,
  safeParse,
} from '../../shared/src/index.ts';
import { errorMessage } from './errors.ts';

export type HostClientOptions = {
  baseUrl: string;
  token: string;
  fetchImpl?: typeof fetch | undefined;
};

export type HostError = {
  status: number;
  /** The error code of the response; null when the body was not a Hoplet error response. */
  code: HttpErrorCode | null;
  message: string;
};

export class HostClient {
  readonly baseUrl: string;
  readonly token: string;
  private readonly fetchImpl: typeof fetch;

  constructor(opts: HostClientOptions) {
    this.baseUrl = opts.baseUrl.replace(/\/+$/, '');
    this.token = opts.token;
    this.fetchImpl = opts.fetchImpl ?? fetch;
  }

  async request<S extends GenericSchema>(
    method: string,
    path: string,
    schema: S,
    body?: unknown,
  ): Promise<{ ok: true; data: InferOutput<S> } | { ok: false; error: HostError }> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.token}`,
    };
    const init: RequestInit = { method, headers };
    if (body !== undefined) {
      headers['content-type'] = 'application/json';
      init.body = JSON.stringify(body);
    }
    const res = await this.fetchImpl(`${this.baseUrl}${path}`, init);
    const json: unknown = await res.json().catch(() => null);
    if (!res.ok) {
      const parsed = safeParse(ErrorResponseSchema, json);
      const error = parsed.success ? parsed.output : null;
      return {
        ok: false,
        error: {
          status: res.status,
          code: error?.code ?? null,
          message: errorMessage(res.status, error),
        },
      };
    }
    const parsed = safeParse(schema, json);
    if (!parsed.success) {
      return {
        ok: false,
        error: { status: res.status, code: null, message: 'Unexpected response from the core.' },
      };
    }
    return { ok: true, data: parsed.output };
  }

  prompt(sessionId: string, text: string) {
    return this.request(
      'POST',
      `/api/sessions/${encodeURIComponent(sessionId)}/prompt`,
      PromptResultSchema,
      { text, promptId: randomUUID() },
    );
  }

  /** Moves the session to another connected device; resolves when the handoff is over. */
  handoff(sessionId: string, targetDeviceId: string, targetPath?: string, cloneInto?: string) {
    return this.request(
      'POST',
      `/api/sessions/${encodeURIComponent(sessionId)}/handoff`,
      HandoffResultSchema,
      {
        targetDeviceId,
        ...(targetPath !== undefined ? { targetPath } : {}),
        ...(cloneInto !== undefined ? { cloneInto } : {}),
      },
    );
  }

  /** Cancels the clone step of a running handoff; the handoff fails as `cancelled`. */
  cancelHandoff(sessionId: string) {
    return this.request(
      'POST',
      `/api/sessions/${encodeURIComponent(sessionId)}/handoff/cancel`,
      HandoffCancelResultSchema,
    );
  }

  interrupt(sessionId: string) {
    return this.request(
      'POST',
      `/api/sessions/${encodeURIComponent(sessionId)}/interrupt`,
      RuntimeCommandResultSchema,
    );
  }

  devices() {
    return this.request('GET', '/api/devices', DeviceListResponseSchema);
  }

  /** Mints a device token; for an existing deviceId it replaces that device's token. */
  mintDevice(name: string, deviceId?: string) {
    return this.request(
      'POST',
      '/api/devices',
      DeviceMintResultSchema,
      deviceId === undefined ? { name } : { name, deviceId },
    );
  }
}
