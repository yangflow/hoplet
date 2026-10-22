// Where a hook may send: which addresses are acceptable and a transport that connects only to a checked
// address (ADR 0031). The host is resolved, every answer is classified, and the connection is pinned to the
// address that was checked, so a second lookup cannot hand the request somewhere else.
import { lookup as dnsLookup } from 'node:dns/promises';
import http from 'node:http';
import https from 'node:https';
import type { LookupFunction } from 'node:net';
import { isIP } from 'node:net';
import { BlockedAddressError, type Fetch } from '../notify/index.ts';

/** `public` may always be called; `private` (loopback, private, shared) only with `HOPLET_HOOK_ALLOW_PRIVATE`; `never` is refused. */
export type AddressClass = 'public' | 'private' | 'never';

export type Resolver = (host: string) => Promise<string[]>;

function v4(parts: number[]): AddressClass {
  const [a = 0, b = 0] = parts;
  if (a === 0) return 'never';
  if (a === 127) return 'private';
  if (a === 10) return 'private';
  if (a === 172 && b >= 16 && b <= 31) return 'private';
  if (a === 192 && b === 168) return 'private';
  if (a === 100 && b >= 64 && b <= 127) return 'private';
  if (a === 169 && b === 254) return 'never';
  if (a >= 224) return 'never';
  return 'public';
}

/** The eight 16-bit groups of an IPv6 address; null when it is not one. */
function groups(address: string): number[] | null {
  let text = address.split('%')[0] ?? '';
  const dotted = /(\d+\.\d+\.\d+\.\d+)$/.exec(text);
  if (dotted?.[1]) {
    const octets = dotted[1].split('.').map(Number);
    if (octets.length !== 4 || octets.some((o) => !(o >= 0 && o <= 255))) return null;
    const hi = (octets[0] ?? 0) * 256 + (octets[1] ?? 0);
    const lo = (octets[2] ?? 0) * 256 + (octets[3] ?? 0);
    text = `${text.slice(0, -dotted[1].length)}${hi.toString(16)}:${lo.toString(16)}`;
  }
  const halves = text.split('::');
  if (halves.length > 2) return null;
  const head = halves[0] ? halves[0].split(':') : [];
  const tail = halves.length === 2 && halves[1] ? halves[1].split(':') : [];
  const missing = 8 - head.length - tail.length;
  if (halves.length === 1 ? missing !== 0 : missing < 1) return null;
  const all = [...head, ...Array<string>(halves.length === 2 ? missing : 0).fill('0'), ...tail];
  const numbers = all.map((g) => Number.parseInt(g, 16));
  return numbers.length === 8 && numbers.every((n) => n >= 0 && n <= 0xffff) ? numbers : null;
}

/** Classifies an IP address; anything that is not one is `never`. */
export function classifyAddress(address: string): AddressClass {
  const kind = isIP(address);
  if (kind === 4) return v4(address.split('.').map(Number));
  if (kind !== 6) return 'never';
  const g = groups(address);
  if (!g) return 'never';
  const embeddedV4 = (): AddressClass =>
    v4([(g[6] ?? 0) >> 8, (g[6] ?? 0) & 255, (g[7] ?? 0) >> 8, (g[7] ?? 0) & 255]);
  const zeros = (n: number) => g.slice(0, n).every((x) => x === 0);
  if (zeros(8)) return 'never';
  if (zeros(7) && g[7] === 1) return 'private';
  // IPv4-mapped (::ffff:a.b.c.d), IPv4-compatible (::a.b.c.d) and NAT64 (64:ff9b::a.b.c.d) carry an IPv4 address.
  if (zeros(5) && g[5] === 0xffff) return embeddedV4();
  if (zeros(6)) return embeddedV4();
  if (g[0] === 0x64 && g[1] === 0xff9b && g.slice(2, 6).every((x) => x === 0)) return embeddedV4();
  if (((g[0] ?? 0) & 0xfe00) === 0xfc00) return 'private';
  if (((g[0] ?? 0) & 0xffc0) === 0xfe80) return 'never';
  if (((g[0] ?? 0) & 0xff00) === 0xff00) return 'never';
  return 'public';
}

export function addressAllowed(address: string, allowPrivate: boolean): boolean {
  const kind = classifyAddress(address);
  return kind === 'public' || (kind === 'private' && allowPrivate);
}

export const systemResolver: Resolver = async (host) =>
  (await dnsLookup(host, { all: true, verbatim: true })).map((a) => a.address);

/** The IP literal of a URL host, without the brackets of IPv6; null for a name. */
function literal(hostname: string): string | null {
  const bare = hostname.startsWith('[') ? hostname.slice(1, -1) : hostname;
  return isIP(bare) ? bare : null;
}

export async function resolveHost(hostname: string, resolve: Resolver): Promise<string[]> {
  const ip = literal(hostname);
  if (ip) return [ip];
  return resolve(hostname);
}

export type UrlRefusal =
  | 'invalid-url'
  | 'scheme'
  | 'credentials'
  | 'http-needs-allow-private'
  | 'unresolvable'
  | 'blocked-address';

/**
 * Why a hook may not be given this URL, or null. Used when a hook is saved; the transport checks again at
 * every attempt.
 */
export async function refuseUrl(
  value: string,
  allowPrivate: boolean,
  resolve: Resolver,
): Promise<UrlRefusal | null> {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return 'invalid-url';
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') return 'scheme';
  if (url.username || url.password) return 'credentials';
  if (url.protocol === 'http:' && !allowPrivate) return 'http-needs-allow-private';
  let addresses: string[];
  try {
    addresses = await resolveHost(url.hostname, resolve);
  } catch {
    return 'unresolvable';
  }
  if (addresses.length === 0) return 'unresolvable';
  for (const address of addresses) {
    if (!addressAllowed(address, allowPrivate)) return 'blocked-address';
    if (url.protocol === 'http:' && classifyAddress(address) !== 'private') {
      return 'http-needs-allow-private';
    }
  }
  return null;
}

/**
 * A `fetch`-shaped POST that resolves the host, refuses an address hooks may not call and connects to the
 * address it checked. It follows no redirect and reads nothing of the answer but its status.
 */
export function createPinnedFetch(options: { allowPrivate: boolean; resolve: Resolver }): Fetch {
  const impl = async (input: string | URL | Request, init?: RequestInit): Promise<Response> => {
    const url = new URL(typeof input === 'string' || input instanceof URL ? input : input.url);
    const addresses = await resolveHost(url.hostname, options.resolve);
    const checked = addresses[0];
    if (
      checked === undefined ||
      addresses.some((a) => !addressAllowed(a, options.allowPrivate)) ||
      (url.protocol === 'http:' && addresses.some((a) => classifyAddress(a) !== 'private')) ||
      (url.protocol !== 'http:' && url.protocol !== 'https:') ||
      (url.protocol === 'http:' && !options.allowPrivate)
    ) {
      throw new BlockedAddressError();
    }
    const family = isIP(checked);
    const lookup: LookupFunction = (_host, lookupOptions, callback) => {
      if (lookupOptions.all) callback(null, [{ address: checked, family }]);
      else callback(null, checked, family);
    };
    const headers = Object.fromEntries(new Headers(init?.headers).entries());
    const body = init?.body;
    const signal = init?.signal ?? undefined;
    return new Promise<Response>((resolve, reject) => {
      const transport = url.protocol === 'https:' ? https : http;
      const req = transport.request(
        {
          hostname: url.hostname.startsWith('[') ? url.hostname.slice(1, -1) : url.hostname,
          port: url.port || undefined,
          path: `${url.pathname}${url.search}`,
          method: 'POST',
          headers,
          agent: false,
          lookup,
          ...(signal ? { signal } : {}),
        },
        (res) => {
          const status = res.statusCode ?? 0;
          res.destroy();
          resolve(new Response(null, { status }));
        },
      );
      req.on('error', reject);
      req.end(typeof body === 'string' || body instanceof Uint8Array ? body : undefined);
    });
  };
  return impl as Fetch;
}
