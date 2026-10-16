import { createECDH, randomBytes } from 'node:crypto';
import { chmodSync, existsSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import type { StoreContext } from './layout.ts';

/** The file in the data directory that holds what must not sit in `hoplet.db` (ADR 0021). */
export const SECRETS_FILE = 'notify-secrets.json';

/** The VAPID key pair as unpadded base64url: the 65-byte uncompressed public point and the 32-byte private scalar. */
export interface VapidKeys {
  publicKey: string;
  privateKey: string;
}

export interface NotifySecrets {
  vapid: VapidKeys;
  /** Signs action tokens: base64url of 32 random bytes. */
  actionKey: string;
  /** Webhook signing secrets by webhook ID. */
  webhooks: Record<string, string>;
}

function generate(): NotifySecrets {
  const ecdh = createECDH('prime256v1');
  ecdh.generateKeys();
  return {
    vapid: {
      publicKey: ecdh.getPublicKey().toString('base64url'),
      privateKey: ecdh.getPrivateKey().toString('base64url'),
    },
    actionKey: randomBytes(32).toString('base64url'),
    webhooks: {},
  };
}

function valid(value: unknown): value is NotifySecrets {
  const s = value as NotifySecrets | null;
  return (
    typeof s === 'object' &&
    s !== null &&
    typeof s.actionKey === 'string' &&
    typeof s.vapid?.publicKey === 'string' &&
    typeof s.vapid?.privateKey === 'string' &&
    typeof s.webhooks === 'object' &&
    s.webhooks !== null
  );
}

function save(ctx: StoreContext, secrets: NotifySecrets): void {
  const file = path.join(ctx.dataDir, SECRETS_FILE);
  const temp = `${file}.tmp`;
  writeFileSync(temp, `${JSON.stringify(secrets)}\n`, { mode: 0o600 });
  chmodSync(temp, 0o600);
  renameSync(temp, file);
}

/** The secrets, generated and written with mode 0600 on first use; deleting the file rotates every one. */
export function notifySecrets(ctx: StoreContext): NotifySecrets {
  if (ctx.secrets) return ctx.secrets;
  const file = path.join(ctx.dataDir, SECRETS_FILE);
  if (existsSync(file)) {
    try {
      const parsed: unknown = JSON.parse(readFileSync(file, 'utf8'));
      if (valid(parsed)) {
        ctx.secrets = parsed;
        return parsed;
      }
    } catch {
      // A file that cannot be read is replaced like a missing one.
    }
  }
  const fresh = generate();
  save(ctx, fresh);
  ctx.secrets = fresh;
  return fresh;
}

/** Sets a webhook's signing secret, or removes it with `null`. */
export function setWebhookSecret(ctx: StoreContext, id: string, secret: string | null): void {
  const secrets = notifySecrets(ctx);
  if (secret === null) delete secrets.webhooks[id];
  else secrets.webhooks[id] = secret;
  save(ctx, secrets);
}

export function webhookSecret(ctx: StoreContext, id: string): string | null {
  return notifySecrets(ctx).webhooks[id] ?? null;
}
