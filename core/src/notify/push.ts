// Web Push on node:crypto (ADR 0021): the payload encryption of RFC 8291 (aes128gcm) and the VAPID
// JWT of RFC 8292 (ES256). No dependency.

import {
  createCipheriv,
  createECDH,
  createPrivateKey,
  hkdfSync,
  randomBytes,
  sign,
} from 'node:crypto';

const RECORD_SIZE = 4096;
const UNCOMPRESSED_POINT = 65;
const AUTH_SECRET = 16;
/** The longest plaintext one record holds: the record size less the tag, the delimiter and the header. */
export const MAX_PLAINTEXT = RECORD_SIZE - 16 - 1 - (16 + 4 + 1 + UNCOMPRESSED_POINT);

export const b64u = (buf: Uint8Array): string => Buffer.from(buf).toString('base64url');
export const fromB64u = (text: string): Buffer => Buffer.from(text, 'base64url');

export interface EncryptOptions {
  /** The 16-byte salt; random unless a test fixes it. */
  salt?: Buffer;
  /** The sender's key pair for this message; generated unless a test fixes it. */
  sender?: { privateKey: Buffer };
}

/**
 * Encrypts `plaintext` for the subscription whose public key is `p256dh` (65 bytes) and whose secret is `auth`
 * (16 bytes) into the body of a Web Push request: the aes128gcm header followed by the single record.
 */
export function encryptPayload(
  plaintext: Buffer,
  p256dh: Buffer,
  auth: Buffer,
  options: EncryptOptions = {},
): Buffer {
  if (p256dh.length !== UNCOMPRESSED_POINT || p256dh[0] !== 0x04) throw new Error('bad p256dh');
  if (auth.length !== AUTH_SECRET) throw new Error('bad auth secret');
  if (plaintext.length > MAX_PLAINTEXT) throw new Error('payload too large');
  const ecdh = createECDH('prime256v1');
  if (options.sender) ecdh.setPrivateKey(options.sender.privateKey);
  else ecdh.generateKeys();
  const senderPublic = ecdh.getPublicKey();
  const shared = ecdh.computeSecret(p256dh);
  const salt = options.salt ?? randomBytes(16);

  const keyInfo = Buffer.concat([Buffer.from('WebPush: info\0'), p256dh, senderPublic]);
  const ikm = Buffer.from(hkdfSync('sha256', shared, auth, keyInfo, 32));
  const cek = Buffer.from(
    hkdfSync('sha256', ikm, salt, Buffer.from('Content-Encoding: aes128gcm\0'), 16),
  );
  const nonce = Buffer.from(
    hkdfSync('sha256', ikm, salt, Buffer.from('Content-Encoding: nonce\0'), 12),
  );

  const cipher = createCipheriv('aes-128-gcm', cek, nonce);
  // 0x02 closes the last (here the only) record.
  const record = Buffer.concat([
    cipher.update(Buffer.concat([plaintext, Buffer.from([0x02])])),
    cipher.final(),
    cipher.getAuthTag(),
  ]);

  const header = Buffer.alloc(16 + 4 + 1);
  salt.copy(header, 0);
  header.writeUInt32BE(RECORD_SIZE, 16);
  header.writeUInt8(senderPublic.length, 20);
  return Buffer.concat([header, senderPublic, record]);
}

export interface Vapid {
  /** Unpadded base64url of the 65-byte public point. */
  publicKey: string;
  /** Unpadded base64url of the 32-byte private scalar. */
  privateKey: string;
}

/** The ES256 JWT of RFC 8292 for a push service at `audience` (an origin), valid until `now + ttlSeconds`. */
export function vapidJwt(
  vapid: Vapid,
  audience: string,
  subject: string,
  now: number,
  ttlSeconds = 12 * 3600,
): string {
  const header = b64u(Buffer.from(JSON.stringify({ typ: 'JWT', alg: 'ES256' })));
  const claims = b64u(
    Buffer.from(
      JSON.stringify({ aud: audience, exp: Math.floor(now / 1000) + ttlSeconds, sub: subject }),
    ),
  );
  const point = fromB64u(vapid.publicKey);
  const key = createPrivateKey({
    key: {
      kty: 'EC',
      crv: 'P-256',
      d: vapid.privateKey,
      x: b64u(point.subarray(1, 33)),
      y: b64u(point.subarray(33, 65)),
    },
    format: 'jwk',
  });
  const input = `${header}.${claims}`;
  const signature = sign('sha256', Buffer.from(input), { key, dsaEncoding: 'ieee-p1363' });
  return `${input}.${b64u(signature)}`;
}

/** The `Authorization` header value of a VAPID request. */
export function vapidAuthorization(
  vapid: Vapid,
  audience: string,
  subject: string,
  now: number,
): string {
  return `vapid t=${vapidJwt(vapid, audience, subject, now)}, k=${vapid.publicKey}`;
}

export interface PushRequest {
  headers: Record<string, string>;
  body: Buffer;
}

/** The headers and body of one Web Push delivery. */
export function pushRequest(
  subscription: { endpoint: string; p256dh: string; auth: string },
  plaintext: Buffer,
  vapid: Vapid,
  subject: string,
  now: number,
  urgency: 'normal' | 'high',
): PushRequest {
  const body = encryptPayload(
    plaintext,
    fromB64u(subscription.p256dh),
    fromB64u(subscription.auth),
  );
  return {
    headers: {
      'content-encoding': 'aes128gcm',
      'content-type': 'application/octet-stream',
      ttl: '600',
      urgency,
      authorization: vapidAuthorization(vapid, new URL(subscription.endpoint).origin, subject, now),
    },
    body,
  };
}
