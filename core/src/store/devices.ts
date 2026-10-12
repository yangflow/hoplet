import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { registerSecret } from '../log/index.ts';
import type { StoreContext } from './layout.ts';

function hashToken(token: string): string {
  return createHash('sha256').update(token).digest('hex');
}

export function upsertDevice(
  ctx: StoreContext,
  deviceId: string,
  name: string,
  version: string,
): void {
  const now = Date.now();
  ctx.db
    .prepare(
      `INSERT INTO devices(device_id, name, version, first_seen, last_seen)
       VALUES(?, ?, ?, ?, ?)
       ON CONFLICT(device_id) DO UPDATE SET name=excluded.name, version=excluded.version, last_seen=excluded.last_seen`,
    )
    .run(deviceId, name, version, now, now);
}

export function listDevices(ctx: StoreContext): { deviceId: string; name: string }[] {
  const rows = ctx.db
    .prepare(`SELECT device_id, name FROM devices ORDER BY last_seen DESC`)
    .all() as { device_id: string; name: string }[];
  return rows.map((r) => ({ deviceId: r.device_id, name: r.name }));
}

export function mintDevice(
  ctx: StoreContext,
  name: string,
  deviceId?: string,
): { deviceId: string; token: string; name: string } {
  const id = (deviceId?.trim() || randomUUID()).replace(/[^A-Za-z0-9_-]/g, '');
  if (!id) throw new Error('invalid device id');
  const token = randomBytes(32).toString('hex');
  registerSecret(token);
  const hash = hashToken(token);
  const now = Date.now();
  const existing = ctx.db.prepare(`SELECT device_id FROM devices WHERE device_id = ?`).get(id) as
    | { device_id: string }
    | undefined;
  if (existing) {
    ctx.db
      .prepare(`UPDATE devices SET name = ?, token_hash = ?, last_seen = ? WHERE device_id = ?`)
      .run(name, hash, now, id);
  } else {
    ctx.db
      .prepare(
        `INSERT INTO devices(device_id, name, version, first_seen, last_seen, token_hash)
         VALUES(?, ?, '', ?, ?, ?)`,
      )
      .run(id, name, now, now, hash);
  }
  return { deviceId: id, token, name };
}

export function deviceIdForToken(ctx: StoreContext, token: string): string | null {
  if (!token) return null;
  const row = ctx.db
    .prepare(`SELECT device_id FROM devices WHERE token_hash = ?`)
    .get(hashToken(token)) as { device_id: string } | undefined;
  return row?.device_id ?? null;
}
