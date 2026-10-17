import { HTTP_ERROR_CODES } from '@hoplet/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
import en from '../src/i18n/en.json';
import zhHans from '../src/i18n/zh-Hans.json';
import {
  fetchDeviceClone,
  fetchDeviceProjects,
  fetchSessions,
  postHandoff,
  postHandoffCancel,
} from '../src/lib/api.ts';
import {
  ApiError,
  addProjectErrorText,
  browseErrorText,
  ERROR_COPY,
  errorText,
} from '../src/lib/errors.ts';

afterEach(() => {
  vi.unstubAllGlobals();
});

function respond(status: number, body: unknown) {
  vi.stubGlobal('localStorage', { getItem: () => 'tok' });
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(JSON.stringify(body), {
          status,
          headers: { 'content-type': 'application/json' },
        }),
    ),
  );
}

describe('error copy', () => {
  it('has English and Chinese copy for every error code', () => {
    for (const code of HTTP_ERROR_CODES) {
      const copy = ERROR_COPY[code];
      expect(copy, code).toBeTruthy();
      expect((en as Record<string, string>)[copy], code).toBe(copy);
      expect((zhHans as Record<string, string>)[copy], code).toBeTruthy();
      expect((zhHans as Record<string, string>)[copy], code).not.toBe(copy);
    }
  });

  it('shows the copy of the code and the agent reason when there is one', () => {
    expect(errorText(new ApiError(409, 'owner-offline'))).toBe(ERROR_COPY['owner-offline']);
    expect(errorText(new ApiError(502, 'rejected', 'no such branch'))).toBe(
      `${ERROR_COPY.rejected} (no such branch)`,
    );
    expect(errorText(new ApiError(502, null))).toBe('HTTP 502');
    expect(errorText(new Error('boom'))).toBe('boom');
  });
});

describe('add project errors', () => {
  it('says the machine is offline or that the path is not a directory on it', () => {
    expect(addProjectErrorText(new ApiError(409, 'target-offline'), 'studio')).toBe(
      'studio is offline',
    );
    expect(
      addProjectErrorText(new ApiError(502, 'rejected', '/x is not a directory'), 'studio'),
    ).toBe('That path is not a directory on studio');
    expect(addProjectErrorText(new ApiError(504, 'timeout'), 'studio')).toBe(ERROR_COPY.timeout);
  });
});

describe('api client clone calls', () => {
  it('asks the core for the clone with the origin and the chosen folder', async () => {
    respond(200, { deviceId: 'd1', path: null });
    const f = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    await fetchDeviceClone('d1', 'https://github.com/o/r.git', undefined, '/Users/mark/x');
    const url = String(f.mock.calls[0]?.[0]);
    expect(url).toContain('/api/devices/d1/clone?');
    expect(url).toContain('origin=https%3A%2F%2Fgithub.com%2Fo%2Fr.git');
    expect(url).toContain('cloneInto=%2FUsers%2Fmark%2Fx');
  });

  it('keeps the reason and the detail of a failed clone', async () => {
    respond(502, { code: 'clone-failed', reason: 'git-failed', detail: 'fatal: no' });
    const err = await postHandoff('s1', 'd1', { cloneInto: '/x' }).catch((e: unknown) => e);
    expect(err).toMatchObject({ code: 'clone-failed', reason: 'git-failed', detail: 'fatal: no' });
    expect(errorText(err)).toBe(`${ERROR_COPY['clone-failed']} (git-failed)`);
  });

  it('cancels a handoff and reports one that is not running', async () => {
    respond(200, { ok: true });
    await expect(postHandoffCancel('s1')).resolves.toEqual({ ok: true });
    respond(409, { code: 'handoff-not-running' });
    await expect(postHandoffCancel('s1')).rejects.toMatchObject({ code: 'handoff-not-running' });
  });
});

describe('api client', () => {
  it('throws the code of an error response', async () => {
    respond(409, { code: 'session-changed' });
    const err = await postHandoff('s1', 'dev-b').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 409, code: 'session-changed' });
  });

  it('reads an offline owner as no catalog', async () => {
    respond(409, { code: 'owner-offline' });
    expect(await fetchDeviceProjects('dev-a')).toBeNull();
    respond(409, { code: 'cancelled' });
    await expect(fetchDeviceProjects('dev-a')).rejects.toMatchObject({ code: 'cancelled' });
  });

  it('rejects a success response that does not match its schema', async () => {
    respond(200, { sessions: [{ sessionId: 'a/b' }] });
    await expect(fetchSessions()).rejects.toThrow('unexpected response');
  });
});

describe('browseErrorText', () => {
  const invalid = (reason: string) => new ApiError(400, 'invalid-request', reason);

  it('names the agent reason for a refused folder', () => {
    expect(browseErrorText(invalid('outside the browse root'), 'MacBook Pro')).toBe(
      'That folder is outside the browse root on MacBook Pro',
    );
    expect(browseErrorText(invalid('not a directory'), 'MacBook Pro')).toBe(
      'That is not a folder on MacBook Pro',
    );
    expect(browseErrorText(invalid('unreadable'), 'MacBook Pro')).toBe(
      'MacBook Pro cannot read that folder',
    );
    expect(browseErrorText(invalid('relative path'), 'MacBook Pro')).toBe(
      'Type an absolute path, or one that starts with ~',
    );
  });

  it('says when the machine is offline or silent', () => {
    expect(browseErrorText(new ApiError(409, 'target-offline'), 'studio')).toBe(
      'studio is offline',
    );
    expect(browseErrorText(new ApiError(504, 'timeout'), 'studio')).toBe(
      'studio did not answer in time',
    );
  });

  it('keeps the copy of the code for an unknown reason', () => {
    expect(browseErrorText(invalid('weird'), 'studio')).toBe(
      errorText(new ApiError(400, 'invalid-request', 'weird')),
    );
  });

  it('has Chinese copy for every sentence it can show', () => {
    for (const key of [
      '{name} did not answer in time',
      'That folder is outside the browse root on {name}',
      'Type an absolute path, or one that starts with ~',
      'That is not a folder on {name}',
      '{name} cannot read that folder',
    ]) {
      expect((en as Record<string, string>)[key], key).toBe(key);
      expect((zhHans as Record<string, string>)[key], key).toBeTruthy();
    }
  });
});
