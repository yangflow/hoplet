import { createHash, randomBytes, randomUUID } from 'node:crypto';
import {
  type DeviceInventory,
  DeviceInventorySchema,
  DeviceLabelSchema,
  type DeviceSystem,
  is,
  type LocalPermissionRule,
  LocalPermissionRuleListSchema,
  type LocalPermissionRules,
  safeParse,
} from '@hoplet/shared';
import { registerSecret } from '../log/index.ts';
import { appendAudit, auditedChange } from './audit.ts';
import type { StoreContext } from './layout.ts';

function hashToken(token: string): string {
  return createHash('sha256').update(token).digest('hex');
}

/**
 * The hello describes the agent that is connected now: an agent that sends no `system` leaves the device with
 * none, and one that announces no `permissionDeny` leaves it with no local deny rules.
 */
export function upsertDevice(
  ctx: StoreContext,
  deviceId: string,
  name: string,
  version: string,
  system?: DeviceSystem,
  permissionDeny?: readonly LocalPermissionRule[],
): void {
  const now = Date.now();
  ctx.db
    .prepare(
      `INSERT INTO devices(device_id, name, version, os, arch, permission_deny, first_seen, last_seen)
       VALUES(?, ?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT(device_id) DO UPDATE SET name=excluded.name, version=excluded.version,
         os=excluded.os, arch=excluded.arch, permission_deny=excluded.permission_deny,
         last_seen=excluded.last_seen`,
    )
    .run(
      deviceId,
      name,
      version,
      system?.os ?? null,
      system?.arch ?? null,
      permissionDeny && permissionDeny.length > 0 ? JSON.stringify(permissionDeny) : null,
      now,
      now,
    );
}

/**
 * The deny rules the agents announced from their own configuration, one entry per device that has any, in the
 * order the devices were first seen; only `deviceId` when it is given. Unreadable stored JSON counts as none.
 */
export function localPermissionDeny(ctx: StoreContext, deviceId?: string): LocalPermissionRules[] {
  const rows = (
    deviceId === undefined
      ? ctx.db
          .prepare(
            `SELECT device_id, permission_deny FROM devices WHERE permission_deny IS NOT NULL ORDER BY first_seen, device_id`,
          )
          .all()
      : ctx.db
          .prepare(
            `SELECT device_id, permission_deny FROM devices WHERE permission_deny IS NOT NULL AND device_id = ?`,
          )
          .all(deviceId)
  ) as { device_id: string; permission_deny: string }[];
  const out: LocalPermissionRules[] = [];
  for (const row of rows) {
    try {
      const parsed = safeParse(LocalPermissionRuleListSchema, JSON.parse(row.permission_deny));
      if (parsed.success && parsed.output.length > 0) {
        out.push({ deviceId: row.device_id, rules: parsed.output });
      }
    } catch {
      // Unreadable JSON is none.
    }
  }
  return out;
}

/** Record that the agent of a device just went away; its last connection is when it disconnected. */
export function touchDevice(ctx: StoreContext, deviceId: string): void {
  ctx.db.prepare(`UPDATE devices SET last_seen = ? WHERE device_id = ?`).run(Date.now(), deviceId);
}

/**
 * `lastSeen` is the agent's last connection in milliseconds, absent for a device that never connected: a minted
 * device has no version until its agent says hello. `system` is null until an agent announces it; `inventory` is
 * absent until an agent reports one. `labels` are the host's, empty by default.
 */
export interface StoredDevice {
  deviceId: string;
  name: string;
  system: DeviceSystem | null;
  inventory?: DeviceInventory;
  labels: string[];
  lastSeen?: number;
}

/** The stored labels; the ones that are not valid labels, and unreadable JSON, count for none. */
function labelsOf(raw: string | null): string[] {
  if (raw === null) return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((l): l is string => is(DeviceLabelSchema, l)) : [];
  } catch {
    return [];
  }
}

/** The stored inventory; none when there is not one or it is unreadable. */
function inventoryOf(raw: string | null): { inventory?: DeviceInventory } {
  if (raw === null) return {};
  try {
    const parsed = safeParse(DeviceInventorySchema, JSON.parse(raw));
    if (parsed.success) return { inventory: parsed.output };
  } catch {
    // Unreadable JSON is none.
  }
  return {};
}

export function listDevices(ctx: StoreContext): StoredDevice[] {
  const rows = ctx.db
    .prepare(
      `SELECT device_id, name, version, os, arch, inventory, labels, last_seen FROM devices ORDER BY last_seen DESC, first_seen DESC`,
    )
    .all() as {
    device_id: string;
    name: string;
    version: string;
    os: string | null;
    arch: string | null;
    inventory: string | null;
    labels: string | null;
    last_seen: number;
  }[];
  return rows.map((r) => ({
    deviceId: r.device_id,
    name: r.name,
    system: r.os !== null && r.arch !== null ? { os: r.os, arch: r.arch } : null,
    ...inventoryOf(r.inventory),
    labels: labelsOf(r.labels),
    ...(r.version !== '' ? { lastSeen: r.last_seen } : {}),
  }));
}

/** Keeps the latest inventory of a device, replacing the one before; false for a device the store does not know. */
export function setInventory(
  ctx: StoreContext,
  deviceId: string,
  inventory: DeviceInventory,
): boolean {
  const result = ctx.db
    .prepare(`UPDATE devices SET inventory = ? WHERE device_id = ?`)
    .run(JSON.stringify(inventory), deviceId);
  return Number(result.changes) > 0;
}

/** Replaces the labels of a device; false for a device the store does not know. */
export function setLabels(ctx: StoreContext, deviceId: string, labels: string[]): boolean {
  return auditedChange(
    ctx,
    () =>
      Number(
        ctx.db
          .prepare(`UPDATE devices SET labels = ? WHERE device_id = ?`)
          .run(labels.length === 0 ? null : JSON.stringify(labels), deviceId).changes,
      ) > 0,
    (changed) =>
      changed
        ? {
            type: 'config.changed',
            deviceId,
            summary: {
              area: 'device-labels',
              action: 'updated',
              fields: ['labels'],
              current: { labels },
            },
          }
        : null,
  );
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
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const existing = ctx.db.prepare(`SELECT device_id FROM devices WHERE device_id = ?`).get(id) as
      | { device_id: string }
      | undefined;
    if (existing) {
      ctx.db
        .prepare(`UPDATE devices SET name = ?, token_hash = ? WHERE device_id = ?`)
        .run(name, hash, id);
    } else {
      ctx.db
        .prepare(
          `INSERT INTO devices(device_id, name, version, first_seen, last_seen, token_hash)
           VALUES(?, ?, '', ?, ?, ?)`,
        )
        .run(id, name, now, now, hash);
    }
    appendAudit(ctx, {
      type: 'device.token.issued',
      deviceId: id,
      summary: { deviceId: id, name, replaced: Boolean(existing) },
    });
    ctx.db.exec('COMMIT');
  } catch (err) {
    if (ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
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

/** The highest journalled event of the device the core has applied (ADR 0039); 0 for none or an unknown device. */
export function deviceEventSeq(ctx: StoreContext, deviceId: string): number {
  const row = ctx.db.prepare('SELECT event_seq FROM devices WHERE device_id = ?').get(deviceId) as
    | { event_seq: number }
    | undefined;
  return Number(row?.event_seq ?? 0);
}

/** Records that the core applied the device's journalled events up to `seq`; it never moves back. */
export function setDeviceEventSeq(ctx: StoreContext, deviceId: string, seq: number): void {
  ctx.db
    .prepare('UPDATE devices SET event_seq = ? WHERE device_id = ? AND event_seq < ?')
    .run(seq, deviceId, seq);
}
